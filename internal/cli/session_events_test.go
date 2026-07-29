package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

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
		"verification changed files after staging; inspect changes and rerun":     "verification changed staged state",
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
				OccurredAt:          generatedAt.Add(-15 * time.Second),
				Kind:                sessionEventToolOutput,
				ToolName:            "exec",
				Family:              "mixed shell",
				Shape:               "tool exec",
				OutputBytes:         70_000,
				CallOccurredAt:      generatedAt.Add(-45 * time.Second),
				OwnedOperations:     []string{"bwb/status-env"},
				ConcurrentBatch:     true,
				ConcurrentBatchSize: 3,
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
	if got := record.OversizedOutputs["concurrent tool batch"]; got.Calls != 1 ||
		got.OutputBytes != 70_000 || got.NestedCalls != 3 || got.MaxNestedCalls != 3 {
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

func TestConcurrentBatchUsesSharedStageBudget(t *testing.T) {
	record := newCodexSessionRecord()
	recordOversizedOutput(record, normalizedSessionEvent{
		ConcurrentBatch:     true,
		ConcurrentBatchSize: 2,
		OutputBytes:         concurrentBatchOutputMinimumBytes - 1,
	}, nil)
	if len(record.OversizedOutputs) != 0 {
		t.Fatalf("below-budget concurrent batch was retained: %#v", record.OversizedOutputs)
	}
	recordOversizedOutput(record, normalizedSessionEvent{
		ConcurrentBatch:     true,
		ConcurrentBatchSize: 2,
		OutputBytes:         concurrentBatchOutputMinimumBytes,
	}, nil)
	if got := record.OversizedOutputs["concurrent tool batch"]; got.Calls != 1 || got.NestedCalls != 2 {
		t.Fatalf("at-budget concurrent batch missing: %#v", got)
	}
}

func TestCodexMixedSearchReadMetricsAggregatesOnlyRelevantShapes(t *testing.T) {
	got := codexMixedSearchReadMetrics(map[string]codexToolMetrics{
		"search -> file reads":          {Calls: 2, Sessions: 2, FailedCalls: 1, OutputBytes: 400},
		"file reads -> search -> tests": {Calls: 3, Sessions: 4, TruncatedCalls: 2, OutputBytes: 800},
		"git inspect -> file reads":     {Calls: 9, OutputBytes: 10_000},
	})
	if got.Calls != 5 || got.Sessions != 4 || got.FailedCalls != 1 ||
		got.TruncatedCalls != 2 || got.OutputBytes != 1200 ||
		got.EstimatedOutputTokens != 300 {
		t.Fatalf("unexpected search/read aggregate: %#v", got)
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
