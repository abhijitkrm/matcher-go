// snapdump — apply an engine command stream, print the snapshot to stdout.
// Cross-language snapshot parity check: every implementation must emit
// byte-identical output for the same input (spec/JOURNAL.md).
//
//	matchersnap <engine.cmd.jsonl>
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	matcher "github.com/abhijitkrm/matcher-go"
)

func parseHeader(line string) matcher.BookConfig {
	var v map[string]any
	json.Unmarshal([]byte(line), &v)
	cfg := matcher.DefaultConfig()
	if x, ok := v["pmin"]; ok {
		cfg.PriceMin = int64(x.(float64))
	}
	if x, ok := v["pmax"]; ok {
		cfg.PriceMax = int64(x.(float64))
	}
	if x, ok := v["max_orders"]; ok {
		cfg.MaxOrders = int(x.(float64))
	}
	if v["index"] == "tree" {
		cfg.Index = matcher.IndexTree
	}
	return cfg
}

func parseCmd(line string) (uint32, matcher.Command) {
	var v map[string]any
	json.Unmarshal([]byte(line), &v)
	var sym uint32
	if sv, ok := v["symbol"]; ok {
		sym = uint32(sv.(float64))
	}
	u := func(k string) uint64 { return uint64(v[k].(float64)) }
	i := func(k string) int64 { return int64(v[k].(float64)) }
	s := func(k string) string { return v[k].(string) }
	switch s("cmd") {
	case "new":
		var side matcher.Side
		if s("side") == "ask" {
			side = matcher.Ask
		}
		var ot matcher.OType
		if s("otype") == "market" {
			ot = matcher.Market
		}
		var tif matcher.Tif
		switch s("tif") {
		case "ioc":
			tif = matcher.Ioc
		case "fok":
			tif = matcher.Fok
		case "post_only":
			tif = matcher.PostOnly
		}
		return sym, matcher.Command{Kind: matcher.CmdNew, OrderID: u("order_id"), Side: side, OType: ot, Price: i("price"), Qty: u("qty"), Tif: tif}
	case "cancel":
		return sym, matcher.Command{Kind: matcher.CmdCancel, OrderID: u("order_id")}
	default:
		return sym, matcher.Command{Kind: matcher.CmdReplace, OrderID: u("order_id"), Price: i("price"), Qty: u("qty")}
	}
}

func main() {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	eng := matcher.NewEngine(parseHeader(lines[0]))
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		sym, cmd := parseCmd(line)
		eng.SubmitTagged(sym, cmd, func(uint32, uint64, *matcher.Event) {})
	}
	var snap strings.Builder
	matcher.WriteEngine(eng, &snap)
	fmt.Print(snap.String())
}
