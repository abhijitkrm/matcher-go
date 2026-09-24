# Contributing

The contract that keeps this module honest is `spec/` + `vectors/` (vendored
from the [matcher spec repo](https://github.com/abhijitkrm/matcher)):

- **Semantics changes** start upstream in `spec/SPEC.md` plus a golden vector
  (`vectors/**.cmd.jsonl`) with its canonical `.evt.jsonl`. The implementation
  must emit that stream byte-identically — in every index mode the vector
  declares.
- **Verify**: `go test ./...` replays all golden vectors.
- **Style**: `gofmt`, `go vet`, zero dependencies.
- **Performance**: no hot-path allocations beyond the preallocated pools;
  intrusive levels, direct-indexed ladder. Benchmarks use `tools/vectorgen`
  workloads per `spec/BENCH.md` — report CPU/OS/flags, no unattributed numbers.
