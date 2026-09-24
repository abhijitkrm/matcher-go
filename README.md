# matcher-go

[![ci](https://github.com/abhijitkrm/matcher-go/actions/workflows/ci.yml/badge.svg)](https://github.com/abhijitkrm/matcher-go/actions/workflows/ci.yml)
[![go.dev](https://pkg.go.dev/badge/github.com/abhijitkrm/matcher-go.svg)](https://pkg.go.dev/github.com/abhijitkrm/matcher-go)
[![license](https://img.shields.io/badge/license-MIT%20OR%20Apache--2.0-blue.svg)](LICENSE-MIT)

Deterministic FIFO limit order book and matching engine core for Go —
a port of the reference [Rust implementation](https://github.com/abhijitkrm/matcher-rust).

Single-writer book per symbol, commands in, monotonically sequenced events out.
All I/O hangs off the `Sink` seam; there is no networking, persistence, or
clock dependence in the core. Zero dependencies.

```go
import matcher "github.com/abhijitkrm/matcher-go"
```

```go
book := matcher.NewOrderBook(matcher.DefaultConfig())
sink := &matcher.VecSink{}

book.Apply(matcher.NewLimit(1, matcher.Ask, 100, 10, matcher.GTC), sink)
book.Apply(matcher.NewLimit(2, matcher.Bid, 100, 4, matcher.GTC), sink)
// order 2 filled 4 @100 against order 1 and closed; order 1 keeps 6 resting.
```

```bash
go get github.com/abhijitkrm/matcher-go@latest
```

## Features

- Limit + Market orders, New / Cancel / Replace
- GTC, IOC, FOK, Post-Only
- FIFO price-time priority, maker-price execution, partial fills, sweeps
- Pooled orders, intrusive FIFO price levels, bitmap ladder index
- Thin multi-symbol `Engine` router
- Deterministic event streams — byte-identical to the
  [Rust](https://github.com/abhijitkrm/matcher-rust) and
  [C++](https://github.com/abhijitkrm/matcher-cpp) implementations, verified
  against the shared golden vector corpus (`vectors/`)

## Layout

```
*.go            library (book, engine, index, pool, level, sink, types)
cmd/matcherbench/  benchmark harness
vectors/        shared golden corpus (spec repo: github.com/abhijitkrm/matcher)
spec/           semantics contract (SPEC.md, SCHEMA.md, BENCH.md)
tools/          vectorgen — deterministic workload generator (Rust tool)
```

## Test & bench

```bash
go test ./...       # 41 golden vectors

# benchmark — vectorgen is a small Rust tool (see spec/BENCH.md)
mkdir -p bench
cargo run --release --manifest-path tools/vectorgen/Cargo.toml -- \
  --workload w2 --n 200000 --setup-n 100000 --out bench/w2
go run ./cmd/matcherbench bench/w2
```

## License

MIT OR Apache-2.0
