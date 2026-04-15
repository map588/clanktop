package model

// RingBuffer is a fixed-size circular buffer for any type.
type RingBuffer[T any] struct {
	items []T
	cap   int
	head  int // next write position
	count int
}

func NewRingBuffer[T any](capacity int) *RingBuffer[T] {
	return &RingBuffer[T]{
		items: make([]T, capacity),
		cap:   capacity,
	}
}

func (r *RingBuffer[T]) Push(item T) {
	r.items[r.head] = item
	r.head = (r.head + 1) % r.cap
	if r.count < r.cap {
		r.count++
	}
}

// All returns items in insertion order (oldest first).
func (r *RingBuffer[T]) All() []T {
	if r.count == 0 {
		return nil
	}
	result := make([]T, r.count)
	start := (r.head - r.count + r.cap) % r.cap
	for i := 0; i < r.count; i++ {
		result[i] = r.items[(start+i)%r.cap]
	}
	return result
}

func (r *RingBuffer[T]) Len() int {
	return r.count
}

// Last returns the most recently pushed item.
func (r *RingBuffer[T]) Last() (T, bool) {
	if r.count == 0 {
		var zero T
		return zero, false
	}
	idx := (r.head - 1 + r.cap) % r.cap
	return r.items[idx], true
}
