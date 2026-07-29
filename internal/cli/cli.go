package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// Run executes the Muninn CLI from root.
func Run(root string, args []string) error {
	if len(args) == 0 {
		args = []string{"analyze"}
	}
	return cmdCodex(root, args)
}

func isHelpToken(arg string) bool {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func setFlagSetUsage(fs *flag.FlagSet, usageLine, summary string, examples []string) {
	fs.Usage = func() {
		if strings.TrimSpace(summary) != "" {
			fmt.Println(summary)
			fmt.Println()
		}
		fmt.Println("Usage:")
		fmt.Printf("  %s\n", usageLine)
		fmt.Println()
		fmt.Println("Flags:")
		fs.PrintDefaults()
		if len(examples) == 0 {
			return
		}
		fmt.Println()
		fmt.Println("Examples:")
		for _, example := range examples {
			if strings.TrimSpace(example) != "" {
				fmt.Printf("  %s\n", strings.TrimSpace(example))
			}
		}
	}
}

func cmdCodex(root string, args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printCodexHelp()
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "analyze":
		return cmdAnalyze(root, args[1:])
	case "failures":
		return cmdFailures(root, args[1:])
	default:
		return fmt.Errorf("unknown Muninn command: %s", args[0])
	}
}

func printCodexHelp() {
	fmt.Print(`Muninn agent-session friction analysis

Usage:
  muninn analyze [flags]
  muninn failures <tool/operation> [flags]

Available Commands:
  analyze   Analyze agent-session cost and friction for a repository
  failures  Inspect bounded failure events for one owned operation

Codex is the first session provider. The provider boundary is explicit so
Claude Code and OpenCode adapters can be added later without changing the
analysis/reporting core.

Muninn skips prompts and messages. It scans tool calls locally for fixed,
privacy-safe labels and output volume/status markers. It never prints prompts,
tool inputs, tool output, command text, absolute paths, or session identifiers.

Examples:
  muninn
  muninn analyze --repo .
  muninn analyze --repo /path/to/repository --since 24h
  muninn analyze --repo . --since 1d --compare previous
  muninn analyze --repo . --since-commit HEAD~3
  muninn analyze --repo . --task my-worktree
  muninn analyze --repo . --since 14d --include-archived
  muninn analyze --repo . --since 24h --operation repository-cli
  muninn analyze --repo . --focus structure
  muninn analyze --repo . --details
  muninn analyze --repo . --json
  muninn failures repository-cli/test --repo . --since 14d
`)
}
