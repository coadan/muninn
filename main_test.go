package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

func TestResolveAnalyzeOutputSelectionComposesDetailsWithOperations(t *testing.T) {
	selection, err := resolveAnalyzeOutputSelection(true, "", "bwb", 10, false)
	if err != nil {
		t.Fatalf("resolve operations details output: %v", err)
	}
	if selection.OperationLimit != 0 {
		t.Fatalf("operations details limit=%d, want all rows", selection.OperationLimit)
	}

	selection, err = resolveAnalyzeOutputSelection(true, "", "bwb", 4, true)
	if err != nil {
		t.Fatalf("resolve explicitly bounded operations details output: %v", err)
	}
	if selection.OperationLimit != 4 {
		t.Fatalf("explicit operations details limit=%d, want 4", selection.OperationLimit)
	}
}

func TestResolveAnalyzeOutputSelectionComposesDetailsWithFocus(t *testing.T) {
	selection, err := resolveAnalyzeOutputSelection(true, "structure", "", 10, false)
	if err != nil {
		t.Fatalf("resolve focused details output: %v", err)
	}
	if selection.View != "focused" {
		t.Fatalf("focused details view=%q, want focused", selection.View)
	}
}

func TestResolveAnalyzeOutputSelectionRejectsOperationWithFocus(t *testing.T) {
	if _, err := resolveAnalyzeOutputSelection(false, "structure", "bwb", 10, false); err == nil {
		t.Fatal("expected --operation with --focus to fail")
	}
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

func TestParseCodexSessionUsesPrivacySafeRolloutLineage(t *testing.T) {
	sessionsDir := t.TempDir()
	childID := "provider-child-secret"
	parentID := "provider-parent-secret"
	sessionPath := writeCodexSessionFixture(t, sessionsDir, "lineage", []any{
		map[string]any{
			"timestamp": "2026-07-24T08:00:00Z",
			"type":      "session_meta",
			"payload": map[string]any{
				"id":               childID,
				"parent_thread_id": parentID,
				"cwd":              "/workspace",
			},
		},
	})

	session, err := parseCodexNormalizedSession(sessionPath)
	if err != nil {
		t.Fatalf("parse Codex lineage: %v", err)
	}
	if session.AgentKind != "subagent" ||
		session.LineageKey != ownershipSelectorDigest("provider-thread", childID) ||
		session.ParentLineageKey != ownershipSelectorDigest("provider-thread", parentID) {
		t.Fatalf("rollout lineage mismatch: %#v", session)
	}
	encoded, _ := json.Marshal(session)
	if strings.Contains(string(encoded), childID) || strings.Contains(string(encoded), parentID) {
		t.Fatalf("provider identifiers escaped privacy-safe lineage: %s", encoded)
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
		"parse-error: missing test result sentinel":                               "test harness protocol",
		"nREPL eval failed before reporting test results":                         "test harness protocol",
		"GraphQL: Head sha can't be blank, No commits between main and task/x":    "PR branch state",
		"unknown nREPL target \"test\"":                                           "unsupported command target",
		"Unknown option: --path":                                                  "unsupported command option",
		"diagnostic query cannot combine --table with named views.":               "unsupported command combination",
		"Specify --playthrough latest or an exact id because there are two.":      "ambiguous playthrough selection",
		"The completed run has no diagnostic snapshot (no_playthrough).":          "missing diagnostic evidence",
		"listen tcp 127.0.0.1:8080: bind: address already in use":                 "port collision",
		"dial tcp 127.0.0.1:8090: connection refused":                             "local service unavailable",
		"Void development runtime stopped because SpacetimeDB is not running.":    "local service unavailable",
		"HTTP 502: Bad Gateway":                                                   "transient service failure",
		"GitHub couldn't respond to GitHub's servers":                             "transient service failure",
		"zsh: command not found: playwright-cli":                                  "missing executable",
		"fixture file not found: fixtures/missing.clj":                            "missing path or fixture",
		"command timed out after 30s":                                             "timeout",
		"Process exited with code 130":                                            "interrupted process",
		"linting took 13ms, errors: 1, warnings: 0":                               "lint failure",
		"FAIL in (expected-result)":                                               "test failure",
		"Process exited with code 1":                                              "other non-zero exit",
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
			name:      "local codex review",
			tool:      "exec_command",
			arguments: `{"cmd":"codex review --base origin/main"}`,
			want:      "review",
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

func TestCodexSelectorDigestsRecognizeOwnedCommandSubstitution(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{{
			ID:   "status",
			Args: []string{"task", "*", "status"},
		}},
	}})
	arguments := `{"cmd":"eval \"$(bwb task example status --env-only --env-format shell)\"\nbreyta flows list"}`
	digests := codexSelectorDigests(
		"exec_command",
		arguments,
		"",
	)
	matches := catalog.match(digests)
	if len(matches) != 1 || matches[0] != "bwb" {
		t.Fatalf("owned executable in command substitution was not recognized: %#v", matches)
	}
	operations := catalog.classifyOperations(codexCommandInvocations("exec_command", arguments, ""))
	if len(operations) != 1 || operations[0] != "bwb/status" {
		t.Fatalf("owned operation in command substitution was not classified: %#v", operations)
	}
	if got := len(codexCommandInvocations("exec_command", arguments, "")); got != 2 {
		t.Fatalf("eval wrapper should not add a duplicate invocation; got %d invocations", got)
	}
}

