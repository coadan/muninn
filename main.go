package main

// Muninn analyzes local Codex rollout metadata without exposing
// prompts, raw tool inputs, raw tool output, paths, or session identifiers.

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"analyze"}
	}
	if err := cmdCodex(root, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
