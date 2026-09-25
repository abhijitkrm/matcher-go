// matcherrecover — e2e recovery: load a matcher-snap/1 snapshot, replay a
// command journal tail, emit the canonical tagged event journal to stdout.
// Exits nonzero on a malformed journal line (truncated tail write).
//
//	matcherrecover <snap.jsonl> <cmd-tail.jsonl>
package main

import (
	"fmt"
	"os"
	"strings"

	matcher "github.com/abhijitkrm/matcher-go"
	"github.com/abhijitkrm/matcher-go/internal/vecload"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: matcherrecover <snap.jsonl> <cmd-tail.jsonl>")
		os.Exit(2)
	}
	snapData, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	parsed, err := matcher.ParseSnapshot(string(snapData))
	if err != nil {
		panic(err)
	}
	eng := matcher.RestoreEngine(parsed)

	data, err := os.ReadFile(os.Args[2])
	if err != nil {
		panic(err)
	}
	var scratch []byte
	out := os.Stdout
	for lineno, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" || strings.Contains(line, `"format"`) {
			continue
		}
		sym, cmd, ok := vecload.Cmd(line)
		if !ok {
			fmt.Fprintf(os.Stderr, "%s:%d: malformed journal line: %s\n", os.Args[2], lineno+1, line)
			os.Exit(2)
		}
		eng.SubmitTagged(sym, cmd, func(s uint32, seq uint64, ev *matcher.Event) {
			matcher.JournalEvent(s, seq, ev, out, &scratch)
		})
	}
}
