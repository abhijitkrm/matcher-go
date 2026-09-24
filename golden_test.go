package matcher

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func vectorsDir(t *testing.T) string {
	// repo root -> vectors/
	d, err := filepath.Abs("vectors")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

type vecHeader struct {
	Pmin      int64  `json:"pmin"`
	Pmax      int64  `json:"pmax"`
	MaxOrders int    `json:"max_orders"`
	Index     string `json:"index"`
}

func parseHeader(t *testing.T, line string) vecHeader {
	var h vecHeader
	if err := json.Unmarshal([]byte(line), &h); err != nil {
		t.Fatalf("header: %v", err)
	}
	if h.MaxOrders == 0 {
		h.MaxOrders = 65536
	}
	if h.Index == "" {
		h.Index = "ladder"
	}
	return h
}

func parseCmd(t *testing.T, line string) Command {
	var v map[string]any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		t.Fatalf("cmd: %v", err)
	}
	u := func(k string) uint64 { return uint64(v[k].(float64)) }
	i := func(k string) int64 { return int64(v[k].(float64)) }
	s := func(k string) string { return v[k].(string) }
	switch s("cmd") {
	case "new":
		var side Side
		if s("side") == "ask" {
			side = Ask
		}
		var ot OType
		if s("otype") == "market" {
			ot = Market
		}
		var tif Tif
		switch s("tif") {
		case "ioc":
			tif = Ioc
		case "fok":
			tif = Fok
		case "post_only":
			tif = PostOnly
		}
		return Command{Kind: CmdNew, OrderID: u("order_id"), Side: side, OType: ot, Price: i("price"), Qty: u("qty"), Tif: tif}
	case "cancel":
		return Command{Kind: CmdCancel, OrderID: u("order_id")}
	case "replace":
		return Command{Kind: CmdReplace, OrderID: u("order_id"), Price: i("price"), Qty: u("qty")}
	}
	t.Fatalf("bad cmd: %s", line)
	return Command{}
}

func runVector(t *testing.T, cmdPath string, kind IndexKind) [][]byte {
	f, err := os.Open(cmdPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	sc.Scan()
	h := parseHeader(t, sc.Text())
	cfg := BookConfig{PriceMin: h.Pmin, PriceMax: h.Pmax, MaxOrders: h.MaxOrders, Index: kind}
	book := NewOrderBook(cfg)
	sink := &LinesSink{}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		book.Apply(parseCmd(t, line), sink)
	}
	return sink.Lines
}

func TestGoldenVectors(t *testing.T) {
	dir := vectorsDir(t)
	mf, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Vectors []struct {
			File string `json:"file"`
		}
	}
	if err := json.Unmarshal(mf, &manifest); err != nil {
		t.Fatal(err)
	}

	checked := 0
	for _, v := range manifest.Vectors {
		cmdPath := filepath.Join(dir, v.File+".cmd.jsonl")
		evtPath := filepath.Join(dir, v.File+".evt.jsonl")

		ef, err := os.ReadFile(evtPath)
		if err != nil {
			t.Fatalf("%s: missing evt file: %v", v.File, err)
		}
		exp := bytes.Split(bytes.TrimRight(ef, "\n"), []byte("\n"))[1:] // skip header

		hdrLine, _ := os.ReadFile(cmdPath)
		hdr := parseHeader(t, string(bytes.SplitN(hdrLine, []byte("\n"), 2)[0]))

		var primary IndexKind
		modes := []IndexKind{IndexLadder}
		switch hdr.Index {
		case "both":
			modes = []IndexKind{IndexLadder, IndexTree}
			primary = IndexLadder
		case "tree":
			modes = []IndexKind{IndexTree}
			primary = IndexTree
		default:
			primary = IndexLadder
		}

		canonical := runVector(t, cmdPath, primary)
		for _, kind := range modes {
			got := runVector(t, cmdPath, kind)
			if len(got) != len(canonical) || !bytes.Equal(bytes.Join(got, []byte("\n")), bytes.Join(canonical, []byte("\n"))) {
				t.Errorf("%s [%v]: index-mode divergence", v.File, kind)
			}
		}

		checked++
		if len(canonical) != len(exp) || !bytes.Equal(bytes.Join(canonical, []byte("\n")), bytes.Join(exp, []byte("\n"))) {
			t.Errorf("%s: golden mismatch", v.File)
			n := len(exp)
			if len(canonical) > n {
				n = len(canonical)
			}
			for i := 0; i < n; i++ {
				var e, a string
				if i < len(exp) {
					e = string(exp[i])
				}
				if i < len(canonical) {
					a = string(canonical[i])
				}
				if e != a {
					t.Errorf("  line %d:\n    expected %s\n    actual   %s", i+2, e, a)
				}
			}
		}
	}
	t.Logf("golden: %d vectors passed", checked)
}
