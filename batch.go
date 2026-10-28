package matcher

// Look-ahead distances and the live-order threshold of the batch pipeline
// (docs/LAYOUT.md §4).
const (
	dMap   = 16
	dChunk = 8
	dLevel = 4
)

// lookahead absorbs the look-ahead loads so the compiler keeps them. Go has
// no prefetch intrinsic: an ordinary load whose result is only folded here
// lets the out-of-order core overlap the miss with the current command.
var lookahead uint64

// ApplyBatch applies cmds in order. The event stream and resulting state are
// identical to calling Apply on each; once the book holds ≥ 262144 live
// orders (no longer cache-resident) the batch also looks ahead and touches
// the memory upcoming commands will need — map slot, then the order's chunk
// (or the level for a New), then its level (or the level's tail chunk) —
// hiding cache misses behind the current command. Look-ahead only reads,
// possibly stale state, and never writes book state.
func (b *OrderBook) ApplyBatch(cmds []Command, sink Sink) {
	if b.st.live < prefetchMinLive {
		for i := range cmds {
			b.apply(&cmds[i], sink)
		}
		return
	}
	var acc uint64
	n := len(cmds)
	for i := range cmds {
		if i+dMap < n {
			acc += b.touchMap(cmds[i+dMap].OrderID)
		}
		if lookaheadStages >= 2 && i+dChunk < n {
			acc += b.touchChunk(&cmds[i+dChunk])
		}
		if lookaheadStages >= 3 && i+dLevel < n {
			acc += b.touchLevel(&cmds[i+dLevel])
		}
		b.apply(&cmds[i], sink)
	}
	lookahead += acc
}

func (b *OrderBook) touchMap(k uint64) uint64 {
	i := mix(k) & b.omap.mask
	return uint64(b.omap.tags[i]) + b.omap.entries[i].key
}

func (b *OrderBook) touchChunk(c *Command) uint64 {
	if c.Kind == CmdNew {
		if c.OType == Limit {
			if lv := b.ownIndex(c.Side).peek(c.Price); lv != nil {
				return uint64(lv.tail)
			}
		}
		return 0
	}
	if e := b.omap.get(c.OrderID); e >= 0 {
		return uint64(b.st.chunks[locChunk(b.omap.entries[e].val)].live)
	}
	return 0
}

func (b *OrderBook) touchLevel(c *Command) uint64 {
	if c.Kind == CmdNew {
		// Resting appends to the level's tail chunk.
		if c.OType == Limit {
			if lv := b.ownIndex(c.Side).peek(c.Price); lv != nil && lv.tail != NIL {
				return uint64(b.st.chunks[lv.tail].tail)
			}
		}
		return 0
	}
	e := b.omap.get(c.OrderID)
	if e < 0 {
		return 0
	}
	ch := &b.st.chunks[locChunk(b.omap.entries[e].val)]
	own := b.ownIndex(ch.side)
	var acc uint64
	if lv := own.peek(ch.price); lv != nil {
		acc += lv.total
	}
	if c.Kind == CmdReplace && c.Price != ch.price {
		if lv := own.peek(c.Price); lv != nil {
			acc += lv.total
		}
	}
	return acc
}
