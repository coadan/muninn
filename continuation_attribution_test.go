package main

import (
	"testing"
	"time"
)

func TestContinuationCallDoesNotDuplicateCommandOrOwnedOperationCalls(t *testing.T) {
	root := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	startedAt := generatedAt.Add(-time.Minute)
	session := normalizedSession{
		Provider: "codex",
		CWD:      root,
		Events: []normalizedSessionEvent{
			{
				OccurredAt:      startedAt,
				Kind:            sessionEventToolCall,
				ToolName:        "exec_command",
				Family:          "other shell",
				FirstFamily:     "other shell",
				LastFamily:      "other shell",
				OwnedOperations: []string{"bwb/git"},
			},
			{
				OccurredAt:      startedAt.Add(time.Second),
				CallOccurredAt:  startedAt,
				Kind:            sessionEventToolOutput,
				ToolName:        "exec_command",
				Family:          "other shell",
				OutputBytes:     10,
				OwnedOperations: []string{"bwb/git"},
			},
			{
				OccurredAt:      startedAt.Add(2 * time.Second),
				Kind:            sessionEventToolCall,
				ToolName:        "write_stdin",
				Family:          "other shell",
				OwnedOperations: []string{"bwb/git"},
			},
			{
				OccurredAt:      startedAt.Add(3 * time.Second),
				CallOccurredAt:  startedAt.Add(2 * time.Second),
				Kind:            sessionEventToolOutput,
				ToolName:        "write_stdin",
				Family:          "other shell",
				OutputBytes:     20,
				OwnedOperations: []string{"bwb/git"},
			},
		},
	}

	record, err := sessionRecordFromNormalized(
		session,
		root,
		generatedAt.Add(-time.Hour),
		generatedAt,
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("sessionRecordFromNormalized: %v", err)
	}
	if record.ToolCalls != 2 {
		t.Fatalf("physical tool calls=%d want 2", record.ToolCalls)
	}
	if got := record.ShellCommandsByFamily["other shell"]; got.Calls != 1 || got.OutputBytes != 30 {
		t.Fatalf("shell command metrics=%#v want one call with all continuation output", got)
	}
	if got := record.OwnedOperations["bwb/git"]; got.Calls != 1 || got.OutputBytes != 30 {
		t.Fatalf("owned operation metrics=%#v want one call with all continuation output", got)
	}
	if got := record.ToolMetricsByName["write_stdin"]; got.Calls != 1 || got.OutputBytes != 20 {
		t.Fatalf("physical continuation metrics=%#v", got)
	}
}

func TestSessionRecordFindsAbandonedYieldedOperation(t *testing.T) {
	root := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	startedAt := generatedAt.Add(-time.Hour)
	session := normalizedSession{
		Provider: "codex",
		CWD:      root,
		Events: []normalizedSessionEvent{
			{
				OccurredAt:         startedAt,
				CallOccurredAt:     startedAt.Add(-time.Second),
				Kind:               sessionEventToolOutput,
				ToolName:           "exec_command",
				Family:             "other shell",
				OwnedOperations:    []string{"bwb/api-start"},
				OperationContinues: true,
			},
		},
	}

	record, err := sessionRecordFromNormalized(
		session,
		root,
		generatedAt.Add(-2*time.Hour),
		generatedAt,
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("sessionRecordFromNormalized: %v", err)
	}
	if got := record.AbandonedContinuations["bwb/api-start"]; got != 1 {
		t.Fatalf("abandoned continuations=%#v want one bwb/api-start operation", record.AbandonedContinuations)
	}
}

func TestSessionRecordClearsTerminalContinuationAndIgnoresRecentLiveWork(t *testing.T) {
	root := t.TempDir()
	generatedAt := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	startedAt := generatedAt.Add(-time.Minute)
	events := []normalizedSessionEvent{
		{
			OccurredAt:         startedAt,
			CallOccurredAt:     startedAt.Add(-time.Second),
			Kind:               sessionEventToolOutput,
			ToolName:           "exec_command",
			Family:             "tests",
			OperationContinues: true,
		},
	}
	session := normalizedSession{Provider: "codex", CWD: root, Events: events}

	live, err := sessionRecordFromNormalized(
		session,
		root,
		generatedAt.Add(-time.Hour),
		generatedAt,
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("live sessionRecordFromNormalized: %v", err)
	}
	if len(live.AbandonedContinuations) != 0 {
		t.Fatalf("recent live operation was classified as abandoned: %#v", live.AbandonedContinuations)
	}

	session.Events = append(session.Events, normalizedSessionEvent{
		OccurredAt:     startedAt.Add(10 * time.Second),
		CallOccurredAt: startedAt.Add(9 * time.Second),
		Kind:           sessionEventToolOutput,
		ToolName:       "write_stdin",
		Family:         "tests",
	})
	session.Events = append(session.Events, normalizedSessionEvent{
		OccurredAt: startedAt.Add(11 * time.Second),
		Kind:       sessionEventComplete,
	})
	terminal, err := sessionRecordFromNormalized(
		session,
		root,
		generatedAt.Add(-time.Hour),
		generatedAt,
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("terminal sessionRecordFromNormalized: %v", err)
	}
	if len(terminal.AbandonedContinuations) != 0 {
		t.Fatalf("terminal continuation remained pending: %#v", terminal.AbandonedContinuations)
	}
}
