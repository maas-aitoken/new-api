package model

import (
	"context"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// 一次 auto 请求的选路结果。
const (
	AutoRouteOutcomeRouted = "routed" // 选出了模型
	AutoRouteOutcomeFailed = "failed" // 没有任何模型能服务，请求被拦下
)

const (
	// AutoRouteLogDetailMax 是 detail 的字节上限。MySQL 的 TEXT 最多 65535 字节，
	// 一行超限会让整批 INSERT 失败，所以写入方超出时要先精简内容，不能靠截断 JSON。
	AutoRouteLogDetailMax = 32 * 1024

	// autoRouteLogInsertChunk 是单条 INSERT 的行数。15 列 × 50 行 = 750 个占位符，
	// 加上最坏 50 × 32KB 的 detail，仍远低于各数据库的单语句和单包上限。
	autoRouteLogInsertChunk = 50
)

// AutoRouteLog 是一次 auto 请求的选路留痕：指令长什么样、怎么判断的、各榜单试了什么、
// 为什么没选更靠前的模型。它是追加写的遥测数据，落在日志库（LOG_SQL_DSN 为空时与主库同库），
// 由 service 里的异步 writer 批量写入，不在请求路径上。
//
// 列只放列表要显示、要筛选的内容，完整过程在 Detail（JSON）里。
type AutoRouteLog struct {
	Id        int64 `json:"id" gorm:"primaryKey"`
	CreatedAt int64 `json:"created_at" gorm:"bigint;index"`
	// RequestId 与 logs、relay_attempts 的 request_id 同源，使用日志的详情靠它找到这里。
	RequestId  string `json:"request_id" gorm:"type:varchar(64);index"`
	UserId     int    `json:"user_id" gorm:"index"`
	TokenId    int    `json:"token_id"`
	UsingGroup string `json:"using_group" gorm:"type:varchar(64)"`
	Outcome    string `json:"outcome" gorm:"type:varchar(16);index"`
	// Judgment 是判断的来源（现调 jev、缓存、辅助请求……），取值见 pkg/autorouter 的 Judgment*。
	Judgment string `json:"judgment" gorm:"type:varchar(32)"`
	// Source 是路由的来源：jev、cache、inherit、default。
	Source string `json:"source" gorm:"type:varchar(16)"`
	// TaskType 是 jev 判出的任务类型，没有判断时为空。
	TaskType string `json:"task_type" gorm:"type:varchar(64)"`
	// Model 是榜单里的基础模型名，不带档位后缀，选路失败时为空。
	Model string `json:"model" gorm:"type:varchar(255);index"`
	// Effort 是这次请求最终用的推理档位；沿用客户端自己字段的请求没有这个值。
	Effort     string `json:"effort" gorm:"type:varchar(16)"`
	Preference string `json:"preference" gorm:"type:varchar(16)"`
	// JevLatencyMs 只在这次请求调过 jev 时有值。
	JevLatencyMs int64 `json:"jev_latency_ms"`
	// Detail 是完整的选路过程（JSON），不超过 AutoRouteLogDetailMax 字节。列表查询不取这一列，
	// omitempty 让列表的每一行不带这个键，免得空串被当成"这条日志没有过程"。
	Detail string `json:"detail,omitempty" gorm:"type:text"`
}

// BatchInsertAutoRouteLogs 向日志库追加一批留痕，调用方是异步 writer。
func BatchInsertAutoRouteLogs(logs []*AutoRouteLog) error {
	if len(logs) == 0 {
		return nil
	}
	return LOG_DB.CreateInBatches(logs, autoRouteLogInsertChunk).Error
}

// AutoRouteLogQuery 是选路日志列表的筛选条件，零值的字段不参与筛选。
type AutoRouteLogQuery struct {
	RequestId string
	UserId    int
	Model     string
	Outcome   string
	Judgment  string
	TaskType  string
	StartTime int64
	EndTime   int64
	Offset    int
	Limit     int
}

// GetAutoRouteLogs 按时间倒序返回一页选路日志。列表不带 detail：一页最多 100 行，
// 每行 detail 可达 32KB，要看过程的去按请求 id 取单条。
func GetAutoRouteLogs(query AutoRouteLogQuery) ([]AutoRouteLog, int64, error) {
	db := LOG_DB.Model(&AutoRouteLog{})
	// struct 条件只带非零字段，空的筛选项不会参与查询；列名也由 GORM 按方言加引号。
	db = db.Where(&AutoRouteLog{
		RequestId: strings.TrimSpace(query.RequestId),
		UserId:    query.UserId,
		Model:     strings.TrimSpace(query.Model),
		Outcome:   strings.TrimSpace(query.Outcome),
		Judgment:  strings.TrimSpace(query.Judgment),
		TaskType:  strings.TrimSpace(query.TaskType),
	})
	if query.StartTime > 0 {
		db = db.Where("created_at >= ?", query.StartTime)
	}
	if query.EndTime > 0 {
		db = db.Where("created_at <= ?", query.EndTime)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := query.Limit
	if limit < 1 {
		limit = 20
	}
	limit = min(limit, 100)
	offset := max(query.Offset, 0)
	clickHouse := common.UsingLogDatabase(common.DatabaseTypeClickHouse)
	order := "id DESC"
	if clickHouse {
		order = clickHouseLogOrder("")
	}
	var logs []AutoRouteLog
	if err := db.Omit("detail").Order(order).Offset(offset).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	if clickHouse {
		// ClickHouse 没有自增主键，id 恒为 0；给前端分配页内唯一的展示 id。
		for i := range logs {
			logs[i].Id = int64(offset + i + 1)
		}
	}
	return logs, total, nil
}

// GetAutoRouteLogByRequestId 返回一次请求的选路日志（含 detail）。found 为 false 表示没有：
// 没开记录、记录模式没覆盖这类请求、或已过保留期，调用方应当把它当成正常情况。
func GetAutoRouteLogByRequestId(requestId string) (log AutoRouteLog, found bool, err error) {
	requestId = strings.TrimSpace(requestId)
	if requestId == "" {
		return AutoRouteLog{}, false, nil
	}
	order := "id DESC"
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		order = clickHouseLogOrder("")
	}
	var logs []AutoRouteLog
	if err := LOG_DB.Where(&AutoRouteLog{RequestId: requestId}).Order(order).Limit(1).Find(&logs).Error; err != nil {
		return AutoRouteLog{}, false, err
	}
	if len(logs) == 0 {
		return AutoRouteLog{}, false, nil
	}
	return logs[0], true, nil
}

// CleanupAutoRouteLogs 分块删除 before 之前的留痕，每块行数由
// AUTO_ROUTE_LOG_CLEANUP_CHUNK 调整，细节见 purgeBefore。
func CleanupAutoRouteLogs(ctx context.Context, before int64) (int64, error) {
	return purgeBefore[AutoRouteLog](ctx, "AUTO_ROUTE_LOG_CLEANUP_CHUNK", before)
}

// HasAutoRouteLogsBefore 报告是否还有 before 之前的留痕，供清理任务判断要不要运行。
func HasAutoRouteLogsBefore(before int64) bool {
	return hasRowsBefore[AutoRouteLog](before)
}
