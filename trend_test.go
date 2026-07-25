package main

import (
	"strings"
	"testing"
	"time"
)

func trendTestReport(since, generatedAt time.Time, scope sessionAnalysisScope) codexSessionInsightsReport {
	report := newSessionInsightsReport("codex", nil, tTempWorkspace, since, generatedAt)
	report.AnalysisScope = scope
	return report
}

const tTempWorkspace = "/workspace"

func TestValidateSessionTrendComparisonAcceptsMatchedLookbackScope(t *testing.T) {
	now := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	scope := sessionAnalysisScope{WindowKind: "lookback", LookbackSeconds: int64((7 * 24 * time.Hour) / time.Second)}
	baseline := trendTestReport(now.Add(-7*24*time.Hour), now, scope)
	current := trendTestReport(now.Add(time.Hour-7*24*time.Hour), now.Add(time.Hour), scope)
	if err := validateSessionTrendComparison(baseline, current, "before"); err != nil {
		t.Fatalf("matched trend scope: %v", err)
	}
}

func TestValidateSessionTrendComparisonRejectsMismatchedLookbackWithCorrection(t *testing.T) {
	now := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	baseline := trendTestReport(now.Add(-7*24*time.Hour), now, sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64((7 * 24 * time.Hour) / time.Second),
	})
	current := trendTestReport(now.Add(-4*time.Hour), now, sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64((4 * time.Hour) / time.Second),
	})
	err := validateSessionTrendComparison(baseline, current, "before")
	if err == nil || !strings.Contains(err.Error(), "rerun with --since 1w") {
		t.Fatalf("expected exact matched-window correction, got %v", err)
	}
}

func TestValidateSessionTrendComparisonRejectsScopeMismatches(t *testing.T) {
	now := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	baseScope := sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64((24 * time.Hour) / time.Second),
	}
	for _, test := range []struct {
		name    string
		current sessionAnalysisScope
		want    string
	}{
		{
			name: "task",
			current: sessionAnalysisScope{
				WindowKind:      "lookback",
				LookbackSeconds: int64((24 * time.Hour) / time.Second),
				Task:            "focused-task",
			},
			want: "--task scope",
		},
		{
			name: "archives",
			current: sessionAnalysisScope{
				WindowKind:      "lookback",
				LookbackSeconds: int64((24 * time.Hour) / time.Second),
				IncludeArchived: true,
			},
			want: "--include-archived",
		},
		{
			name: "focus",
			current: sessionAnalysisScope{
				WindowKind:      "lookback",
				LookbackSeconds: int64((24 * time.Hour) / time.Second),
				Focus:           "structure",
			},
			want: "--focus scope",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline := trendTestReport(now.Add(-24*time.Hour), now, baseScope)
			current := trendTestReport(now.Add(-24*time.Hour), now, test.current)
			err := validateSessionTrendComparison(baseline, current, "before")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q mismatch, got %v", test.want, err)
			}
		})
	}
}

func TestValidateSessionTrendComparisonSupportsLegacyDefaultCheckpoint(t *testing.T) {
	now := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	baseline := trendTestReport(now.Add(-7*24*time.Hour), now, sessionAnalysisScope{})
	baseline.SchemaVersion = 28
	current := trendTestReport(now.Add(-7*24*time.Hour), now, sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64((7 * 24 * time.Hour) / time.Second),
	})
	if err := validateSessionTrendComparison(baseline, current, "legacy"); err != nil {
		t.Fatalf("legacy default checkpoint should remain comparable: %v", err)
	}
}

func TestValidateSessionTrendComparisonTreatsBroadFrictionFocusAsDefault(t *testing.T) {
	now := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	baseline := trendTestReport(now.Add(-24*time.Hour), now, sessionAnalysisScope{
		WindowKind:      "lookback",
		LookbackSeconds: int64((24 * time.Hour) / time.Second),
	})
	current := baseline
	current.AnalysisScope.Focus = "friction"
	if err := validateSessionTrendComparison(baseline, current, "before"); err != nil {
		t.Fatalf("broad friction focus should match default findings: %v", err)
	}
}
