package model

import (
	"context"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	// passed：全部渠道满足；soft：没有渠道被淘汰，但有"仅记录"维度的越界。
	CapabilityGateEventPassed = "passed"
	CapabilityGateEventSoft   = "soft"
	// filtered：enforce 下淘汰了部分渠道，请求仍由剩余渠道完成。
	CapabilityGateEventFiltered = "filtered"
	// rejected：enforce 下所有候选渠道都被淘汰，请求被拦下。
	CapabilityGateEventRejected = "rejected"
	// would_filter / would_reject 是 shadow 模式下"如果开启强制会怎样"的预演。
	CapabilityGateEventWouldFilter = "would_filter"
	CapabilityGateEventWouldReject = "would_reject"
)

const (
	// CapabilityGateEventRejectedChannelsMax 是 rejected_channels 的列宽，跟表结构绑定，
	// 不能用环境变量调大。超出部分由写入方截断，完整数量见 RejectedCount。
	CapabilityGateEventRejectedChannelsMax = 255

	// capabilityGateEventInsertChunk 是单条 INSERT 的行数。一次 flush 可以带更多事件，
	// 这里再切块：12 列 × 200 行 = 2400 个占位符，远低于 SQLite、MySQL、PostgreSQL 的单语句上限。
	capabilityGateEventInsertChunk = 200
)

// CapabilityGateEvent 是一次渠道级能力判定留痕。
// 它是追加写的遥测数据，落在日志库（LOG_SQL_DSN 为空时与主库同库），
// 由 service 里的异步 writer 批量写入，不在请求路径上。
type CapabilityGateEvent struct {
	Id                int64  `json:"id" gorm:"primaryKey"`
	Mode              string `json:"mode" gorm:"type:varchar(16);index"`
	Model             string `json:"model" gorm:"type:varchar(255);index"`
	Action            string `json:"action" gorm:"type:varchar(16);index"`
	ChannelsEvaluated int    `json:"channels_evaluated"`
	RejectedChannels  string `json:"rejected_channels" gorm:"type:varchar(255)"`
	Dimensions        string `json:"dimensions" gorm:"type:text"`
	CreatedAt         int64  `json:"created_at" gorm:"bigint;index"`

	// RejectedCount 是被淘汰渠道的准确数量；RejectedChannels 过长时会被截断，
	// 不能再用它推算数量。历史行没有这一列，此时为 0。
	RejectedCount int `json:"rejected_count"`
	// RequestId 与 logs、relay_attempts 的 request_id 同源，用来把判定和请求的真实结果对上。
	RequestId  string `json:"request_id" gorm:"type:varchar(64);index:idx_capability_gate_events_request_id"`
	UserId     int    `json:"user_id"`
	TokenId    int    `json:"token_id"`
	UsingGroup string `json:"using_group" gorm:"type:varchar(64)"`
}

// BatchInsertCapabilityGateEvents 向日志库追加一批事件，调用方是异步 writer。
func BatchInsertCapabilityGateEvents(events []*CapabilityGateEvent) error {
	if len(events) == 0 {
		return nil
	}
	return LOG_DB.CreateInBatches(events, capabilityGateEventInsertChunk).Error
}

type CapabilityGateEventQuery struct {
	Model     string
	Action    string
	RequestId string
	StartTime int64
	EndTime   int64
	Offset    int
	Limit     int
}

func GetCapabilityGateEvents(query CapabilityGateEventQuery) ([]CapabilityGateEvent, int64, error) {
	db := LOG_DB.Model(&CapabilityGateEvent{})
	// struct 条件只带非零字段，空的筛选项不会参与查询；列名也由 GORM 按方言加引号。
	db = db.Where(&CapabilityGateEvent{
		Model:     strings.TrimSpace(query.Model),
		Action:    strings.TrimSpace(query.Action),
		RequestId: strings.TrimSpace(query.RequestId),
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
	if limit > 100 {
		limit = 100
	}
	offset := max(query.Offset, 0)
	clickHouse := common.UsingLogDatabase(common.DatabaseTypeClickHouse)
	order := "id DESC"
	if clickHouse {
		order = clickHouseLogOrder("")
	}
	var events []CapabilityGateEvent
	if err := db.Order(order).Offset(offset).Limit(limit).Find(&events).Error; err != nil {
		return nil, 0, err
	}
	if clickHouse {
		// ClickHouse 没有自增主键，id 恒为 0；给前端分配页内唯一的展示 id。
		for i := range events {
			events[i].Id = int64(offset + i + 1)
		}
	}
	return events, total, nil
}

// CleanupCapabilityGateEvents 分块删除 before 之前的事件，每块行数由
// CAPABILITY_GATE_EVENT_CLEANUP_CHUNK 调整，细节见 purgeBefore。
func CleanupCapabilityGateEvents(ctx context.Context, before int64) (int64, error) {
	return purgeBefore[CapabilityGateEvent](ctx, "CAPABILITY_GATE_EVENT_CLEANUP_CHUNK", before)
}

// HasCapabilityGateEventsBefore 报告是否还有 before 之前的事件，供清理任务判断要不要运行。
func HasCapabilityGateEventsBefore(before int64) bool {
	return hasRowsBefore[CapabilityGateEvent](before)
}
