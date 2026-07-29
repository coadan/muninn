package cli

import (
	"os"
	"path/filepath"
	"strconv"
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

func TestBuildSessionFindingsExposesManagedRepositoryOwner(t *testing.T) {
	root := t.TempDir()
	rawTarget := ".workbench/repos/breyta/src/large_owner.clj"
	path := filepath.Join(root, filepath.FromSlash(rawTarget))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir managed source: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 16*1024)), 0o644); err != nil {
		t.Fatalf("write managed source: %v", err)
	}
	report := newSessionInsightsReport("codex", nil, root, zeroTime(), zeroTime())
	report.Summary.ReadTargets[rawTarget] = codexTargetMetrics{
		Reads:           12,
		SearchReadLoops: 4,
		Sessions:        3,
	}

	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 ||
		findings[0].Repository != "breyta" ||
		findings[0].Target != "src/large_owner.clj" ||
		findings[0].Signal != "code-structure/breyta/src/large_owner.clj" ||
		sessionFindingDisplayTarget(findings[0]) != " · breyta/src/large_owner.clj" {
		t.Fatalf("managed repository owner leaked cache identity: %#v", findings)
	}
}

func TestInstructionDiscoveryRequiresMaterialWithinSessionRediscovery(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "deps.edn")
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	report := newSessionInsightsReport("codex", nil, root, zeroTime(), zeroTime())
	report.Summary.ReadTargets["deps.edn"] = codexTargetMetrics{
		Reads:                       141,
		SearchReadLoops:             6,
		Sessions:                    128,
		RediscoverySessions:         6,
		UneditedRediscoverySessions: 6,
	}
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("near-once-per-session manifest reads became friction: %#v", findings)
	}

	report.Summary.ReadTargets["deps.edn"] = codexTargetMetrics{
		Reads:                       180,
		SearchReadLoops:             30,
		Sessions:                    100,
		RediscoverySessions:         30,
		UneditedRediscoverySessions: 30,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "instruction-discovery" ||
		findings[0].Confidence != "medium" ||
		!strings.Contains(findings[0].Evidence, "rediscovery without edits affected 30 sessions") {
		t.Fatalf("material rediscovery finding mismatch: %#v", findings)
	}

	report.Summary.ReadTargets["deps.edn"] = codexTargetMetrics{
		Reads:                       300,
		SearchReadLoops:             80,
		Sessions:                    100,
		RediscoverySessions:         80,
		UneditedRediscoverySessions: 80,
	}
	findings = buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Confidence != "high" {
		t.Fatalf("majority-session rediscovery should be high confidence: %#v", findings)
	}
}

func TestInstructionDiscoveryExcludesSessionsThatEditTheOwner(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("current"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	report := newSessionInsightsReport("codex", nil, root, zeroTime(), zeroTime())
	report.Summary.ReadTargets["README.md"] = codexTargetMetrics{
		Reads:                       20,
		SearchReadLoops:             8,
		Sessions:                    4,
		RediscoverySessions:         4,
		EditedSessions:              2,
		UneditedRediscoverySessions: 2,
	}
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("edit-driven rereads became rediscovery friction: %#v", findings)
	}
	report.Summary.ReadTargets["README.md"] = codexTargetMetrics{
		Reads:                       20,
		SearchReadLoops:             8,
		Sessions:                    4,
		RediscoverySessions:         4,
		EditedSessions:              1,
		UneditedRediscoverySessions: 3,
	}
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 1 {
		t.Fatalf("repeated unedited rediscovery was hidden: %#v", findings)
	}
}

func TestDiversifySessionFindingsKeepsHighestImpactWithinCategory(t *testing.T) {
	findings := []sessionFinding{
		{Category: "instruction-discovery", Target: "recent-low.md", LastSeen: "2026-07-29T12:00:00Z", score: 10},
		{Category: "instruction-discovery", Target: "recent-two.md", LastSeen: "2026-07-29T11:00:00Z", score: 20},
		{Category: "instruction-discovery", Target: "recent-three.md", LastSeen: "2026-07-29T10:00:00Z", score: 30},
		{Category: "instruction-discovery", Target: "recent-four.md", LastSeen: "2026-07-29T09:00:00Z", score: 40},
		{Category: "instruction-discovery", Target: "older-high.md", LastSeen: "2026-07-20T09:00:00Z", score: 1_000},
	}
	got := diversifySessionFindings(findings)
	if len(got) != 4 {
		t.Fatalf("diversified findings=%#v want four", got)
	}
	targets := map[string]bool{}
	for _, finding := range got {
		targets[finding.Target] = true
	}
	if !targets["older-high.md"] || targets["recent-low.md"] {
		t.Fatalf("category cap did not retain highest impact: %#v", got)
	}
}

