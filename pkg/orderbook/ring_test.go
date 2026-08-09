package orderbook

import (
	"reflect"
	"testing"
)

func TestRing_PushUnderCapacity(t *testing.T) {
	r := NewRing[int](5)
	for i := 1; i <= 3; i++ {
		r.Push(i)
	}
	if r.Len() != 3 {
		t.Fatalf("len: got %d, want 3", r.Len())
	}
	if r.Cap() != 5 {
		t.Fatalf("cap: got %d, want 5", r.Cap())
	}
	got := r.Snapshot()
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("snapshot: got %v, want [1 2 3]", got)
	}
}

func TestRing_OverflowDropsOldest(t *testing.T) {
	r := NewRing[int](3)
	for i := 1; i <= 5; i++ {
		r.Push(i)
	}
	if r.Len() != 3 {
		t.Fatalf("len: got %d, want 3", r.Len())
	}
	got := r.Snapshot()
	if !reflect.DeepEqual(got, []int{3, 4, 5}) {
		t.Fatalf("snapshot oldest-first: got %v, want [3 4 5]", got)
	}
}

func TestRing_SnapshotNewestFirst(t *testing.T) {
	r := NewRing[int](4)
	for i := 1; i <= 6; i++ {
		r.Push(i)
	}
	// After 6 pushes into cap 4, contents oldest-first are [3 4 5 6].
	got := r.SnapshotNewestFirst(0)
	if !reflect.DeepEqual(got, []int{6, 5, 4, 3}) {
		t.Fatalf("newest-first all: got %v, want [6 5 4 3]", got)
	}
	got = r.SnapshotNewestFirst(2)
	if !reflect.DeepEqual(got, []int{6, 5}) {
		t.Fatalf("newest-first limit=2: got %v, want [6 5]", got)
	}
	got = r.SnapshotNewestFirst(100)
	if !reflect.DeepEqual(got, []int{6, 5, 4, 3}) {
		t.Fatalf("newest-first limit>len: got %v, want [6 5 4 3]", got)
	}
}

func TestRing_LatestAndSetLatest(t *testing.T) {
	r := NewRing[int](3)
	if _, ok := r.Latest(); ok {
		t.Fatal("Latest on empty ring should return ok=false")
	}
	r.Push(10)
	r.Push(20)
	v, ok := r.Latest()
	if !ok || v != 20 {
		t.Fatalf("Latest: got (%d,%v), want (20,true)", v, ok)
	}
	r.SetLatest(99)
	v, _ = r.Latest()
	if v != 99 {
		t.Fatalf("after SetLatest: got %d, want 99", v)
	}
	if got := r.Snapshot(); !reflect.DeepEqual(got, []int{10, 99}) {
		t.Fatalf("snapshot after SetLatest: got %v, want [10 99]", got)
	}
}

func TestRing_SetLatestEmpty(t *testing.T) {
	r := NewRing[int](3)
	r.SetLatest(42) // must not panic
	if _, ok := r.Latest(); ok {
		t.Fatal("ring should still be empty")
	}
}

func TestRing_LatestSurvivesOverflow(t *testing.T) {
	r := NewRing[int](3)
	for i := 0; i < 100; i++ {
		r.Push(i)
	}
	v, _ := r.Latest()
	if v != 99 {
		t.Fatalf("latest after overflow: got %d, want 99", v)
	}
	if got := r.Snapshot(); !reflect.DeepEqual(got, []int{97, 98, 99}) {
		t.Fatalf("snapshot after overflow: got %v, want [97 98 99]", got)
	}
}

func TestRing_ZeroCapacityCoercedToOne(t *testing.T) {
	r := NewRing[int](0)
	if r.Cap() != 1 {
		t.Fatalf("cap: got %d, want 1", r.Cap())
	}
	r.Push(7)
	r.Push(8)
	if got := r.Snapshot(); !reflect.DeepEqual(got, []int{8}) {
		t.Fatalf("snapshot: got %v, want [8]", got)
	}
}
