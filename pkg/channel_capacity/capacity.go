// Package channel_capacity 统计每个 <渠道, 上游模型> 的实时用量：最近 60 秒
// 开始的尝试数（RPM）、最近 60 秒的 token 数（TPM），以及正在进行的尝试数。
//
// 它只统计、不拦截。额度存在渠道 setting.model_limits 里，执行准入的是调度器；
// 管理页拿这里的用量和额度相减得到剩余量，调度器读同一份 Redis 计数做负载排序。
//
// 计数池按 <渠道, 上游模型> 划分，同一渠道的多个 key 共用一个池，与额度
// "整个渠道合计"的口径一致。配了 Redis 时各节点共享计数；没配时退回本进程
// 内存，只反映本节点。
//
// TPM 的口径：尝试开始时只记输入估算，结束后按实际输入校正并补上输出；
// 失败且没有任何用量时撤回输入估算。所以在途请求只占它的输入部分。
package channel_capacity

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	// windowSeconds 是 RPM/TPM 的滑动窗口长度。
	windowSeconds = 60
	// leaseTTL 是在途租约的有效期。节点挂掉后它的在途数最多再保留这么久。
	leaseTTL = 90 * time.Second
	// renewInterval 是本节点给在途租约续期的间隔，必须明显小于 leaseTTL。
	renewInterval = 30 * time.Second
	// poolKeyTTL 是计数池 Redis 键的过期时间，要比窗口和租约都长，
	// 闲下来的池会自己消失。
	poolKeyTTL = 120 * time.Second
	// redisTimeout 与调度器运行时计数一致，Redis 慢了不拖累请求。
	redisTimeout = 50 * time.Millisecond
	// snapshotTimeout 给管理页的批量读取用，一次可能读上百个池。
	snapshotTimeout = 2 * time.Second
	// defaultRuntimePrefix 与 maas-scheduler 的 SCHEDULER_RUNTIME_PREFIX 默认值一致，
	// 调度器按同一个前缀读计数池。
	defaultRuntimePrefix = "new-api:scheduler:runtime"
)

// now 便于测试替换时钟。
var now = time.Now

// PoolID 是 <渠道, 上游模型> 计数池的标识，new-api 与调度器都把它当作不透明字符串。
// 上游模型名取 sha1 前 12 位，避免名字里的 "/"、":" 混进 Redis 键。
func PoolID(channelID int, upstreamModel string) string {
	return fmt.Sprintf("c%d-%s", channelID, common.Sha1([]byte(upstreamModel))[:12])
}

// Lease 是一次已登记的尝试，必须配一次 Finish。
type Lease struct {
	id             string
	poolID         string
	startSecond    int64
	estimatedInput int64
	// redis 为 true 表示这次登记在 Redis 里，结束和续租也只走 Redis。
	redis    bool
	finished atomic.Bool
}

// Begin 为发往 <channelID, upstreamModel> 的一次尝试登记用量：RPM 加 1，
// TPM 记上输入估算，在途数加 1。返回的 Lease 必须交给 Finish；
// 渠道或模型为空时返回 nil，Redis 出错时也返回 nil（这次不计数，请求照常）。
func Begin(channelID int, upstreamModel string, estimatedInput int) *Lease {
	if channelID <= 0 || strings.TrimSpace(upstreamModel) == "" {
		return nil
	}
	current := now()
	lease := &Lease{
		id:             fmt.Sprintf("%d-%s", current.UnixNano(), common.GetRandomString(8)),
		poolID:         PoolID(channelID, upstreamModel),
		startSecond:    current.Unix(),
		estimatedInput: int64(max(estimatedInput, 0)),
	}
	if !Shared() {
		memoryBegin(lease)
		return lease
	}
	if err := redisBegin(lease, current); err != nil {
		logRedisError("begin", err)
		return nil
	}
	lease.redis = true
	trackLease(lease)
	return lease
}

