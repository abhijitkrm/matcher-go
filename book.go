package matcher

// OrderBook is a single-writer deterministic FIFO price-time book.
// Data structures follow docs/LAYOUT.md (v0.3); mirrors matcher-cpp book.hpp.
type OrderBook struct {
	st   store
	omap orderMap
	bids priceIndex
	asks priceIndex
	seq  uint64
	cfg  BookConfig
	ev   Event // the event being delivered: reused, so emitting never allocates
}

func NewOrderBook(cfg BookConfig) *OrderBook {
	b := &OrderBook{
		st:   newStore(cfg.MaxOrders),
		omap: newOrderMap(cfg.MaxOrders),
		seq:  0,
		cfg:  cfg,
	}
	if cfg.Index == IndexTree {
		b.bids = priceIndex{tree: newTreeIndex(Bid)}
		b.asks = priceIndex{tree: newTreeIndex(Ask)}
	} else {
		b.bids = priceIndex{lad: newLadderIndex(Bid, cfg.PriceMin, cfg.PriceMax)}
		b.asks = priceIndex{lad: newLadderIndex(Ask, cfg.PriceMin, cfg.PriceMax)}
	}
	return b
}

// Prefault touches all order storage now (it is otherwise committed lazily
// as the book grows). Call before latency-sensitive use; costs ~128 B of
// resident memory per MaxOrders.
func (b *OrderBook) Prefault() { b.st.prefault() }

// Apply runs one command, emitting its event stream through sink.
func (b *OrderBook) Apply(cmd Command, sink Sink) { b.apply(&cmd, sink) }

func (b *OrderBook) apply(cmd *Command, sink Sink) {
	switch cmd.Kind {
	case CmdNew:
		b.newOrder(cmd, sink)
	case CmdCancel:
		b.cancel(cmd.OrderID, sink)
	case CmdReplace:
		b.replace(cmd.OrderID, cmd.Price, cmd.Qty, sink)
	}
}

func (b *OrderBook) newOrder(cmd *Command, sink Sink) {
	oid := cmd.OrderID
	// SPEC §4.3 validation precedence.
	if cmd.Qty == 0 {
		b.reject(sink, oid, InvalidQty)
		return
	}
	if cmd.OType == Limit && !b.priceOk(cmd.Price) {
		b.reject(sink, oid, InvalidPrice)
		return
	}
	if b.omap.contains(oid) {
		b.reject(sink, oid, DuplicateOrderID)
		return
	}
	if b.st.live >= b.cfg.MaxOrders {
		b.reject(sink, oid, BookFull)
		return
	}
	if cmd.OType == Limit {
		switch cmd.Tif {
		case PostOnly:
			if b.wouldCross(cmd.Side, cmd.Price) {
				b.reject(sink, oid, PostOnlyWouldCross)
				return
			}
		case Fok:
			if b.fillable(cmd.Side, cmd.Price) < cmd.Qty {
				b.reject(sink, oid, FokCannotFill)
				return
			}
		}
	}

	remaining := cmd.Qty
	var bound int64
	hasBound := cmd.OType == Limit
	if hasBound {
		bound = cmd.Price
	}
	remaining = b.cross(cmd.Side, hasBound, bound, oid, remaining, sink)

	switch {
	case remaining == 0:
		b.ev = Event{Kind: EvClosed, OrderID: oid, Reason: uint8(Filled)}
		b.emitEv(sink)
	case cmd.OType == Limit && (cmd.Tif == Gtc || cmd.Tif == PostOnly):
		b.rest(oid, cmd.Side, cmd.Price, remaining, cmd.Tif)
		b.ev = Event{Kind: EvAccepted, OrderID: oid, LeavesQty: remaining}
		b.emitEv(sink)
	default:
		b.ev = Event{Kind: EvClosed, OrderID: oid, Reason: uint8(Expired)}
		b.emitEv(sink)
	}
}

func (b *OrderBook) cancel(oid uint64, sink Sink) {
	e := b.omap.get(oid)
	if e < 0 {
		b.reject(sink, oid, UnknownOrderID)
		return
	}
	loc := b.omap.entries[e].val
	ch := &b.st.chunks[locChunk(loc)]
	price := ch.price
	own := b.ownIndex(ch.side)
	if lv := own.levelMut(price); lv != nil {
		b.st.unlink(lv, loc)
	}
	own.unlinkLevel(price)
	b.omap.remove(oid)
	b.ev = Event{Kind: EvClosed, OrderID: oid, Reason: uint8(Cancelled)}
	b.emitEv(sink)
}

