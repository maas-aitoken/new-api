package service

import (
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/autorouter"
	"github.com/QuantumNous/new-api/setting/autorouter_setting"
	"github.com/gin-gonic/gin"
)

// autoRouteLogs 是进程唯一的选路日志 writer。
// 没有启动 writer 的入口（命令行子命令、单元测试）产生的留痕会被直接丢弃。
var autoRouteLogs atomic.Pointer[eventWriter[model.AutoRouteLog]]

// StartAutoRouteLogWriter 启动选路日志 writer，重复调用无副作用。
// 队列、批量和刷新间隔由 AUTO_ROUTE_LOG_* 环境变量调整，见 startEventWriter。
func StartAutoRouteLogWriter() {
	startEventWriter(&autoRouteLogs, "auto route log", "AUTO_ROUTE_LOG", model.BatchInsertAutoRouteLogs)
}

// StopAutoRouteLogWriter 停止 writer 并把队列里剩余的留痕写完。
func StopAutoRouteLogWriter() {
	stopEventWriter(&autoRouteLogs)
}

// autoRouteDetail 是选路日志里完整的选路过程。Result 与消费日志 admin_info.auto_router
// 里的摘要是同一份数据，详情页可以读同样的字段；Trace 是只属于选路日志的部分。
type autoRouteDetail struct {
	Result *autorouter.Result `json:"result,omitempty"`
	Trace  *autorouter.Trace  `json:"trace,omitempty"`
	Error  string             `json:"error,omitempty"`
}

// recordAutoRoute 按 route_log_mode 决定这次 auto 请求要不要留选路日志。
// decisions 只记真正做了决定的请求：这次现调了 jev（无论成败），或者找不到任何模型。
// 命中判断缓存的后续请求、辅助请求、会话继承得到的结果都是重复的，量大，只有 all 才记。
func recordAutoRoute(c *gin.Context, result *autorouter.Result, resolveErr error) {
	writer := autoRouteLogs.Load()
	if writer == nil {
		return
	}
	switch autorouter_setting.GetAutoRouterSetting().RouteLogMode {
	case autorouter_setting.RouteLogAll:
	case autorouter_setting.RouteLogDecisions:
		judgment := result.Trace.Judgment
		if resolveErr == nil && judgment != autorouter.JudgmentLive && judgment != autorouter.JudgmentLiveFailed {
			return
		}
	default:
		return
	}
	writer.enqueue(newAutoRouteLog(c, result, resolveErr))
}

// newAutoRouteLog 把一次选路的结果整理成日志行。可能被外部影响长度的列先按列宽截断，
// 一行写不进会让整批 INSERT 失败。
func newAutoRouteLog(c *gin.Context, result *autorouter.Result, resolveErr error) *model.AutoRouteLog {
	entry := &model.AutoRouteLog{
		CreatedAt:  common.GetTimestamp(),
		RequestId:  truncateStr(c.GetString(common.RequestIdKey), 64),
		UserId:     common.GetContextKeyInt(c, constant.ContextKeyUserId),
		TokenId:    common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		UsingGroup: truncateStr(common.GetContextKeyString(c, constant.ContextKeyUsingGroup), 64),
		Outcome:    model.AutoRouteOutcomeRouted,
		Judgment:   result.Trace.Judgment,
		Source:     result.Source,
		Preference: result.Preference,
		Detail:     encodeAutoRouteDetail(result, resolveErr),
	}
	if result.Decision != nil {
		entry.TaskType = truncateStr(result.Decision.TaskType, 64)
	}
	if result.Jev != nil {
		entry.JevLatencyMs = result.Jev.LatencyMs
	}
	if resolveErr != nil {
		entry.Outcome = model.AutoRouteOutcomeFailed
		return entry
	}
	entry.Model = truncateStr(result.Model, 255)
	// 档位写进请求体的模型，档位就是写进去的那个（沿用客户端自己字段时为空）；
	// 其余模型的档位拼在模型名后缀上，裸模型名是服务商的默认档位。
	if result.EffortInBody {
		entry.Effort = truncateStr(result.AppliedEffort, 16)
	} else if suffix, found := strings.CutPrefix(result.ResolvedModel, result.Model+"-"); found {
		entry.Effort = truncateStr(suffix, 16)
	}
	return entry
}

// encodeAutoRouteDetail 返回不超过 AutoRouteLogDetailMax 字节的详情。正常情况远小于上限，
// 只有榜单配得极长时才会超出，此时依次去掉最占地方的比较候选带、整个路由结果，只留留痕。
func encodeAutoRouteDetail(result *autorouter.Result, resolveErr error) string {
	detail := autoRouteDetail{Result: result, Trace: result.Trace}
	if resolveErr != nil {
		detail.Error = resolveErr.Error()
	}
	withoutPick := *result
	withoutPick.Pick = nil
	tiers := []autoRouteDetail{
		detail,
		{Result: &withoutPick, Trace: detail.Trace, Error: detail.Error},
		{Trace: detail.Trace, Error: detail.Error},
	}
	for _, tier := range tiers {
		if encoded, err := common.Marshal(tier); err == nil && len(encoded) <= model.AutoRouteLogDetailMax {
			return string(encoded)
		}
	}
	return "{}"
}
