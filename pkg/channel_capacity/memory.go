package channel_capacity

import (
	"sync"
	"time"
)

// memoryPool 是没配 Redis 时的进程内计数池，结构与 Redis 版相同。
// 在途数不需要租约过期：进程退出时计数一起消失。池的数量受
// <渠道, 上游模型> 配置规模限制，不做回收。
type memoryPool struct {
	rpm      map[int64]int64
	tpm      map[int64]int64
	inFlight int64
	totals   Totals
}

var (
	memoryMu    sync.Mutex
	memoryPools = make(map[string]*memoryPool)
)

func memoryBegin(lease *Lease, poolID string) {
	memoryMu.Lock()
	defer memoryMu.Unlock()
	pool := memoryPools[poolID]
	if pool == nil {
		pool = &memoryPool{rpm: make(map[int64]int64), tpm: make(map[int64]int64)}
		memoryPools[poolID] = pool
	}
	pool.prune(lease.startSecond - windowSeconds)
	pool.rpm[lease.startSecond]++
	if lease.estimatedInput > 0 {
		pool.tpm[lease.startSecond] += lease.estimatedInput
	}
	pool.inFlight++
}

func memoryFinish(lease *Lease, poolID string, current time.Time, inputDelta, output int64, totals Totals) {
	memoryMu.Lock()
	defer memoryMu.Unlock()
	pool := memoryPools[poolID]
	if pool == nil {
		return
	}
	pool.inFlight = max(pool.inFlight-1, 0)
	if inputDelta != 0 {
		pool.tpm[lease.startSecond] += inputDelta
		if pool.tpm[lease.startSecond] <= 0 {
			delete(pool.tpm, lease.startSecond)
		}
	}
	if output > 0 {
		pool.tpm[current.Unix()] += output
	}
	pool.totals.Requests += totals.Requests
	pool.totals.Tokens += totals.Tokens
}

func memorySnapshot(poolIDs []string) map[string]Usage {
	cutoff := now().Unix() - windowSeconds
	memoryMu.Lock()
	defer memoryMu.Unlock()
	result := make(map[string]Usage, len(poolIDs))
	for _, poolID := range poolIDs {
		pool := memoryPools[poolID]
		if pool == nil {
			result[poolID] = Usage{}
			continue
		}
		pool.prune(cutoff)
		usage := Usage{InFlight: pool.inFlight}
		for _, count := range pool.rpm {
			usage.RPM += count
		}
		for _, tokens := range pool.tpm {
			usage.TPM += tokens
		}
		result[poolID] = usage
	}
	return result
}

func memoryTotals(poolIDs []string) map[string]Totals {
	memoryMu.Lock()
	defer memoryMu.Unlock()
	result := make(map[string]Totals, len(poolIDs))
	for _, poolID := range poolIDs {
		if pool := memoryPools[poolID]; pool != nil && (pool.totals.Requests > 0 || pool.totals.Tokens > 0) {
			result[poolID] = pool.totals
		}
	}
	return result
}

// prune 删掉 cutoff 秒及以前的字段。
func (p *memoryPool) prune(cutoff int64) {
	for second := range p.rpm {
		if second <= cutoff {
			delete(p.rpm, second)
		}
	}
	for second := range p.tpm {
		if second <= cutoff {
			delete(p.tpm, second)
		}
	}
}
