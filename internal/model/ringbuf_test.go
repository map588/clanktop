package model

import (
	"testing"
)

func TestRingBuffer_Basic(t *testing.T) {
	rb := NewRingBuffer[int](3)

	if rb.Len() != 0 {
		t.Fatalf("expected len 0, got %d", rb.Len())
	}

	rb.Push(1)
	rb.Push(2)
	rb.Push(3)

	if rb.Len() != 3 {
		t.Fatalf("expected len 3, got %d", rb.Len())
	}

	got := rb.All()
	want := []int{1, 2, 3}
	for i, v := range got {
		if v != want[i] {
			t.Fatalf("index %d: got %d, want %d", i, v, want[i])
		}
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := NewRingBuffer[int](3)
	rb.Push(1)
	rb.Push(2)
	rb.Push(3)
	rb.Push(4) // overwrites 1

	got := rb.All()
	want := []int{2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("len: got %d, want %d", len(got), len(want))
	}
	for i, v := range got {
		if v != want[i] {
			t.Fatalf("index %d: got %d, want %d", i, v, want[i])
		}
	}
}

func TestRingBuffer_Last(t *testing.T) {
	rb := NewRingBuffer[string](5)
	_, ok := rb.Last()
	if ok {
		t.Fatal("expected no last on empty buffer")
	}

	rb.Push("a")
	rb.Push("b")
	val, ok := rb.Last()
	if !ok || val != "b" {
		t.Fatalf("expected last=b, got %s (ok=%v)", val, ok)
	}
}
