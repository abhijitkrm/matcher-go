package matcher

import "testing"

// Emitting events must not allocate: the sink gets the book's reused event.
func TestEmittingEventsDoesNotAllocate(t *testing.T) {
	b := NewOrderBook(DefaultConfig())
	sink := &NullSink{}
	id := uint64(0)
	allocs := testing.AllocsPerRun(1000, func() {
		id += 2
		b.Apply(NewLimit(id, Ask, 100, 5, Gtc), sink)   // accepted, rests
		b.Apply(NewLimit(id+1, Bid, 100, 5, Gtc), sink) // trade + two closes
		b.Apply(Cancel(id), sink)                       // rejected: unknown id
	})
	if allocs > 0 {
		t.Fatalf("%.1f allocations per round", allocs)
	}
}