func TestCodexShellCommandClassifiesPushAsDelivery(t *testing.T) {
	family, shape := codexShellCommandAnalysis(
		"exec_command",
		`{"cmd":"git -C breyta push origin feature"}`,
		"",
	)
	if family != "delivery" || shape != "" {
		t.Fatalf("git push should establish a delivery boundary: (%q, %q)", family, shape)
	}
}

func TestCodexShellCommandClassifiesRevertAsDownstreamOutcome(t *testing.T) {
	family, shape := codexShellCommandAnalysis(
		"exec_command",
		`{"cmd":"git -C repository revert --no-edit HEAD"}`,
		"",
	)
	if family != "revert" || shape != "" {
		t.Fatalf("git revert should establish a downstream outcome: (%q, %q)", family, shape)
	}
}

func TestOperationsOnlyDoesNotClaimSharedLauncher(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:             "void-cli",
		Executables:    []string{"npm"},
		OperationsOnly: true,
		Operations: []ownedOperationConfig{{
			ID:   "context",
			Args: []string{"run", "--silent", "void", "--", "context"},
		}},
	}})
	voidCommand := `{"cmd":"npm run --silent void -- context actor --path src"}`
	if matches := catalog.match(codexSelectorDigests("exec_command", voidCommand, "")); len(matches) != 0 {
		t.Fatalf("operations-only launcher should not be attributed as a whole tool: %#v", matches)
	}
	operations := catalog.classifyOperations(codexCommandInvocations("exec_command", voidCommand, ""))
	if len(operations) != 1 || operations[0] != "void-cli/context" {
		t.Fatalf("configured launcher operation was not recognized: %#v", operations)
	}
	unrelated := `{"cmd":"npm test"}`
	if operations := catalog.classifyOperations(codexCommandInvocations("exec_command", unrelated, "")); len(operations) != 0 {
		t.Fatalf("unrelated launcher use should remain unattributed: %#v", operations)
	}
}

func TestOperationsOnlyRequiresExecutableOperations(t *testing.T) {
	err := validateOwnedToolConfig([]ownedToolConfig{{
		ID:             "invalid",
		ToolCalls:      []string{"exec"},
		OperationsOnly: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "requires executables and operations") {
		t.Fatalf("expected bounded operations-only validation error, got %v", err)
	}
}

func TestOwnedOperationClassificationPrefersSpecificRule(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{
			{ID: "comments", Args: []string{"task", "*", "comments"}},
			{ID: "comments-wait", Args: []string{"task", "*", "comments", "**", "--wait"}},
		},
	}})
	invocations := []ownedCommandInvocation{{
		Executable: "bwb",
		Args:       []string{"task", "my-task", "comments", "--repo", "breyta", "--pr", "42", "--wait"},
	}}
	if got := catalog.classifyOperations(invocations); !reflect.DeepEqual(got, []string{"bwb/comments-wait"}) {
		t.Fatalf("specific operation was not preferred: %#v", got)
	}
}

