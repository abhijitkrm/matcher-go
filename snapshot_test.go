package matcher

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// runTagged applies symbol-tagged commands and collects canonical event lines.
func runTagged(e *Engine, cmds [][2]any) [][]byte {
	var out [][]byte
	for _, c := range cmds {
		sym := c[0].(uint32)
		cmd := c[1].(Command)
		e.SubmitTagged(sym, cmd, func(s uint32, seq uint64, ev *Event) {
			var b []byte
			ev.WriteCanonicalSym(seq, s, &b)
			out = append(out, b)
		})
	}
	return out
}

func loadEngineCmds(t *testing.T, dir string) [][2]any {
	data, err := os.ReadFile(dir + "/vectors/engine/001_multisymbol.cmd.jsonl")
	if err != nil {
		t.Skipf("vector missing: %v", err)
	}
	var cmds [][2]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(line, `"format"`) {
			continue
		}
		sym, cmd := parseCmd(t, line)
		cmds = append(cmds, [2]any{sym, cmd})
	}
	return cmds
}

func TestSnapshotContinuationByteIdentical(t *testing.T) {
	dir := vectorsDir(t)
	cmds := loadEngineCmds(t, dir)
	cfg := BookConfig{PriceMin: 0, PriceMax: 1000000, MaxOrders: 65536, Index: IndexLadder}
	split := len(cmds) / 2

	reference := NewEngine(cfg)
	expected := runTagged(reference, cmds)

	eng := NewEngine(cfg)
	var out [][]byte
	out = append(out, runTagged(eng, cmds[:split])...)
	var snap strings.Builder
	WriteEngine(eng, &snap)
	parsed, err := ParseSnapshot(snap.String())
	if err != nil {
		t.Fatal(err)
	}
	eng2 := RestoreEngine(parsed)

	// snap → restore → snap must be byte-identical.
	var snap2 strings.Builder
	WriteEngine(eng2, &snap2)
	if snap.String() != snap2.String() {
		t.Fatalf("re-snapshot not byte-identical\nleft:\n%s\nright:\n%s", snap.String(), snap2.String())
	}

	out = append(out, runTagged(eng2, cmds[split:])...)
	for i := range out {
		if !bytes.Equal(out[i], expected[i]) {
			t.Fatalf("event %d diverged:\n got %s\nwant %s", i, out[i], expected[i])
		}
	}
	if len(out) != len(expected) {
		t.Fatalf("event count: got %d want %d", len(out), len(expected))
	}
}

func TestJournalReplayReproducesEvents(t *testing.T) {
	dir := vectorsDir(t)
	cmds := loadEngineCmds(t, dir)
	cfg := BookConfig{PriceMin: 0, PriceMax: 1000000, MaxOrders: 65536, Index: IndexLadder}

	var cmdJournal bytes.Buffer
	var evtJournal bytes.Buffer
	var scratch []byte
	eng := NewEngine(cfg)
	for _, c := range cmds {
		sym := c[0].(uint32)
		cmd := c[1].(Command)
		j := NewSymCmdJournal(&cmdJournal, sym)
		j.Record(&cmd)
		eng.SubmitTagged(sym, cmd, func(s uint32, seq uint64, ev *Event) {
			JournalEvent(s, seq, ev, &evtJournal, &scratch)
		})
	}

	// Replay the command journal → must reproduce the event journal exactly.
	eng2 := NewEngine(cfg)
	var replayed [][]byte
	for _, line := range strings.Split(strings.TrimSpace(cmdJournal.String()), "\n") {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatal(err)
		}
		// reuse the vector parser by re-encoding — journal lines are canonical
		sym, cmd := parseCmd(t, line)
		eng2.SubmitTagged(sym, cmd, func(s uint32, seq uint64, ev *Event) {
			var b []byte
			ev.WriteCanonicalSym(seq, s, &b)
			replayed = append(replayed, b)
		})
	}
	var replayBuf bytes.Buffer
	for _, l := range replayed {
		replayBuf.Write(l)
		replayBuf.WriteByte('\n')
	}
	if replayBuf.String() != evtJournal.String() {
		t.Fatal("journal replay diverged")
	}
}

func TestSnapshotEmptyEngine(t *testing.T) {
	cfg := BookConfig{PriceMin: 0, PriceMax: 1000, MaxOrders: 1024, Index: IndexLadder}
	eng := NewEngine(cfg)
	var snap strings.Builder
	WriteEngine(eng, &snap)
	parsed, err := ParseSnapshot(snap.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Books) != 0 || parsed.Cfg != cfg {
		t.Fatalf("bad empty snapshot: %+v", parsed)
	}
}

// xorshift64* — same generator as fuzzgen, duplicated locally.
type fuzzRng uint64

func (r *fuzzRng) next() uint64 {
	x := uint64(*r)
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	*r = fuzzRng(x)
	return x * 0x2545F4914F6CDD1D
}
func (r *fuzzRng) below(n uint64) uint64 { return r.next() % n }

func TestSnapshotMidFuzzStream(t *testing.T) {
	cfg := BookConfig{PriceMin: 0, PriceMax: 1000, MaxOrders: 4096, Index: IndexLadder}
	rng := fuzzRng(0xC0FFEE)
	var cmds [][2]any
	for i := 0; i < 4000; i++ {
		sym := uint32(rng.below(6))
		id := rng.below(256)
		var cmd Command
		switch rng.below(3) {
		case 0:
			side := Bid
			if rng.below(2) == 1 {
				side = Ask
			}
			tif := Gtc
			switch rng.below(4) {
			case 0:
				tif = Ioc
			case 1:
				tif = Fok
			case 2:
				tif = PostOnly
			}
			cmd = NewLimit(id, side, int64(rng.below(999))+1, rng.below(200)+1, tif)
		case 1:
			cmd = Cancel(id)
		default:
			cmd = Replace(id, int64(rng.below(999))+1, rng.below(200)+1)
		}
		cmds = append(cmds, [2]any{sym, cmd})
	}

	split := 2000
	reference := NewEngine(cfg)
	expected := runTagged(reference, cmds)

	eng := NewEngine(cfg)
	out := runTagged(eng, cmds[:split])
	var snap strings.Builder
	WriteEngine(eng, &snap)
	parsed, err := ParseSnapshot(snap.String())
	if err != nil {
		t.Fatal(err)
	}
	eng2 := RestoreEngine(parsed)
	out = append(out, runTagged(eng2, cmds[split:])...)
	for i := range out {
		if !bytes.Equal(out[i], expected[i]) {
			t.Fatalf("event %d diverged:\n got %s\nwant %s", i, out[i], expected[i])
		}
	}
}
