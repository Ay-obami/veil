package stress

import (
	"sync"
	"testing"
	"time"
)

func TestMetrics_RecordLatencyAndSnapshot(t *testing.T) {
	m := NewMetrics()
	for i := 1; i <= 100; i++ {
		m.RecordLatency("place_order", time.Duration(i)*time.Millisecond)
	}
	snap := m.Snapshot()
	ps := snap.Actions["place_order"]
	if ps.Count != 100 {
		t.Fatalf("count: want 100, got %d", ps.Count)
	}
	if ps.P50 < 49*time.Millisecond || ps.P50 > 51*time.Millisecond {
		t.Fatalf("p50: want ~50ms, got %s", ps.P50)
	}
	if ps.P95 < 94*time.Millisecond || ps.P95 > 96*time.Millisecond {
		t.Fatalf("p95: want ~95ms, got %s", ps.P95)
	}
	if ps.P99 < 98*time.Millisecond || ps.P99 > 100*time.Millisecond {
		t.Fatalf("p99: want ~99ms, got %s", ps.P99)
	}
}

func TestMetrics_ErrorRate(t *testing.T) {
	m := NewMetrics()
	for i := 0; i < 80; i++ {
		m.RecordLatency("deposit", time.Millisecond)
	}
	for i := 0; i < 20; i++ {
		m.RecordError("deposit", "timeout")
	}
	snap := m.Snapshot()
	ps := snap.Actions["deposit"]
	if ps.Count != 80 {
		t.Fatalf("success count: want 80, got %d", ps.Count)
	}
	if ps.Errors["timeout"] != 20 {
		t.Fatalf("timeout errors: want 20, got %d", ps.Errors["timeout"])
	}
	if ps.ErrorRate < 0.19 || ps.ErrorRate > 0.21 {
		t.Fatalf("error rate: want ~0.20, got %f", ps.ErrorRate)
	}
}

func TestMetrics_Concurrent(t *testing.T) {
	m := NewMetrics()
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				m.RecordLatency("cancel", time.Millisecond)
			}
		}()
	}
	wg.Wait()
	if got := m.Snapshot().Actions["cancel"].Count; got != 10_000 {
		t.Fatalf("concurrent count: want 10000, got %d", got)
	}
}