func TestOwnedOperationTaskUsesConfiguredBoundedArgument(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:                "bwb",
		Executables:       []string{"bwb"},
		TaskArgumentAfter: "task",
	}})
	invocations := []ownedCommandInvocation{
		{Executable: "bwb", Args: []string{"task", "installer-create-api-connections", "test"}},
		{Executable: "git", Args: []string{"status"}},
	}
	if got := catalog.taskForInvocations(invocations); got != "installer-create-api-connections" {
		t.Fatalf("task=%q want configured task argument", got)
	}
	invocations = append(invocations, ownedCommandInvocation{
		Executable: "bwb",
		Args:       []string{"task", "other-task", "status"},
	})
	if got := catalog.taskForInvocations(invocations); got != "" {
		t.Fatalf("ambiguous task=%q want empty", got)
	}
}

func TestOwnedOperationTaskRejectsUnsafeArgument(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:                "bwb",
		Executables:       []string{"bwb"},
		TaskArgumentAfter: "task",
	}})
	invocations := []ownedCommandInvocation{{
		Executable: "bwb",
		Args:       []string{"task", "/private/task", "test"},
	}}
	if got := catalog.taskForInvocations(invocations); got != "" {
		t.Fatalf("unsafe task=%q want empty", got)
	}
}

func TestConfiguredExpectedOwnedOperationFailureRemainsQueryableWithoutFriction(t *testing.T) {
	generatedAt := time.Date(2026, 7, 25, 3, 0, 0, 0, time.UTC)
	ownership := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{{
			ID:                     "comments-wait",
			Args:                   []string{"task", "*", "comments", "**", "--wait"},
			ExpectedFailureReasons: []string{"timeout"},
		}},
	}})
	session := normalizedSession{
		Provider: "codex",
		CWD:      t.TempDir(),
		Events: []normalizedSessionEvent{{
			OccurredAt:      generatedAt,
			CallOccurredAt:  generatedAt.Add(-time.Minute),
			Kind:            sessionEventToolOutput,
			ToolName:        "exec_command",
			Failed:          true,
			FailureReason:   "timeout",
			OwnedOperations: []string{"bwb/comments-wait"},
		}},
	}
	record, err := sessionRecordFromNormalized(session, session.CWD, generatedAt.Add(-time.Hour), generatedAt, ownership)
	if err != nil {
		t.Fatalf("normalize configured expected failure: %v", err)
	}
	if got := record.OwnedOperationFailureReasons["bwb/comments-wait"]["timeout"]; got != 1 {
		t.Fatalf("configured expected failure was not retained: %#v", record.OwnedOperationFailureReasons)
	}
	if got := record.Activity[sessionActivityKey("owned-operation-friction", "bwb/comments-wait")]; !got.IsZero() {
		t.Fatalf("configured expected failure refreshed friction activity: %s", got)
	}
	reasons := map[string]codexOccurrenceMetrics{
		"timeout":                   {Count: 3, Sessions: 1},
		"transient service failure": {Count: 1, Sessions: 1},
	}
	actionable, expected := ownedOperationFailureCounts(ownership, "bwb/comments-wait", reasons)
	if actionable != 1 || expected != 3 {
		t.Fatalf("failure split=(%d,%d) want (1,3)", actionable, expected)
	}
}

func TestExpectedWaitTreatsInterruptedProcessAsExpectedFailure(t *testing.T) {
	ownership := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{
			{
				ID:           "publish",
				Args:         []string{"publish", "--wait"},
				ExpectedWait: true,
			},
			{
				ID:   "status",
				Args: []string{"status"},
			},
		},
	}})
	if !ownership.operationFailureExpected("bwb/publish", "interrupted process") {
		t.Fatal("expected wait interruption was actionable")
	}
	if ownership.operationFailureExpected("bwb/status", "interrupted process") {
		t.Fatal("non-wait interruption was expected")
	}
}

func TestOwnedOperationClassificationRetainsSpecificTies(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{
			{ID: "test", Args: []string{"task", "*", "test"}},
			{ID: "test-nses", Args: []string{"task", "*", "test", "**", ":nses"}},
			{ID: "test-changed", Args: []string{"task", "*", "test", "**", ":changed-since"}},
		},
	}})
	invocations := []ownedCommandInvocation{{
		Executable: "bwb",
		Args:       []string{"task", "my-task", "test", ":nses", "[example-test]", ":changed-since", "origin/main"},
	}}
	want := []string{"bwb/test-changed", "bwb/test-nses"}
	if got := catalog.classifyOperations(invocations); !reflect.DeepEqual(got, want) {
		t.Fatalf("equally specific operations were not retained: got=%#v want=%#v", got, want)
	}
}

