package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverCodexSessionsReturnsOnlySortedRollouts(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a", "first.jsonl")
	second := filepath.Join(root, "b", "second.JSONL")
	for _, path := range []string{second, first} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}

	discovery, err := discoverCodexSessions([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(discovery.Dirs, []string{root}) ||
		!reflect.DeepEqual(discovery.Files, []string{first, second}) {
		t.Fatalf("Codex discovery mismatch: %#v", discovery)
	}
}
