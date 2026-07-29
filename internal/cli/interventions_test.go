package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSessionInterventionsConsolidatesDiscoveryEvidence(t *testing.T) {
	findings := []sessionFinding{
		{
			Category: "session-loop", Signal: "session-loop/discovery",
			Title:    "context compactions concentrate in phase: discovery",
			Evidence: "phase evidence", Action: "improve bounded discovery",
			Lever: "repository workflow", Confidence: "medium", LastSeen: "2026-07-29T10:00:00Z", score: 700,
		},
		{
			Category: "discovery", Signal: "discovery/bundled-search",
			Title:    "bundled search/read discovery remains output-heavy",
			Evidence: "output evidence", Lever: "tooling", Confidence: "medium", score: 800,
		},
		{
			Category: "agent-interface", Signal: "agent-interface/repeated-cross-call-workflow-source-discovery-and-navigation",
			Title:    "repeated cross-call workflow: source discovery and navigation",
			Evidence: "transition evidence", Lever: "tooling", Confidence: "medium", score: 900,
		},
		{
			Category: "session-loop", Signal: "session-loop/context-compactions-indicate-long-or-looping-sessions",
			Title:    "context compactions indicate long or looping sessions",
			Evidence: "generic compaction evidence", Lever: "instructions/docs", Confidence: "medium", score: 600,
		},
	}
	got := buildSessionInterventions(findings)
	if len(got) != 1 {
		t.Fatalf("interventions=%#v want one discovery intervention", got)
	}
	intervention := got[0]
	if intervention.ID != "intervention/workflow/discovery" ||
		intervention.PrimarySignal != "session-loop/discovery" ||
		intervention.FindingCount != 4 ||
		len(intervention.SupportingSignals) != 3 ||
		!strings.Contains(intervention.Evidence, "3 additional findings") {
		t.Fatalf("discovery intervention mismatch: %#v", intervention)
	}
}

func TestBuildSessionInterventionsKeepsOwnedOperationsExact(t *testing.T) {
	findings := []sessionFinding{
		{
			Category: "owned-tool", Signal: "owned-tool/bwb", Target: "bwb",
			Title: "tool friction", Control: "local", Lever: "tooling", Confidence: "high", score: 900,
		},
		{
			Category: "owned-operation", Signal: "owned-operation/bwb/publish", Target: "bwb/publish",
			Title: "publish friction", Action: "fix publish", Control: "local",
			Lever: "tooling", Confidence: "high", score: 700,
		},
		{
			Category: "delivery-quality", Signal: "delivery-quality/bwb/test", Target: "bwb/test",
			Title:   "delivered changes repeatedly fail downstream checks",
			Control: "local", Lever: "tooling", Confidence: "high", score: 650,
		},
		{
			Category: "session-loop", Signal: "session-loop/progress-stall/bwb/api", Target: "bwb/api",
			Title:   "progress stalls while waiting on: bwb/api",
			Control: "local", Lever: "tooling", Confidence: "medium", score: 600,
		},
	}
	got := buildSessionInterventions(findings)
	if len(got) != 4 ||
		got[0].ID != "intervention/operation/bwb/publish" ||
		got[0].PrimarySignal != "owned-operation/bwb/publish" ||
		got[0].Action != "fix publish" ||
		len(got[0].SupportingSignals) != 0 ||
		got[1].ID != "intervention/tool/bwb" {
		t.Fatalf("exact owned-operation interventions mismatch: %#v", got)
	}
}

func TestBuildSessionInterventionsDoesNotTreatRepositorySourceAsLocalTool(t *testing.T) {
	findings := []sessionFinding{{
		Category: "delivery-quality", Signal: "delivery-quality/packages/runtime",
		Target: "packages/runtime", Title: "source failure",
		Control: "repository", Lever: "source code", Confidence: "medium",
	}}
	got := buildSessionInterventions(findings)
	if len(got) != 1 || got[0].ID != "intervention/delivery-quality/packages/runtime" {
		t.Fatalf("repository source was grouped as a local tool: %#v", got)
	}
}

func TestBuildSessionInterventionsRanksActionabilityBeforeRawCategoryScore(t *testing.T) {
	findings := []sessionFinding{
		{
			Category: "session-loop", Signal: "session-loop/large", Title: "large generic cost",
			Control: "repository", Lever: "instructions/docs", Confidence: "medium", score: 100_000,
		},
		{
			Category: "diagnostic-failure", Signal: "diagnostic-failure/repeated", Title: "repeated failure",
			Control: "repository", Lever: "source code", Confidence: "high", score: 1_000,
		},
		{
			Category: "owned-operation", Signal: "owned-operation/cli/check", Target: "cli/check",
			Title: "owned operation friction", Control: "local", Lever: "tooling", Confidence: "high", score: 500,
		},
	}
	got := buildSessionInterventions(findings)
	if len(got) != 3 {
		t.Fatalf("interventions=%#v", got)
	}
	if got[0].ID != "intervention/operation/cli/check" || got[0].Priority != "highest" ||
		got[1].ID != "intervention/diagnostic-failure/repeated" || got[1].Priority != "high" ||
		got[2].ID != "intervention/session-loop/large" || got[2].Priority != "medium" {
		t.Fatalf("actionability ordering mismatch: %#v", got)
	}
}

