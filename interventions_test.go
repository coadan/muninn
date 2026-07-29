package main

import (
	"bytes"
	"io"
	"os"
	"reflect"
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

func TestBuildSessionInterventionsGroupsOwnedOperationsUnderTool(t *testing.T) {
	findings := []sessionFinding{
		{
			Category: "owned-tool", Signal: "owned-tool/bwb", Target: "bwb",
			Title: "tool friction", Lever: "tooling", Confidence: "high", score: 900,
		},
		{
			Category: "owned-operation", Signal: "owned-operation/bwb/publish", Target: "bwb/publish",
			Title: "publish friction", Action: "fix publish", Lever: "tooling", Confidence: "high", score: 700,
		},
		{
			Category: "delivery-quality", Signal: "delivery-quality/bwb/test", Target: "bwb/test",
			Title: "delivered changes repeatedly fail downstream checks",
			Lever: "tooling", Confidence: "high", score: 650,
		},
	}
	got := buildSessionInterventions(findings)
	if len(got) != 1 || got[0].ID != "intervention/tool/bwb" ||
		got[0].PrimarySignal != "owned-operation/bwb/publish" ||
		got[0].Action != "fix publish" ||
		!reflect.DeepEqual(got[0].SupportingSignals, []string{"delivery-quality/bwb/test", "owned-tool/bwb"}) {
		t.Fatalf("owned-tool intervention mismatch: %#v", got)
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
	if got[0].ID != "intervention/tool/cli" || got[0].Priority != "highest" ||
		got[1].ID != "intervention/diagnostic-failure/repeated" || got[1].Priority != "high" ||
		got[2].ID != "intervention/session-loop/large" || got[2].Priority != "medium" {
		t.Fatalf("actionability ordering mismatch: %#v", got)
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
