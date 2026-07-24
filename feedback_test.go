package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeFeedbackLabelsRejectRawContent(t *testing.T) {
	if got, err := normalizeFeedbackTarget("BWB/PR"); err != nil || got != "bwb/pr" {
		t.Fatalf("expected logical target normalization, got %q, %v", got, err)
	}
	if got, err := normalizeFeedbackSignal("Existing-PR-Create-Failed"); err != nil || got != "existing-pr-create-failed" {
		t.Fatalf("expected signal normalization, got %q, %v", got, err)
	}
	for _, raw := range []string{"/private/repo", "../repo", "bwb pr", "https://example.com"} {
		if _, err := normalizeFeedbackTarget(raw); err == nil {
			t.Fatalf("expected unsafe target %q to be rejected", raw)
		}
	}
	for _, raw := range []string{"some prose", "../secret", "failed: token=abc"} {
		if _, err := normalizeFeedbackSignal(raw); err == nil {
			t.Fatalf("expected unsafe signal %q to be rejected", raw)
		}
	}
}

func TestSessionStoreFeedbackLifecycleAggregatesSources(t *testing.T) {
	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	observedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	for _, feedback := range []agentFeedback{
		{
			RepositoryKey: "repo-digest",
			Source:        "codex",
			Control:       "local",
			Category:      "roundtrip",
			Target:        "bwb/pr",
			Signal:        "existing-pr-create-failed",
			Occurrences:   2,
			ObservedAt:    observedAt,
		},
		{
			RepositoryKey: "repo-digest",
			Source:        "claude",
			Control:       "local",
			Category:      "roundtrip",
			Target:        "bwb/pr",
			Signal:        "existing-pr-create-failed",
			Occurrences:   1,
			ObservedAt:    observedAt.Add(time.Minute),
		},
	} {
		if err := store.addFeedback(ctx, feedback); err != nil {
			t.Fatalf("add feedback: %v", err)
		}
	}
	rows, err := store.listFeedback(ctx, "repo-digest", observedAt.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("list feedback: %v", err)
	}
	if len(rows) != 1 || rows[0].Occurrences != 3 ||
		strings.Join(rows[0].Sources, ",") != "claude,codex" ||
		rows[0].Status != "open" {
		t.Fatalf("unexpected feedback aggregate: %#v", rows)
	}
	resolved, err := store.resolveFeedback(
		ctx,
		"repo-digest",
		"roundtrip",
		"bwb/pr",
		"existing-pr-create-failed",
		observedAt.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("resolve feedback: %v", err)
	}
	if resolved != 2 {
		t.Fatalf("expected both source records resolved, got %d", resolved)
	}
	openRows, err := store.listFeedback(ctx, "repo-digest", observedAt.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("list open feedback: %v", err)
	}
	if len(openRows) != 0 {
		t.Fatalf("expected no open feedback, got %#v", openRows)
	}
	allRows, err := store.listFeedback(ctx, "repo-digest", observedAt.Add(-time.Hour), true)
	if err != nil {
		t.Fatalf("list all feedback: %v", err)
	}
	if len(allRows) != 1 || allRows[0].Status != "resolved" || allRows[0].Occurrences != 3 {
		t.Fatalf("unexpected resolved feedback: %#v", allRows)
	}
}

func TestBuildSessionFindingsIncludesDirectFeedback(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), zeroTime(), zeroTime())
	report.Feedback = []agentFeedbackAggregate{{
		Control:     "local",
		Category:    "roundtrip",
		Target:      "bwb/pr",
		Signal:      "existing-pr-create-failed",
		Occurrences: 3,
		Sources:     []string{"claude", "codex"},
		Status:      "open",
		LastSeen:    "2026-07-24T12:01:00Z",
	}}
	findings := buildSessionFindings(report, defaultRepositoryConfig())
	if len(findings) != 1 {
		t.Fatalf("expected one direct feedback finding, got %#v", findings)
	}
	finding := findings[0]
	if finding.Category != "agent-interface" || finding.Control != "local" ||
		finding.Target != "bwb/pr" ||
		!strings.Contains(finding.Title, "existing-pr-create-failed") ||
		!strings.Contains(finding.Action, "muninn feedback resolve") {
		t.Fatalf("unexpected direct feedback finding: %#v", finding)
	}
}