func TestBundledDiscoveryRequiresCrossSessionEvidenceForMediumConfidence(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.MixedShellShapes["search -> file reads"] = codexToolMetrics{
		Calls:       11,
		Sessions:    1,
		OutputBytes: 240_000,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "discovery" ||
		findings[0].Confidence != "low" || findings[0].Sessions != 1 {
		t.Fatalf("single-session discovery was overconfident: %#v", findings)
	}

	metrics := report.Summary.MixedShellShapes["search -> file reads"]
	metrics.Sessions = 2
	report.Summary.MixedShellShapes["search -> file reads"] = metrics
	findings = buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Confidence != "medium" ||
		!strings.Contains(findings[0].Evidence, "at least 2 sessions") {
		t.Fatalf("cross-session discovery confidence mismatch: %#v", findings)
	}
}

func TestBundledDiscoveryRecognizesFallbackAfterBoundedInspect(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.MixedShellShapes["search -> file reads"] = codexToolMetrics{
		Calls:       20,
		Sessions:    3,
		OutputBytes: 240_000,
	}
	report.Summary.CrossCallTransitions["bounded task inspect -> file reads"] = codexTransitionMetrics{
		Count:    6,
		Sessions: 2,
	}
	report.Summary.CrossCallTransitions["bounded task inspect -> search"] = codexTransitionMetrics{
		Count:    5,
		Sessions: 2,
	}

	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "discovery" {
		t.Fatalf("expected one discovery finding: %#v", findings)
	}
	discovery := findings[0]
	if !strings.Contains(discovery.Evidence, "followed by raw search or file reads 11 times") ||
		!strings.Contains(discovery.Action, "already used") ||
		!strings.Contains(discovery.Action, "improve its result or continuation boundary") {
		t.Fatalf("bounded-inspect fallback was not actionable: %#v", discovery)
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

func TestCodexInlineOrchestrationFlagsVeryLongCLIProgram(t *testing.T) {
	command := "node --import tsx --env-file=.env.local -e '" +
		strings.Repeat("const value = await import(\"./worker/provider.ts\");", 48) + "'"
	arguments := `{"cmd":` + strconv.Quote(command) + `}`
	if got := codexInlineOrchestrationBytes("exec_command", arguments, ""); got != int64(len(command)) {
		t.Fatalf("very long CLI program bytes=%d want %d", got, len(command))
	}
	if got := codexShellCommandFamily("exec_command", arguments, ""); got != "inline runtime" {
		t.Fatalf("very long CLI program family=%q want inline runtime", got)
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
	if size := codexConcurrentToolBatchSize("exec", batch); size != 2 {
		t.Fatalf("concurrent batch size=%d want 2", size)
	}
	if codexConcurrentToolBatch("exec", `const result = await tools.exec_command({cmd:"one"});`) {
		t.Fatal("single nested tool call was misclassified as a concurrent batch")
	}
	if codexConcurrentToolBatch("exec_command", batch) {
		t.Fatal("non-Code Mode tool call was misclassified as a concurrent batch")
	}
}

func TestCodexNestedToolContextKeepsOnlyBoundedToolNames(t *testing.T) {
	input := `const first = await tools.web__run({secret:"hidden"});
	const second = await tools.exec_command({cmd:"private"});
	const repeated = await tools.web__run({secret:"hidden-again"});`
	if got := codexNestedToolContext("exec", input); got != "nested tools exec_command + web__run" {
		t.Fatalf("nested tool context=%q", got)
	}
	if got := codexNestedToolContext("exec_command", input); got != "" {
		t.Fatalf("non-Code Mode call received nested context %q", got)
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

func TestBuildSessionFindingsFlagsOneVeryLongCLIInput(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.InlineOrchestrationCalls = 1
	report.Summary.InlineOrchestrationBytes = 2300
	report.Summary.InlineOrchestrationMaxBytes = 2300
	report.Summary.InlineOrchestrationSessions = 1
	report.Summary.InlineOrchestrationByTool["exec_command"] = codexInlineMetrics{
		Calls:    1,
		Sessions: 1,
		Bytes:    2300,
		MaxBytes: 2300,
	}
	report.Summary.InlineOrchestrationByFamily["inline runtime"] = codexInlineMetrics{
		Calls:    1,
		Sessions: 1,
		Bytes:    2300,
		MaxBytes: 2300,
	}
	report.Summary.InlineOrchestrationByOwner["void-cli/diagnostic"] = codexInlineMetrics{
		Calls:    1,
		Sessions: 1,
		Bytes:    2300,
		MaxBytes: 2300,
	}
	report.Tasks = []codexTaskInsights{{
		Task: "pressure-proof",
		codexAggregateMetrics: codexAggregateMetrics{
			InlineOrchestrationCalls:    1,
			InlineOrchestrationBytes:    2300,
			InlineOrchestrationMaxBytes: 2300,
			InlineOrchestrationSessions: 1,
			InlineOrchestrationByTool:   map[string]codexInlineMetrics{},
			InlineOrchestrationByFamily: map[string]codexInlineMetrics{},
			InlineOrchestrationByOwner:  map[string]codexInlineMetrics{},
		},
	}}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 ||
		findings[0].Title != "very long CLI input is rebuilding an inspection workflow" ||
		findings[0].Control != "local" ||
		findings[0].Lever != "tooling" ||
		findings[0].Target != "void-cli/diagnostic" ||
		!strings.Contains(findings[0].Evidence, "exec_command 1 calls/2,300 bytes") ||
		!strings.Contains(findings[0].Evidence, "families: inline runtime 1 calls/2,300 bytes") ||
		!strings.Contains(findings[0].Evidence, "ownership: void-cli/diagnostic 1 calls/2,300 bytes") ||
		!strings.Contains(findings[0].Evidence, "top task cohort pressure-proof: 1 calls/2,300 bytes") {
		t.Fatalf("very long CLI input finding mismatch: %#v", findings)
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

func TestBuildSessionFindingsRequiresMaterialWorkflowRepetition(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.CrossCallTransitions["git inspect -> git inspect"] = codexTransitionMetrics{
		Count:    866,
		Sessions: 344,
	}
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("ordinary change inspection produced workflow finding: %#v", findings)
	}

	report.Summary.CrossCallTransitions["git inspect -> git inspect"] = codexTransitionMetrics{
		Count:    1_032,
		Sessions: 344,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Title != "repeated cross-call workflow: change inspection" {
		t.Fatalf("material change inspection workflow missing: %#v", findings)
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

func TestBuildSessionFindingsFlagsRepeatedSuccessfulOwnedOperation(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.OwnedOperations["repo/check-worker"] = codexOwnedOperationMetrics{
		Calls: 112, Sessions: 1,
	}
	report.Summary.Activity[sessionActivityKey("owned-operation", "repo/check-worker")] = time.Now()
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 ||
		!strings.Contains(findings[0].Title, "repeated excessively") ||
		!strings.Contains(findings[0].Evidence, "112.0 definitely attributed successful calls per session") ||
		!strings.Contains(findings[0].Action, "once at the verification boundary") {
		t.Fatalf("repeated successful operation finding mismatch: %#v", findings)
	}

	report.Summary.OwnedOperations["repo/check-worker"] = codexOwnedOperationMetrics{
		Calls: 123, AmbiguousCalls: 36, Sessions: 5,
		FailedCalls: 29, AmbiguousFailedCalls: 13,
	}
	report.Summary.OwnedOperationFailureReasons["repo/check-worker"] = map[string]codexOccurrenceMetrics{
		"other non-zero exit": {Count: 29, Sessions: 4},
	}
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("failed repair attempts became repeated successful operation friction: %#v", findings)
	}

	report.Summary.OwnedOperations["repo/check-worker"] = codexOwnedOperationMetrics{
		Calls: 753, AmbiguousCalls: 713, Sessions: 35,
	}
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("bundled operation attribution became repeated roundtrip friction: %#v", findings)
	}

	report.Summary.OwnedOperations["repo/check-worker"] = codexOwnedOperationMetrics{
		Calls: 39, Sessions: 1,
	}
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("ordinary successful operation volume became friction: %#v", findings)
	}
}

func TestBuildSessionFindingsDoesNotChargeBundledFrictionToOwnedTool(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.OwnedTooling["repo"] = codexToolMetrics{
		Calls:                          10,
		AmbiguousCalls:                 10,
		FailedCalls:                    3,
		AmbiguousFailedCalls:           3,
		TruncatedCalls:                 4,
		AmbiguousTruncatedCalls:        4,
		OutputBytes:                    240_000,
		AmbiguousOutputBytes:           240_000,
		EstimatedOutputTokens:          60_000,
		EstimatedAmbiguousOutputTokens: 60_000,
	}
	config := defaultRepositoryConfig()
	config.OwnedTools = []ownedToolConfig{{ID: "repo"}}
	if findings := buildSessionFindings(report, config); len(findings) != 0 {
		t.Fatalf("ambiguous bundled metrics became owned-tool friction: %#v", findings)
	}
}

func TestBuildSessionFindingsRequiresRecurringOwnedToolFriction(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.OwnedTooling["repo"] = codexToolMetrics{
		Calls: 10, Sessions: 1, FailedCalls: 2,
	}
	report.sessionRecords = []codexSessionRecord{{
		OwnedTooling: map[string]codexToolMetrics{
			"repo": {Calls: 10, FailedCalls: 2},
		},
	}}
	config := defaultRepositoryConfig()
	config.OwnedTools = []ownedToolConfig{{ID: "repo"}}
	if findings := buildSessionFindings(report, config); len(findings) != 0 {
		t.Fatalf("one-session owned-tool failures became recurring friction: %#v", findings)
	}

	report.Summary.OwnedTooling["repo"] = codexToolMetrics{
		Calls: 10, Sessions: 1, FailedCalls: 5,
	}
	report.sessionRecords[0].OwnedTooling["repo"] = codexToolMetrics{
		Calls: 10, FailedCalls: 5,
	}
	findings := buildSessionFindings(report, config)
	if len(findings) != 1 ||
		!strings.Contains(findings[0].Title, "concentrated single-session friction") ||
		findings[0].Confidence != "medium" {
		t.Fatalf("concentrated owned-tool friction mismatch: %#v", findings)
	}
	if interventions := buildSessionInterventions(findings); len(interventions) != 1 ||
		interventions[0].Priority != "medium" {
		t.Fatalf("concentrated owned-tool priority mismatch: %#v", interventions)
	}

	report.Summary.OwnedTooling["repo"] = codexToolMetrics{
		Calls: 20, Sessions: 2, FailedCalls: 2,
	}
	report.sessionRecords = []codexSessionRecord{
		{OwnedTooling: map[string]codexToolMetrics{"repo": {Calls: 10, FailedCalls: 1}}},
		{OwnedTooling: map[string]codexToolMetrics{"repo": {Calls: 10, FailedCalls: 1}}},
	}
	findings = buildSessionFindings(report, config)
	if len(findings) != 1 ||
		!strings.Contains(findings[0].Evidence, "2 attributable failures across 2 failure sessions") {
		t.Fatalf("cross-session owned-tool friction missing: %#v", findings)
	}
}

func TestBuildSessionFindingsRequiresRecurringOwnedOperationFriction(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.OwnedOperations["repo/worktree-land"] = codexOwnedOperationMetrics{
		Calls: 12, Sessions: 2, FailedCalls: 2,
	}
	report.Summary.OwnedOperationFailureReasons["repo/worktree-land"] = map[string]codexOccurrenceMetrics{
		"other non-zero exit": {Count: 2, Sessions: 1},
	}
	report.sessionRecords = []codexSessionRecord{
		{
			OwnedOperations: map[string]codexToolMetrics{
				"repo/worktree-land": {Calls: 2, FailedCalls: 2},
			},
			OwnedOperationFailureReasons: map[string]map[string]int{
				"repo/worktree-land": {"other non-zero exit": 2},
			},
		},
		{
			OwnedOperations: map[string]codexToolMetrics{
				"repo/worktree-land": {Calls: 10},
			},
		},
	}
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("one-session owned-operation failures became recurring friction: %#v", findings)
	}

	report.Summary.OwnedOperations["repo/worktree-land"] = codexOwnedOperationMetrics{
		Calls: 12, Sessions: 2, FailedCalls: 5,
	}
	report.Summary.OwnedOperationFailureReasons["repo/worktree-land"] = map[string]codexOccurrenceMetrics{
		"other non-zero exit": {Count: 5, Sessions: 1},
	}
	report.sessionRecords[0].OwnedOperations["repo/worktree-land"] = codexToolMetrics{
		Calls: 5, FailedCalls: 5,
	}
	report.sessionRecords[0].OwnedOperationFailureReasons["repo/worktree-land"] = map[string]int{
		"other non-zero exit": 5,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 ||
		!strings.Contains(findings[0].Title, "concentrated single-session friction") ||
		findings[0].Confidence != "medium" {
		t.Fatalf("concentrated owned-operation friction mismatch: %#v", findings)
	}
	if interventions := buildSessionInterventions(findings); len(interventions) != 1 ||
		interventions[0].Priority != "medium" {
		t.Fatalf("concentrated owned-operation priority mismatch: %#v", interventions)
	}

	report.Summary.OwnedOperations["repo/worktree-land"] = codexOwnedOperationMetrics{
		Calls: 13, Sessions: 2, FailedCalls: 3,
	}
	report.Summary.OwnedOperationFailureReasons["repo/worktree-land"] = map[string]codexOccurrenceMetrics{
		"other non-zero exit": {Count: 3, Sessions: 2},
	}
	report.sessionRecords[0].OwnedOperations["repo/worktree-land"] = codexToolMetrics{
		Calls: 2, FailedCalls: 2,
	}
	report.sessionRecords[0].OwnedOperationFailureReasons["repo/worktree-land"] = map[string]int{
		"other non-zero exit": 2,
	}
	report.sessionRecords[1].OwnedOperations["repo/worktree-land"] = codexToolMetrics{
		Calls: 11, FailedCalls: 1,
	}
	report.sessionRecords[1].OwnedOperationFailureReasons = map[string]map[string]int{
		"repo/worktree-land": {"other non-zero exit": 1},
	}
	findings = buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 ||
		!strings.Contains(findings[0].Evidence, "3 actionable failures across 2 failure sessions") {
		t.Fatalf("cross-session owned-operation friction missing: %#v", findings)
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
		Calls:                   30,
		AmbiguousCalls:          7,
		Sessions:                2,
		FailedCalls:             5,
		TruncatedCalls:          2,
		AmbiguousTruncatedCalls: 1,
		OutputBytes:             43_000,
		AmbiguousOutputBytes:    4_000,
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
			OwnedOperationAmbiguous: map[string]codexToolMetrics{
				"bwb/run": {Calls: 7, TruncatedCalls: 1, OutputBytes: 4_000},
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
		"recent 24h sessions: 10 calls/1 sessions",
		"7 bundled calls",
		"0 actionable failures",
		"0 truncations",
		"1 ambiguous bundled truncations",
		"~750 attributed output tokens",
		"~1,000 ambiguous bundled output tokens",
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
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("modest successful operation output became friction: %#v", findings)
	}

	metrics := report.Summary.OwnedOperations["repo/inspect"]
	metrics.EstimatedOutputTokens = 60_000
	report.Summary.OwnedOperations["repo/inspect"] = metrics
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Target != "repo/inspect" ||
		!strings.Contains(findings[0].Title, "high-cost") ||
		findings[0].Confidence != "medium" {
		t.Fatalf("material operation output finding mismatch: %#v", findings)
	}
	interventions := buildSessionInterventions(findings)
	if len(interventions) != 1 || interventions[0].Priority != "medium" {
		t.Fatalf("output-only operation priority mismatch: %#v", interventions)
	}
}

func TestBuildSessionFindingsReportsInputCostAndProgressStalls(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Tasks = []codexTaskInsights{{
		Task: "expensive-task",
		codexAggregateMetrics: codexAggregateMetrics{
			Sessions: 4,
			Tokens: normalizedTokenUsage{
				InputTokens:         1_200_000,
				CachedInputTokens:   400_000,
				UncachedInputTokens: 800_000,
			},
			FreshTokens: 900_000,
			Compactions: 2,
		},
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
	report.Summary.AbandonedContinuations["bwb/publish"] = codexOccurrenceMetrics{
		Count:    3,
		Sessions: 2,
	}
	report.Summary.AbandonedContinuations["tests"] = codexOccurrenceMetrics{
		Count:    16,
		Sessions: 16,
	}
	report.Summary.ProgressStalls["mixed shell"] = codexWaitMetrics{
		Calls:    20,
		Seconds:  900,
		Sessions: 4,
	}
	config := defaultRepositoryConfig()
	config.OwnedTools = []ownedToolConfig{{ID: "bwb"}}
	config.Actions.YieldedOperation = "Use `bwb test` and resume it to completion."
	findings := buildSessionFindings(report, config)
	bySignal := map[string]sessionFinding{}
	for _, finding := range findings {
		bySignal[finding.Signal] = finding
	}
	inputSignal := "session-loop/input-cost/expensive-task"
	if finding, ok := bySignal[inputSignal]; !ok ||
		finding.Title != "high input-token cost in task: expensive-task" ||
		!strings.Contains(finding.Evidence, "800,000 uncached") ||
		!strings.Contains(finding.Evidence, "200,000 uncached input") {
		t.Fatalf("input-cost finding mismatch: %#v", finding)
	}
	stallSignal := "session-loop/progress-stall/bwb/api-start"
	if finding, ok := bySignal[stallSignal]; !ok ||
		finding.Confidence != "medium" ||
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
	abandonedSignal := "agent-interface/abandoned-continuation/bwb/publish"
	if finding, ok := bySignal[abandonedSignal]; !ok ||
		!strings.Contains(finding.Evidence, "3 yielded operations") ||
		!strings.Contains(finding.Action, "explicitly terminate") {
		t.Fatalf("abandoned continuation finding mismatch: %#v", finding)
	}
	genericAbandonedSignal := "agent-interface/abandoned-continuation/tests"
	if finding, ok := bySignal[genericAbandonedSignal]; !ok ||
		finding.Control != "repository" ||
		!strings.Contains(finding.Action, "`bwb test`") ||
		!strings.Contains(finding.Evidence, "16 yielded operations") {
		t.Fatalf("generic abandoned continuation finding mismatch: %#v", finding)
	}
}

func TestBuildSessionFindingsRequiresMaterialRapidPolling(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.RapidPolls["tests"] = codexWaitMetrics{
		Calls:    131,
		Seconds:  587,
		Sessions: 72,
	}
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("low-density rapid polls produced finding: %#v", findings)
	}

	report.Summary.RapidPolls["tests"] = codexWaitMetrics{
		Calls:    216,
		Seconds:  900,
		Sessions: 72,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Title != "rapid continuation polling: tests" {
		t.Fatalf("material rapid polling finding missing: %#v", findings)
	}
}

func TestBuildSessionFindingsDoesNotTreatRootAsTaskCohort(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Tasks = []codexTaskInsights{{
		Task: "(root)",
		codexAggregateMetrics: codexAggregateMetrics{
			Sessions: 4,
			Tokens: normalizedTokenUsage{
				InputTokens:         1_200_000,
				CachedInputTokens:   400_000,
				UncachedInputTokens: 800_000,
			},
			FreshTokens: 900_000,
			Compactions: 2,
		},
	}}

	for _, finding := range buildSessionFindings(report, defaultRepositoryConfig()) {
		if strings.Contains(finding.Signal, "/input-cost/") {
			t.Fatalf("root catch-all became a task cohort: %#v", finding)
		}
	}
}

func TestBuildSessionFindingsCompactionConfidenceRequiresRecurrence(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.Sessions = 3
	report.Summary.Compactions = 5
	report.Summary.SessionsWithCompactions = 1
	report.Summary.Tokens.InputTokens = 10_000
	report.Summary.Tokens.CachedInputTokens = 9_000
	report.Summary.Tokens.UncachedInputTokens = 1_000
	report.Summary.Tokens.OutputTokens = 500

	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Confidence != "low" ||
		!strings.Contains(findings[0].Evidence, "1,500 fresh tokens per session") {
		t.Fatalf("single-session compaction confidence mismatch: %#v", findings)
	}

	report.Summary.Compactions = 3
	report.Summary.SessionsWithCompactions = 2
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("one-off cross-session compactions produced finding: %#v", findings)
	}

	report.Summary.Compactions = 4
	findings = buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Confidence != "medium" {
		t.Fatalf("recurring compaction confidence mismatch: %#v", findings)
	}
}

func TestProgressStallConfidenceRequiresCrossSessionRecurrence(t *testing.T) {
	if got := recurringPatternConfidence(1); got != "low" {
		t.Fatalf("single-session pattern confidence=%q want low", got)
	}
	if got := recurringPatternConfidence(2); got != "medium" {
		t.Fatalf("cross-session pattern confidence=%q want medium", got)
	}
}

func TestBuildSessionFindingsRoutesRecurringFailureToFixedContext(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.FailureContexts = map[string]map[string]codexOccurrenceMetrics{
		"test failure": {
			"tests": {Count: 7, Sessions: 3},
		},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 ||
		findings[0].Title != "test failure recurs in tests" ||
		findings[0].Target != "tests" ||
		findings[0].Signal != "recurring-failure/tests" {
		t.Fatalf("recurring failure context mismatch: %#v", findings)
	}
}

func TestBuildSessionFindingsKeepsGenericUnownedFailuresInDetails(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.FailureContexts = map[string]map[string]codexOccurrenceMetrics{
		"other non-zero exit": {
			"other shell": {Count: 8, Sessions: 3},
		},
		"local CLI targeting": {
			"file reads": {Count: 4, Sessions: 2},
		},
	}
	if findings := buildSessionFindings(report, defaultRepositoryConfig()); len(findings) != 0 {
		t.Fatalf("generic unowned failures became interventions: %#v", findings)
	}
	if got := report.Summary.FailureContexts["other non-zero exit"]["other shell"]; got.Count != 8 {
		t.Fatalf("generic failure detail was discarded: %#v", got)
	}

	config := defaultRepositoryConfig()
	config.OwnedTools = []ownedToolConfig{{ID: "repo"}}
	report.Summary.FailureContexts["other non-zero exit"] = map[string]codexOccurrenceMetrics{
		"repo/check": {Count: 4, Sessions: 2},
	}
	findings := buildSessionFindings(report, config)
	if len(findings) != 1 || findings[0].Control != "local" ||
		findings[0].Target != "repo/check" {
		t.Fatalf("owned generic failure lost its repair boundary: %#v", findings)
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

func TestNestedExecOversizedOutputActionNamesTheBoundedControl(t *testing.T) {
	got := oversizedOutputAction("nested tool exec_command", "repository")
	if !strings.Contains(got, "max_output_tokens") ||
		!strings.Contains(got, "narrow or page only") {
		t.Fatalf("nested exec output action was not concrete: %q", got)
	}
}

func TestConcurrentBatchOversizedOutputFindingPreservesBatching(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.OversizedOutputs["concurrent tool batch"] = codexOversizedOutputMetrics{
		Calls: 2, OutputBytes: 75_000, MaxOutputBytes: 40_000, Sessions: 1,
		NestedCalls: 5, MaxNestedCalls: 3,
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	outputFindings, err := filterSessionFindings(findings, "output")
	if err != nil || len(outputFindings) != 1 {
		t.Fatalf("concurrent batch output finding mismatch: findings=%#v err=%v", outputFindings, err)
	}
	finding := outputFindings[0]
	if finding.Title != "concurrent tool batches exceed the shared output budget" ||
		finding.Confidence != "low" ||
		!strings.Contains(finding.Action, "Keep independent calls concurrent") ||
		!strings.Contains(finding.Action, "inspect every partial result") ||
		!strings.Contains(finding.Action, "12,000 visible tokens") ||
		!strings.Contains(finding.Action, "about 4,000 tokens per result") ||
		!strings.Contains(finding.Evidence, "5 nested calls averaged ~3,750 visible output tokens each") ||
		!strings.Contains(finding.Evidence, "largest batch contained 3 calls") {
		t.Fatalf("concurrent batch guidance mismatch: %#v", finding)
	}
}

func TestOversizedOutputFindingRequiresRecurrenceOrSevereCall(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.OversizedOutputs["isolated moderate"] = codexOversizedOutputMetrics{
		Calls: 1, OutputBytes: 40_157, MaxOutputBytes: 40_157, Sessions: 1,
	}
	findings, err := filterSessionFindings(
		buildSessionFindings(report, defaultRepositoryConfig()),
		"output",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("isolated moderate output became a finding: %#v", findings)
	}

	report.Summary.OversizedOutputs["isolated severe"] = codexOversizedOutputMetrics{
		Calls: 1, OutputBytes: 60_000, MaxOutputBytes: 60_000, Sessions: 1,
	}
	findings, err = filterSessionFindings(
		buildSessionFindings(report, defaultRepositoryConfig()),
		"output",
	)
	if err != nil || len(findings) != 1 ||
		findings[0].Signal != "output-cost/isolated-severe" ||
		findings[0].Confidence != "low" ||
		!strings.Contains(findings[0].Evidence, "1 call") ||
		!strings.Contains(findings[0].Evidence, "1 session") {
		t.Fatalf("isolated severe output finding mismatch: findings=%#v err=%v", findings, err)
	}

	report.Summary.OversizedOutputs["cross-session recurring"] = codexOversizedOutputMetrics{
		Calls: 2, OutputBytes: 70_000, MaxOutputBytes: 35_000, Sessions: 2,
	}
	findings, err = filterSessionFindings(
		buildSessionFindings(report, defaultRepositoryConfig()),
		"output",
	)
	if err != nil {
		t.Fatal(err)
	}
	var crossSession sessionFinding
	for _, finding := range findings {
		if finding.Signal == "output-cost/cross-session-recurring" {
			crossSession = finding
		}
	}
	if crossSession.Confidence != "medium" {
		t.Fatalf("cross-session output confidence mismatch: %#v", crossSession)
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
		if got := sessionFindingRepositoryScope(sessionFinding{Target: target}); got != want {
			t.Fatalf("scope for %q=%q want %q", target, got, want)
		}
	}
	if got := sessionFindingRepositoryScope(sessionFinding{
		Repository: "breyta",
		Target:     "src/runtime.clj",
	}); got != "breyta" {
		t.Fatalf("structured repository scope=%q want breyta", got)
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

func TestBuildSessionFindingsFlagsExpensiveVerificationRepairLoop(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.DeliveryRework = deliveryReworkMetrics{
		Deliveries: 1,
		Sessions:   1,
		VerificationChecks: map[string]verificationMetrics{
			"repo/browser-test": {
				Deliveries:            1,
				FailedRuns:            6,
				FailFixPassDeliveries: 1,
			},
			"repo/unit-test": {
				Deliveries:            1,
				FailedRuns:            2,
				FailFixPassDeliveries: 1,
			},
		},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 || findings[0].Category != "verification-loop" ||
		findings[0].Target != "repo/browser-test" ||
		!strings.Contains(findings[0].Evidence, "6 failed runs before 1 fail-fix-pass deliveries") ||
		!strings.Contains(findings[0].Action, "repeat one boundary") {
		t.Fatalf("verification repair-loop finding mismatch: %#v", findings)
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
		FailureSessions:              2,
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
	config := defaultRepositoryConfig()
	config.OwnedTools = []ownedToolConfig{{ID: "repo"}}
	findings := buildSessionFindings(report, config)
	if len(findings) != 1 {
		t.Fatalf("expected one downstream-quality finding: %#v", findings)
	}
	finding := findings[0]
	if finding.Category != "delivery-quality" || finding.Lever != "tooling" ||
		finding.Control != "local" ||
		finding.Confidence != "high" || finding.Target != "repo/test-unit" ||
		!strings.Contains(finding.Action, "require repo/test-unit to pass") ||
		!strings.Contains(finding.Evidence, "top downstream check repo/test-unit: 4 failures") {
		t.Fatalf("downstream finding mismatch: %#v", finding)
	}
}

func TestBuildSessionFindingsKeepsSourceCohortWhenFreshChecksStillFail(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.DownstreamQuality = downstreamQualityMetrics{
		Deliveries:                   8,
		DeliveriesWithFailure:        3,
		FailedDeliveriesWithPreTests: 2,
		FailureRuns:                  3,
		FollowUpEditCycles:           2,
		Sessions:                     3,
		FailureSessions:              2,
		FailureChecks:                map[string]int{"repo/test-unit": 3},
		Cohorts: map[string]downstreamCohortMetrics{
			"packages/runtime": {
				Deliveries:                   5,
				DeliveriesWithFailure:        3,
				FailedDeliveriesWithPreTests: 2,
				FailureRuns:                  3,
				FollowUpEditCycles:           2,
			},
		},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 {
		t.Fatalf("expected one downstream-quality finding: %#v", findings)
	}
	finding := findings[0]
	if finding.Lever != "source code" || finding.Confidence != "medium" ||
		finding.Target != "packages/runtime" ||
		!strings.Contains(finding.Action, "source boundary") {
		t.Fatalf("source-backed downstream finding mismatch: %#v", finding)
	}
}

func TestBuildSessionFindingsDoesNotConfirmUncorrectedPostDeliveryFailures(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.DownstreamQuality = downstreamQualityMetrics{
		Deliveries:            40,
		DeliveriesWithFailure: 12,
		FailureRuns:           30,
		Reverts:               1,
		Sessions:              8,
		FailureSessions:       3,
		FailureChecks:         map[string]int{"tests": 30},
		Cohorts: map[string]downstreamCohortMetrics{
			"scripts": {
				Deliveries:            8,
				DeliveriesWithFailure: 4,
				FailureRuns:           12,
			},
		},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 {
		t.Fatalf("expected one post-delivery association: %#v", findings)
	}
	finding := findings[0]
	if finding.Title != "post-delivery check failures have limited correction evidence" ||
		finding.Confidence != "medium" || finding.Sessions != 3 ||
		!strings.Contains(finding.Why, "1 matching") ||
		!strings.Contains(finding.Why, "not yet confirmed as recurring delivery escapes") ||
		!strings.Contains(finding.Action, "Confirm that the next post-delivery tests failure") {
		t.Fatalf("uncorrected post-delivery failure was overclaimed: %#v", finding)
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

func TestBuildSessionFindingsTurnsActionableHotspotsIntoStableSignals(t *testing.T) {
	root := t.TempDir()
	for _, target := range []string{
		"src/expensive.ts",
		"src/rework.ts",
		"src/risky.ts",
		"src/popular.ts",
	} {
		path := filepath.Join(root, filepath.FromSlash(target))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", target, err)
		}
		if err := os.WriteFile(path, []byte("current"), 0o644); err != nil {
			t.Fatalf("write %s: %v", target, err)
		}
	}
	report := newSessionInsightsReport("codex", nil, root, zeroTime(), zeroTime())
	report.Outcomes.FileHotspots = []fileHotspotMetrics{
		{
			Target:         "src/expensive.ts",
			CompletedTasks: 6,
			EditCalls:      12,
			TaskShare:      0.3,
			FreshTokens:    outcomeDistribution{P50: 80_000, P90: 160_000},
			ToolRoundtrips: outcomeDistribution{P50: 70, P90: 140},
			Classification: "expensive-owner",
		},
		{
			Target:              "src/rework.ts",
			CompletedTasks:      5,
			EditCalls:           9,
			PostReviewEditCalls: 2,
			FollowUpEdits:       1,
			Classification:      "review/rework",
		},
		{
			Target:             "src/risky.ts",
			CompletedTasks:     4,
			EditCalls:          8,
			DownstreamFailures: 2,
			Classification:     "downstream-risk",
		},
		{
			Target:         "src/popular.ts",
			CompletedTasks: 20,
			EditCalls:      40,
			Classification: "healthy-demand",
		},
		{
			Target:         "src/deleted.ts",
			CompletedTasks: 10,
			EditCalls:      30,
			ToolRoundtrips: outcomeDistribution{P50: 100, P90: 200},
			Classification: "expensive-owner",
		},
	}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 3 {
		t.Fatalf("findings=%#v want three actionable hotspots", findings)
	}
	byTarget := map[string]sessionFinding{}
	for _, finding := range findings {
		byTarget[finding.Target] = finding
	}
	if _, exists := byTarget["src/popular.ts"]; exists {
		t.Fatalf("healthy demand produced a finding: %#v", findings)
	}
	if _, exists := byTarget["src/deleted.ts"]; exists {
		t.Fatalf("deleted historical target produced a current-owner finding: %#v", findings)
	}
	expensive := byTarget["src/expensive.ts"]
	if expensive.Signal != "code-structure/file-cost/src/expensive.ts" ||
		expensive.Confidence != "medium" ||
		!strings.Contains(expensive.Action, "split only") {
		t.Fatalf("expensive-owner finding mismatch: %#v", expensive)
	}
	rework := byTarget["src/rework.ts"]
	if rework.Signal != "delivery-quality/file-rework/src/rework.ts" ||
		rework.Confidence != "high" {
		t.Fatalf("rework finding mismatch: %#v", rework)
	}
	risky := byTarget["src/risky.ts"]
	if risky.Signal != "delivery-quality/file-failure/src/risky.ts" ||
		risky.Confidence != "medium" {
		t.Fatalf("downstream-risk finding mismatch: %#v", risky)
	}
}

func TestConsolidateOwnerFindingsPromotesCorroboratedPrimary(t *testing.T) {
	findings := []sessionFinding{
		{
			Category: "code-structure", Title: "repeated navigation into a current source owner",
			Target: "src/runtime.ts", Signal: "code-structure/src/runtime.ts",
			Confidence: "high", Evidence: "navigation", score: 400,
		},
		{
			Category: "code-structure", Title: "repeated edits correlate with high task cost",
			Target: "src/runtime.ts", Signal: "code-structure/file-cost/src/runtime.ts",
			Confidence: "medium", Evidence: "cost", score: 600,
		},
		{
			Category: "delivery-quality", Title: "frequently edited target requires repeated rework",
			Target: "src/runtime.ts", Signal: "delivery-quality/file-rework/src/runtime.ts",
			Confidence: "medium", Evidence: "quality", score: 700,
		},
	}
	got := consolidateOwnerFindings(findings)
	if len(got) != 1 {
		t.Fatalf("consolidated findings=%#v want one", got)
	}
	finding := got[0]
	if finding.Category != "delivery-quality" ||
		finding.Signal != "owner/src/runtime.ts" ||
		finding.Confidence != "high" ||
		len(finding.Supporting) != 3 ||
		!strings.Contains(finding.Why, "high agent task cost") ||
		!strings.Contains(finding.Evidence, "corroborating signals") {
		t.Fatalf("consolidated owner finding mismatch: %#v", finding)
	}
}

func TestConsolidateOwnerFindingsDemotesUncorroboratedNavigation(t *testing.T) {
	got := consolidateOwnerFindings([]sessionFinding{{
		Category:   "code-structure",
		Title:      "repeated navigation into a current source owner",
		Target:     "src/runtime.ts",
		Confidence: "high",
		score:      400,
	}})
	if len(got) != 1 || got[0].Confidence != "medium" || got[0].score != 325 ||
		!strings.Contains(got[0].Why, "discovery evidence only") {
		t.Fatalf("isolated navigation calibration mismatch: %#v", got)
	}
}

func TestFilterSessionFindingsKeepsConsolidatedSupportingCategory(t *testing.T) {
	finding := sessionFinding{
		Category:   "delivery-quality",
		Signal:     "owner/src/runtime.ts",
		Supporting: []string{"code-structure/file-cost/src/runtime.ts"},
	}
	got, err := filterSessionFindings([]sessionFinding{finding}, "structure")
	if err != nil || len(got) != 1 {
		t.Fatalf("structure focus lost consolidated supporting signal: findings=%#v err=%v", got, err)
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
	finding.Title = "an authoritative owner is repeatedly rediscovered"
	if got := sessionFindingDisplayTarget(finding); got != " · file reads" {
		t.Fatalf("distinct target should remain visible: %q", got)
	}
	finding.Repository = "breyta"
	finding.Target = "src/runtime.clj"
	if got := sessionFindingDisplayTarget(finding); got != " · breyta/src/runtime.clj" {
		t.Fatalf("structured repository target mismatch: %q", got)
	}
}

func TestBuildSessionFindingsSuggestsFrequentlyRepeatedFlagAsDefault(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.OwnedFlagEligibleCalls["muninn/analyze"] = codexOccurrenceMetrics{
		Count:    9,
		Sessions: 3,
	}
	report.Summary.OwnedFlags["muninn/analyze/json"] = codexOccurrenceMetrics{
		Count:    8,
		Sessions: 3,
	}
	findings := buildSessionFindings(report, repositoryConfig{
		SchemaVersion: 1,
		OwnedTools: []ownedToolConfig{{
			ID:          "muninn",
			Executables: []string{"muninn"},
		}},
	})
	if len(findings) != 1 ||
		findings[0].Target != "muninn/analyze/json" ||
		!strings.Contains(findings[0].Title, "muninn analyze --json") ||
		!strings.Contains(findings[0].Evidence, "8 of 9") {
		t.Fatalf("candidate-default finding mismatch: %#v", findings)
	}

	report.Summary.OwnedFlags["muninn/analyze/json"] = codexOccurrenceMetrics{
		Count:    7,
		Sessions: 3,
	}
	if findings := buildSessionFindings(report, repositoryConfig{
		SchemaVersion: 1,
		OwnedTools: []ownedToolConfig{{
			ID:          "muninn",
			Executables: []string{"muninn"},
		}},
	}); len(findings) != 0 {
		t.Fatalf("sub-threshold flag produced finding: %#v", findings)
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

func zeroTime() time.Time {
	return time.Time{}
}
