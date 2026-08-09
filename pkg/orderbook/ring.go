package orderbook

// Ring is a fixed-size FIFO ring buffer.
// Push appends a value; once full, the oldest value is overwritten.
// Snapshot returns the contents oldest-first as a fresh slice.
type Ring[T any] struct {
	buf  []T
	head int // index of the oldest element when full; otherwise unused
	size int // number of valid elements (<= cap(buf))
}

// NewRing returns a Ring with the given capacity. Capacity must be > 0.
func NewRing[T any](capacity int) *Ring[T] {
	if capacity <= 0 {
		capacity = 1
	}
	return &Ring[T]{buf: make([]T, capacity)}
}

// Push appends v. If the ring is full, the oldest entry is overwritten.
func (r *Ring[T]) Push(v T) {
	c := cap(r.buf)
	if r.size < c {
		r.buf[(r.head+r.size)%c] = v
		r.size++
		return
	}
	r.buf[r.head] = v
	r.head = (r.head + 1) % c
}

// Len returns the current number of stored elements.
func (r *Ring[T]) Len() int { return r.size }

// Cap returns the ring's capacity.
func (r *Ring[T]) Cap() int { return cap(r.buf) }

// Latest returns the newest element and true, or the zero value and false if empty.
func (r *Ring[T]) Latest() (T, bool) {
	var zero T
	if r.size == 0 {
		return zero, false
	}
	c := cap(r.buf)
	idx := (r.head + r.size - 1) % c
	return r.buf[idx], true
}

// SetLatest replaces the newest element. No-op if the ring is empty.
func (r *Ring[T]) SetLatest(v T) {
	if r.size == 0 {
		return
	}
	c := cap(r.buf)
	idx := (r.head + r.size - 1) % c
	r.buf[idx] = v
}

// Snapshot returns a copy of the contents oldest-first.
func (r *Ring[T]) Snapshot() []T {
	out := make([]T, r.size)
	c := cap(r.buf)
	for i := 0; i < r.size; i++ {
		out[i] = r.buf[(r.head+i)%c]
	}
	return out
}

// SnapshotNewestFirst returns a copy of the contents newest-first, capped at limit
// (limit <= 0 returns all). Useful for "most recent N" responses.
func (r *Ring[T]) SnapshotNewestFirst(limit int) []T {
	n := r.size
	if limit > 0 && limit < n {
		n = limit
	}
	out := make([]T, n)
	c := cap(r.buf)
	// Walk from newest to oldest.
	for i := 0; i < n; i++ {
		idx := (r.head + r.size - 1 - i + c) % c
		out[i] = r.buf[idx]
	}
	return out
}
