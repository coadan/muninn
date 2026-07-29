package cli

import (
	"strings"
	"testing"
)

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
