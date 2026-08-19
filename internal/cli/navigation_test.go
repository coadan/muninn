package cli

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

func TestCodexGuidanceArtifactSizesUsesCurrentLargestInstalledMatch(t *testing.T) {
	codexHome := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	writeInstructionFixture(
		t,
		filepath.Join(codexHome, "skills", "current-skill", "SKILL.md"),
		"12345678",
	)
	writeInstructionFixture(
		t,
		filepath.Join(codexHome, "plugins", "cache", "owner", "plugin", "skills", "current-skill", "SKILL.md"),
		"123456789012",
	)
	writeInstructionFixture(
		t,
		filepath.Join(workspace, ".codex", "skills", "local-skill", "SKILL.md"),
		"1234",
	)

	got := codexGuidanceArtifactSizes(
		workspace,
		[]string{"current-skill", "local-skill", "missing-skill", "not valid"},
	)
	if got["current-skill"] != 3 || got["local-skill"] != 1 {
		t.Fatalf("current guidance sizes=%#v", got)
	}
	if _, ok := got["missing-skill"]; ok {
		t.Fatalf("missing skill received a guessed size: %#v", got)
	}
}

func TestCodexEditTargetsRemainAttributableAfterWorktreeRemoval(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".workbench", "repos", "breyta"), 0o755); err != nil {
		t.Fatalf("mkdir cached repo: %v", err)
	}
	removedWorktree := filepath.Join(root, ".worktrees", "task", "breyta")
	patch := "*** Begin Patch\n*** Update File: src/runtime.clj\n*** Move to: src/runtime_v2.clj\n*** End Patch\n"
	candidates := codexEditTargetCandidates("apply_patch", patch)
	got := normalizeRepositoryEditTargets(candidates, removedWorktree, root)
	want := []string{
		".workbench/repos/breyta/src/runtime.clj",
		".workbench/repos/breyta/src/runtime_v2.clj",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected canonical edit targets: got=%q want=%q", got, want)
	}
	for _, target := range got {
		if reworkTargetScope(target) != "breyta" || reworkTargetLever(target) != "source code" {
			t.Fatalf("unexpected rework attribution for %q", target)
		}
	}
}

func TestRepositoryTaskForTargetCandidatesPreservesWorktreeIdentity(t *testing.T) {
	root := t.TempDir()
	candidates := []string{
		filepath.Join(root, ".worktrees", "task-one", "breyta", "src", "one.clj"),
		filepath.Join(root, ".worktrees", "task-one", "breyta", "test", "one_test.clj"),
	}
	if got := repositoryTaskForTargetCandidates(candidates, root, root); got != "task-one" {
		t.Fatalf("worktree task=%q want task-one", got)
	}
	candidates = append(candidates, filepath.Join(root, ".worktrees", "task-two", "breyta", "src", "two.clj"))
	if got := repositoryTaskForTargetCandidates(candidates, root, root); got != "" {
		t.Fatalf("mixed worktree task=%q want empty", got)
	}
}

func TestRepositoryTaskForWorkingDirectoryPreservesWorktreeIdentity(t *testing.T) {
	root := t.TempDir()
	if got := repositoryTaskForWorkingDirectory(filepath.Join(root, ".worktrees", "task-one"), root); got != "task-one" {
		t.Fatalf("worktree task=%q want task-one", got)
	}
	if got := repositoryTaskForWorkingDirectory(filepath.Join(root, ".workbench", "worktrees", "legacy-task", "repo"), root); got != "legacy-task" {
		t.Fatalf("legacy worktree task=%q want legacy-task", got)
	}
	if got := repositoryTaskForWorkingDirectory(root, root); got != "" {
		t.Fatalf("root task=%q want empty", got)
	}
}
