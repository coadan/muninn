package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionStorePreservesNormalizedDiagnosticFailure(t *testing.T) {
	sessionsDir := t.TempDir()
	repositoryRoot := filepath.Join(t.TempDir(), "repo")
	failedAt := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	session := normalizedSession{
		Provider:   "codex",
		SourcePath: filepath.Join(sessionsDir, "session.jsonl"),
		CWD:        repositoryRoot,
		Events: []normalizedSessionEvent{
			{
				Sequence: 1, OccurredAt: failedAt, Kind: sessionEventToolOutput,
				Diagnostic: &normalizedDiagnosticObservation{
					Status: "failed", Source: "heimdal", Target: "target", Finished: failedAt,
					Failure: &normalizedDiagnosticFailure{
						Source: "heimdal", Classification: "infrastructure", FailureSource: "runner",
						FailureClass: "error", Fingerprint: "digest", FixturePhase: "fixture-startup",
						DiagnosticStatus: "pending", FailedAt: failedAt, Lever: "tooling",
					},
				},
			},
			{
				Sequence: 2, OccurredAt: failedAt.Add(time.Minute), Kind: sessionEventToken,
				Tokens: normalizedTokenUsage{TotalTokens: 50},
			},
			{Sequence: 3, OccurredAt: failedAt.Add(2 * time.Minute), Kind: sessionEventComplete},
		},
	}
	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	markRepositoryEventScope(&session, repositoryRoot)
	if err := store.replaceSession(
		context.Background(),
		repositoryStoreScopeKey(repositoryRoot, ownershipCatalog{}),
		session,
		1,
		1,
		true,
	); err != nil {
		t.Fatalf("replace session: %v", err)
	}
	report, err := store.analyze(
		context.Background(),
		"codex",
		[]string{sessionsDir},
		repositoryRoot,
		failedAt.Add(-time.Minute),
		failedAt.Add(3*time.Minute),
		"",
		ownershipCatalog{},
		sessionRefreshStats{},
	)
	if err != nil {
		t.Fatalf("analyze store: %v", err)
	}
	if !report.Diagnostics.Available || len(report.Diagnostics.Failures) != 1 {
		t.Fatalf("stored diagnostic missing: %#v", report.Diagnostics)
	}
	if report.Diagnostics.Failures[0].PostFailureTokens.TotalTokens != 50 {
		t.Fatalf("stored cost missing: %#v", report.Diagnostics.Failures[0])
	}
}

func TestCodexIngestionAttributesCostAfterHeimdalReport(t *testing.T) {
	sessionsDir := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "repo")
	reportJSON := `{"status":"failed","finished_at":"2026-07-26T09:00:02Z","primary_failure":{"class":"error","semantic_fingerprint":"same-failure"},"metadata":{"void.diagnostics":{"snapshot":{"status":"pending"}}}}`
	sessionPath := writeCodexSessionFixture(t, sessionsDir, "heimdal-report", []any{
		map[string]any{
			"timestamp": "2026-07-27T09:00:00Z",
			"type":      "session_meta",
			"payload":   map[string]any{"cwd": workspaceRoot},
		},
		map[string]any{
			"timestamp": "2026-07-27T09:00:01Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call", "call_id": "report", "name": "exec",
				"input": `const r = await tools.exec_command({cmd:"heimdal report --run latest --json"}); text(r.output);`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-27T09:00:03Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call_output", "call_id": "report",
				"output": []any{map[string]any{"type": "input_text", "text": reportJSON}},
			},
		},
		map[string]any{
			"timestamp": "2026-07-27T09:00:04Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{"total_token_usage": map[string]any{
					"input_tokens": 100, "cached_input_tokens": 20, "output_tokens": 10,
					"reasoning_output_tokens": 2, "total_tokens": 110,
				}},
			},
		},
		map[string]any{
			"timestamp": "2026-07-27T09:00:05Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call", "call_id": "fix", "name": "exec_command",
				"arguments": `{"cmd":"go test ./..."}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-27T09:01:02Z",
			"type":      "event_msg",
			"payload":   map[string]any{"type": "task_complete"},
		},
		map[string]any{
			"timestamp": "2026-07-27T09:01:03Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{"total_token_usage": map[string]any{
					"input_tokens": 1000, "cached_input_tokens": 200, "output_tokens": 100,
					"reasoning_output_tokens": 20, "total_tokens": 1100,
				}},
			},
		},
		map[string]any{
			"timestamp": "2026-07-27T09:01:04Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call", "call_id": "unrelated", "name": "exec_command",
				"arguments": `{"cmd":"git status --short"}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-27T09:02:02Z",
			"type":      "event_msg",
			"payload":   map[string]any{"type": "task_complete"},
		},
	})
	session, err := parseCodexNormalizedSession(sessionPath)
	if err != nil {
		t.Fatalf("parse session: %v", err)
	}
	record, err := sessionRecordFromNormalized(
		session,
		workspaceRoot,
		time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("normalize session: %v", err)
	}
	if len(record.DiagnosticFailures) != 1 {
		t.Fatalf("diagnostic failures=%#v", record.DiagnosticFailures)
	}
	got := record.DiagnosticFailures[0]
	if got.ToolCalls != 1 || got.Tokens.TotalTokens != 110 ||
		got.EndedAt.Sub(got.FailedAt) != 59*time.Second {
		t.Fatalf("post-failure attribution used stale artifact time or crossed task completion=%#v", got)
	}
}

