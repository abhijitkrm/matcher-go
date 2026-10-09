// matcherbench — spec/BENCH.md measurement protocol (Go).
//
//	matcherbench <corpus-prefix> [--tag name]
//
// Loads <prefix>.setup.cmd.jsonl (untimed) + <prefix>.run.cmd.jsonl (measured),
// prints one RESULTS.md row. Parsing before timing; all per-op latencies (ns)
// stored, sorted, reported exactly.
package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	matcher "github.com/abhijitkrm/matcher-go"
)

func load(path string) (matcher.BookConfig, []matcher.Command) {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	sc.Scan()
	cfg := parseHeader(sc.Text())
	var cmds []matcher.Command
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		cmds = append(cmds, parseCmd(line))
	}
	return cfg, cmds
}

// minimal flat-JSON extraction (corpus grammar needs no escapes/nesting).
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

func parseHeader(line string) matcher.BookConfig {
	cfg := matcher.DefaultConfig()
	cfg.PriceMin = atoi(field(line, "pmin"), 0)
	cfg.PriceMax = atoi(field(line, "pmax"), 1_000_000)
	cfg.MaxOrders = int(atu(field(line, "max_orders"), 65_536))
	if field(line, "index") == "tree" {
		cfg.Index = matcher.IndexTree
	}
	return cfg
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
	}
	return c
}

func pct(sorted []uint64, p float64) uint64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int((float64(len(sorted)-1))*p + 0.999999)
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

func main() {
	args := os.Args[1:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: matcherbench <corpus-prefix> [--tag name]")
		os.Exit(2)
	}
	prefix := args[0]
	tag := prefix
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		tag = prefix[i+1:]
	}
	for i, a := range args {
		if a == "--tag" && i+1 < len(args) {
			tag = args[i+1]
		}
	}

	cfg, setup := load(prefix + ".setup.cmd.jsonl")
	_, run := load(prefix + ".run.cmd.jsonl")

	// Warmup: throwaway book, setup + first 10% of run.
	{
		book := matcher.NewOrderBook(cfg)
		sink := &matcher.NullSink{}
		for _, c := range setup {
			book.Apply(c, sink)
		}
		for _, c := range run[:len(run)/10] {
			book.Apply(c, sink)
		}
		_ = sink.Acc
	}

	// Timed pass.
	book := matcher.NewOrderBook(cfg)
	sink := &matcher.NullSink{}
	for _, c := range setup {
		book.Apply(c, sink)
	}
	lat := make([]uint64, len(run))
	wall := time.Now()
	for i, c := range run {
		t0 := time.Now()
		book.Apply(c, sink)
		lat[i] = uint64(time.Since(t0).Nanoseconds())
	}
	wallNs := time.Since(wall).Nanoseconds()
	_ = sink.Acc

	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	ops := len(run)
	opsS := float64(ops) / (float64(wallNs) / 1e9)
	var sum uint64
	for _, v := range lat {
		sum += v
	}
	mean := sum / uint64(ops)

	fmt.Printf("| %s | %d | %.0f | %d | %d | %d | %d | %d | %d |\n",
		tag, ops, opsS, mean, pct(lat, .50), pct(lat, .90), pct(lat, .99), pct(lat, .999), lat[len(lat)-1])
	fmt.Fprintf(os.Stderr, "env: arm64 / darwin / go\n")
}
