// matcherrun — e2e runner: apply an engine command stream, emit the canonical
// tagged event journal to stdout, optionally write a snapshot at the end.
// Part of the spec/JOURNAL.md recovery loop (with matcherrecover).
//
//	matcherrun <engine.cmd.jsonl> [--snap <path>]
package main

import (
	"fmt"
	"os"
	"strings"

	matcher "github.com/abhijitkrm/matcher-go"
	"github.com/abhijitkrm/matcher-go/internal/vecload"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: matcherrun <cmd.jsonl> [--snap <path>]")
		os.Exit(2)
	}
	path := os.Args[1]
	snapPath := ""
	for i := 2; i < len(os.Args); i += 2 {
		if os.Args[i] == "--snap" && i+1 < len(os.Args) {
			snapPath = os.Args[i+1]
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	eng := matcher.NewEngine(vecload.Header(lines[0]))

	var scratch []byte
	out := os.Stdout
	for lineno, line := range lines[1:] {
		if line == "" {
			continue
		}
		sym, cmd, ok := vecload.Cmd(line)
		if !ok {
			fmt.Fprintf(os.Stderr, "%s:%d: malformed command: %s\n", path, lineno+2, line)
			os.Exit(2)
		}
		eng.SubmitTagged(sym, cmd, func(s uint32, seq uint64, ev *matcher.Event) {
			matcher.JournalEvent(s, seq, ev, out, &scratch)
		})
	}
	if snapPath != "" {
		var snap strings.Builder
		matcher.WriteEngine(eng, &snap)
		if err := os.WriteFile(snapPath, []byte(snap.String()), 0o644); err != nil {
			panic(err)
		}
	}
}