func TestParseHeimdalDiagnosticFailureClassifiesFixtureStartup(t *testing.T) {
	report := `{
	  "status":"failed",
	  "finished_at":"2026-07-27T09:00:00Z",
	  "primary_failure":{"class":"error","semantic_fingerprint":"stable-one"},
	  "metadata":{"void.diagnostics":{"snapshot":{"status":"pending"}}}
	}`
	got := parseHeimdalDiagnosticFailure(report)
	if got == nil {
		t.Fatal("expected diagnostic failure")
	}
	if got.Classification != "infrastructure" || got.FixturePhase != "fixture-startup" || got.Lever != "tooling" {
		t.Fatalf("unexpected classification: %#v", got)
	}
	if got.DiagnosticStatus != "pending" || got.Fingerprint == "stable-one" || len(got.Fingerprint) != 16 {
		t.Fatalf("diagnostic metadata was not normalized safely: %#v", got)
	}
}

func TestParseHeimdalDiagnosticFailureClassifiesExecutedTest(t *testing.T) {
	report := `{
	  "status":"failed",
	  "finished_at":"2026-07-27T09:00:00Z",
	  "primary_failure":{"class":"timeout","semantic_fingerprint":"stable-two","test":"flow"},
	  "trace_diagnosis":{"failure_source":"trace_error","caught_probe_count":2},
	  "tests":{"executed":1,"skipped":0,"did_not_run":0},
	  "metadata":{"void.diagnostics":{"snapshot":{"status":"captured"}}}
	}`
	got := parseHeimdalDiagnosticFailure(report)
	if got == nil || got.Classification != "product" || got.FixturePhase != "test-execution" {
		t.Fatalf("unexpected classification: %#v", got)
	}
	if got.FailureSource != "trace_error" || got.ProbeCount != 2 || got.DiagnosticStatus != "captured" {
		t.Fatalf("structured evidence missing: %#v", got)
	}
}

func TestParseHeimdalDiagnosticObservationKeepsOnlyPassedTargetDigest(t *testing.T) {
	report := `{
	  "status":"passed",
	  "finished_at":"2026-07-27T09:00:00Z",
	  "invocation":{"test_files":["tests/browser/auth.spec.ts"],"grep":"enters onboarding"}
	}`
	got := parseHeimdalDiagnosticObservation(report)
	if got == nil || got.Status != "passed" || got.Source != "heimdal" || len(got.Target) != 16 {
		t.Fatalf("unexpected pass observation: %#v", got)
	}
	if strings.Contains(got.Target, "auth") || got.Failure != nil {
		t.Fatalf("passed target retained raw identity: %#v", got)
	}
}