func TestOperationPatternDoubleWildcardMatchesZeroOrManySegments(t *testing.T) {
	pattern := []string{"task", "*", "comments", "**", "--wait"}
	for _, args := range [][]string{
		{"task", "one", "comments", "--wait"},
		{"task", "one", "comments", "--repo", "breyta", "--pr", "42", "--wait"},
	} {
		if !operationPatternMatches(pattern, args) {
			t.Fatalf("double wildcard did not match %#v", args)
		}
	}
	if operationPatternMatches(pattern, []string{"task", "one", "comments", "--repo", "breyta"}) {
		t.Fatal("double wildcard matched without required trailing flag")
	}
}

func TestFormatRefreshCompletionIsBoundedAndActionable(t *testing.T) {
	got := formatRefreshCompletion(sessionRefreshStats{
		FilesScanned:    600,
		FilesIndexed:    24,
		FilesReused:     576,
		FilesUnreadable: 1,
	})
	want := "Refresh complete: 600 scanned, 24 indexed, 576 reused, 1 unreadable."
	if got != want {
		t.Fatalf("unexpected refresh completion: %q", got)
	}
}

func TestCodexTokenUsageIncrementHandlesCounterReset(t *testing.T) {
	previous := codexTokenUsage{
		InputTokens:         1_000,
		CachedInputTokens:   800,
		UncachedInputTokens: 200,
		OutputTokens:        100,
		TotalTokens:         1_100,
	}
	reset := codexTokenUsage{
		InputTokens:         80,
		CachedInputTokens:   50,
		UncachedInputTokens: 30,
		OutputTokens:        10,
		TotalTokens:         90,
	}
	if got := codexTokenUsageIncrement(reset, previous, true); got != reset {
		t.Fatalf("counter reset should start a new token epoch: %#v", got)
	}
}

func TestCodexCommandInvocationsExposeBundledOperationAttribution(t *testing.T) {
	single := `{"cmd":"eval \"$(bwb task example status --env-only --env-format shell)\""}`
	if got := len(codexCommandInvocations("exec_command", single, "")); got != 1 {
		t.Fatalf("single wrapped operation produced %d invocations", got)
	}
	bundled := `{"cmd":"sed -n '1,20p' AGENTS.md; bwb task example status"}`
	if got := len(codexCommandInvocations("exec_command", bundled, "")); got != 2 {
		t.Fatalf("bundled command produced %d invocations", got)
	}
}

func TestOwnedOperationFailuresAreDefiniteOnlyForOneMatchedOperation(t *testing.T) {
	workspaceRoot := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	session := normalizedSession{
		Provider: "codex",
		CWD:      workspaceRoot,
		Events: []normalizedSessionEvent{
			{
				OccurredAt:                    generatedAt.Add(-time.Minute),
				Kind:                          sessionEventToolCall,
				ToolName:                      "exec_command",
				OwnedOperations:               []string{"bwb/git", "bwb/test"},
				OperationAttributionAmbiguous: true,
			},
			{
				OccurredAt:                    generatedAt,
				CallOccurredAt:                generatedAt.Add(-time.Minute),
				Kind:                          sessionEventToolOutput,
				ToolName:                      "exec_command",
				Failed:                        true,
				FailureReason:                 "test failure",
				OutputBytes:                   100,
				OwnedOperations:               []string{"bwb/git", "bwb/test"},
				OperationAttributionAmbiguous: true,
			},
		},
	}
	record, err := sessionRecordFromNormalized(session, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt, ownershipCatalog{})
	if err != nil {
		t.Fatalf("normalize session: %v", err)
	}
	for _, operation := range []string{"bwb/git", "bwb/test"} {
		if got := record.OwnedOperations[operation].FailedCalls; got != 0 {
			t.Fatalf("%s received ambiguous failure as definite: %d", operation, got)
		}
		if got := record.OwnedOperationAmbiguous[operation]; got.FailedCalls != 1 || got.OutputBytes != 100 {
			t.Fatalf("%s ambiguous metrics=%#v want one failure and 100 bytes", operation, got)
		}
	}
}

