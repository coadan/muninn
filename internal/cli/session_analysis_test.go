package cli

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAnalyzeCodexSessionsAggregatesFinalCumulativeUsageAndFriction(t *testing.T) {
	sessionsDir := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "breyta-workbench")
	taskCWD := filepath.Join(workspaceRoot, ".worktrees", "cost-task", "breyta")
	writeCodexSessionFixture(t, sessionsDir, "rollout-task", []any{
		map[string]any{
			"timestamp": "2026-07-24T08:00:00Z",
			"type":      "session_meta",
			"payload":   map[string]any{"cwd": taskCWD},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:01Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{"total_token_usage": map[string]any{
					"input_tokens": 100, "cached_input_tokens": 20, "output_tokens": 10,
					"reasoning_output_tokens": 3, "total_tokens": 110,
				}},
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:02Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "custom_tool_call",
				"call_id": "call-1",
				"name":    "exec",
				"input":   `const r = await tools.exec_command({cmd:"printf test"}); text(r.output);`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:03Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type": "custom_tool_call_output", "call_id": "call-1",
				"output": []any{
					map[string]any{"type": "input_text", "text": "Script failed with code 1"},
					map[string]any{"type": "input_text", "text": "Warning: truncated output (original token count: 10000)"},
				},
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:04Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{"total_token_usage": map[string]any{
					"input_tokens": 250, "cached_input_tokens": 150, "output_tokens": 25,
					"reasoning_output_tokens": 8, "total_tokens": 275,
				}},
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:05Z",
			"type":      "event_msg",
			"payload":   map[string]any{"type": "task_complete"},
		},
	})

	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	report, err := analyzeCodexSessions([]string{sessionsDir}, workspaceRoot, generatedAt.Add(-24*time.Hour), generatedAt)
	if err != nil {
		t.Fatalf("analyzeCodexSessions: %v", err)
	}
	if report.Summary.Sessions != 1 || report.Summary.CompletedSessions != 1 {
		t.Fatalf("unexpected session counts: %#v", report.Summary)
	}
	if report.Summary.Tokens.InputTokens != 250 || report.Summary.Tokens.CachedInputTokens != 150 {
		t.Fatalf("expected only final cumulative token count, got %#v", report.Summary.Tokens)
	}
	if report.Summary.Tokens.UncachedInputTokens != 100 || report.Summary.FreshTokens != 125 {
		t.Fatalf("unexpected fresh token calculation: %#v", report.Summary)
	}
	if report.Summary.ToolCalls != 1 || report.Summary.FailedToolCalls != 1 || report.Summary.TruncatedToolCalls != 1 {
		t.Fatalf("unexpected tool signals: %#v", report.Summary)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].Task != "cost-task" {
		t.Fatalf("unexpected task grouping: %#v", report.Tasks)
	}
	if got := report.Tasks[0].ShellCommandsByFamily["other shell"]; got.Calls != 1 || got.FailedCalls != 1 {
		t.Fatalf("task-level shell attribution missing: %#v", report.Tasks[0].ShellCommandsByFamily)
	}
	if report.Tasks[0].FailureReasons["other non-zero exit"] != 1 {
		t.Fatalf("task-level failure reason missing: %#v", report.Tasks[0].FailureReasons)
	}
	if got := report.Tasks[0].FailureContexts["other non-zero exit"]["other shell"]; got.Count != 1 || got.Sessions != 1 {
		t.Fatalf("task-level failure context missing: %#v", report.Tasks[0].FailureContexts)
	}
}

func TestNormalizedTokenUsageIncrementHandlesCounterReset(t *testing.T) {
	previous := normalizedTokenUsage{
		InputTokens:         1_000,
		CachedInputTokens:   800,
		UncachedInputTokens: 200,
		OutputTokens:        100,
		TotalTokens:         1_100,
	}
	reset := normalizedTokenUsage{
		InputTokens:         80,
		CachedInputTokens:   50,
		UncachedInputTokens: 30,
		OutputTokens:        10,
		TotalTokens:         90,
	}
	if got := normalizedTokenUsageIncrement(reset, previous, true); got != reset {
		t.Fatalf("counter reset should start a new token epoch: %#v", got)
	}
}

