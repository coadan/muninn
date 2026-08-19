package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInspectRepositoryInstructionsSeparatesRootAndScopedFiles(t *testing.T) {
	root := t.TempDir()
	writeInstructionFixture(t, filepath.Join(root, "AGENTS.md"), "12345678")
	writeInstructionFixture(t, filepath.Join(root, "CLAUDE.md"), "123456")
	writeInstructionFixture(t, filepath.Join(root, "packages", "api", "AGENTS.md"), "12345")
	writeInstructionFixture(t, filepath.Join(root, ".worktrees", "task", "AGENTS.md"), "ignored")
	writeInstructionFixture(t, filepath.Join(root, ".midgard", "benchmark-worktrees", "task", "AGENTS.md"), "ignored")
	writeInstructionFixture(t, filepath.Join(root, "README.md"), "not instructions")

	got := inspectRepositoryInstructions(root, "codex")
	if got.RootFiles != 1 || got.RootBytes != 8 || got.RootEstimatedTokens != 2 {
		t.Fatalf("root footprint=%#v", got)
	}
	if got.ScopedFiles != 1 || got.ScopedBytes != 5 || got.ScopedEstimatedTokens != 2 {
		t.Fatalf("scoped footprint=%#v", got)
	}
	if len(got.Files) != 2 || got.Files[0].Path != "AGENTS.md" || got.Files[1].Path != "packages/api/AGENTS.md" {
		t.Fatalf("instruction files=%#v", got.Files)
	}
	claude := inspectRepositoryInstructions(root, "claude-code")
	if claude.RootFiles != 1 || claude.RootBytes != 6 || claude.ScopedFiles != 0 {
		t.Fatalf("Claude instruction footprint=%#v", claude)
	}
}

func TestInspectRepositoryInstructionsAtUsesCommittedWindowState(t *testing.T) {
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
	writeInstructionFixture(t, filepath.Join(root, "AGENTS.md"), "12345678")
	for _, args := range [][]string{{"add", "AGENTS.md"}, {"commit", "-m", "instructions"}} {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_DATE=2026-01-01T12:00:00Z",
			"GIT_COMMITTER_DATE=2026-01-01T12:00:00Z",
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	writeInstructionFixture(t, filepath.Join(root, "AGENTS.md"), "current contents are longer")
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	got, ok := inspectRepositoryInstructionsAt(root, "codex", at)
	if !ok || !got.comparisonResolved || got.RootBytes != 8 {
		t.Fatalf("historical instruction footprint=%#v available=%v", got, ok)
	}
}

func TestBuildSessionFindingsReportsLargeRootInstructionFootprint(t *testing.T) {
	report := codexSessionInsightsReport{
		Instructions: repositoryInstructionFootprint{
			RootFiles:           1,
			RootBytes:           17 * 1024,
			RootEstimatedTokens: estimatedTokens(17 * 1024),
		},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "instruction-footprint" {
		t.Fatalf("instruction findings=%#v", findings)
	}
	focused, err := filterSessionFindings(findings, "instructions")
	if err != nil || len(focused) != 1 {
		t.Fatalf("instruction-focused findings=%#v, error=%v", focused, err)
	}
}

func TestInstructionFocusExcludesUnrelatedSessionLoops(t *testing.T) {
	findings := []sessionFinding{
		{Category: "instruction-discovery", Title: "instruction rediscovery"},
		{Category: "instruction-footprint", Title: "instruction footprint"},
		{Category: "session-loop", Title: "discovery compaction"},
	}
	focused, err := filterSessionFindings(findings, "instructions")
	if err != nil {
		t.Fatal(err)
	}
	if len(focused) != 2 ||
		focused[0].Category != "instruction-discovery" ||
		focused[1].Category != "instruction-footprint" {
		t.Fatalf("instruction focus leaked unrelated findings: %#v", focused)
	}

	corroborated := sessionFinding{
		Category:   "code-structure",
		Title:      "instruction owner",
		Supporting: []string{"instruction-discovery/agents.md"},
	}
	focused, err = filterSessionFindings([]sessionFinding{corroborated}, "instructions")
	if err != nil || len(focused) != 1 {
		t.Fatalf("instruction focus lost corroborated owner: %#v, %v", focused, err)
	}
}

func TestBuildSessionFindingsReportsRecurringSkillReloads(t *testing.T) {
	report := codexSessionInsightsReport{
		Summary: codexSessionInsightsSummary{codexAggregateMetrics: newCodexAggregateMetrics()},
	}
	report.Summary.SkillLoads["browser-qa"] = guidanceLoadMetrics{
		Loads: 10, Sessions: 5, Reloads: 5, ReloadSessions: 3,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "skill-reload" || findings[0].Target != "browser-qa" {
		t.Fatalf("skill reload findings=%#v", findings)
	}
}

func writeInstructionFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir instruction fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write instruction fixture: %v", err)
	}
}
