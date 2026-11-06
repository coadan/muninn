package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// discoverClaudeSessionSource resolves Claude Code's projects directory and
// lists every session transcript under it, including subagent transcripts
// stored beside their parent session. Claude Code keeps no separate archive.
func discoverClaudeSessionSource(explicit string, _ bool) (sessionDiscovery, error) {
	resolved, err := resolveClaudeSessionsDir(explicit)
	if err != nil {
		return sessionDiscovery{}, err
	}
	return discoverJSONLSessions("Claude Code", []string{resolved})
}

func resolveClaudeSessionsDir(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		path, err := filepath.Abs(strings.TrimSpace(explicit))
		if err != nil {
			return "", err
		}
		if !dirExists(path) {
			return "", fmt.Errorf("Claude Code sessions directory does not exist: %s", path)
		}
		return path, nil
	}
	configDir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		configDir = filepath.Join(home, ".claude")
	}
	path := filepath.Join(configDir, "projects")
	if !dirExists(path) {
		return "", fmt.Errorf("Claude Code sessions directory does not exist: %s (use --sessions-dir)", path)
	}
	return path, nil
}
