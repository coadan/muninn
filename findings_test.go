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

func TestCodexInlineOrchestrationExcludesRoutineCodeModeWrappers(t *testing.T) {
	patch := `const result = await tools.apply_patch(` + strings.Repeat("x", 9*1024) + `); text(result);`
	if got := codexInlineOrchestrationBytes("exec", "", patch); got != 0 {
		t.Fatalf("nested apply_patch wrapper was misclassified as inline orchestration: %d", got)
	}

	batch := `const results = await Promise.allSettled([
		tools.exec_command({cmd:"one"}),
		tools.exec_command({cmd:"two"})
	]);` + strings.Repeat(" ", 5*1024) + `
	for (const result of results) text(result.status);`
	if got := codexInlineOrchestrationBytes("exec", "", batch); got != 0 {
		t.Fatalf("routine concurrent tool batch was misclassified as inline orchestration: %d", got)
	}
}

func TestCodexConcurrentToolBatchRecognizesBoundedAndCustomBatches(t *testing.T) {
	batch := `const results = await Promise.allSettled([
		tools.exec_command({cmd:"one"}),
		tools.exec_command({cmd:"two"})
	]);`
	if !codexConcurrentToolBatch("exec", batch) {
		t.Fatal("concurrent Code Mode batch was not recognized")
	}
	if codexConcurrentToolBatch("exec", `const result = await tools.exec_command({cmd:"one"});`) {
		t.Fatal("single nested tool call was misclassified as a concurrent batch")
	}
	if codexConcurrentToolBatch("exec_command", batch) {
		t.Fatal("non-Code Mode tool call was misclassified as a concurrent batch")
	}
}