// Finish 结束一次登记。actualInput>0 时用它校正开始时的输入估算（开始那一秒
// 已滑出窗口就不再校正），actualOutput 记到结束这一秒；失败且没有任何用量时
// 撤回输入估算。RPM 不撤回：这次尝试确实发出去过。
// 对 nil 或已经结束的 Lease 什么也不做，所以可以放在 defer 里兜底。
func Finish(lease *Lease, success bool, actualInput, actualOutput int) {
	if lease == nil || !lease.finished.CompareAndSwap(false, true) {
		return
	}
	current := now()
	inputDelta := int64(0)
	switch {
	case actualInput > 0:
		inputDelta = int64(actualInput) - lease.estimatedInput
	case !success && actualOutput <= 0:
		inputDelta = -lease.estimatedInput
	}
	if lease.startSecond <= current.Unix()-windowSeconds {
		inputDelta = 0
	}
	output := int64(max(actualOutput, 0))
	if !lease.redis {
		memoryFinish(lease, current, inputDelta, output)
		return
	}
	untrackLease(lease)
	if err := redisFinish(lease, current, inputDelta, output); err != nil {
		logRedisError("finish", err)
	}
}

// Usage 是一个计数池此刻的用量。
type Usage struct {
	// RPM 是最近 60 秒开始的尝试数。
	RPM int64
	// TPM 是最近 60 秒的 token 数；在途请求只含输入估算。
	TPM int64
	// InFlight 是正在进行的尝试数。
	InFlight int64
}

// Shared 表示计数是否放在 Redis 里、由所有节点共享；false 时只反映本节点。
func Shared() bool {
	return common.RedisEnabled && common.RDB != nil
}

// Snapshot 批量读取计数池的用量，没有流量的池返回零值。
func Snapshot(poolIDs []string) (map[string]Usage, error) {
	if len(poolIDs) == 0 {
		return map[string]Usage{}, nil
	}
	if !Shared() {
		return memorySnapshot(poolIDs), nil
	}
	return redisSnapshot(poolIDs)
}

// runtimePrefix 与 service.schedulerRuntimeNamespace 读同一个环境变量。
func runtimePrefix() string {
	prefix := strings.TrimSpace(common.GetEnvOrDefaultString("SCHEDULER_RUNTIME_PREFIX", defaultRuntimePrefix))
	if prefix == "" {
		return defaultRuntimePrefix
	}
	return prefix
}

// 本节点登记在 Redis 里、还没结束的租约，续租循环按它续期。
var (
	leasesMu   sync.Mutex
	leases     = make(map[string]*Lease)
	renewStart sync.Once
)

func trackLease(lease *Lease) {
	renewStart.Do(func() { go renewLoop() })
	leasesMu.Lock()
	leases[lease.id] = lease
	leasesMu.Unlock()
}

func untrackLease(lease *Lease) {
	leasesMu.Lock()
	delete(leases, lease.id)
	leasesMu.Unlock()
}

func renewLoop() {
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()
	for range ticker.C {
		renewLeases()
	}
}

// renewLeases 给本节点所有在途租约续期。只续还在的成员，已经结束的租约
// 不会被续回来；出错只记日志，下一轮再续。
func renewLeases() {
	defer func() {
		if recovered := recover(); recovered != nil {
			common.SysError(fmt.Sprintf("channel capacity lease renewal panic: %v", recovered))
		}
	}()
	if !Shared() {
		return
	}
	leasesMu.Lock()
	active := make([]*Lease, 0, len(leases))
	for _, lease := range leases {
		active = append(active, lease)
	}
	leasesMu.Unlock()
	if len(active) == 0 {
		return
	}
	if err := redisRenew(active, now()); err != nil {
		logRedisError("renew", err)
	}
}

// lastRedisErrorLog 让 Redis 出故障时每分钟最多记一条日志，免得刷屏。
var lastRedisErrorLog atomic.Int64

func logRedisError(operation string, err error) {
	current := time.Now().Unix()
	last := lastRedisErrorLog.Load()
	if current-last < 60 || !lastRedisErrorLog.CompareAndSwap(last, current) {
		return
	}
	common.SysError(common.LogText("channel capacity %s failed in Redis: %v", operation, err))
}
