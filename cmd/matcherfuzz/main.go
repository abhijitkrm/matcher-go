// matcherfuzz — replay a fuzzgen corpus and print the canonical event
// stream (symbol-tagged for engine corpora) to stdout. scripts/diffuzz.sh
// byte-diffs this output across implementations.
//
//	matcherfuzz <corpus.cmd.jsonl>
package main

import (
	"bufio"
	"os"
	"strings"

	matcher "github.com/abhijitkrm/matcher-go"
)

func field(line, key string) string {
	pat := `"` + key + `":`
	i := strings.Index(line, pat)
	if i < 0 {
		return ""
	}
	rest := line[i+len(pat):]
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		return rest[1 : 1+end]
	}
	end := strings.IndexAny(rest, ",}")
	if end < 0 {
		return rest
	}
	return strings.TrimSpace(rest[:end])
}

func atoi(s string, def int64) int64 {
	if s == "" {
		return def
	}
	var v, sign int64 = 0, 1
	for i, c := range s {
		if i == 0 && c == '-' {
			sign = -1
			continue
		}
		v = v*10 + int64(c-'0')
	}
	return v * sign
}

func atu(s string, def uint64) uint64 {
	if s == "" {
		return def
	}
	var v uint64
	for _, c := range s {
		v = v*10 + uint64(c-'0')
	}
	return v
}

func parseCmd(line string) matcher.Command {
	var c matcher.Command
	switch field(line, "cmd") {
	case "new":
		c.Kind = matcher.CmdNew
		c.OrderID = atu(field(line, "order_id"), 0)
		if field(line, "side") == "ask" {
			c.Side = matcher.Ask
		}
		if field(line, "otype") == "market" {
			c.OType = matcher.Market
		}
		switch field(line, "tif") {
		case "ioc":
			c.Tif = matcher.Ioc
		case "fok":
			c.Tif = matcher.Fok
		case "post_only":
			c.Tif = matcher.PostOnly
		}
		c.Price = atoi(field(line, "price"), 0)
		c.Qty = atu(field(line, "qty"), 0)
	case "cancel":
		c.Kind = matcher.CmdCancel
		c.OrderID = atu(field(line, "order_id"), 0)
	case "replace":
		c.Kind = matcher.CmdReplace
		c.OrderID = atu(field(line, "order_id"), 0)
		c.Price = atoi(field(line, "price"), 0)
		c.Qty = atu(field(line, "qty"), 0)
	default:
		panic("bad command line: " + line)
	}
	return c
}

func main() {
	path := os.Args[1]
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	sc.Scan()
	hdr := sc.Text()
	cfg := matcher.DefaultConfig()
	cfg.PriceMin = atoi(field(hdr, "pmin"), 0)
	cfg.PriceMax = atoi(field(hdr, "pmax"), 1_000_000)
	cfg.MaxOrders = int(atu(field(hdr, "max_orders"), 65_536))
	cfg.Index = matcher.IndexLadder

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	if field(hdr, "engine") == "true" {
		eng := matcher.NewEngine(cfg)
		for sc.Scan() {
			line := sc.Text()
			if line == "" {
				continue
			}
			sym := uint32(atu(field(line, "symbol"), 0))
			eng.SubmitTagged(sym, parseCmd(line), func(s uint32, seq uint64, ev *matcher.Event) {
				var buf []byte
				ev.WriteCanonicalSym(seq, s, &buf)
				buf = append(buf, '\n')
				out.Write(buf)
			})
		}
		return
	}

	book := matcher.NewOrderBook(cfg)
	sink := fnSink(func(seq uint64, ev *matcher.Event) {
		var buf []byte
		ev.WriteCanonical(seq, &buf)
		buf = append(buf, '\n')
		out.Write(buf)
	})
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		book.Apply(parseCmd(line), sink)
	}
}

type fnSink func(uint64, *matcher.Event)

func (f fnSink) OnEvent(seq uint64, ev *matcher.Event) { f(seq, ev) }
