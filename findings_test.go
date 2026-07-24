package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildSessionFindingsUsesCurrentRepoRelativeSourceState(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "src", "large-owner.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(target, []byte(strings.Repeat("x", 16*1024)), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	report := newSessionInsightsReport("codex", nil, root, zeroTime(), zeroTime())
	report.Summary.ReadTargets["src/large-owner.go"] = codexTargetMetrics{
		Reads:           12,
		SearchReadLoops: 4,
		Sessions:        3,
	}
	report.Summary.ReadTargets["src/no-longer-exists.go"] = codexTargetMetrics{
		Reads:           40,
		SearchReadLoops: 20,
		Sessions:        8,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "code-structure" || findings[0].Target != "src/large-owner.go" {
		t.Fatalf("current-state source finding mismatch: %#v", findings)
	}
}

func TestCodexInlineOrchestrationExcludesEdits(t *testing.T) {
	large := strings.Repeat("x", 5000)
	if got := codexInlineOrchestrationBytes("exec", "", large); got != int64(len(large)) {
		t.Fatalf("large exec input not classified: %d", got)
	}
	if got := codexInlineOrchestrationBytes("apply_patch", "", large); got != 0 {
		t.Fatalf("large edit was misclassified as inline orchestration: %d", got)
	}
	if got := codexInlineOrchestrationBytes("exec_command", `{"cmd":"go test ./..."}`, ""); got != 0 {
		t.Fatalf("small shell command was misclassified: %d", got)
	}
}

func TestBuildSessionFindingsFlagsOneVeryLongInlineToolCall(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.InlineOrchestrationCalls = 1
	report.Summary.InlineOrchestrationBytes = 9 * 1024
	report.Summary.InlineOrchestrationMaxBytes = 9 * 1024
	report.Summary.InlineOrchestrationSessions = 1
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "agent-interface" ||
		!strings.Contains(findings[0].Title, "long inline code") {
		t.Fatalf("long inline call finding mismatch: %#v", findings)
	}
}

func TestFilterSessionFindingsUsesActionFamilies(t *testing.T) {
	findings := []sessionFinding{
		{Category: "owned-tool", Title: "tool"},
		{Category: "code-structure", Title: "source"},
		{Category: "session-loop", Title: "loop"},
	}
	filtered, err := filterSessionFindings(findings, "structure")
	if err != nil {
		t.Fatalf("filter findings: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Title != "source" {
		t.Fatalf("unexpected focused findings: %#v", filtered)
	}
	if _, err := filterSessionFindings(findings, "unknown"); err == nil {
		t.Fatal("unsupported focus did not fail")
	}
}

func zeroTime() time.Time {
	return time.Time{}
}
