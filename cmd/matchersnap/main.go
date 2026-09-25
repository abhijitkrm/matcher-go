// snapdump — apply an engine command stream, print the snapshot to stdout.
// Cross-language snapshot parity check: every implementation must emit
// byte-identical output for the same input (spec/JOURNAL.md).
//
//	matchersnap <engine.cmd.jsonl>
package main

import (
	"fmt"
	"os"
	"strings"

	matcher "github.com/abhijitkrm/matcher-go"
	"github.com/abhijitkrm/matcher-go/internal/vecload"
)

func main() {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	eng := matcher.NewEngine(vecload.Header(lines[0]))
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		sym, cmd, ok := vecload.Cmd(line)
		if !ok {
			fmt.Fprintf(os.Stderr, "malformed command: %s\n", line)
			os.Exit(2)
		}
		eng.SubmitTagged(sym, cmd, func(uint32, uint64, *matcher.Event) {})
	}
	var snap strings.Builder
	matcher.WriteEngine(eng, &snap)
	fmt.Print(snap.String())
}
