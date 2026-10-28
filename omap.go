package matcher

// orderMap: order_id -> loc, open addressing with linear probing over two
// arrays (docs/LAYOUT.md §3): a 1-byte tag per slot (0 = empty, else
// 0x80 | 7 hash bits) and the {key, val} entries. Absent-key probes — every
// New's duplicate check — touch only the dense tag array; a hit costs one
// tag byte plus one 16-byte entry. Backward-shift deletion, no tombstones.
type mapEntry struct {
	key uint64
	val uint32
}

type orderMap struct {
	tags    []uint8
	entries []mapEntry
	mask    uint64
	len     int
	// removes is bumped by every remove(): an entry index from get() stays
	// valid until the next remove (backward shift may relocate entries;
	// inserting a new key never moves existing ones).
	removes uint64
}

func newOrderMap(liveMax int) orderMap {
	cp := 16
	for cp < liveMax*2 {
		cp <<= 1
	}
	return orderMap{
		tags:    make([]uint8, cp),
		entries: make([]mapEntry, cp),
		mask:    uint64(cp - 1),
	}
}

func mix(k uint64) uint64 {
	h := k * 0x9E3779B97F4A7C15
	return h ^ (h >> 32)
}

// Slot from the low bits, tag from the top 7 (independent of the slot).
func tagOf(h uint64) uint8 { return uint8(h>>57) | 0x80 }

// get returns the entry index holding k, or -1.
func (m *orderMap) get(k uint64) int {
	h := mix(k)
	t := tagOf(h)
	for i := h & m.mask; m.tags[i] != 0; i = (i + 1) & m.mask {
		if m.tags[i] == t && m.entries[i].key == k {
			return int(i)
		}
	}
	return -1
}

func (m *orderMap) contains(k uint64) bool { return m.get(k) >= 0 }

func (m *orderMap) insert(k uint64, v uint32) {
	h := mix(k)
	t := tagOf(h)
	i := h & m.mask
	for {
		ti := m.tags[i]
		if ti == 0 {
			break
		}
		if ti == t && m.entries[i].key == k {
			m.entries[i].val = v
			return
		}
		i = (i + 1) & m.mask
	}
	m.tags[i] = t
	m.entries[i] = mapEntry{k, v}
	m.len++
}

func (m *orderMap) remove(k uint64) {
	h := mix(k)
	t := tagOf(h)
	mask := m.mask
	tags, entries := m.tags, m.entries
	i := h & mask
	for {
		ti := tags[i]
		if ti == 0 {
			return
		}
		if ti == t && entries[i].key == k {
			break
		}
		i = (i + 1) & mask
	}
	tags[i] = 0
	m.len--
	m.removes++
	// Backward-shift deletion (Knuth 6.4R). Scan forward from the hole; move
	// back any entry whose probe chain crosses it. An entry that cannot move
	// leaves the hole where it is — only the scan advances.
	hole := i
	for j := (i + 1) & mask; tags[j] != 0; j = (j + 1) & mask {
		home := mix(entries[j].key) & mask
		var inInterval bool
		if hole < j {
			inInterval = home > hole && home <= j
		} else {
			inInterval = home > hole || home <= j
		}
		if !inInterval {
			tags[hole] = tags[j]
			entries[hole] = entries[j]
			tags[j] = 0
			hole = j
		}
	}
}
