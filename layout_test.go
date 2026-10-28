package matcher

import (
	"testing"
	"unsafe"
)

func TestChunkIs128Bytes(t *testing.T) {
	if s := unsafe.Sizeof(chunk{}); s != 128 {
		t.Fatalf("sizeof(chunk) = %d, want 128", s)
	}
	if s := unsafe.Sizeof(mapEntry{}); s != 16 {
		t.Fatalf("sizeof(mapEntry) = %d, want 16", s)
	}
}

// Go does not promise 128-byte alignment, but slices of 128-byte elements
// land on size classes / spans that are multiples of 128. Check it holds.
func TestChunksAligned(t *testing.T) {
	for _, n := range []int{1, 3, 7, 100, 1000, 65536} {
		b := NewOrderBook(BookConfig{PriceMin: 0, PriceMax: 10, MaxOrders: n})
		b.Prefault()
		if a := uintptr(unsafe.Pointer(&b.st.chunks[0])) % 128; a != 0 {
			t.Errorf("max_orders=%d: chunks misaligned by %d", n, a)
		}
	}
}

type recSink struct{ out []uint64 }

func (s *recSink) OnEvent(seq uint64, ev *Event) {
	s.out = append(s.out, seq, uint64(ev.Kind), ev.OrderID, ev.Maker, ev.Taker,
		uint64(ev.Price), ev.Qty, ev.LeavesQty, uint64(ev.Reason))
}

// ApplyBatch (with its look-ahead pipeline engaged: >= prefetchMinLive live orders)
// produces exactly the per-command Apply stream and state.
func TestApplyBatchMatchesApply(t *testing.T) {
	cfg := BookConfig{PriceMin: 1, PriceMax: 4000, MaxOrders: 400_000}
	x := uint64(0x243F6A8885A308D3)
	rnd := func() uint64 { x ^= x << 13; x ^= x >> 7; x ^= x << 17; return x }
	var setup, run []Command
	id := uint64(0)
	for i := 0; i < 300_000; i++ { // deep resting book, uncrossed
		id++
		if i%2 == 0 {
			setup = append(setup, NewLimit(id, Bid, int64(1+rnd()%1990), 1+rnd()%50, Gtc))
		} else {
			setup = append(setup, NewLimit(id, Ask, int64(2010+rnd()%1990), 1+rnd()%50, Gtc))
		}
	}
	for i := 0; i < 200_000; i++ {
		r := rnd()
		target := 1 + (r>>8)%(id+10) // includes unknown ids
		switch r % 10 {
		case 0, 1, 2:
			run = append(run, Cancel(target))
		case 3, 4:
			run = append(run, Replace(target, int64(1+(r>>20)%4100), (r>>40)%60))
		case 5:
			id++
			run = append(run, NewMarket(id, Side(r>>3&1), 1+(r>>16)%200))
		default:
			id++
			tif := Tif((r >> 5) % 4)
			p := int64(1900 + (r>>24)%200)
			if r>>50&7 == 0 {
				p = int64((r >> 24) % 4200) // some invalid prices
			}
			run = append(run, NewLimit(id, Side(r>>3&1), p, (r>>32)%80, tif))
		}
		if i%977 == 0 {
			run = append(run, NewLimit(target, Bid, 100, 5, Gtc)) // duplicate ids
		}
	}
	a, b := NewOrderBook(cfg), NewOrderBook(cfg)
	b.Prefault()
	sa, sb := &recSink{}, &recSink{}
	for _, c := range setup {
		a.Apply(c, sa)
	}
	b.ApplyBatch(setup, sb)
	if prefetchMinLive < 1<<40 && b.OrderCount() < prefetchMinLive {
		t.Fatalf("book too shallow to engage the pipeline: %d", b.OrderCount())
	}
	for _, c := range run {
		a.Apply(c, sa)
	}
	b.ApplyBatch(run, sb)
	if len(sa.out) != len(sb.out) {
		t.Fatalf("event count %d vs %d", len(sa.out), len(sb.out))
	}
	for i := range sa.out {
		if sa.out[i] != sb.out[i] {
			t.Fatalf("divergence at word %d", i)
		}
	}
	ra, rb := a.RestingOrders(), b.RestingOrders()
	if len(ra) != len(rb) {
		t.Fatalf("resting %d vs %d", len(ra), len(rb))
	}
	for i := range ra {
		if ra[i] != rb[i] {
			t.Fatalf("resting order %d differs", i)
		}
	}
}

// The timed batch path must not allocate either.
func TestApplyBatchDoesNotAllocate(t *testing.T) {
	b := NewOrderBook(DefaultConfig())
	b.Prefault()
	sink := &NullSink{}
	id := uint64(0)
	cmds := make([]Command, 3)
	allocs := testing.AllocsPerRun(1000, func() {
		id += 2
		cmds[0] = NewLimit(id, Ask, 100, 5, Gtc)
		cmds[1] = NewLimit(id+1, Bid, 100, 5, Gtc)
		cmds[2] = Cancel(id)
		b.ApplyBatch(cmds, sink)
	})
	if allocs > 0 {
		t.Fatalf("%.1f allocations per round", allocs)
	}
}
