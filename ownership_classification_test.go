package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

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
