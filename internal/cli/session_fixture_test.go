package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCodexSessionFixture(t *testing.T, dir, name string, events []any) string {
	t.Helper()
	path := filepath.Join(dir, "2026", "07", "24", name+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	var lines []string
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal fixture event: %v", err)
		}
		lines = append(lines, string(raw))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
