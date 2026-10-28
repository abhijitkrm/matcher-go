package main

import matcher "github.com/abhijitkrm/matcher-go"

func prefault(b *matcher.OrderBook) { b.Prefault() }

func applyBatch(b *matcher.OrderBook, cmds []matcher.Command, sink matcher.Sink) {
	b.ApplyBatch(cmds, sink)
}
