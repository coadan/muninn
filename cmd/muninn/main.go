// Muninn analyzes local coding-agent session metadata without exposing
// prompts, raw tool inputs, raw tool output, paths, or session identifiers.
package main

import (
	"errors"
	"flag"
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
	if writeErr := cli.WriteError(os.Stderr, err); writeErr != nil {
		_, _ = os.Stderr.WriteString("{\"schemaVersion\":1,\"status\":\"error\",\"error\":{\"code\":\"error-report-failed\"}}\n")
	}
	os.Exit(1)
}
