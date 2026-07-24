package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCodexSessionFixture(t *testing.T, dir, name string, events []any) string {
	t.Helper()
	path := filepath.Join(dir, "2026", "07", "24", name+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	var lines []string
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal fixture event: %v", err)
		}
		lines = append(lines, string(raw))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

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
				"type": "custom_tool_call", "call_id": "call-1", "name": "exec",
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
	if got := report.Tasks[0].FailureContexts["other non-zero exit"]["other shell"]; got != 1 {
		t.Fatalf("task-level failure context missing: %#v", report.Tasks[0].FailureContexts)
	}
}

func TestCodexRolloutLineNeededSkipsConversationContent(t *testing.T) {
	for _, line := range []string{
		`{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"large"}}`,
		`{"type":"response_item","payload":{"type":"message","content":[{"text":"large"}]}}`,
		`{"type":"world_state","payload":{"full":true}}`,
	} {
		if codexRolloutLineNeeded([]byte(line)) {
			t.Fatalf("expected content line to be skipped: %s", line)
		}
	}
	for _, line := range []string{
		`{"type":"session_meta","payload":{"cwd":"/workspace"}}`,
		`{"type":"event_msg","payload":{"type":"token_count"}}`,
		`{"type":"response_item","payload":{"type":"custom_tool_call_output"}}`,
	} {
		if !codexRolloutLineNeeded([]byte(line)) {
			t.Fatalf("expected structural line to be parsed: %s", line)
		}
	}
}