func TestOwnedOperationFailureReasonsSeparateExpectedFailures(t *testing.T) {
	workspaceRoot := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	session := normalizedSession{
		Provider: "codex",
		CWD:      workspaceRoot,
		Events: []normalizedSessionEvent{
			{
				OccurredAt:      generatedAt.Add(-time.Minute),
				Kind:            sessionEventToolCall,
				ToolName:        "exec_command",
				OwnedOperations: []string{"bwb/test"},
			},
			{
				OccurredAt:      generatedAt,
				CallOccurredAt:  generatedAt.Add(-time.Minute),
				Kind:            sessionEventToolOutput,
				ToolName:        "exec_command",
				Failed:          true,
				FailureReason:   "test failure",
				OwnedOperations: []string{"bwb/test"},
			},
		},
	}
	record, err := sessionRecordFromNormalized(session, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt, ownershipCatalog{})
	if err != nil {
		t.Fatalf("normalize session: %v", err)
	}
	if got := record.OwnedOperations["bwb/test"].FailedCalls; got != 1 {
		t.Fatalf("definite failures=%d want 1", got)
	}
	if got := record.OwnedOperationFailureReasons["bwb/test"]["test failure"]; got != 1 {
		t.Fatalf("test failure reasons=%d want 1", got)
	}
	if got := record.Activity[sessionActivityKey("owned-operation-friction", "bwb/test")]; !got.IsZero() {
		t.Fatalf("expected product failure refreshed friction activity: %s", got)
	}
	report := newSessionInsightsReport("codex", nil, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt)
	addCodexSessionToReport(&report, map[string]*codexTaskInsights{}, record)
	actionable, expected := ownedOperationFailureCounts(ownershipCatalog{}, "bwb/test", report.Summary.OwnedOperationFailureReasons["bwb/test"])
	if actionable != 0 || expected != 1 {
		t.Fatalf("failure split=(%d,%d) want (0,1)", actionable, expected)
	}
}

func TestOwnedOperationFrictionActivityIgnoresLaterSuccessfulCalls(t *testing.T) {
	workspaceRoot := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	frictionAt := generatedAt.Add(-10 * time.Minute)
	session := normalizedSession{
		Provider: "codex",
		CWD:      workspaceRoot,
		Events: []normalizedSessionEvent{
			{
				OccurredAt:      frictionAt.Add(-time.Second),
				Kind:            sessionEventToolCall,
				ToolName:        "exec_command",
				OwnedOperations: []string{"bwb/test"},
			},
			{
				OccurredAt:      frictionAt,
				CallOccurredAt:  frictionAt.Add(-time.Second),
				Kind:            sessionEventToolOutput,
				ToolName:        "exec_command",
				Failed:          true,
				FailureReason:   "test harness protocol",
				OwnedOperations: []string{"bwb/test"},
			},
			{
				OccurredAt:      generatedAt.Add(-time.Second),
				Kind:            sessionEventToolCall,
				ToolName:        "exec_command",
				OwnedOperations: []string{"bwb/test"},
			},
			{
				OccurredAt:      generatedAt,
				CallOccurredAt:  generatedAt.Add(-time.Second),
				Kind:            sessionEventToolOutput,
				ToolName:        "exec_command",
				OwnedOperations: []string{"bwb/test"},
			},
		},
	}
	record, err := sessionRecordFromNormalized(session, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt, ownershipCatalog{})
	if err != nil {
		t.Fatalf("normalize session: %v", err)
	}
	if got := record.Activity[sessionActivityKey("owned-operation", "bwb/test")]; !got.Equal(generatedAt) {
		t.Fatalf("operation activity=%s want %s", got, generatedAt)
	}
	if got := record.Activity[sessionActivityKey("owned-operation-friction", "bwb/test")]; !got.Equal(frictionAt) {
		t.Fatalf("friction activity=%s want %s", got, frictionAt)
	}
}

