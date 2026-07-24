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
	report.Summary.InlineOrchestrationByTool["exec"] = codexInlineMetrics{
		Calls:    1,
		Sessions: 1,
		Bytes:    9 * 1024,
		MaxBytes: 9 * 1024,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "agent-interface" ||
		!strings.Contains(findings[0].Title, "long inline code") ||
		!strings.Contains(findings[0].Evidence, "exec 1 calls/9,216 bytes") {
		t.Fatalf("long inline call finding mismatch: %#v", findings)
	}
}

func TestBuildSessionFindingsShowsBoundedOwnedOperationFailureReasons(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.OwnedOperations["bwb/test"] = codexOwnedOperationMetrics{
		Calls:       30,
		Sessions:    4,
		FailedCalls: 19,
		OutputBytes: 8_000,
	}
	report.Summary.OwnedOperationFailureReasons["bwb/test"] = map[string]codexOccurrenceMetrics{
		"timeout":                 {Count: 5, Sessions: 4},
		"other non-zero exit":     {Count: 4, Sessions: 3},
		"missing path or fixture": {Count: 3, Sessions: 1},
		"port collision":          {Count: 2, Sessions: 2},
		"test failure":            {Count: 5, Sessions: 3},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "owned-operation" {
		t.Fatalf("owned-operation finding mismatch: %#v", findings)
	}
	evidence := findings[0].Evidence
	for _, want := range []string{
		"14 actionable failures",
		"5 expected/product failures",
		"actionable reasons: timeout 5 calls/4 sessions, other non-zero exit 4 calls/3 sessions, missing path or fixture 3 calls/1 session",
	} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("evidence %q missing %q", evidence, want)
		}
	}
	if strings.Contains(evidence, "port collision") || strings.Contains(evidence, "test failure 5") {
		t.Fatalf("evidence should contain only the top three actionable reasons: %q", evidence)
	}
}