func TestAddCodexTargetMetricsCountsSessionsWithRediscovery(t *testing.T) {
	target := map[string]codexTargetMetrics{}
	addCodexTargetMetrics(target, map[string]codexTargetMetrics{
		"deps.edn": {Reads: 1},
	}, nil)
	addCodexTargetMetrics(target, map[string]codexTargetMetrics{
		"deps.edn": {Reads: 2},
	}, map[string]int{"deps.edn": 1})
	addCodexTargetMetrics(target, map[string]codexTargetMetrics{
		"deps.edn": {Reads: 1, SearchReadLoops: 1},
	}, nil)
	got := target["deps.edn"]
	if got.Sessions != 3 || got.Reads != 4 || got.SearchReadLoops != 1 ||
		got.RediscoverySessions != 2 || got.EditedSessions != 1 ||
		got.UneditedRediscoverySessions != 1 {
		t.Fatalf("target metrics=%#v want edited and unedited rediscovery separated", got)
	}
}

func TestAnalyzeCodexSessionsAttributesContinuationOutputToCommandFamily(t *testing.T) {
	sessionsDir := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "breyta-workbench")
	sessionPath := writeCodexSessionFixture(t, sessionsDir, "rollout-attribution", []any{
		map[string]any{
			"timestamp": "2026-07-24T08:00:00Z",
			"type":      "session_meta",
			"payload":   map[string]any{"cwd": workspaceRoot},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:01Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "exec-1",
				"name":      "exec_command",
				"arguments": `{"cmd":"go test ./..."}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:02Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "exec-1",
				"output":  "Script running with session ID 42",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:03Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "wait-1",
				"name":      "write_stdin",
				"arguments": `{"session_id":42,"chars":""}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:04Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "wait-1",
				"output":  "Warning: truncated output\nok",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:05Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "custom_tool_call",
				"call_id": "exec-2",
				"name":    "exec",
				"input":   `const r = await tools.exec_command({cmd:"go test -race ./..."}); text(r.output);`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:06Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "custom_tool_call_output",
				"call_id": "exec-2",
				"output": []any{
					map[string]any{"type": "input_text", "text": "Script completed\nWall time 1s\nOutput:\n"},
					map[string]any{"type": "input_text", "text": `{"output":"","cell_id":"Race_7","status":"Script running with cell ID Race_7"}`},
				},
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:07Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "wait-2",
				"name":      "wait",
				"arguments": `{"cell_id":"Race_7"}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:08Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "wait-2",
				"output":  "ok race",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:09Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "custom_tool_call",
				"call_id": "nested-wait-3",
				"name":    "exec",
				"input":   `const r = await tools.write_stdin({"session_id":42,"chars":""}); text(r.output);`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:10Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "custom_tool_call_output",
				"call_id": "nested-wait-3",
				"output":  "nested continuation output",
			},
		},
	})

	normalized, err := parseCodexNormalizedSession(sessionPath)
	if err != nil {
		t.Fatalf("parse normalized continuation fixture: %v", err)
	}
	var outputs []normalizedSessionEvent
	for _, event := range normalized.Events {
		if event.Kind == sessionEventToolOutput {
			outputs = append(outputs, event)
		}
	}
	if len(outputs) != 5 || !outputs[0].OperationContinues || outputs[1].OperationContinues ||
		!outputs[2].OperationContinues || outputs[3].OperationContinues ||
		outputs[4].OperationContinues {
		t.Fatalf("terminal continuation classification mismatch: %#v", outputs)
	}

	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	report, err := analyzeCodexSessions([]string{sessionsDir}, workspaceRoot, generatedAt.Add(-24*time.Hour), generatedAt)
	if err != nil {
		t.Fatalf("analyzeCodexSessions: %v", err)
	}
	tests := report.Summary.ShellCommandsByFamily["tests"]
	if tests.Calls != 2 || tests.TruncatedCalls != 1 {
		t.Fatalf("continuation was not attributed to tests: %#v", tests)
	}
	if tests.OutputBytes == 0 || tests.EstimatedOutputTokens == 0 {
		t.Fatalf("test output volume was not attributed: %#v", tests)
	}
	if got := report.Summary.ToolMetricsByName["write_stdin"]; got.Calls != 1 || got.TruncatedCalls != 1 {
		t.Fatalf("write_stdin metrics missing: %#v", got)
	}
}

func TestAnalyzeCodexSessionsAttributesExplicitWrapperSessionContinuation(t *testing.T) {
	sessionsDir := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "breyta-workbench")
	writeCodexSessionFixture(t, sessionsDir, "rollout-wrapper-attribution", []any{
		map[string]any{
			"timestamp": "2026-07-24T08:00:00Z",
			"type":      "session_meta",
			"payload":   map[string]any{"cwd": workspaceRoot},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:01Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "custom_tool_call",
				"call_id": "exec-1",
				"name":    "exec",
				"input":   "const r = await tools.exec_command({cmd:\"codex review --base main\"}); text(r.output); if(r.session_id) text(`SESSION_ID=${r.session_id}`);",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:02Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "custom_tool_call_output",
				"call_id": "exec-1",
				"output": []any{
					map[string]any{"type": "input_text", "text": "Script completed\nOutput:\n"},
					map[string]any{"type": "input_text", "text": "review started"},
					map[string]any{"type": "input_text", "text": "SESSION_ID=40401"},
				},
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:03Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "wait-1",
				"name":      "write_stdin",
				"arguments": `{"session_id":40401,"chars":""}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:04Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "wait-1",
				"output":  strings.Repeat("review output", 3000),
			},
		},
	})

	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	report, err := analyzeCodexSessions([]string{sessionsDir}, workspaceRoot, generatedAt.Add(-24*time.Hour), generatedAt)
	if err != nil {
		t.Fatalf("analyzeCodexSessions: %v", err)
	}
	review := report.Summary.ShellCommandsByFamily["review"]
	if review.Calls != 1 || review.OutputBytes < oversizedOutputMinimumBytes {
		t.Fatalf("wrapper continuation was not attributed to review: %#v", review)
	}
	if got := report.Summary.OversizedOutputs["review"]; got.Calls != 1 {
		t.Fatalf("oversized continuation was not attributed to review: %#v", report.Summary.OversizedOutputs)
	}
}