func TestProgressWaitsSeparateCandidateStallsFromExpectedWork(t *testing.T) {
	workspaceRoot := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	eventPair := func(start time.Time, family string, operations []string, outputBytes int64) []normalizedSessionEvent {
		return []normalizedSessionEvent{
			{
				OccurredAt:      start,
				Kind:            sessionEventToolCall,
				ToolName:        "exec_command",
				Family:          family,
				OwnedOperations: operations,
			},
			{
				OccurredAt:      start.Add(30 * time.Second),
				CallOccurredAt:  start,
				Kind:            sessionEventToolOutput,
				ToolName:        "exec_command",
				Family:          family,
				OutputBytes:     outputBytes,
				OwnedOperations: operations,
			},
		}
	}
	events := eventPair(generatedAt.Add(-4*time.Minute), "other shell", []string{"bwb/api-start"}, 0)
	events = append(events, eventPair(generatedAt.Add(-3*time.Minute), "other shell", []string{"bwb/api-start"}, 10)...)
	events = append(events, eventPair(generatedAt.Add(-2*time.Minute), "other shell", []string{"bwb/comments"}, 0)...)
	events = append(events, eventPair(generatedAt.Add(-90*time.Second), "other shell", []string{"bwb/comments-resolve"}, 0)...)
	events = append(events, eventPair(generatedAt.Add(-time.Minute), "tests", nil, 0)...)
	events = append(events, eventPair(generatedAt.Add(-45*time.Second), "other shell", []string{"bwb/test-nses"}, 0)...)
	events = append(events, eventPair(generatedAt.Add(-30*time.Second), "other shell", []string{"bwb/publish"}, 0)...)
	for index := 0; index < 5; index++ {
		start := generatedAt.Add(time.Duration(-25+index*5) * time.Second)
		events = append(events, normalizedSessionEvent{
			OccurredAt:         start.Add(5 * time.Second),
			CallOccurredAt:     start,
			Kind:               sessionEventToolOutput,
			ToolName:           "write_stdin",
			Family:             "tests",
			OutputBytes:        100,
			OwnedOperations:    []string{"bwb/test-nses"},
			OperationContinues: true,
		})
	}
	session := normalizedSession{
		Provider: "codex",
		CWD:      workspaceRoot,
		Events:   events,
	}
	ownership := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{{
			ID:           "publish",
			Args:         []string{"publish"},
			ExpectedWait: true,
		}},
	}})
	record, err := sessionRecordFromNormalized(session, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt, ownership)
	if err != nil {
		t.Fatalf("normalize session: %v", err)
	}
	if got := record.ProgressStalls["bwb/api-start"]; got.Calls != 2 || got.Seconds != 60 {
		t.Fatalf("candidate progress stalls=%#v want two calls and 60 seconds", got)
	}
	if got := record.ExpectedWaits["bwb/comments"]; got.Calls != 1 || got.Seconds != 30 {
		t.Fatalf("review wait=%#v want one call and 30 seconds", got)
	}
	if got := record.ExpectedWaits["bwb/comments-resolve"]; got.Calls != 1 || got.Seconds != 30 {
		t.Fatalf("review resolve wait=%#v want one call and 30 seconds", got)
	}
	if got := record.ExpectedWaits["tests"]; got.Calls != 1 || got.Seconds != 30 {
		t.Fatalf("test wait=%#v want one call and 30 seconds", got)
	}
	if got := record.ExpectedWaits["bwb/test-nses"]; got.Calls != 1 || got.Seconds != 30 {
		t.Fatalf("owned test wait=%#v want one call and 30 seconds", got)
	}
	if got := record.ExpectedWaits["bwb/publish"]; got.Calls != 1 || got.Seconds != 30 {
		t.Fatalf("configured publish wait=%#v want one call and 30 seconds", got)
	}
	if got := record.RapidPolls["bwb/test-nses"]; got.Calls != 5 || got.Seconds != 25 {
		t.Fatalf("rapid test polling=%#v want five calls and 25 seconds", got)
	}
	report := newSessionInsightsReport("codex", nil, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt)
	addCodexSessionToReport(&report, map[string]*codexTaskInsights{}, record)
	if got := report.Summary.ProgressStalls["bwb/api-start"]; got.Sessions != 1 {
		t.Fatalf("candidate progress stall sessions=%#v want one", got)
	}
	if got := report.Summary.ExpectedWaits["tests"]; got.Sessions != 1 {
		t.Fatalf("expected test wait sessions=%#v want one", got)
	}
	if got := report.Summary.RapidPolls["bwb/test-nses"]; got.Sessions != 1 {
		t.Fatalf("rapid poll sessions=%#v want one", got)
	}
}