func TestBuildSessionInterventionsDefersAdditionalOwnerHotspots(t *testing.T) {
	findings := []sessionFinding{
		{
			Category: "code-structure", Signal: "code-structure/file-cost/src/first.go",
			Title: "first owner", Target: "src/first.go",
			Lever: "source code", Confidence: "medium", score: 900,
		},
		{
			Category: "code-structure", Signal: "code-structure/file-cost/src/second.go",
			Title: "second owner", Target: "src/second.go",
			Lever: "source code", Confidence: "medium", score: 800,
		},
		{
			Category: "output-cost", Signal: "output-cost/tool-exec",
			Title: "oversized output", Lever: "tooling", Confidence: "medium", score: 700,
		},
	}

	got := buildSessionInterventions(findings)
	if len(got) != 3 ||
		got[0].ID != "intervention/code-structure/file-cost/src/first.go" ||
		got[1].ID != "intervention/output-cost/tool-exec" ||
		got[2].ID != "intervention/code-structure/file-cost/src/second.go" {
		t.Fatalf("owner diversity mismatch: %#v", got)
	}
}

func TestBuildSessionInterventionsRanksRecurringDiscoveryAboveOneSessionCompaction(t *testing.T) {
	findings := []sessionFinding{
		{
			Category: "session-loop", Signal: "session-loop/context-compactions-indicate-long-or-looping-sessions",
			Title:   "context compactions indicate long or looping sessions",
			Control: "instructions", Lever: "instructions/docs", Confidence: "low", score: 10_000,
		},
		{
			Category: "discovery", Signal: "discovery/bundled-search",
			Title:   "bundled search/read discovery remains output-heavy",
			Control: "repository", Lever: "tooling", Confidence: "medium", score: 500,
		},
	}

	got := buildSessionInterventions(findings)
	if len(got) != 2 ||
		got[0].ID != "intervention/workflow/discovery" ||
		got[0].Priority != "medium" ||
		got[1].ID != "intervention/session/compaction" ||
		got[1].Priority != "low" {
		t.Fatalf("recurrence did not drive intervention order: %#v", got)
	}
}

func TestBuildSessionInterventionsUsesPriorityDriverAsPrimaryEvidence(t *testing.T) {
	findings := []sessionFinding{
		{
			Category: "verification-loop", Signal: "verification-loop/repair", Target: "check",
			Title: "large repair loop", Control: "repository", Lever: "unknown",
			Confidence: "medium", score: 100_000,
		},
		{
			Category: "verification-loop", Signal: "verification-loop/repeated", Target: "check",
			Title: "repeated check", Control: "repository", Lever: "tooling",
			Confidence: "high", Action: "reuse the terminal result", score: 500,
		},
	}
	got := buildSessionInterventions(findings)
	if len(got) != 1 || got[0].Priority != "high" ||
		got[0].PrimarySignal != "verification-loop/repeated" ||
		got[0].Confidence != "high" || got[0].Lever != "tooling" ||
		got[0].Action != "reuse the terminal result" {
		t.Fatalf("priority driver was not primary evidence: %#v", got)
	}
}

func TestFindingsViewPrintsCompactInterventionQueue(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Summary.Sessions = 4
	report.Summary.ToolCalls = 20
	report.Summary.FreshTokens = 1_000
	report.Interventions = []sessionIntervention{{
		ID:            "intervention/workflow/discovery",
		Title:         "improve discovery",
		PrimarySignal: "session-loop/discovery",
		Evidence:      "evidence",
		Action:        "act",
		Lever:         "tooling",
		Confidence:    "medium",
	}}
	out, err := captureStdout(t, func() error {
		printCodexSessionInsights(report, defaultRepositoryConfig(), 5, "findings")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Scope: 4 sessions", "Interventions:", "intervention/workflow/discovery"} {
		if !strings.Contains(out, want) {
			t.Fatalf("compact findings output missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"Model tokens:", "Top tasks by fresh-token proxy:", "Signals:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("compact findings output retained %q:\n%s", unwanted, out)
		}
	}
}

func TestFocusedDiscoveryViewPrintsBoundedActionEvidence(t *testing.T) {
	root := t.TempDir()
	for _, target := range []string{"owner.go", "hidden.go"} {
		if err := os.WriteFile(filepath.Join(root, target), []byte("current"), 0o644); err != nil {
			t.Fatalf("write %s: %v", target, err)
		}
	}
	report := newSessionInsightsReport("codex", nil, root, zeroTime(), zeroTime())
	report.AnalysisScope.Focus = "discovery"
	report.Summary.ReadTargets["owner.go"] = codexTargetMetrics{
		Reads: 12, SearchReadLoops: 4, Sessions: 2, RediscoverySessions: 2,
	}
	report.Summary.ReadTargets["hidden.go"] = codexTargetMetrics{
		Reads: 11, SearchReadLoops: 3, Sessions: 1, RediscoverySessions: 1,
	}
	report.Summary.MixedShellShapes["search -> file reads"] = codexToolMetrics{
		Calls: 6, Sessions: 2, OutputBytes: 40_000,
	}

	out, err := captureStdout(t, func() error {
		printCodexSessionInsights(report, defaultRepositoryConfig(), 1, "focused")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Discovery evidence:",
		"owner.go: 12 reads, 4 search/read loops, 2 sessions, 2 rediscovery sessions",
		"search -> file reads: 6 calls, 2 sessions, ~10,000 visible output tokens",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("focused discovery output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "hidden.go") {
		t.Fatalf("focused discovery output ignored limit:\n%s", out)
	}
}

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = write
	runErr := run()
	os.Stdout = original
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := io.Copy(&output, read); err != nil {
		t.Fatal(err)
	}
	if err := read.Close(); err != nil {
		t.Fatal(err)
	}
	return output.String(), runErr
}
