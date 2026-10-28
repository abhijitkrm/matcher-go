package matcher

const NIL = ^uint32(0) // sentinel link ("null index")

// ---- order store: per-level chunked FIFOs (docs/LAYOUT.md §1) -------------

// Resting orders live inline in their level's queue: an unrolled linked list
// of 128-byte chunks (one M1 cache line), each holding up to 6 {id, qty}
// slots plus the level's price/side. Matching walks slots sequentially; a
// cancel touches one chunk. A dead slot has qty == 0 (live resting qty is
// always > 0). A chunk is released when its last live slot dies, so chunks
// in use never exceed live orders.
//
// An order's location is loc = chunk<<3 | slot — the orderMap value.
const chunkSlots = 6

type slot struct {
	id  uint64
	qty uint64
}

type chunk struct {
	price            int64
	prev, next       uint32
	head, tail, live uint8 // [head, tail) may hold live slots
	side             Side
	_                uint32
	s                [chunkSlots]slot
	tif              [8]Tif
}

func locChunk(loc uint32) uint32 { return loc >> 3 }
func locSlot(loc uint32) uint32  { return loc & 7 }

// level is a price level: head/tail chunk indices plus a running total (FOK
// pre-checks, depth queries).
type level struct {
	head  uint32
	tail  uint32
	total uint64
}

func newLevel() level { return level{head: NIL, tail: NIL} }

func (l *level) empty() bool { return l.head == NIL }

type store struct {
	// chunks has capacity max_orders from construction, so appending never
	// reallocates (locs stay stable) and the timed path never allocates.
	// Go's large allocations come from fresh zeroed spans: pages commit on
	// first touch, which prefault() forces up front.
	chunks    []chunk
	used      uint32 // chunks[0, used) have been handed out
	freeChunk uint32
	live      int
}

func newStore(maxOrders int) store {
	return store{chunks: make([]chunk, 0, max(maxOrders, 1)), freeChunk: NIL}
}

// prefault commits and touches every chunk so no page fault lands on the
// hot path (BENCH.md: no growth inside timed sections).
func (s *store) prefault() {
	s.chunks = s.chunks[:cap(s.chunks)]
	const stride = 4096 / 128 // one write per 4 KiB page
	for i := int(s.used); i < len(s.chunks); i += stride {
		s.chunks[i].live = 0
	}
}

// allocChunk never fails: chunks in use <= live orders <= max_orders.
func (s *store) allocChunk(side Side, price int64) uint32 {
	var c uint32
	if s.freeChunk != NIL {
		c = s.freeChunk
		s.freeChunk = s.chunks[c].next
	} else {
		c = s.used
		s.used++
		if int(c) == len(s.chunks) {
			s.chunks = s.chunks[:c+1] // within capacity: no allocation
		}
	}
	ch := &s.chunks[c]
	ch.price = price
	ch.prev, ch.next = NIL, NIL
	ch.head, ch.tail, ch.live = 0, 0, 0
	ch.side = side
	return c
}

func (s *store) dropChunk(l *level, c uint32) {
	ch := &s.chunks[c]
	if ch.prev != NIL {
		s.chunks[ch.prev].next = ch.next
	} else {
		l.head = ch.next
	}
	if ch.next != NIL {
		s.chunks[ch.next].prev = ch.prev
	} else {
		l.tail = ch.prev
	}
	ch.next = s.freeChunk
	s.freeChunk = c
}

// push appends a resting order at the level's tail; returns its loc.
func (s *store) push(l *level, side Side, price int64, id, qty uint64, tif Tif) uint32 {
	c := l.tail
	if c == NIL || s.chunks[c].tail == chunkSlots {
		n := s.allocChunk(side, price)
		s.chunks[n].prev = c
		if c != NIL {
			s.chunks[c].next = n
		} else {
			l.head = n
		}
		l.tail = n
		c = n
	}
	ch := &s.chunks[c]
	i := ch.tail % chunkSlots // always < chunkSlots here; the mod drops the bounds check
	ch.tail++
	ch.s[i] = slot{id, qty}
	ch.tif[i] = tif
	ch.live++
	l.total += qty
	s.live++
	return c<<3 | uint32(i)
}

// unlink removes a live order from its level.
func (s *store) unlink(l *level, loc uint32) {
	c := locChunk(loc)
	ch := &s.chunks[c]
	sl := &ch.s[locSlot(loc)%chunkSlots]
	l.total -= sl.qty
	sl.qty = 0
	s.live--
	ch.live--
	if ch.live == 0 {
		s.dropChunk(l, c)
	}
}

// forEach visits live orders of l in FIFO order.
func (s *store) forEach(l *level, f func(loc uint32)) {
	for c := l.head; c != NIL; c = s.chunks[c].next {
		ch := &s.chunks[c]
		for i := uint32(ch.head); i < uint32(ch.tail); i++ {
			if ch.s[i].qty != 0 {
				f(c<<3 | i)
			}
		}
	}
}
