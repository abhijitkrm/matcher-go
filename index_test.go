package matcher

import (
	"sort"
	"testing"
)

// The ladder's cursor agrees with a sorted reference under random churn on a
// wide ladder, including whole summary words emptying and refilling.
func TestLadderBestMatchesReference(t *testing.T) {
	const span = int64(1 << 20)
	for _, side := range []Side{Bid, Ask} {
		lad := newLadderIndex(side, 0, span-1)
		set := map[int64]bool{}
		sorted := func() []int64 {
			ks := make([]int64, 0, len(set))
			for k := range set {
				ks = append(ks, k)
			}
			sort.Slice(ks, func(a, b int) bool { return ks[a] < ks[b] })
			return ks
		}
		x := uint64(0x9E3779B97F4A7C15)
		for step := 0; step < 20000; step++ {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			var p int64
			switch x % 8 {
			case 0:
				p = 0
			case 1:
				p = span - 1
			case 2:
				p = int64((x >> 8) % uint64(span))
			default:
				p = span/2 - 6000 + int64((x>>8)%12000)
			}
			if step%1000 < 520 && len(set) > 0 {
				ks := sorted()
				var q int64
				if x&16 == 0 {
					if side == Bid {
						q = ks[len(ks)-1]
					} else {
						q = ks[0]
					}
				} else {
					j := sort.Search(len(ks), func(j int) bool { return ks[j] >= p })
					if j == len(ks) {
						j--
					}
					q = ks[j]
				}
				delete(set, q)
				lad.levelMut(q).head = NIL
				lad.unlinkLevel(q)
			} else if !set[p] {
				set[p] = true
				lad.levelInsert(p).head = 0
			}
			ks := sorted()
			got, ok := lad.bestPrice()
			switch {
			case len(ks) == 0:
				if ok {
					t.Fatalf("%v step %d: best %d on empty side", side, step, got)
				}
			case side == Bid && (!ok || got != ks[len(ks)-1]), side == Ask && (!ok || got != ks[0]):
				t.Fatalf("%v step %d: best %d,%v", side, step, got, ok)
			}
			if lad.count != len(set) {
				t.Fatalf("count %d want %d", lad.count, len(set))
			}
		}
	}
}