func TestCodexInlineOrchestrationRetainsLargeCustomExecCells(t *testing.T) {
	sequential := `const first = await tools.exec_command({cmd:"one"});
	const second = await tools.exec_command({cmd:"two"});` + strings.Repeat("x", 9*1024)
	if got := codexInlineOrchestrationBytes("exec", "", sequential); got != int64(len(sequential)) {
		t.Fatalf("large sequential exec bytes=%d want %d", got, len(sequential))
	}

	oversizedBatch := `const results = await Promise.allSettled([
		tools.exec_command({cmd:"one"}),
		tools.exec_command({cmd:"two"})
	]);` + strings.Repeat("x", 17*1024)
	if got := codexInlineOrchestrationBytes("exec", "", oversizedBatch); got != int64(len(oversizedBatch)) {
		t.Fatalf("oversized batch bytes=%d want %d", got, len(oversizedBatch))
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

func TestBuildSessionFindingsShowsDominantWorkflowTransitions(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.CrossCallTransitions = map[string]codexTransitionMetrics{
		"tests -> tests":                                     {Count: 7, Sessions: 4},
		"tests -> build, lint, or install":                   {Count: 6, Sessions: 3},
		"build, lint, or install -> tests":                   {Count: 5, Sessions: 3},
		"build, lint, or install -> build, lint, or install": {Count: 4, Sessions: 2},
	}

	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "agent-interface" {
		t.Fatalf("workflow finding mismatch: %#v", findings)
	}
	evidence := findings[0].Evidence
	for _, want := range []string{
		"22 transitions across at least 4 sessions",
		"tests -> tests: 7 transitions/4 sessions",
		"tests -> build, lint, or install: 6 transitions/3 sessions",
		"build, lint, or install -> tests: 5 transitions/3 sessions",
	} {
		if !strings.Contains(evidence, want) {
			t.Fatalf("evidence %q missing %q", evidence, want)
		}
	}
	if strings.Contains(evidence, "build, lint, or install -> build, lint, or install") {
		t.Fatalf("evidence should contain only the top three transitions: %q", evidence)
	}
}

func TestBuildSessionFindingsShowsBoundedOwnedOperationFailureReasons(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	latestCall := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	latestFriction := latestCall.Add(-time.Hour)
	report.Summary.OwnedOperations["bwb/run"] = codexOwnedOperationMetrics{
		Calls:       30,
		Sessions:    4,
		FailedCalls: 19,
		OutputBytes: 8_000,
	}
	report.Summary.OwnedOperationFailureReasons["bwb/run"] = map[string]codexOccurrenceMetrics{
		"timeout":                 {Count: 5, Sessions: 4},
		"other non-zero exit":     {Count: 4, Sessions: 3},
		"missing path or fixture": {Count: 3, Sessions: 1},
		"port collision":          {Count: 2, Sessions: 2},
		"test failure":            {Count: 5, Sessions: 3},
	}
	report.Summary.Activity[sessionActivityKey("owned-operation", "bwb/run")] = latestCall
	report.Summary.Activity[sessionActivityKey("owned-operation-friction", "bwb/run")] = latestFriction
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
	if findings[0].LastSeen != latestFriction.Format(time.RFC3339) {
		t.Fatalf("last seen=%q want latest friction %s", findings[0].LastSeen, latestFriction)
	}
}

func TestBuildSessionFindingsShowsRecentOwnedOperationEvidence(t *testing.T) {
	generatedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	report := newSessionInsightsReport(
		"codex",
		nil,
		t.TempDir(),
		generatedAt.Add(-14*24*time.Hour),
		generatedAt,
	)
	report.Summary.OwnedOperations["bwb/run"] = codexOwnedOperationMetrics{
		Calls:          23,
		Sessions:       2,
		FailedCalls:    5,
		TruncatedCalls: 2,
		OutputBytes:    43_000,
	}
	report.Summary.OwnedOperationFailureReasons["bwb/run"] = map[string]codexOccurrenceMetrics{
		"timeout": {Count: 5, Sessions: 1},
	}
	activityKey := sessionActivityKey("owned-operation", "bwb/run")
	report.sessionRecords = []codexSessionRecord{
		{
			OwnedOperations: map[string]codexToolMetrics{
				"bwb/run": {
					Calls:          20,
					FailedCalls:    5,
					TruncatedCalls: 2,
					OutputBytes:    40_000,
				},
			},
			OwnedOperationFailureReasons: map[string]map[string]int{
				"bwb/run": {"timeout": 5},
			},
			Activity: map[string]time.Time{
				activityKey: generatedAt.Add(-72 * time.Hour),
			},
		},
		{
			OwnedOperations: map[string]codexToolMetrics{
				"bwb/run": {Calls: 3, OutputBytes: 3_000},
			},
			OwnedOperationFailureReasons: map[string]map[string]int{},
			Activity: map[string]time.Time{
				activityKey: generatedAt.Add(-time.Hour),
			},
		},
	}

	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 {
		t.Fatalf("owned-operation finding mismatch: %#v", findings)
	}
	for _, want := range []string{
		"recent 24h sessions: 3 calls/1 sessions",
		"0 actionable failures",
		"0 truncations",
		"~750 output tokens",
	} {
		if !strings.Contains(findings[0].Evidence, want) {
			t.Fatalf("recent evidence %q missing %q", findings[0].Evidence, want)
		}
	}
}

func TestBuildSessionFindingsDoesNotConfuseOperationDemandWithFriction(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.ToolOutputTokens = 2_000_000
	report.Summary.OwnedOperations["repo/publish"] = codexOwnedOperationMetrics{
		Calls:                 80,
		Sessions:              4,
		EstimatedOutputTokens: 8_000,
	}
	report.Summary.OwnedOperations["repo/verify"] = codexOwnedOperationMetrics{
		Calls:                 40,
		Sessions:              3,
		FailedCalls:           6,
		EstimatedOutputTokens: 12_000,
	}
	report.Summary.OwnedOperationFailureReasons["repo/verify"] = map[string]codexOccurrenceMetrics{
		"other non-zero exit": {Count: 6, Sessions: 3},
	}

	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("ordinary demand or expected verification failures became friction: %#v", findings)
	}
}

func TestBuildSessionFindingsFlagsMaterialOwnedOperationOutput(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.ToolOutputTokens = 2_000_000
	report.Summary.OwnedOperations["repo/inspect"] = codexOwnedOperationMetrics{
		Calls:                 30,
		Sessions:              3,
		EstimatedOutputTokens: 30_000,
	}

	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Target != "repo/inspect" ||
		!strings.Contains(findings[0].Title, "high-cost") {
		t.Fatalf("material operation output finding mismatch: %#v", findings)
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
	report.Summary.RapidPolls["bwb/test-nses"] = codexWaitMetrics{
		Calls:    8,
		Seconds:  40,
		Sessions: 1,
	}
	report.Summary.ProgressStalls["mixed shell"] = codexWaitMetrics{
		Calls:    20,
		Seconds:  900,
		Sessions: 4,
	}
	config := defaultRepositoryConfig()
	config.OwnedTools = []ownedToolConfig{{ID: "bwb"}}
	findings := buildSessionFindings(report, config)
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
	if _, exists := bySignal["session-loop/progress-stall/mixed-shell"]; exists {
		t.Fatalf("unattributed waits should remain metrics, not actionable findings: %#v", findings)
	}
	rapidSignal := "agent-interface/rapid-poll/bwb/test-nses"
	if finding, ok := bySignal[rapidSignal]; !ok ||
		!strings.Contains(finding.Action, "30-second wait") ||
		!strings.Contains(finding.Evidence, "8 continuation polls") {
		t.Fatalf("rapid polling finding mismatch: %#v", finding)
	}
}

func TestBuildSessionFindingsSuppressesOnlyExactSignal(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.ProgressStalls["bwb/api-start"] = codexWaitMetrics{Calls: 2, Seconds: 60, Sessions: 2}
	report.Summary.ProgressStalls["bwb/fixture"] = codexWaitMetrics{Calls: 2, Seconds: 60, Sessions: 2}
	config := defaultRepositoryConfig()
	config.OwnedTools = []ownedToolConfig{{ID: "bwb"}}
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

func TestConcurrentBatchOversizedOutputFindingPreservesBatching(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.OversizedOutputs["concurrent tool batch"] = codexOversizedOutputMetrics{
		Calls: 2, OutputBytes: 75_000, MaxOutputBytes: 40_000, Sessions: 1,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	outputFindings, err := filterSessionFindings(findings, "output")
	if err != nil || len(outputFindings) != 1 {
		t.Fatalf("concurrent batch output finding mismatch: findings=%#v err=%v", outputFindings, err)
	}
	finding := outputFindings[0]
	if finding.Title != "concurrent tool batches exceed the shared output budget" ||
		!strings.Contains(finding.Action, "Keep independent calls concurrent") ||
		!strings.Contains(finding.Action, "inspect every partial result") {
		t.Fatalf("concurrent batch guidance mismatch: %#v", finding)
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
	report.Summary.ProgressStalls["bwb/older-high-count"] = codexWaitMetrics{
		Calls: 20, Seconds: 600, Sessions: 5,
	}
	report.Summary.ProgressStalls["bwb/recent-low-count"] = codexWaitMetrics{
		Calls: 2, Seconds: 40, Sessions: 2,
	}
	older := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	recent := older.Add(24 * time.Hour)
	touchSessionActivity(report.Summary.Activity, "progress-stall", "bwb/older-high-count", older)
	touchSessionActivity(report.Summary.Activity, "progress-stall", "bwb/recent-low-count", recent)

	config := defaultRepositoryConfig()
	config.OwnedTools = []ownedToolConfig{{ID: "bwb"}}
	findings := buildSessionFindings(report, config)
	if len(findings) != 2 {
		t.Fatalf("expected two progress findings, got %#v", findings)
	}
	if findings[0].Target != "bwb/recent-low-count" {
		t.Fatalf("freshness should outrank cumulative score: %#v", findings)
	}
	if findings[0].LastSeen != "2026-07-24T08:00:00Z" ||
		findings[1].LastSeen != "2026-07-23T08:00:00Z" {
		t.Fatalf("finding freshness mismatch: %#v", findings)
	}
}

func TestDiversifySessionFindingsPreservesHighImpactRepositoryScopes(t *testing.T) {
	findings := []sessionFinding{
		{Category: "code-structure", Target: "bwb-src/recent.go", LastSeen: "2026-07-24T12:00:00Z", score: 400},
		{Category: "code-structure", Target: "bwb-src/second.go", LastSeen: "2026-07-24T11:00:00Z", score: 410},
		{Category: "code-structure", Target: "bwb-src/dominant.go", LastSeen: "2026-07-24T10:00:00Z", score: 700},
		{Category: "code-structure", Target: "bwb-src/fourth.go", LastSeen: "2026-07-24T09:00:00Z", score: 390},
		{Category: "code-structure", Target: "bwb-src/fifth.go", LastSeen: "2026-07-24T08:00:00Z", score: 380},
		{Category: "code-structure", Target: "bwb-src/sixth.go", LastSeen: "2026-07-24T07:00:00Z", score: 370},
		{Category: "code-structure", Target: ".workbench/repos/breyta/src/installations.clj", LastSeen: "2026-07-20T12:00:00Z", score: 1600},
		{Category: "code-structure", Target: ".workbench/repos/breyta-cli/internal/cli/flows_lint.go", LastSeen: "2026-07-19T12:00:00Z", score: 900},
	}

	got := diversifySessionFindings(findings)
	if len(got) != 6 {
		t.Fatalf("code-structure findings should remain capped at six: %#v", got)
	}
	targets := map[string]bool{}
	for _, finding := range got {
		targets[finding.Target] = true
	}
	for _, target := range []string{
		"bwb-src/dominant.go",
		".workbench/repos/breyta/src/installations.clj",
		".workbench/repos/breyta-cli/internal/cli/flows_lint.go",
	} {
		if !targets[target] {
			t.Fatalf("repository-scope representative %q was crowded out: %#v", target, got)
		}
	}
	if targets["bwb-src/sixth.go"] {
		t.Fatalf("lower-impact same-scope finding should make room for another repository: %#v", got)
	}
}

func TestSessionFindingRepositoryScopeRecognizesWorkbenchSources(t *testing.T) {
	tests := map[string]string{
		"bwb-src/task.go":                                      "workspace",
		".workbench/repos/breyta/src/runtime.clj":              "breyta",
		".workbench/repos/breyta-cli/internal/cli/flows_v1.go": "breyta-cli",
		".worktrees/my-task/breyta/src/runtime.clj":            "breyta",
	}
	for target, want := range tests {
		if got := sessionFindingRepositoryScope(target); got != want {
			t.Fatalf("scope for %q=%q want %q", target, got, want)
		}
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

func TestBuildSessionFindingsLocalizesDeliveryReworkToGenericCohort(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	if err := os.MkdirAll(filepath.Join(report.WorkspaceRoot, "packages/runtime/src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(report.WorkspaceRoot, "packages/runtime/src/engine.go"),
		[]byte("package runtime\n\nfunc Run() {}\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	report.Summary.DeliveryRework = deliveryReworkMetrics{
		Deliveries:            8,
		DeliveriesWithRework:  3,
		ReviewToEditCycles:    5,
		PostDeliveryEditCalls: 5,
		Sessions:              4,
		ReworkLevers:          map[string]int{"source code": 5},
		ReworkScopes:          map[string]int{"(root)": 5},
		ReworkTargets: map[string]int{
			"packages/runtime/src/engine.go": 4,
			"packages/runtime/src/cache.go":  3,
			"apps/web/src/page.ts":           2,
		},
		DeliveriesWithPreTests:  2,
		DeliveriesWithPreReview: 1,
		Cohorts: map[string]deliveryCohortMetrics{
			"packages/runtime": {
				Deliveries:             5,
				DeliveriesWithRework:   3,
				ReviewToEditCycles:     5,
				DeliveriesWithPreTests: 1,
			},
			"apps/web": {
				Deliveries:           3,
				DeliveriesWithRework: 1,
				ReviewToEditCycles:   1,
			},
		},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 {
		t.Fatalf("expected one localized delivery finding: %#v", findings)
	}
	finding := findings[0]
	if finding.Target != "packages/runtime/src/engine.go" || finding.Lever != "source code" ||
		!strings.Contains(finding.Action, "exact target") ||
		!strings.Contains(finding.Evidence, "top cohort packages/runtime") ||
		!strings.Contains(finding.Evidence, "top exact rework target packages/runtime/src/engine.go: 4 cycles, current file 3 lines") {
		t.Fatalf("localized delivery finding mismatch: %#v", finding)
	}
}

func TestBuildSessionFindingsIdentifiesEffectiveConfiguredCheck(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.DeliveryRework = deliveryReworkMetrics{
		Deliveries:           8,
		DeliveriesWithRework: 4,
		ReviewToEditCycles:   6,
		Sessions:             4,
		ReworkLevers:         map[string]int{"source code": 6},
		ReworkScopes:         map[string]int{"(root)": 6},
		Cohorts: map[string]deliveryCohortMetrics{
			"packages/runtime": {
				Deliveries:           8,
				DeliveriesWithRework: 4,
				ReviewToEditCycles:   6,
				VerificationChecks: map[string]verificationMetrics{
					"repo/test-unit": {
						Deliveries:            4,
						DeliveriesWithRework:  1,
						FailedRuns:            2,
						FailFixPassDeliveries: 1,
					},
				},
			},
		},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 {
		t.Fatalf("expected one verification finding: %#v", findings)
	}
	finding := findings[0]
	if !strings.Contains(finding.Evidence, "check repo/test-unit: 1/4 verified deliveries reworked versus 3/4 without it") ||
		!strings.Contains(finding.Action, "Run repo/test-unit after the latest edit") {
		t.Fatalf("configured check effectiveness mismatch: %#v", finding)
	}
}

func TestBuildSessionFindingsAttributesDownstreamFailureToMissingFreshCheck(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.DownstreamQuality = downstreamQualityMetrics{
		Deliveries:                   10,
		DeliveriesWithFailure:        3,
		FailedDeliveriesWithPreTests: 1,
		FailureRuns:                  4,
		FollowUpEditCycles:           2,
		RedeliveryAttempts:           2,
		RecoveredDeliveries:          1,
		Sessions:                     3,
		FailureChecks:                map[string]int{"repo/test-unit": 4},
		Cohorts: map[string]downstreamCohortMetrics{
			"packages/runtime": {
				Deliveries:                   6,
				DeliveriesWithFailure:        3,
				FailedDeliveriesWithPreTests: 1,
				FailureRuns:                  4,
				RecoveredDeliveries:          1,
			},
		},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 {
		t.Fatalf("expected one downstream-quality finding: %#v", findings)
	}
	finding := findings[0]
	if finding.Category != "delivery-quality" || finding.Lever != "tooling" ||
		finding.Confidence != "high" || finding.Target != "packages/runtime" ||
		!strings.Contains(finding.Action, "require the failed check") ||
		!strings.Contains(finding.Evidence, "top downstream check repo/test-unit: 4 failures") {
		t.Fatalf("downstream finding mismatch: %#v", finding)
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

func TestBuildSessionFindingsLocalizesCompletedTaskCostTail(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Outcomes = completionEpisodeAnalysis{
		ToolUsingCompleted:       40,
		TopDecileFreshTokenShare: 0.45,
		FreshTokens: outcomeDistribution{
			Count: 40,
			P50:   100,
			P90:   500,
			Max:   3_000,
		},
		TailDrivers: taskCostTailDrivers{
			TailEpisodes:     4,
			OrdinaryEpisodes: 36,
			TargetCohorts: []taskCostTailDriver{{
				Name:             "packages/runtime",
				TailEpisodes:     4,
				OrdinaryEpisodes: 6,
				PrevalenceDelta:  0.83,
				PrevalenceLift:   6,
			}},
		},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 {
		t.Fatalf("expected one task cost finding: %#v", findings)
	}
	finding := findings[0]
	if finding.Target != "packages/runtime" ||
		!strings.Contains(finding.Evidence, "strongest observed cohort association packages/runtime") ||
		!strings.Contains(finding.Action, "repository cohort") {
		t.Fatalf("localized task-cost finding mismatch: %#v", finding)
	}
}

func TestBuildSessionFindingsUsesHighTailPhaseMix(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Outcomes = completionEpisodeAnalysis{
		ToolUsingCompleted:       40,
		TopDecileFreshTokenShare: 0.45,
		FreshTokens: outcomeDistribution{
			Count: 40,
			P50:   100,
			P90:   500,
			Max:   3_000,
		},
		TailPhases: []taskPhaseTailAssociation{{
			Phase:         "verification",
			TailShare:     0.42,
			OrdinaryShare: 0.17,
			ShareDelta:    0.25,
		}},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 ||
		!strings.Contains(findings[0].Evidence, "verification accounted for 42%") ||
		!strings.Contains(findings[0].Action, "checks, failures, and output") {
		t.Fatalf("phase cost finding mismatch: %#v", findings)
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
	friction, err := filterSessionFindings(findings, "friction")
	if err != nil {
		t.Fatalf("friction focus: %v", err)
	}
	if len(friction) != len(findings) {
		t.Fatalf("friction focus should preserve the broad action queue: %#v", friction)
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
