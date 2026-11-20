package autorouter

import (
	"sync"
	"time"
)

// MetricSample is a model's recent outcomes, aggregated across the gateway's
// instances. Successes and Failures count only outcomes attributable to the model.
type MetricSample struct {
	Successes int64
	Failures  int64
}

// Metrics records auto-routed outcomes and reads their sliding-window samples.
// Implementations must not make the outcome counter depend on telemetry sampling.
type Metrics interface {
	Finish(model string, success bool)
	Snapshot(models []string) map[string]MetricSample
}

var (
	modelMetricsMu sync.RWMutex
	modelMetrics   Metrics = NewMemoryMetrics()
)

func SetMetrics(metrics Metrics) {
	if metrics == nil {
		metrics = NewMemoryMetrics()
	}
	modelMetricsMu.Lock()
	modelMetrics = metrics
	modelMetricsMu.Unlock()
}

func currentMetrics() Metrics {
	modelMetricsMu.RLock()
	defer modelMetricsMu.RUnlock()
	return modelMetrics
}

func FinishModelRequest(model string, success bool) {
	if model != "" {
		currentMetrics().Finish(model, success)
	}
}

// MemoryMetrics is the single-node fallback used when Redis is unavailable.
type MemoryMetrics struct {
	mu      sync.Mutex
	window  time.Duration
	now     func() time.Time
	samples map[string]*memorySample
}

type memorySample struct {
	buckets map[int64]memoryBucket
}

type memoryBucket struct {
	successes int64
	failures  int64
}

func NewMemoryMetrics() *MemoryMetrics {
	return NewMemoryMetricsClock(5*time.Minute, time.Now)
}

func NewMemoryMetricsClock(window time.Duration, now func() time.Time) *MemoryMetrics {
	return &MemoryMetrics{window: window, now: now, samples: make(map[string]*memorySample)}
}

func (m *MemoryMetrics) update(model string, mutate func(*memoryBucket)) {
	second := m.now().Unix()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.samples[model] == nil {
		m.samples[model] = &memorySample{buckets: make(map[int64]memoryBucket)}
	}
	m.prune(m.samples[model], second)
	bucket := m.samples[model].buckets[second]
	mutate(&bucket)
	m.samples[model].buckets[second] = bucket
}

func (m *MemoryMetrics) Finish(model string, success bool) {
	m.update(model, func(bucket *memoryBucket) {
		if success {
			bucket.successes++
		} else {
			bucket.failures++
		}
	})
}

func (m *MemoryMetrics) prune(sample *memorySample, second int64) {
	cutoff := second - int64(m.window/time.Second)
	for bucket := range sample.buckets {
		if bucket <= cutoff {
			delete(sample.buckets, bucket)
		}
	}
}

func (m *MemoryMetrics) Snapshot(models []string) map[string]MetricSample {
	second := m.now().Unix()
	result := make(map[string]MetricSample, len(models))
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, model := range models {
		sample := m.samples[model]
		if sample == nil {
			continue
		}
		m.prune(sample, second)
		var total MetricSample
		for _, bucket := range sample.buckets {
			total.Successes += bucket.successes
			total.Failures += bucket.failures
		}
		if total.Successes > 0 || total.Failures > 0 {
			result[model] = total
		}
		if len(sample.buckets) == 0 {
			delete(m.samples, model)
		}
	}
	return result
}
