// Muninn analyzes local coding-agent session metadata without exposing
// prompts, raw tool inputs, raw tool output, paths, or session identifiers.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/coadan/muninn/internal/cli"
)

func main() {
	root, err := os.Getwd()
	if err == nil {
		err = cli.Run(root, os.Args[1:])
	}
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