// replace is the ladder fast path of SPEC §4.3 replace, expanded by hand: Go's
// calling convention saves and restores every live register around each call,
// and the inliner's budget (80) is far below the cost of the helpers, so the
// unlink, level bookkeeping, cross check, chunk allocation and push that a
// replace performs are written out here instead of called. Semantics are those
// of replaceGeneric (a tree index takes that path).
func (b *OrderBook) replace(oid uint64, price int64, qty uint64, sink Sink) {
	own := b.bids.lad
	if own == nil {
		b.replaceGeneric(oid, price, qty, sink)
		return
	}
	e := b.omap.get(oid)
	if e < 0 {
		b.reject(sink, oid, UnknownOrderID)
		return
	}
	loc := b.omap.entries[e].val
	if qty == 0 {
		b.reject(sink, oid, InvalidQty)
		return
	}
	if !b.priceOk(price) {
		b.reject(sink, oid, InvalidPrice)
		return
	}
	st := &b.st
	c := locChunk(loc)
	ch := &st.chunks[c]
	si := locSlot(loc) % chunkSlots
	oldPrice, oldQty, side, tif := ch.price, ch.s[si].qty, ch.side, ch.tif[si]
	opp := b.asks.lad
	if side == Ask {
		own, opp = opp, own
	}
	i := int(oldPrice - own.base)
	lv := &own.levels[i]

	if price == oldPrice && qty <= oldQty {
		lv.total -= oldQty - qty
		ch.s[si].qty = qty
		b.ev = Event{Kind: EvReplaced, OrderID: oid, Price: price, Qty: qty}
		b.emitEv(sink)
		return
	}

	// Leave the book (losing time priority): store.unlink + dropChunk +
	// ladderIndex.unlinkLevel/clear.
	lv.total -= oldQty
	ch.s[si].qty = 0
	st.live--
	ch.live--
	if ch.live == 0 {
		if ch.prev != NIL {
			st.chunks[ch.prev].next = ch.next
		} else {
			lv.head = ch.next
		}
		if ch.next != NIL {
			st.chunks[ch.next].prev = ch.prev
		} else {
			lv.tail = ch.prev
		}
		ch.next = st.freeChunk
		st.freeChunk = c
	}
	if lv.head == NIL {
		w := i >> 6
		own.bits[w] &^= 1 << (uint(i) & 63)
		if own.bits[w] == 0 {
			own.summary[w>>6] &^= 1 << (uint(w) & 63)
		}
		own.count--
		if own.count == 0 {
			own.best = NIL
		} else if uint32(i) == own.best {
			own.best = own.rescan(i)
		}
	}

	// Re-cross at the new price; cross is called only when the opposite best
	// price is marketable (otherwise it returns qty untouched).
	removes := b.omap.removes
	remaining := qty
	if ob := opp.best; ob != NIL {
		bp := opp.base + int64(ob)
		if (side == Bid && bp <= price) || (side == Ask && bp >= price) {
			remaining = b.cross(side, true, price, oid, qty, sink)
		}
	}
	if remaining == 0 {
		b.omap.remove(oid)
		b.ev = Event{Kind: EvClosed, OrderID: oid, Reason: uint8(Filled)}
		b.emitEv(sink)
		return
	}

	// Rest the remainder at the back of the new level: levelInsert + store.push
	// (+ allocChunk).
	j := int(price - own.base)
	nlv := &own.levels[j]
	if nlv.head == NIL {
		own.bits[j>>6] |= 1 << (uint(j) & 63)
		own.summary[j>>12] |= 1 << (uint(j>>6) & 63)
		own.count++
		if own.best == NIL || (side == Bid && uint32(j) > own.best) || (side == Ask && uint32(j) < own.best) {
			own.best = uint32(j)
		}
	}
	tc := nlv.tail
	if tc == NIL || st.chunks[tc].tail == chunkSlots {
		var n uint32
		if st.freeChunk != NIL {
			n = st.freeChunk
			st.freeChunk = st.chunks[n].next
		} else {
			n = st.used
			st.used++
			if int(n) == len(st.chunks) {
				st.chunks = st.chunks[:n+1] // within capacity: no allocation
			}
		}
		nc := &st.chunks[n]
		nc.price = price
		nc.prev, nc.next = tc, NIL
		nc.head, nc.tail, nc.live = 0, 0, 0
		nc.side = side
		if tc != NIL {
			st.chunks[tc].next = n
		} else {
			nlv.head = n
		}
		nlv.tail = n
		tc = n
	}
	pch := &st.chunks[tc]
	k := pch.tail % chunkSlots
	pch.tail++
	pch.s[k] = slot{oid, remaining}
	pch.tif[k] = tif
	pch.live++
	nlv.total += remaining
	st.live++
	nloc := tc<<3 | uint32(k)
	if b.omap.removes == removes {
		b.omap.entries[e].val = nloc // no entry moved: skip the re-probe
	} else {
		b.omap.insert(oid, nloc)
	}
	b.ev = Event{Kind: EvReplaced, OrderID: oid, Price: price, Qty: remaining}
	b.emitEv(sink)
}

