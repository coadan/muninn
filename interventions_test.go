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
	}
	got := buildSessionInterventions(findings)
	if len(got) != 1 || got[0].ID != "intervention/tool/bwb" ||
		got[0].PrimarySignal != "owned-operation/bwb/publish" ||
		got[0].Action != "fix publish" ||
		!reflect.DeepEqual(got[0].SupportingSignals, []string{"owned-tool/bwb"}) {
		t.Fatalf("owned-tool intervention mismatch: %#v", got)
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