func TestAnalyzeCodexSessionsFiltersWorkspaceAndLookback(t *testing.T) {
	sessionsDir := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "breyta-workbench")
	for _, fixture := range []struct {
		name      string
		timestamp string
		cwd       string
	}{
		{name: "matching", timestamp: "2026-07-24T08:00:00Z", cwd: workspaceRoot},
		{name: "old", timestamp: "2026-07-20T08:00:00Z", cwd: workspaceRoot},
		{name: "other", timestamp: "2026-07-24T08:00:00Z", cwd: filepath.Join(t.TempDir(), "other")},
	} {
		writeCodexSessionFixture(t, sessionsDir, fixture.name, []any{
			map[string]any{
				"timestamp": fixture.timestamp,
				"type":      "session_meta",
				"payload":   map[string]any{"cwd": fixture.cwd},
			},
			map[string]any{
				"timestamp": fixture.timestamp,
				"type":      "response_item",
				"payload": map[string]any{
					"type":      "function_call",
					"call_id":   fixture.name,
					"name":      "exec_command",
					"arguments": `{"cmd":"git status --short"}`,
				},
			},
		})
	}
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	report, err := analyzeCodexSessions([]string{sessionsDir}, workspaceRoot, generatedAt.Add(-24*time.Hour), generatedAt)
	if err != nil {
		t.Fatalf("analyzeCodexSessions: %v", err)
	}
	if report.Summary.FilesScanned != 3 || report.Summary.Sessions != 1 {
		t.Fatalf("unexpected filtered report: %#v", report.Summary)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].Task != "(root)" {
		t.Fatalf("unexpected root task grouping: %#v", report.Tasks)
	}
}