func TestOversizedOutputsUsePrivacySafeOwnedOrFamilyContext(t *testing.T) {
	workspaceRoot := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	session := normalizedSession{
		Provider: "codex",
		CWD:      workspaceRoot,
		Events: []normalizedSessionEvent{
			{
				OccurredAt:     generatedAt.Add(-3 * time.Minute),
				Kind:           sessionEventToolOutput,
				ToolName:       "exec_command",
				Family:         "search",
				OutputBytes:    oversizedOutputMinimumBytes - 1,
				CallOccurredAt: generatedAt.Add(-4 * time.Minute),
			},
			{
				OccurredAt:     generatedAt.Add(-2 * time.Minute),
				Kind:           sessionEventToolOutput,
				ToolName:       "exec_command",
				Family:         "search",
				OutputBytes:    oversizedOutputMinimumBytes,
				CallOccurredAt: generatedAt.Add(-3 * time.Minute),
			},
			{
				OccurredAt:      generatedAt.Add(-time.Minute),
				Kind:            sessionEventToolOutput,
				ToolName:        "exec_command",
				Family:          "mixed shell",
				Shape:           "search -> file reads",
				OutputBytes:     45_000,
				CallOccurredAt:  generatedAt.Add(-2 * time.Minute),
				OwnedOperations: []string{"bwb/inspect", "bwb/inspect-namespace"},
			},
			{
				OccurredAt:                    generatedAt.Add(-30 * time.Second),
				Kind:                          sessionEventToolOutput,
				ToolName:                      "exec_command",
				Family:                        "mixed shell",
				Shape:                         "file reads -> tests",
				OutputBytes:                   60_000,
				CallOccurredAt:                generatedAt.Add(-90 * time.Second),
				OwnedOperations:               []string{"bwb/status-env", "bwb/cli"},
				OperationAttributionAmbiguous: true,
			},
			{
				OccurredAt:      generatedAt.Add(-15 * time.Second),
				Kind:            sessionEventToolOutput,
				ToolName:        "exec",
				Family:          "mixed shell",
				Shape:           "tool exec",
				OutputBytes:     70_000,
				CallOccurredAt:  generatedAt.Add(-45 * time.Second),
				OwnedOperations: []string{"bwb/status-env"},
				ConcurrentBatch: true,
			},
		},
	}
	record, err := sessionRecordFromNormalized(session, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt, ownershipCatalog{})
	if err != nil {
		t.Fatalf("normalize session: %v", err)
	}
	if got := record.OversizedOutputs["search"]; got.Calls != 1 ||
		got.OutputBytes != oversizedOutputMinimumBytes ||
		got.MaxOutputBytes != oversizedOutputMinimumBytes {
		t.Fatalf("search oversized output=%#v", got)
	}
	if got := record.OversizedOutputs["bwb/inspect-namespace"]; got.Calls != 1 ||
		got.OutputBytes != 45_000 ||
		got.MaxOutputBytes != 45_000 {
		t.Fatalf("owned oversized output=%#v", got)
	}
	if got := record.OversizedOutputs["file reads -> tests"]; got.Calls != 1 || got.OutputBytes != 60_000 {
		t.Fatalf("ambiguous oversized output should use bounded shape context: %#v", got)
	}
	if _, exists := record.OversizedOutputs["bwb/status-env"]; exists {
		t.Fatalf("ambiguous bundled output must not be charged to an owned operation: %#v", record.OversizedOutputs)
	}
	if got := record.OversizedOutputs["concurrent tool batch"]; got.Calls != 1 || got.OutputBytes != 70_000 {
		t.Fatalf("concurrent batch output should use its shared stage context: %#v", got)
	}
	if len(record.OversizedOutputs) != 4 {
		t.Fatalf("below-threshold output should be absent: %#v", record.OversizedOutputs)
	}
	report := newSessionInsightsReport("codex", nil, workspaceRoot, generatedAt.Add(-time.Hour), generatedAt)
	addCodexSessionToReport(&report, map[string]*codexTaskInsights{}, record)
	if got := report.Summary.OversizedOutputs["search"]; got.Sessions != 1 {
		t.Fatalf("oversized output sessions=%#v want one", got)
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

	explicitMarker := json.RawMessage(`[
		{"type":"input_text","text":"Script completed\nOutput:\n"},
		{"type":"input_text","text":"SESSION_ID=40401"}
	]`)
	if got := codexToolContinuationReferences(explicitMarker); len(got) != 0 {
		t.Fatalf("unguarded explicit marker was recognized: %#v", got)
	}
	got = codexExplicitSessionMarkerReferences(explicitMarker)
	if len(got) != 1 || got[0].Type != "session" || got[0].ID != "40401" {
		t.Fatalf("guarded explicit marker was not recognized: %#v", got)
	}
	if codexEmitsExplicitSessionMarker("exec", `text("SESSION_ID=40401")`) {
		t.Fatal("application output without a session-returning call enabled marker parsing")
	}
	if !codexEmitsExplicitSessionMarker(
		"exec",
		"const r = await tools.exec_command({cmd:\"go test\"}); if(r.session_id) text(`SESSION_ID=${r.session_id}`);",
	) {
		t.Fatal("session-returning wrapper marker was not recognized")
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
	override := `{
		"schemaVersion": 1,
		"actions": {
			"sourceContext": "Use repo context.",
			"yieldedOperation": "Use the managed test command."
		},
		"suppressSignals": [
			"session-loop/progress-stall/bwb/api-start",
			"session-loop/progress-stall/bwb/api-start"
		],
		"ownedTools": [{
			"id": "bwb",
			"repository": "breyta-workbench",
			"executables": ["bwb"],
			"taskArgumentAfter": "task",
			"operations": [{
				"id": "comments-wait",
				"args": ["comments", "--wait"],
				"expectedWait": true,
				"expectedFailureReasons": ["timeout"]
			}],
			"recommendation": "Improve the local CLI first."
		}]
	}`
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
	if config.Actions.YieldedOperation != "Use the managed test command." {
		t.Fatalf("yielded-operation action override missing: %#v", config)
	}
	if len(config.OwnedTools) != 1 || config.OwnedTools[0].ID != "bwb" {
		t.Fatalf("owned tooling config missing: %#v", config)
	}
	if config.OwnedTools[0].TaskArgumentAfter != "task" {
		t.Fatalf("owned task argument marker missing: %#v", config.OwnedTools[0])
	}
	if got := config.OwnedTools[0].Operations[0].ExpectedFailureReasons; !reflect.DeepEqual(got, []string{"timeout"}) {
		t.Fatalf("expected failure reasons missing: %#v", got)
	}
	if !config.OwnedTools[0].Operations[0].ExpectedWait {
		t.Fatalf("expected wait marker missing: %#v", config.OwnedTools[0].Operations[0])
	}
	if len(config.SuppressSignals) != 1 || config.SuppressSignals[0] != "session-loop/progress-stall/bwb/api-start" {
		t.Fatalf("signal suppressions were not normalized: %#v", config.SuppressSignals)
	}
}

func TestLoadRepositoryConfigRejectsInvalidTaskArgumentMarker(t *testing.T) {
	root := t.TempDir()
	override := `{
		"schemaVersion": 1,
		"ownedTools": [{
			"id": "bwb",
			"executables": ["bwb"],
			"taskArgumentAfter": "two tokens"
		}]
	}`
	if err := os.WriteFile(filepath.Join(root, ".muninn.json"), []byte(override), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadRepositoryConfig(root, ""); err == nil || !strings.Contains(err.Error(), "taskArgumentAfter") {
		t.Fatalf("invalid task argument marker error=%v", err)
	}
}

func TestLoadRepositoryConfigRejectsInvalidExpectedFailureReasons(t *testing.T) {
	for _, reasons := range []string{`[""]`, `["timeout", " TIMEOUT "]`} {
		root := t.TempDir()
		override := `{
			"schemaVersion": 1,
			"ownedTools": [{
				"id": "bwb",
				"executables": ["bwb"],
				"operations": [{
					"id": "comments-wait",
					"args": ["comments", "--wait"],
					"expectedFailureReasons": ` + reasons + `
				}]
			}]
		}`
		if err := os.WriteFile(filepath.Join(root, ".muninn.json"), []byte(override), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		if _, err := loadRepositoryConfig(root, ""); err == nil || !strings.Contains(err.Error(), "expectedFailureReasons") {
			t.Fatalf("invalid expected failure reasons %s error=%v", reasons, err)
		}
	}
}

func TestLoadRepositoryConfigRejectsEmptySuppressedSignal(t *testing.T) {
	root := t.TempDir()
	override := `{"schemaVersion":1,"suppressSignals":["  "]}`
	if err := os.WriteFile(filepath.Join(root, ".muninn.json"), []byte(override), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := loadRepositoryConfig(root, ""); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty suppression error=%v want actionable validation", err)
	}
}

func TestNormalizeSuppressedSignalsRejectsNonSignalText(t *testing.T) {
	if _, err := normalizeSuppressedSignals([]string{"Progress stall: /private/path"}); err == nil ||
		!strings.Contains(err.Error(), "exact printed signal ID") {
		t.Fatalf("invalid signal error=%v want exact-ID guidance", err)
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
