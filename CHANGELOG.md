# Changelog

## Unreleased

- `replace` has a hand-expanded ladder fast path (unlink, level bookkeeping,
  a guard that skips `cross` unless the opposite best is marketable, chunk
  allocation and push written out): Go's calling convention spills every live
  register around a call and the inliner budget (80) is far below the cost of
  the helpers. The tree index keeps the generic path (`replaceGeneric`).
  Events are built directly in the book's reused `Event` (no by-value temporary
  and copy). PGO profile regenerated from the new code. W5 1k 60.8M -> 67.8M,
  W4 26.5M -> 30.0M, W5 100k +9%, W5 1M +19%.
- New hot-path layout (`../matcher/docs/LAYOUT.md`): resting orders live
  inline in 128-byte per-level chunks (`store.go`), `omap.go` gets a 1-byte
  tag array; `pool.go` and `level.go` are gone.
- `OrderBook.ApplyBatch()` and `Prefault()`. The look-ahead pipeline starts
  at 2^18 live orders (it lost 30% on W4 at ~100k); `-tags matcher_noprefetch`
  disables it.
- `matcherbench --mode latency|throughput|scale` (BENCH.md v2) and a PGO
  profile (`cmd/matcherbench/default.pgo`, about +7%).
- Throughput, Apple M1, Go 1.27: 61M ops/s on one core (1k live orders),
  26M on the mixed workload; 8-shard aggregate 61M (mixed) to 223M (1k live).
  See `../matcher/docs/RESULTS.md`.

## v0.2.0 — 2026-10-10

- Journals and snapshots (`spec/JOURNAL.md`): command/event journal,
  `matcher-snap/1` snapshots, restore; `matcherrun`, `matcherrecover`,
  `matchersnap`; `matcherfuzz` differential-fuzz harness.
- `engine: true` golden vectors (multi-symbol `Engine`).
- Vector `edge/042_dense_map_churn` (43 golden vectors).
- `depth` sizes its result by the levels present, not the requested count
  (asking for every level allocated 16 GiB).
- Events are delivered through one reused `Event` per book: emitting no
  longer allocates.
- `cmd/matcherbench` is committed (an unanchored ignore rule hid it).
- The price ladder finds the next best price through a summary bitmap,
  and an emptied side resets at once. A side whose last level emptied
  used to scan the whole ladder: W3 at 1M ops (the book drains) ran at
  0.36M and now runs at 7.2M orders/s. Output unchanged; a randomized
  test checks the cursor against a sorted reference.

## v0.1.0 — 2026-09-24

Initial release.

- `OrderBook` + `Engine`, `Sink` event seam
- Limit/Market, New/Cancel/Replace, GTC/IOC/FOK/Post-Only
- FIFO price-time priority, maker-price execution
- Pooled orders, intrusive FIFO levels, bitmap ladder index
- 41 golden vectors passing — byte-identical to matcher-rust and matcher-cpp
- Zero dependencies