func TestBuildSessionFindingsReportsInputCostAndProgressStalls(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Tasks = []codexTaskInsights{{
		Task:     "expensive-task",
		Sessions: 4,
		Tokens: codexTokenUsage{
			InputTokens:         1_200_000,
			CachedInputTokens:   400_000,
			UncachedInputTokens: 800_000,
		},
		FreshTokens: 900_000,
		Compactions: 2,
	}}
	report.Summary.ProgressStalls["bwb/api-start"] = codexWaitMetrics{
		Calls:    3,
		Seconds:  95,
		Sessions: 2,
	}
	report.Summary.ExpectedWaits["tests"] = codexWaitMetrics{
		Calls:    9,
		Seconds:  300,
		Sessions: 3,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	bySignal := map[string]sessionFinding{}
	for _, finding := range findings {
		bySignal[finding.Signal] = finding
	}
	inputSignal := "session-loop/input-cost/expensive-task"
	if finding, ok := bySignal[inputSignal]; !ok ||
		!strings.Contains(finding.Evidence, "800,000 uncached") ||
		!strings.Contains(finding.Evidence, "200,000 uncached input") {
		t.Fatalf("input-cost finding mismatch: %#v", finding)
	}
	stallSignal := "session-loop/progress-stall/bwb/api-start"
	if finding, ok := bySignal[stallSignal]; !ok ||
		!strings.Contains(finding.Evidence, "1m35s") {
		t.Fatalf("progress-stall finding mismatch: %#v", finding)
	}
	if _, exists := bySignal["session-loop/progress-stall/tests"]; exists {
		t.Fatalf("expected test waits should not produce a finding: %#v", findings)
	}
}

func TestBuildSessionFindingsSuppressesOnlyExactSignal(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.ProgressStalls["bwb/api-start"] = codexWaitMetrics{Calls: 2, Seconds: 60, Sessions: 2}
	report.Summary.ProgressStalls["bwb/fixture"] = codexWaitMetrics{Calls: 2, Seconds: 60, Sessions: 2}
	config := defaultRepositoryConfig()
	config.SuppressSignals = []string{"session-loop/progress-stall/bwb/api-start"}
	findings := buildSessionFindings(report, config)
	if len(findings) != 1 {
		t.Fatalf("exact suppression should preserve the other finding: %#v", findings)
	}
	if findings[0].Signal != "session-loop/progress-stall/bwb/fixture" {
		t.Fatalf("unexpected remaining signal: %#v", findings[0])
	}
	if report.Summary.ProgressStalls["bwb/api-start"].Calls != 2 {
		t.Fatalf("suppression mutated source metrics: %#v", report.Summary.ProgressStalls)
	}
}

func TestBuildSessionFindingsCapsOversizedOutputsAndMarksOwnedContext(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.OversizedOutputs["bwb/test"] = codexOversizedOutputMetrics{
		Calls: 3, OutputBytes: 180_000, MaxOutputBytes: 90_000, Sessions: 2,
	}
	report.Summary.OversizedOutputs["search"] = codexOversizedOutputMetrics{
		Calls: 2, OutputBytes: 100_000, MaxOutputBytes: 60_000, Sessions: 2,
	}
	report.Summary.OversizedOutputs["git inspect"] = codexOversizedOutputMetrics{
		Calls: 1, OutputBytes: 80_000, MaxOutputBytes: 80_000, Sessions: 1,
	}
	report.Summary.OversizedOutputs["tests"] = codexOversizedOutputMetrics{
		Calls: 1, OutputBytes: 30_000, MaxOutputBytes: 30_000, Sessions: 1,
	}
	config := defaultRepositoryConfig()
	config.OwnedTools = []ownedToolConfig{{ID: "bwb"}}
	findings := buildSessionFindings(report, config)
	outputFindings, err := filterSessionFindings(findings, "output")
	if err != nil {
		t.Fatalf("filter output findings: %v", err)
	}
	if len(outputFindings) != 3 {
		t.Fatalf("oversized output findings should be capped at three: %#v", outputFindings)
	}
	bySignal := map[string]sessionFinding{}
	for _, finding := range outputFindings {
		bySignal[finding.Signal] = finding
	}
	owned := bySignal["output-cost/bwb/test"]
	if owned.Control != "local" || !strings.Contains(owned.Action, "locally controlled") ||
		!strings.Contains(owned.Evidence, "largest call 90,000 bytes") {
		t.Fatalf("owned output finding mismatch: %#v", owned)
	}
	if search := bySignal["output-cost/search"]; !strings.Contains(search.Action, "cap matches") {
		t.Fatalf("search output action mismatch: %#v", search)
	}
	if _, exists := bySignal["output-cost/tests"]; exists {
		t.Fatalf("lowest-ranked fourth output should not displace top three: %#v", outputFindings)
	}
}

func TestOversizedOutputActionKeepsCompoundWorkflowBundled(t *testing.T) {
	action := oversizedOutputAction("git inspect -> file reads -> tests", "repository")
	if !strings.Contains(action, "Keep the workflow bundled") || !strings.Contains(action, "cap each") {
		t.Fatalf("compound oversized-output action should reduce output without adding roundtrips: %q", action)
	}
}

func TestSessionFindingSignalIsPrivacySafeAndBounded(t *testing.T) {
	finding := sessionFinding{
		Category: "session-loop",
		Title:    "progress stalls while waiting on: private",
		Target:   "BWB/API Start (Local)",
	}
	signal := sessionFindingSignal(finding)
	if signal != "session-loop/progress-stall/bwb/api-start-local" {
		t.Fatalf("unexpected signal ID: %q", signal)
	}
	if got := len(signalID("category", strings.Repeat("long target ", 100))); got > 200 {
		t.Fatalf("signal ID length=%d exceeds bound", got)
	}
}

func TestBuildSessionFindingsRanksRecentSignalsFirst(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.ProgressStalls["older-high-count"] = codexWaitMetrics{
		Calls: 20, Seconds: 600, Sessions: 5,
	}
	report.Summary.ProgressStalls["recent-low-count"] = codexWaitMetrics{
		Calls: 2, Seconds: 40, Sessions: 2,
	}
	older := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	recent := older.Add(24 * time.Hour)
	touchSessionActivity(report.Summary.Activity, "progress-stall", "older-high-count", older)
	touchSessionActivity(report.Summary.Activity, "progress-stall", "recent-low-count", recent)

	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 2 {
		t.Fatalf("expected two progress findings, got %#v", findings)
	}
	if findings[0].Target != "recent-low-count" {
		t.Fatalf("freshness should outrank cumulative score: %#v", findings)
	}
	if findings[0].LastSeen != "2026-07-24T08:00:00Z" ||
		findings[1].LastSeen != "2026-07-23T08:00:00Z" {
		t.Fatalf("finding freshness mismatch: %#v", findings)
	}
}

func TestBuildSessionFindingsAttributesDeliveryReworkToMissingGate(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.DeliveryRework = deliveryReworkMetrics{
		Deliveries:               4,
		DeliveriesWithRework:     3,
		ReviewToEditCycles:       3,
		PostDeliveryReviewChecks: 4,
		Sessions:                 2,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 {
		t.Fatalf("expected one delivery-quality finding: %#v", findings)
	}
	finding := findings[0]
	if finding.Category != "delivery-quality" || finding.Lever != "tooling" ||
		finding.Confidence != "high" || !strings.Contains(finding.Action, "pre-delivery review gate") {
		t.Fatalf("delivery-quality attribution mismatch: %#v", finding)
	}
}

func TestBuildSessionFindingsFlagsRepeatedPostDeliveryReviewChecks(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.DeliveryRework = deliveryReworkMetrics{
		Deliveries:               4,
		PostDeliveryReviewChecks: 25,
		Sessions:                 2,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Lever != "tooling" ||
		!strings.Contains(findings[0].Title, "repeated checks") {
		t.Fatalf("review-check finding mismatch: %#v", findings)
	}
}

func TestBuildSessionFindingsFlagsCompletedTaskCostTail(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Outcomes = completionEpisodeAnalysis{
		ToolUsingCompleted: 40,
		FreshTokens: outcomeDistribution{
			Count: 40,
			P50:   100,
			P90:   300,
			Max:   2_000,
		},
		TopDecileFreshTokenShare: 0.42,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "task-cost" ||
		findings[0].Lever != "unknown" {
		t.Fatalf("task-cost tail finding mismatch: %#v", findings)
	}
}

func TestSessionFindingDisplayAvoidsDuplicateTarget(t *testing.T) {
	finding := sessionFinding{
		Title:  "individual tool calls return oversized output: file reads",
		Target: "file reads",
	}
	if got := sessionFindingDisplayTarget(finding); got != "" {
		t.Fatalf("embedded target should not be repeated: %q", got)
	}
	finding.Title = "a small current owner is repeatedly reread"
	if got := sessionFindingDisplayTarget(finding); got != " · file reads" {
		t.Fatalf("distinct target should remain visible: %q", got)
	}
}

func TestSessionFindingLeverSeparatesToolingInstructionsAndSource(t *testing.T) {
	tests := []struct {
		finding sessionFinding
		lever   string
	}{
		{finding: sessionFinding{Category: "code-structure", Target: "scripts/tool.go"}, lever: "tooling"},
		{finding: sessionFinding{Category: "code-structure", Target: "bwb-src/task.go"}, lever: "tooling"},
		{finding: sessionFinding{Category: "code-structure", Target: "docs/runtime.md"}, lever: "instructions/docs"},
		{finding: sessionFinding{Category: "code-structure", Target: ".workbench/repos/breyta/docs/runtime.md"}, lever: "instructions/docs"},
		{finding: sessionFinding{Category: "code-structure", Target: ".workbench/repos/breyta/scripts/check.clj"}, lever: "tooling"},
		{finding: sessionFinding{Category: "code-structure", Target: "src/runtime.go"}, lever: "source code"},
	}
	for _, test := range tests {
		if lever, confidence := sessionFindingLever(test.finding); lever != test.lever || confidence != "high" {
			t.Fatalf("finding %#v attributed to %q/%q", test.finding, lever, confidence)
		}
	}
}

func TestFormatSessionFindingAge(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	if got := formatSessionFindingAge("2026-07-24T10:30:00Z", now); got != "1h ago" {
		t.Fatalf("unexpected finding age: %q", got)
	}
}

func TestFilterSessionFindingsUsesActionFamilies(t *testing.T) {
	findings := []sessionFinding{
		{Category: "owned-tool", Title: "tool"},
		{Category: "code-structure", Title: "source"},
		{Category: "session-loop", Title: "loop"},
		{Category: "delivery-quality", Title: "quality"},
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
	output, err := filterSessionFindings([]sessionFinding{{Category: "output-cost", Title: "large"}}, "output")
	if err != nil || len(output) != 1 {
		t.Fatalf("output focus mismatch: %#v, %v", output, err)
	}
	quality, err := filterSessionFindings(findings, "quality")
	if err != nil || len(quality) != 1 || quality[0].Title != "quality" {
		t.Fatalf("quality focus mismatch: %#v, %v", quality, err)
	}
}

func zeroTime() time.Time {
	return time.Time{}
}
