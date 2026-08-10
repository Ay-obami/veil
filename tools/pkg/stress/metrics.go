package stress

import (
	"sort"
	"sync"
	"time"
)

type ActionStats struct {
	Count     int
	P50       time.Duration
	P95       time.Duration
	P99       time.Duration
	Errors    map[string]int
	ErrorRate float64
}

type MetricsSnapshot struct {
	Actions map[string]ActionStats
	TakenAt time.Time
}

type Metrics struct {
	mu        sync.Mutex
	latencies map[string][]time.Duration
	errors    map[string]map[string]int // action -> errType -> count
}

func NewMetrics() *Metrics {
	return &Metrics{
		latencies: make(map[string][]time.Duration),
		errors:    make(map[string]map[string]int),
	}
}

func (m *Metrics) RecordLatency(action string, d time.Duration) {
	m.mu.Lock()
	m.latencies[action] = append(m.latencies[action], d)
	m.mu.Unlock()
}

func (m *Metrics) RecordError(action, errType string) {
	m.mu.Lock()
	if _, ok := m.errors[action]; !ok {
		m.errors[action] = make(map[string]int)
	}
	m.errors[action][errType]++
	m.mu.Unlock()
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := MetricsSnapshot{
		Actions: make(map[string]ActionStats),
		TakenAt: time.Now(),
	}

	seen := make(map[string]bool)
	for action, lats := range m.latencies {
		seen[action] = true
		out.Actions[action] = computeStats(lats, m.errors[action])
	}
	for action, errs := range m.errors {
		if seen[action] {
			continue
		}
		out.Actions[action] = computeStats(nil, errs)
	}
	return out
}

func computeStats(lats []time.Duration, errs map[string]int) ActionStats {
	n := len(lats)
	stats := ActionStats{Count: n, Errors: make(map[string]int)}
	for k, v := range errs {
		stats.Errors[k] = v
	}
	totalErrs := 0
	for _, v := range errs {
		totalErrs += v
	}
	if n+totalErrs > 0 {
		stats.ErrorRate = float64(totalErrs) / float64(n+totalErrs)
	}
	if n == 0 {
		return stats
	}
	sorted := make([]time.Duration, n)
	copy(sorted, lats)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	stats.P50 = sorted[percentileIndex(n, 50)]
	stats.P95 = sorted[percentileIndex(n, 95)]
	stats.P99 = sorted[percentileIndex(n, 99)]
	return stats
}

func percentileIndex(n, p int) int {
	idx := (p * n) / 100
	if idx >= n {
		idx = n - 1
	}
	return idx
}
