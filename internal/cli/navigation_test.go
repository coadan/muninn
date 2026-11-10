package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestCodexReadTargetsInRemovedWorktreeUseCachedRepositoryFile(t *testing.T) {
	root := t.TempDir()
	writeInstructionFixture(t, filepath.Join(root, ".workbench", "repos", "breyta", "src", "runtime.clj"), "(ns runtime)\n")
	removedWorktree := filepath.Join(root, ".worktrees", "task", "breyta")
	got := normalizeRepositoryTargets([]string{"src/runtime.clj", "src/missing.clj"}, removedWorktree, root)
	want := []string{".workbench/repos/breyta/src/runtime.clj"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected read targets: got=%q want=%q", got, want)
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

func TestNormalizeRepositoryTargetsCollapsesClaudeWorktreesToRepositoryPath(t *testing.T) {
	root := t.TempDir()
	relativeSource := filepath.Join("pkg", "ledger", "posting_test.go")
	for _, path := range []string{
		filepath.Join(root, relativeSource),
		filepath.Join(root, ".claude", "worktrees", "task-one", relativeSource),
		filepath.Join(root, ".claude", "worktrees", "task-two", relativeSource),
	} {
		writeInstructionFixture(t, path, "package ledger\n")
	}
	candidates := []string{
		filepath.Join(root, ".claude", "worktrees", "task-one", relativeSource),
		filepath.Join(root, ".claude", "worktrees", "task-two", relativeSource),
		filepath.Join(root, relativeSource),
	}
	got := normalizeRepositoryTargets(candidates, root, root)
	want := []string{filepath.ToSlash(relativeSource)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected canonical targets: got=%q want=%q", got, want)
	}
	// Relative reads resolve against the worktree working directory.
	worktree := filepath.Join(root, ".claude", "worktrees", "task-one")
	if got := normalizeRepositoryTargets([]string{relativeSource}, worktree, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected relative worktree targets: got=%q want=%q", got, want)
	}
	for _, target := range got {
		if cohort := deliveryTargetCohort(target); cohort != "pkg/ledger" {
			t.Fatalf("deliveryTargetCohort(%q)=%q want pkg/ledger", target, cohort)
		}
		if label := deliveryTargetLabel(target); label != filepath.ToSlash(relativeSource) {
			t.Fatalf("deliveryTargetLabel(%q)=%q", target, label)
		}
	}
}

func TestClaudeWorktreeTargetsRemainAttributableAfterWorktreeRemoval(t *testing.T) {
	root := t.TempDir()
	relativeSource := filepath.Join("pkg", "ledger", "posting.go")
	writeInstructionFixture(t, filepath.Join(root, relativeSource), "package ledger\n")
	removedWorktree := filepath.Join(root, ".claude", "worktrees", "task-one")
	want := []string{filepath.ToSlash(relativeSource)}
	if got := normalizeRepositoryTargets([]string{relativeSource}, removedWorktree, root); !reflect.DeepEqual(got, want) {
		t.Fatalf("read target in removed worktree: got=%q want=%q", got, want)
	}
	newSource := filepath.Join(removedWorktree, "pkg", "ledger", "posting_v2.go")
	if got := normalizeRepositoryEditTargets([]string{newSource}, removedWorktree, root); !reflect.DeepEqual(got, []string{"pkg/ledger/posting_v2.go"}) {
		t.Fatalf("edit target in removed worktree: got=%q", got)
	}
	missing := filepath.Join(removedWorktree, "pkg", "ledger", "missing.go")
	if got := normalizeRepositoryTargets([]string{missing}, removedWorktree, root); len(got) != 0 {
		t.Fatalf("read target with no file anywhere: got=%q want none", got)
	}
}

func TestRepositoryTaskForTargetCandidatesUsesClaudeWorktreeName(t *testing.T) {
	root := t.TempDir()
	candidates := []string{
		filepath.Join(root, ".claude", "worktrees", "task-one", "pkg", "one.go"),
		filepath.Join(root, ".claude", "worktrees", "task-one", "pkg", "one_test.go"),
	}
	if got := repositoryTaskForTargetCandidates(candidates, root, root); got != "task-one" {
		t.Fatalf("claude worktree task=%q want task-one", got)
	}
	candidates = append(candidates, filepath.Join(root, ".claude", "worktrees", "task-two", "pkg", "two.go"))
	if got := repositoryTaskForTargetCandidates(candidates, root, root); got != "" {
		t.Fatalf("mixed claude worktree task=%q want empty", got)
	}
}

// Two Claude Code sessions edit and read the same file from different task
// worktrees. Direct and indexed reports must attribute both to the one
// in-repository path and never expose the worktree prefix.
func TestClaudeWorktreeTargetsReachReportsAsRepositoryPaths(t *testing.T) {
	root, sessionsDir := t.TempDir(), t.TempDir()
	relative := filepath.Join("pkg", "ledger", "posting.go")
	writeInstructionFixture(t, filepath.Join(root, relative), "package ledger\n")
	usage := map[string]any{"input_tokens": 2, "output_tokens": 3}
	for index, task := range []string{"task-one", "task-two"} {
		worktree := filepath.Join(root, ".claude", "worktrees", task)
		file := filepath.Join(worktree, relative)
		writeInstructionFixture(t, file, "package ledger\n")
		offset := index * 20
		records := []any{
			claudeFixtureRecord("user", offset, map[string]any{"message": map[string]any{"role": "user", "content": "PROMPT-SECRET"}}),
			claudeFixtureAssistant(offset+1, task+"-m1", "tool_use", usage, claudeFixtureToolUse("r1", "Read", map[string]any{"file_path": file})),
			claudeFixtureToolResult(offset+2, "r1", false, "ok"),
			claudeFixtureAssistant(offset+3, task+"-m2", "tool_use", usage, claudeFixtureToolUse("e1", "Edit", map[string]any{"file_path": file, "old_string": "a", "new_string": "b"})),
			claudeFixtureToolResult(offset+4, "e1", false, "ok"),
			claudeFixtureAssistant(offset+5, task+"-m3", "end_turn", usage, map[string]any{"type": "text", "text": "done"}),
		}
		for _, record := range records {
			fields := record.(map[string]any)
			fields["cwd"] = worktree
			fields["sessionId"] = "33333333-aaaa-bbbb-cccc-00000000000" + string(rune('1'+index))
		}
		writeClaudeSessionFixture(t, sessionsDir, task, records)
	}

	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := provider.Discover(sessionsDir, false)
	if err != nil || len(discovery.Files) != 2 {
		t.Fatalf("discovery=%#v err=%v", discovery, err)
	}
	generatedAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	since := generatedAt.Add(-24 * time.Hour)
	direct, err := analyzeProviderSessions(provider, discovery, root, since, generatedAt, "", ownershipCatalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stats, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, ownershipCatalog{}, false)
	if err != nil || stats.FilesIndexed != 2 {
		t.Fatalf("stats=%#v err=%v", stats, err)
	}
	indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, since, generatedAt, "", ownershipCatalog{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.ToSlash(relative)
	for name, report := range map[string]codexSessionInsightsReport{"direct": direct, "indexed": indexed} {
		detailed, err := json.Marshal(analysisJSONPayload(report, true))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(detailed), ".claude/worktrees") || strings.Contains(string(detailed), "worktrees/task-") {
			t.Fatalf("%s report exposes the worktree prefix: %s", name, detailed)
		}
		if !strings.Contains(string(detailed), `"`+want+`"`) {
			t.Fatalf("%s report lacks the repository target %q: %s", name, want, detailed)
		}
		if len(report.Tasks) == 0 {
			t.Fatalf("%s report has no tasks", name)
		}
		for _, task := range report.Tasks {
			if len(task.ReadTargets) == 0 {
				t.Fatalf("%s task has no read targets", name)
			}
			for target := range task.ReadTargets {
				if target != want {
					t.Fatalf("%s task read target=%q want %q", name, target, want)
				}
			}
		}
	}
}
