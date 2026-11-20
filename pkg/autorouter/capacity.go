package autorouter

import "math"

// CapacitySample 是一个实际路由容量桶的加权用量与上限。
type CapacitySample struct {
	Share            float64
	RPMLimit         float64
	RPMUsed          float64
	TPMLimit         float64
	TPMUsed          float64
	ConcurrencyLimit float64
	ConcurrencyUsed  float64
	Exhausted        bool
}

// AggregateCapacity 按路由权重聚合容量桶，并取 RPM、TPM、并发三个剩余比例中的最小值。
func AggregateCapacity(samples []CapacitySample) float64 {
	var rpmLimit, rpmUsed, tpmLimit, tpmUsed, concurrencyLimit, concurrencyUsed float64
	for _, sample := range samples {
		if sample.Share <= 0 || math.IsNaN(sample.Share) || math.IsInf(sample.Share, 0) {
			continue
		}
		if sample.Exhausted {
			return 0
		}
		if sample.RPMLimit > 0 {
			rpmLimit += sample.Share * sample.RPMLimit
			rpmUsed += sample.Share * max(sample.RPMUsed, 0)
		}
		if sample.TPMLimit > 0 {
			tpmLimit += sample.Share * sample.TPMLimit
			tpmUsed += sample.Share * max(sample.TPMUsed, 0)
		}
		if sample.ConcurrencyLimit > 0 {
			concurrencyLimit += sample.Share * sample.ConcurrencyLimit
			concurrencyUsed += sample.Share * max(sample.ConcurrencyUsed, 0)
		}
	}

	capacity := 1.0
	if rpmLimit > 0 {
		capacity = min(capacity, remainingCapacity(rpmLimit, rpmUsed))
	}
	if tpmLimit > 0 {
		capacity = min(capacity, remainingCapacity(tpmLimit, tpmUsed))
	}
	if concurrencyLimit > 0 {
		capacity = min(capacity, remainingCapacity(concurrencyLimit, concurrencyUsed))
	}
	return clampCapacity(capacity)
}

func remainingCapacity(limit, used float64) float64 {
	if limit <= 0 || math.IsNaN(limit) || math.IsInf(limit, 0) {
		return 1
	}
	return (limit - used) / limit
}

func clampCapacity(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 1
	}
	return min(max(value, 0), 1)
}