// replaceGeneric is replace for any price index (the tree fallback); the ladder
// fast path is replace below.
func (b *OrderBook) replaceGeneric(oid uint64, price int64, qty uint64, sink Sink) {
	e := b.omap.get(oid)
	if e < 0 {
		b.reject(sink, oid, UnknownOrderID)
		return
	}
	loc := b.omap.entries[e].val
	if qty == 0 {
		b.reject(sink, oid, InvalidQty)
		return
	}
	if !b.priceOk(price) {
		b.reject(sink, oid, InvalidPrice)
		return
	}
	ch := &b.st.chunks[locChunk(loc)]
	si := locSlot(loc) % chunkSlots
	oldPrice, oldQty, side, tif := ch.price, ch.s[si].qty, ch.side, ch.tif[si]
	own := b.ownIndex(side)

	if price == oldPrice && qty <= oldQty {
		if lv := own.levelMut(price); lv != nil {
			lv.total -= oldQty - qty
		}
		ch.s[si].qty = qty
		b.ev = Event{Kind: EvReplaced, OrderID: oid, Price: price, Qty: qty}
		b.emitEv(sink)
		return
	}

	// Leave the book (losing time priority), re-cross at the new price, rest
	// any remainder at the back of the new level. The map entry is kept
	// across cross() so the id stays reserved; it is rewritten (or removed)
	// afterwards because cross() may move map entries.
	if lv := own.levelMut(oldPrice); lv != nil {
		b.st.unlink(lv, loc)
	}
	own.unlinkLevel(oldPrice)

	removes := b.omap.removes
	remaining := b.cross(side, true, price, oid, qty, sink)

	if remaining == 0 {
		b.omap.remove(oid)
		b.ev = Event{Kind: EvClosed, OrderID: oid, Reason: uint8(Filled)}
		b.emitEv(sink)
		return
	}
	nloc := b.st.push(own.levelInsert(price), side, price, oid, remaining, tif)
	if b.omap.removes == removes {
		b.omap.entries[e].val = nloc // no entry moved: skip the re-probe
	} else {
		b.omap.insert(oid, nloc)
	}
	b.ev = Event{Kind: EvReplaced, OrderID: oid, Price: price, Qty: remaining}
	b.emitEv(sink)
}

// cross walks the opposite side and returns the quantity left of qty.
// hasBound=false means market (match all available depth).
func (b *OrderBook) cross(side Side, hasBound bool, bound int64, taker uint64, qty uint64, sink Sink) uint64 {
	opp := b.bids
	if side == Bid {
		opp = b.asks
	}
	for {
		bp, ok := opp.bestPrice()
		if !ok {
			return qty
		}
		if hasBound {
			if side == Bid && bp > bound {
				return qty
			}
			if side == Ask && bp < bound {
				return qty
			}
		}
		lv := opp.levelMut(bp)
		if lv == nil {
			return qty
		}
		for lv.head != NIL {
			c := lv.head
			ch := &b.st.chunks[c]
			// front: advance the head chunk's cursor past dead slots (the
			// head chunk always holds a live slot).
			h := ch.head % chunkSlots
			for ch.s[h].qty == 0 {
				h = (h + 1) % chunkSlots
			}
			ch.head = h
			m := &ch.s[h]
			mid, mqty := m.id, m.qty
			q := min(qty, mqty)
			b.ev = Event{Kind: EvTrade, Maker: mid, Taker: taker, Price: bp, Qty: q}
			b.emitEv(sink)
			lv.total -= q
			m.qty = mqty - q
			qty -= q
			if mqty == q {
				// m.qty == 0: the slot is now dead.
				b.st.live--
				ch.live--
				if ch.live == 0 {
					b.st.dropChunk(lv, c)
				}
				b.omap.remove(mid)
				b.ev = Event{Kind: EvClosed, OrderID: mid, Reason: uint8(Filled)}
				b.emitEv(sink)
			}
			if qty == 0 {
				break
			}
		}
		if lv.empty() {
			opp.unlinkLevel(bp)
		}
		if qty == 0 {
			return 0
		}
	}
}

