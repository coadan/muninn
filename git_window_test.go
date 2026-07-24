package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveSinceCommit(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Muninn Test"},
		{"config", "user.email", "muninn@example.test"},
	} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "fixture"}} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	timestamp, err := resolveSinceCommit(root, "HEAD")
	if err != nil {
		t.Fatalf("resolve commit: %v", err)
	}
	if timestamp.IsZero() {
		t.Fatal("resolved commit timestamp is zero")
	}
}
