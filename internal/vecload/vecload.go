// Package vecload parses vector/corpus/journal lines for the cmd/ tools.
// Shared by matcherrun, matcherrecover, matchersnap. (encoding/json is
// stdlib — the matcher package itself stays dependency-free.)
package vecload

import (
	"encoding/json"

	matcher "github.com/abhijitkrm/matcher-go"
)

// Header parses a corpus/vector header line into a BookConfig.
func Header(line string) matcher.BookConfig {
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

// Cmd parses one canonical command line, returning (symbol, command, ok).
func Cmd(line string) (uint32, matcher.Command, bool) {
	var v map[string]any
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		return 0, matcher.Command{}, false
	}
	if _, ok := v["cmd"]; !ok {
		return 0, matcher.Command{}, false
	}
	var sym uint32
	if sv, ok := v["symbol"]; ok {
		sym = uint32(sv.(float64))
	}
	u := func(k string) uint64 {
		if x, ok := v[k]; ok {
			return uint64(x.(float64))
		}
		return 0
	}
	i := func(k string) int64 {
		if x, ok := v[k]; ok {
			return int64(x.(float64))
		}
		return 0
	}
	s := func(k string) string {
		if x, ok := v[k]; ok {
			if st, ok := x.(string); ok {
				return st
			}
		}
		return ""
	}
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
		return sym, matcher.Command{Kind: matcher.CmdNew, OrderID: u("order_id"), Side: side, OType: ot, Price: i("price"), Qty: u("qty"), Tif: tif}, true
	case "cancel":
		return sym, matcher.Command{Kind: matcher.CmdCancel, OrderID: u("order_id")}, true
	case "replace":
		return sym, matcher.Command{Kind: matcher.CmdReplace, OrderID: u("order_id"), Price: i("price"), Qty: u("qty")}, true
	}
	return 0, matcher.Command{}, false
}