// rest inserts a remainder as a resting order (room guaranteed by book_full).
func (b *OrderBook) rest(oid uint64, side Side, price int64, qty uint64, tif Tif) {
	loc := b.st.push(b.ownIndex(side).levelInsert(price), side, price, oid, qty, tif)
	b.omap.insert(oid, loc)
}

func (b *OrderBook) ownIndex(side Side) priceIndex {
	if side == Bid {
		return b.bids
	}
	return b.asks
}

func (b *OrderBook) priceOk(price int64) bool {
	if b.cfg.Index == IndexTree {
		return price > 0
	}
	return price >= b.cfg.PriceMin && price <= b.cfg.PriceMax
}

func (b *OrderBook) wouldCross(side Side, price int64) bool {
	if side == Bid {
		bp, ok := b.asks.bestPrice()
		return ok && price >= bp
	}
	bp, ok := b.bids.bestPrice()
	return ok && price <= bp
}

func (b *OrderBook) fillable(side Side, price int64) uint64 {
	var lo, hi int64
	if b.cfg.Index == IndexTree {
		lo, hi = -1<<62, 1<<62
	} else {
		lo, hi = b.cfg.PriceMin, b.cfg.PriceMax
	}
	if side == Bid {
		return b.asks.sumRange(lo, price)
	}
	return b.bids.sumRange(price, hi)
}

// emit delivers ev with the next seq. The sink gets a pointer to the book's
// reused event, valid only for the call (Sink's contract), so emitting never
// allocates.
func (b *OrderBook) emit(sink Sink, ev Event) {
	b.seq++
	b.ev = ev
	sink.OnEvent(b.seq, &b.ev)
}

// emitEv delivers b.ev, which the caller has just filled in place (no by-value
// Event temporary, no copy).
func (b *OrderBook) emitEv(sink Sink) {
	b.seq++
	sink.OnEvent(b.seq, &b.ev)
}

func (b *OrderBook) reject(sink Sink, oid uint64, reason RejectReason) {
	b.ev = Event{Kind: EvRejected, OrderID: oid, Reason: uint8(reason)}
	b.emitEv(sink)
}

// ---- query API (SPEC §8) ----

type OrderInfo struct {
	OrderID uint64
	Side    Side
	Price   int64
	Qty     uint64
}

func (b *OrderBook) BestBid() (int64, bool) { return b.bids.bestPrice() }
func (b *OrderBook) BestAsk() (int64, bool) { return b.asks.bestPrice() }
func (b *OrderBook) OrderCount() int        { return b.st.live }
func (b *OrderBook) Seq() uint64            { return b.seq }
func (b *OrderBook) LevelCount(s Side) int  { return b.ownIndex(s).len() }
func (b *OrderBook) Depth(s Side, n int) []levelDepth {
	return b.ownIndex(s).depth(n)
}

func (b *OrderBook) Order(oid uint64) (OrderInfo, bool) {
	e := b.omap.get(oid)
	if e < 0 {
		return OrderInfo{}, false
	}
	loc := b.omap.entries[e].val
	ch := &b.st.chunks[locChunk(loc)]
	o := &ch.s[locSlot(loc)]
	return OrderInfo{OrderID: o.id, Side: ch.side, Price: ch.price, Qty: o.qty}, true
}

// RestingOrder is one live order for snapshot serialization.
type RestingOrder struct {
	OrderID uint64
	Side    Side
	Price   int64
	Qty     uint64
	Tif     Tif
}

// RestingOrders returns all live orders in book order: bids best→worst then
// asks best→worst, FIFO within each level.
func (b *OrderBook) RestingOrders() []RestingOrder {
	out := make([]RestingOrder, 0, b.st.live)
	for _, side := range []Side{Bid, Ask} {
		idx := b.ownIndex(side)
		for _, d := range idx.depth(1 << 30) {
			b.st.forEach(idx.levelMut(d.Price), func(loc uint32) {
				ch := &b.st.chunks[locChunk(loc)]
				i := locSlot(loc)
				out = append(out, RestingOrder{
					OrderID: ch.s[i].id, Side: side, Price: ch.price, Qty: ch.s[i].qty, Tif: ch.tif[i],
				})
			})
		}
	}
	return out
}

// Restore rebuilds a book from a snapshot: same config, explicit seq, and
// resting orders replayed in snapshot order (bids then asks, FIFO per level).
func Restore(cfg BookConfig, seq uint64, orders []RestingOrder) *OrderBook {
	b := NewOrderBook(cfg)
	b.seq = seq
	for _, o := range orders {
		if b.st.live >= cfg.MaxOrders {
			break
		}
		b.rest(o.OrderID, o.Side, o.Price, o.Qty, o.Tif)
	}
	return b
}