func TestAnalyzeCodexSessionsIncludesRecentCallsFromOlderSessions(t *testing.T) {
	sessionsDir := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "repository")
	writeCodexSessionFixture(t, sessionsDir, "older-session-recent-call", []any{
		map[string]any{
			"timestamp": "2026-07-20T08:00:00Z",
			"type":      "session_meta",
			"payload":   map[string]any{"cwd": workspaceRoot},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:00Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "recent",
				"name":      "exec_command",
				"arguments": `{"cmd":"go test ./..."}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:01Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "recent",
				"output":  "ok",
			},
		},
	})
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	report, err := analyzeCodexSessions(
		[]string{sessionsDir},
		workspaceRoot,
		generatedAt.Add(-24*time.Hour),
		generatedAt,
	)
	if err != nil {
		t.Fatalf("analyzeCodexSessions: %v", err)
	}
	if report.Summary.Sessions != 1 || report.Summary.ToolCalls != 1 {
		t.Fatalf("recent activity from older session was excluded: %#v", report.Summary)
	}
}

func TestAnalyzeCodexSessionsTracksOnlyCrossCallFamilyTransitions(t *testing.T) {
	sessionsDir := t.TempDir()
	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	writeCodexSessionFixture(t, sessionsDir, "transitions", []any{
		map[string]any{
			"timestamp": "2026-07-24T08:00:00Z",
			"type":      "session_meta",
			"payload":   map[string]any{"cwd": repositoryRoot},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:01Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "search",
				"name":      "exec_command",
				"arguments": `{"cmd":"rg -n target src"}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:02Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "read",
				"name":      "exec_command",
				"arguments": `{"cmd":"sed -n '1,80p' src/example.go"}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:03Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "custom_tool_call",
				"call_id": "edit",
				"name":    "apply_patch",
				"input":   "*** Begin Patch\n*** End Patch\n",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:04Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "search-again",
				"name":      "exec_command",
				"arguments": `{"cmd":"rg -n target src && sed -n '1,20p' src/example.go"}`,
			},
		},
	})
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	report, err := analyzeCodexSessions(
		[]string{sessionsDir},
		repositoryRoot,
		generatedAt.Add(-24*time.Hour),
		generatedAt,
	)
	if err != nil {
		t.Fatalf("analyzeCodexSessions: %v", err)
	}
	got := report.Summary.CrossCallTransitions["search -> file reads"]
	if got.Count != 1 || got.Sessions != 1 {
		t.Fatalf("cross-call search/read transition missing: %#v", report.Summary.CrossCallTransitions)
	}
	if len(report.Summary.CrossCallTransitions) != 1 {
		t.Fatalf("bundled commands or interrupted rounds became transitions: %#v", report.Summary.CrossCallTransitions)
	}
}

func TestAnalyzeCodexSessionsAttributesConfiguredOwnedToolsAndCompactions(t *testing.T) {
	sessionsDir := t.TempDir()
	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	writeCodexSessionFixture(t, sessionsDir, "owned-tooling", []any{
		map[string]any{
			"timestamp": "2026-07-24T08:00:00Z",
			"type":      "session_meta",
			"payload":   map[string]any{"cwd": repositoryRoot},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:01Z",
			"type":      "event_msg",
			"payload":   map[string]any{"type": "context_compacted"},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:02Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "bwb",
				"name":      "exec_command",
				"arguments": `{"cmd":"bwb task example status"}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:03Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "bwb",
				"output":  "exit code: 1",
			},
		},
	})
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	config := []ownedToolConfig{{
		ID:          "bwb",
		Repository:  "breyta-workbench",
		Executables: []string{"bwb"},
	}}
	report, err := analyzeCodexSessionsFilteredWithOwnership(
		[]string{sessionsDir},
		repositoryRoot,
		generatedAt.Add(-24*time.Hour),
		generatedAt,
		"",
		newOwnershipCatalog(config),
	)
	if err != nil {
		t.Fatalf("analyze with ownership: %v", err)
	}
	if report.Summary.Compactions != 1 {
		t.Fatalf("compaction count missing: %#v", report.Summary)
	}
	owned := report.Summary.OwnedTooling["bwb"]
	if owned.Calls != 1 || owned.FailedCalls != 1 {
		t.Fatalf("owned tool attribution missing: %#v", report.Summary.OwnedTooling)
	}
	if got := report.Tasks[0].OwnedTooling["bwb"]; got.Calls != 1 || got.FailedCalls != 1 {
		t.Fatalf("task-owned tool attribution missing: %#v", report.Tasks[0].OwnedTooling)
	}
	if got := report.Summary.OwnedToolUnmatched["bwb"]; got.Calls != 1 || got.FailedCalls != 1 {
		t.Fatalf("unmatched owned-tool attribution missing: %#v", report.Summary.OwnedToolUnmatched)
	}
	if got := report.Tasks[0].OwnedToolUnmatched["bwb"]; got.Calls != 1 || got.FailedCalls != 1 {
		t.Fatalf("task unmatched owned-tool attribution missing: %#v", report.Tasks[0].OwnedToolUnmatched)
	}
}

