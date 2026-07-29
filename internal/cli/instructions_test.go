package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectRepositoryInstructionsSeparatesRootAndScopedFiles(t *testing.T) {
	root := t.TempDir()
	writeInstructionFixture(t, filepath.Join(root, "AGENTS.md"), "12345678")
	writeInstructionFixture(t, filepath.Join(root, "CLAUDE.md"), "123456")
	writeInstructionFixture(t, filepath.Join(root, "packages", "api", "AGENTS.md"), "12345")
	writeInstructionFixture(t, filepath.Join(root, ".worktrees", "task", "AGENTS.md"), "ignored")
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

func writeInstructionFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir instruction fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write instruction fixture: %v", err)
	}
}