func TestAnalyzeDiagnosticFailuresGroupsFingerprintAndProfiles(t *testing.T) {
	failedAt := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	failure := normalizedDiagnosticFailure{
		Source: "heimdal", Classification: "infrastructure", FailureSource: "runner",
		FailureClass: "error", Fingerprint: "digest", FixturePhase: "fixture-startup",
		DiagnosticStatus: "pending", FailedAt: failedAt, Lever: "tooling",
	}
	records := []codexSessionRecord{
		{DiagnosticFailures: []diagnosticFailureEpisode{{
			normalizedDiagnosticFailure: failure, Model: "gpt-5.6", ReasoningEffort: "high",
			AgentKind: "root", EndedAt: failedAt.Add(2 * time.Minute),
			Tokens: normalizedTokenUsage{TotalTokens: 120}, ToolCalls: 4,
		}}},
		{DiagnosticFailures: []diagnosticFailureEpisode{{
			normalizedDiagnosticFailure: failure, Model: "gpt-5.6", ReasoningEffort: "high",
			AgentKind: "root", EndedAt: failedAt.Add(time.Minute),
			Tokens: normalizedTokenUsage{TotalTokens: 80}, ToolCalls: 2,
		}}},
	}
	got := analyzeDiagnosticFailures(records)
	if !got.Available || len(got.Failures) != 1 {
		t.Fatalf("unexpected analysis: %#v", got)
	}
	aggregate := got.Failures[0]
	if aggregate.Occurrences != 2 || aggregate.Sessions != 2 ||
		aggregate.PostFailureTokens.TotalTokens != 200 || aggregate.PostFailureCalls != 6 ||
		aggregate.PostFailureSecs != 180 || len(aggregate.Profiles) != 1 {
		t.Fatalf("unexpected aggregate: %#v", aggregate)
	}
	if strings.Contains(aggregate.Fingerprint, "stable") {
		t.Fatal("raw provider fingerprint leaked")
	}
}

func TestDiagnosticFindingRequiresCrossSessionRecurrence(t *testing.T) {
	report := newSessionInsightsReport(
		"codex",
		nil,
		"/repository",
		time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
	)
	failure := diagnosticFailureAggregate{
		Source: "heimdal", Classification: "infrastructure", FailureSource: "runner",
		Fingerprint: "digest", FixturePhase: "fixture-startup", DiagnosticStatus: "pending",
		Lever: "tooling", Occurrences: 2, Sessions: 1,
	}
	report.Diagnostics = diagnosticFailureAnalysis{Available: true, Failures: []diagnosticFailureAggregate{failure}}
	for _, finding := range buildSessionFindings(report, defaultRepositoryConfig()) {
		if finding.Category == "diagnostic-failure" {
			t.Fatal("same-session report repetition became a recurring finding")
		}
	}
	report.Diagnostics.Failures[0].Sessions = 2
	found := false
	for _, finding := range buildSessionFindings(report, defaultRepositoryConfig()) {
		found = found || finding.Category == "diagnostic-failure"
	}
	if !found {
		t.Fatal("cross-session diagnostic recurrence was not actionable")
	}
}

func TestCompareDiagnosticEffectivenessDistinguishesPassAndAbsence(t *testing.T) {
	failure := func(fingerprint, target string, fresh, calls, seconds int64) diagnosticFailureAggregate {
		return diagnosticFailureAggregate{
			Source: "heimdal", Classification: "infrastructure", Fingerprint: fingerprint,
			Target: target, Lever: "tooling", Occurrences: 1,
			PostFailureTokens: normalizedTokenUsage{UncachedInputTokens: fresh},
			PostFailureCalls:  int(calls), PostFailureSecs: seconds,
		}
	}
	baseline := diagnosticFailureAnalysis{
		Available: true,
		Failures: []diagnosticFailureAggregate{
			failure("resolved", "target-one", 100, 10, 100),
			failure("absent", "target-two", 90, 9, 90),
			failure("better", "target-three", 100, 10, 100),
			failure("worse", "target-four", 100, 10, 100),
		},
	}
	current := diagnosticFailureAnalysis{
		Available: true,
		Failures: []diagnosticFailureAggregate{
			failure("better", "target-three", 80, 8, 80),
			failure("worse", "target-four", 120, 12, 120),
			failure("new", "target-five", 200, 20, 200),
		},
		Passes: []diagnosticPassAggregate{
			{Source: "heimdal", Target: "target-one", Runs: 1, Sessions: 1},
		},
	}
	got := compareDiagnosticEffectiveness(baseline, current)
	states := map[string]string{}
	for _, row := range got.Fingerprints {
		states[row.Fingerprint] = row.State
	}
	want := map[string]string{
		"resolved": "resolved",
		"absent":   "not-observed",
		"better":   "improving",
		"worse":    "regressed",
		"new":      "new",
	}
	for fingerprint, state := range want {
		if states[fingerprint] != state {
			t.Fatalf("state[%s]=%q want %q; all=%#v", fingerprint, states[fingerprint], state, got)
		}
	}
	if got.Fingerprints[0].Fingerprint != "new" {
		t.Fatalf("highest-cost unresolved fingerprint was not first: %#v", got.Fingerprints)
	}
}