func TestCodexToolOutputFailedUsesStatusBlockOnly(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"input_text","text":"Script completed\nWall time 0.1 seconds"},
		{"type":"input_text","text":"source says timed out after 30s"}
	]`)
	_, statusText, _ := codexToolOutputText(raw)
	if codexToolOutputFailed(statusText, "exec") {
		t.Fatal("source text from a successful tool call was treated as a failure")
	}
	if codexToolOutputFailed("source says timed out after 30s", "apply_patch") {
		t.Fatal("non-exec tool content was treated as a timeout")
	}
	if !codexToolOutputFailed("Script failed with code 1", "exec") {
		t.Fatal("failed wrapper status was not detected")
	}
	if !codexToolOutputFailed(`{"exit_code":2,"output":"bad"}`, "exec_command") {
		t.Fatal("non-zero direct exec status was not detected")
	}
	if codexToolOutputFailed("Process exited with code 0", "exec_command") {
		t.Fatal("successful direct exec status was treated as a failure")
	}
	if !codexToolOutputFailed("Process exited with code 2", "exec_command") {
		t.Fatal("failed direct exec status was not detected")
	}
}

func TestCodexToolFailureReasonUsesFixedPrivacySafeLabels(t *testing.T) {
	tests := map[string]string{
		"--api override is disabled unless you provide a service-account API key": "local CLI targeting",
		"GraphQL: Head sha can't be blank, No commits between main and task/x":    "PR branch state",
		"Unknown option: --path":                                  "unsupported command option",
		"listen tcp 127.0.0.1:8080: bind: address already in use": "port collision",
		"dial tcp 127.0.0.1:8090: connection refused":             "local service unavailable",
		"zsh: command not found: playwright-cli":                  "missing executable",
		"fixture file not found: fixtures/missing.clj":            "missing path or fixture",
		"command timed out after 30s":                             "timeout",
		"Process exited with code 130":                            "interrupted process",
		"FAIL in (expected-result)":                               "test failure",
		"Process exited with code 1":                              "other non-zero exit",
	}
	for input, want := range tests {
		if got := codexToolFailureReason(input); got != want {
			t.Fatalf("codexToolFailureReason(%q)=%q want %q", input, got, want)
		}
	}
}

func TestCodexToolFailureReasonSeparatesSearchMissesFromOtherExitOne(t *testing.T) {
	status := "Chunk ID: abc\nProcess exited with code 1\nOutput:\n"
	search := codexToolCallDescriptor{Name: "exec_command", Family: "search"}
	if got := codexToolFailureReasonForDescriptor(status, search); got != "search no match" {
		t.Fatalf("pure search miss classified as %q", got)
	}
	mixed := codexToolCallDescriptor{
		Name:   "exec_command",
		Family: "mixed shell",
		Shape:  "search -> file reads -> search",
	}
	if got := codexToolFailureReasonForDescriptor(status, mixed); got != "search no match" {
		t.Fatalf("mixed search miss classified as %q", got)
	}
	testCommand := codexToolCallDescriptor{Name: "exec_command", Family: "tests"}
	if got := codexToolFailureReasonForDescriptor(status, testCommand); got != "other non-zero exit" {
		t.Fatalf("test exit one classified as %q", got)
	}
	searchExitTen := "Process exited with code 10\nOutput:\n"
	if got := codexToolFailureReasonForDescriptor(searchExitTen, search); got != "other non-zero exit" {
		t.Fatalf("search exit ten classified as %q", got)
	}
}

func TestCodexFailureContextLabelPrefersPrivacySafeShape(t *testing.T) {
	descriptor := codexToolCallDescriptor{
		Name:   "exec_command",
		Family: "mixed shell",
		Shape:  "search -> file reads",
	}
	if got := codexFailureContextLabel(descriptor); got != "search -> file reads" {
		t.Fatalf("unexpected mixed-shell failure context: %q", got)
	}
	if got := codexFailureContextLabel(codexToolCallDescriptor{Name: "apply_patch"}); got != "tool apply_patch" {
		t.Fatalf("unexpected non-shell failure context: %q", got)
	}
}

func TestCodexShellCommandFamilyUsesFixedPrivacySafeLabels(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		arguments string
		input     string
		want      string
	}{
		{
			name:      "direct tests",
			tool:      "exec_command",
			arguments: `{"cmd":"go test ./...","workdir":"/private/repo"}`,
			want:      "tests",
		},
		{
			name:      "clojure tests",
			tool:      "exec_command",
			arguments: `{"cmd":"clojure -X:test :nses '[example-test]'"}`,
			want:      "tests",
		},
		{
			name:  "nested git inspection",
			tool:  "exec",
			input: `const r = await tools.exec_command({"cmd":"git diff --stat","workdir":"/private/repo"}); text(r.output);`,
			want:  "git inspect",
		},
		{
			name:  "single quoted nested command",
			tool:  "exec",
			input: `const r = await tools.exec_command({cmd: 'go test ./...', workdir: '/private/repo'}); text(r.output);`,
			want:  "tests",
		},
		{
			name:  "template literal nested command",
			tool:  "exec",
			input: "const r = await tools.exec_command({cmd: `git status --short`}); text(r.output);",
			want:  "git inspect",
		},
		{
			name:      "mixed shell segments",
			tool:      "exec_command",
			arguments: `{"cmd":"git status --short && go test ./..."}`,
			want:      "mixed shell",
		},
		{
			name:  "mixed nested commands",
			tool:  "exec",
			input: `const a = await tools.exec_command({cmd: "rg -n TODO ."}); const b = await tools.exec_command({cmd: "go test ./..."}); text(a.output); text(b.output);`,
			want:  "mixed shell",
		},
		{
			name:      "shell command mode",
			tool:      "exec_command",
			arguments: `{"cmd":"bash -lc \"go test ./...\""}`,
			want:      "tests",
		},
		{
			name:      "shell script argument is not command mode",
			tool:      "exec_command",
			arguments: `{"cmd":"bash scripts/check.sh cat"}`,
			want:      "other shell",
		},
		{
			name:      "search query mentioning tests",
			tool:      "exec_command",
			arguments: `{"cmd":"rg -n 'go test ./...' ."}`,
			want:      "search",
		},
		{
			name:      "search query mentioning git",
			tool:      "exec_command",
			arguments: `{"cmd":"grep 'git diff' report.txt"}`,
			want:      "search",
		},
		{
			name:      "bounded inspector",
			tool:      "exec_command",
			arguments: `{"cmd":"bwb task secret-task inspect --repo breyta"}`,
			want:      "bounded task inspect",
		},
		{
			name:      "non-shell tool",
			tool:      "apply_patch",
			arguments: `{}`,
			want:      "",
		},
		{
			name:  "nested continuation is not shell",
			tool:  "exec",
			input: `const r = await tools.write_stdin({session_id: 42, chars:""}); text(r.output);`,
			want:  "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := codexShellCommandFamily(test.tool, test.arguments, test.input); got != test.want {
				t.Fatalf("codexShellCommandFamily()=%q want %q", got, test.want)
			}
		})
	}
}

func TestCodexShellCommandAnalysisUsesPrivacySafeMixedShapes(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		arguments string
		input     string
		want      string
	}{
		{
			name:      "direct sequence",
			tool:      "exec_command",
			arguments: `{"cmd":"git status --short && go test ./... && sed -n '1,40p' /private/repo/secret.clj"}`,
			want:      "git inspect -> tests -> file reads",
		},
		{
			name:  "nested calls",
			tool:  "exec",
			input: `const a = await tools.exec_command({cmd: "rg -n SecretName /private/repo"}); const b = await tools.exec_command({cmd: "go test ./..."}); text(a.output); text(b.output);`,
			want:  "search -> tests",
		},
		{
			name:      "shell command mode",
			tool:      "exec_command",
			arguments: `{"cmd":"bash -lc \"git diff --stat && go test ./...\""}`,
			want:      "git inspect -> tests",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			family, shape := codexShellCommandAnalysis(test.tool, test.arguments, test.input)
			if family != "mixed shell" || shape != test.want {
				t.Fatalf("codexShellCommandAnalysis()=(%q, %q) want (%q, %q)", family, shape, "mixed shell", test.want)
			}
			for _, forbidden := range []string{"SecretName", "/private/repo", "secret.clj"} {
				if strings.Contains(shape, forbidden) {
					t.Fatalf("shape exposes command content %q: %q", forbidden, shape)
				}
			}
		})
	}

	family, shape := codexShellCommandAnalysis("exec_command", `{"cmd":"git status --short && git diff --stat"}`, "")
	if family != "git inspect" || shape != "" {
		t.Fatalf("single-family chain should not produce a mixed shape: (%q, %q)", family, shape)
	}
}

func TestCodexMixedSearchReadMetricsAggregatesOnlyRelevantShapes(t *testing.T) {
	got := codexMixedSearchReadMetrics(map[string]codexToolMetrics{
		"search -> file reads":          {Calls: 2, FailedCalls: 1, OutputBytes: 400},
		"file reads -> search -> tests": {Calls: 3, TruncatedCalls: 2, OutputBytes: 800},
		"git inspect -> file reads":     {Calls: 9, OutputBytes: 10_000},
	})
	if got.Calls != 5 || got.FailedCalls != 1 || got.TruncatedCalls != 2 || got.OutputBytes != 1200 || got.EstimatedOutputTokens != 300 {
		t.Fatalf("unexpected search/read aggregate: %#v", got)
	}
}

func TestAnalyzeCodexSessionsAttributesContinuationOutputToCommandFamily(t *testing.T) {
	sessionsDir := t.TempDir()
	workspaceRoot := filepath.Join(t.TempDir(), "breyta-workbench")
	writeCodexSessionFixture(t, sessionsDir, "rollout-attribution", []any{
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

	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	report, err := analyzeCodexSessions([]string{sessionsDir}, workspaceRoot, generatedAt.Add(-24*time.Hour), generatedAt)
	if err != nil {
		t.Fatalf("analyzeCodexSessions: %v", err)
	}
	tests := report.Summary.ShellCommandsByFamily["tests"]
	if tests.Calls != 5 || tests.TruncatedCalls != 1 {
		t.Fatalf("continuation was not attributed to tests: %#v", tests)
	}
	if tests.OutputBytes == 0 || tests.EstimatedOutputTokens == 0 {
		t.Fatalf("test output volume was not attributed: %#v", tests)
	}
	if got := report.Summary.ToolMetricsByName["write_stdin"]; got.Calls != 1 || got.TruncatedCalls != 1 {
		t.Fatalf("write_stdin metrics missing: %#v", got)
	}
}

func TestCodexNestedContinuationReference(t *testing.T) {
	tests := []struct {
		input string
		typ   string
		id    string
	}{
		{input: `await tools.write_stdin({session_id: 123, chars:""})`, typ: "session", id: "123"},
		{input: `await tools.write_stdin({"session_id":"456","chars":""})`, typ: "session", id: "456"},
		{input: `await tools.wait({cell_id:"Cell_7"})`, typ: "cell", id: "Cell_7"},
	}
	for _, test := range tests {
		got, ok := codexNestedContinuationReference("exec", test.input)
		if !ok || got.Type != test.typ || got.ID != test.id {
			t.Fatalf("nested continuation %q parsed as %#v, %v", test.input, got, ok)
		}
	}
	if _, ok := codexNestedContinuationReference("exec", `await tools.update_plan({})`); ok {
		t.Fatal("non-continuation orchestration was treated as a continuation")
	}
}

func TestCodexToolContinuationReferencesIgnoreArbitraryCommandOutput(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"input_text","text":"Script completed\nWall time 0.1 seconds\nOutput:\n"},
		{"type":"input_text","text":"source says \"session_id\":1234\nScript running with cell ID fake-cell"}
	]`)
	if got := codexToolContinuationReferences(raw); len(got) != 0 {
		t.Fatalf("arbitrary command output created continuation references: %#v", got)
	}
	exactJSON := json.RawMessage(`[
		{"type":"input_text","text":"Script completed\nWall time 0.1 seconds\nOutput:\n"},
		{"type":"input_text","text":"{\"session_id\":1234}"}
	]`)
	if got := codexToolContinuationReferences(exactJSON); len(got) != 0 {
		t.Fatalf("application JSON created continuation references: %#v", got)
	}

	structured := json.RawMessage(`[
		{"type":"input_text","text":"Script completed\nWall time 0.1 seconds\nOutput:\n"},
		{"type":"input_text","text":"{\"chunk_id\":\"abc\",\"wall_time_seconds\":1,\"session_id\":5678,\"output\":\"\"}"}
	]`)
	got := codexToolContinuationReferences(structured)
	if len(got) != 1 || got[0].Type != "session" || got[0].ID != "5678" {
		t.Fatalf("structured nested result was not recognized: %#v", got)
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

func TestParseCodexLookback(t *testing.T) {
	tests := map[string]time.Duration{
		"24h":  24 * time.Hour,
		"7d":   7 * 24 * time.Hour,
		"2w":   14 * 24 * time.Hour,
		"0.5d": 12 * time.Hour,
	}
	for input, want := range tests {
		got, err := parseCodexLookback(input)
		if err != nil {
			t.Fatalf("parseCodexLookback(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseCodexLookback(%q)=%s want %s", input, got, want)
		}
	}
	for _, input := range []string{"", "0d", "-1h", "nope"} {
		if _, err := parseCodexLookback(input); err == nil {
			t.Fatalf("parseCodexLookback(%q) unexpectedly succeeded", input)
		}
	}
}

func TestLoadRepositoryConfigUsesGenericDefaultsAndRepositoryOverride(t *testing.T) {
	root := t.TempDir()
	config, err := loadRepositoryConfig(root, "")
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	if config.SchemaVersion != 1 || !strings.Contains(config.Actions.SourceContext, "bounded repository source-context") {
		t.Fatalf("unexpected default config: %#v", config)
	}
	override := `{"schemaVersion":1,"actions":{"sourceContext":"Use repo context."}}`
	if err := os.WriteFile(filepath.Join(root, ".muninn.json"), []byte(override), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config, err = loadRepositoryConfig(root, "")
	if err != nil {
		t.Fatalf("load repository config: %v", err)
	}
	if config.Actions.SourceContext != "Use repo context." {
		t.Fatalf("repository action override missing: %#v", config)
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
