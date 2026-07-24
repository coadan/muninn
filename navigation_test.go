package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeRepositoryTargetsCollapsesBWBWorktreesToCachedRepoOwner(t *testing.T) {
	root := t.TempDir()
	relativeSource := filepath.Join("bases", "flows-api", "src", "breyta", "flows_api", "commands.clj")
	for _, path := range []string{
		filepath.Join(root, ".worktrees", "task-one", "breyta", relativeSource),
		filepath.Join(root, ".worktrees", "task-two", "breyta", relativeSource),
		filepath.Join(root, ".workbench", "worktrees", "legacy-task", "breyta", relativeSource),
		filepath.Join(root, ".workbench", "repos", "breyta", relativeSource),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir target parent: %v", err)
		}
		if err := os.WriteFile(path, []byte("(ns example)\n"), 0o644); err != nil {
			t.Fatalf("write target: %v", err)
		}
	}
	candidates := []string{
		filepath.Join(root, ".worktrees", "task-one", "breyta", relativeSource),
		filepath.Join(root, ".worktrees", "task-two", "breyta", relativeSource),
		filepath.Join(root, ".workbench", "worktrees", "legacy-task", "breyta", relativeSource),
		filepath.Join(root, ".workbench", "repos", "breyta", relativeSource),
	}
	got := normalizeRepositoryTargets(candidates, root, root)
	want := []string{filepath.ToSlash(filepath.Join(".workbench", "repos", "breyta", relativeSource))}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected canonical targets: got=%q want=%q", got, want)
	}
}

func TestCanonicalRepositoryTargetPreservesNonWorkbenchPaths(t *testing.T) {
	root := t.TempDir()
	if got, want := canonicalRepositoryTarget(root, filepath.Join("bwb-src", "main.go")), "bwb-src/main.go"; got != want {
		t.Fatalf("unexpected ordinary target: got=%q want=%q", got, want)
	}
	worktreeTarget := filepath.Join(".worktrees", "task", "repo", "src", "main.go")
	if got, want := canonicalRepositoryTarget(root, worktreeTarget), filepath.ToSlash(worktreeTarget); got != want {
		t.Fatalf("expected non-workbench .worktrees target to remain unchanged: got=%q want=%q", got, want)
	}
}