func TestAnalyzeCodexSessionsRanksFailureContextByCrossSessionRecurrence(t *testing.T) {
	sessionsDir := t.TempDir()
	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	for index := 0; index < 2; index++ {
		callID := "test-" + strconv.Itoa(index)
		writeCodexSessionFixture(t, sessionsDir, callID, []any{
			map[string]any{
				"timestamp": "2026-07-24T08:00:00Z",
				"type":      "session_meta",
				"payload":   map[string]any{"cwd": repositoryRoot},
			},
			map[string]any{
				"timestamp": "2026-07-24T08:00:01Z",
				"type":      "response_item",
				"payload": map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      "exec_command",
					"arguments": `{"cmd":"go test ./..."}`,
				},
			},
			map[string]any{
				"timestamp": "2026-07-24T08:00:02Z",
				"type":      "response_item",
				"payload": map[string]any{
					"type":    "function_call_output",
					"call_id": callID,
					"output":  "tests failed\nexit code: 1",
				},
			},
		})
	}
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	report, err := analyzeCodexSessions(
		[]string{sessionsDir},
		repositoryRoot,
		generatedAt.Add(-24*time.Hour),
		generatedAt,
	)
	if err != nil {
		t.Fatalf("analyze recurring failures: %v", err)
	}
	metrics := report.Summary.FailureContexts["test failure"]["tests"]
	if metrics.Count != 2 || metrics.Sessions != 2 {
		t.Fatalf("cross-session recurrence missing: %#v", report.Summary.FailureContexts)
	}
}

func TestAddCodexOwnedToolMetricsRetainsBundledSubset(t *testing.T) {
	metrics := map[string]codexToolMetrics{}
	addCodexOwnedToolMetrics(metrics, "repo", false, 1, false, false, 400)
	addCodexOwnedToolMetrics(metrics, "repo", true, 1, true, true, 800)

	got := metrics["repo"]
	if got.Calls != 2 || got.AmbiguousCalls != 1 ||
		got.FailedCalls != 1 || got.AmbiguousFailedCalls != 1 ||
		got.TruncatedCalls != 1 || got.AmbiguousTruncatedCalls != 1 ||
		got.OutputBytes != 1_200 || got.AmbiguousOutputBytes != 800 {
		t.Fatalf("owned tool metrics did not retain bundled subset: %#v", got)
	}
}

func TestAnalyzeCodexSessionsFiltersExactTaskAndRebuildsSummary(t *testing.T) {
	sessionsDir := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "breyta-workbench")
	for index, task := range []string{"selected-task", "other-task"} {
		writeCodexSessionFixture(t, sessionsDir, task, []any{
			map[string]any{
				"timestamp": "2026-07-24T08:00:00Z",
				"type":      "session_meta",
				"payload":   map[string]any{"cwd": filepath.Join(workspaceRoot, ".worktrees", task, "breyta")},
			},
			map[string]any{
				"timestamp": "2026-07-24T08:00:01Z",
				"type":      "response_item",
				"payload": map[string]any{
					"type":      "function_call",
					"call_id":   task,
					"name":      "exec_command",
					"arguments": `{"cmd":"git status --short && go test ./..."}`,
				},
			},
			map[string]any{
				"timestamp": "2026-07-24T08:00:02Z",
				"type":      "response_item",
				"payload": map[string]any{
					"type":    "function_call_output",
					"call_id": task,
					"output":  strings.Repeat("x", index+1),
				},
			},
		})
	}
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	report, err := analyzeCodexSessionsFiltered(
		[]string{sessionsDir},
		workspaceRoot,
		generatedAt.Add(-24*time.Hour),
		generatedAt,
		"selected-task",
	)
	if err != nil {
		t.Fatalf("analyzeCodexSessionsFiltered: %v", err)
	}
	if report.Summary.FilesScanned != 2 || report.Summary.Sessions != 1 {
		t.Fatalf("task filter did not preserve scan counts and rebuild session summary: %#v", report.Summary)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].Task != "selected-task" {
		t.Fatalf("unexpected filtered tasks: %#v", report.Tasks)
	}
	shape := report.Summary.MixedShellShapes["git inspect -> tests"]
	if shape.Calls != 1 || shape.OutputBytes != 1 {
		t.Fatalf("mixed shape was not aggregated in the filtered report: %#v", report.Summary.MixedShellShapes)
	}
	if got := report.Tasks[0].MixedShellShapes["git inspect -> tests"]; got != shape {
		t.Fatalf("task mixed shape differs from filtered summary: task=%#v summary=%#v", got, shape)
	}
}

func TestCodexSessionReportJSONOmitsSessionContent(t *testing.T) {
	report := codexSessionInsightsReport{
		SchemaVersion: 1,
		WorkspaceRoot: "/workspace",
		Summary: codexSessionInsightsSummary{
			ToolCallsByName: map[string]int{"exec": 2},
		},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	text := string(raw)
	for _, forbidden := range []string{"prompt", "message", "arguments", "output", "sessionId"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(`"`+forbidden+`"`)) {
			t.Fatalf("report unexpectedly exposes %q field: %s", forbidden, text)
		}
	}
}
