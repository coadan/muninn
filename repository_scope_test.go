package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCodexToolWorkingDirectories(t *testing.T) {
	t.Run("direct exec", func(t *testing.T) {
		got := codexToolWorkingDirectories("exec_command", `{"cmd":"go test ./...","workdir":"/workspace/muninn"}`, "")
		if len(got) != 1 || got[0] != "/workspace/muninn" {
			t.Fatalf("working directories=%v", got)
		}
	})

	t.Run("nested exec", func(t *testing.T) {
		input := `await tools.exec_command({cmd:"one", workdir:"/workspace/muninn"}); await tools.exec_command({cmd:"two", workdir:'/workspace/heimdal'})`
		got := codexToolWorkingDirectories("exec", "", input)
		if len(got) != 2 || got[0] != "/workspace/muninn" || got[1] != "/workspace/heimdal" {
			t.Fatalf("working directories=%v", got)
		}
	})
}

func TestEventRepositoryScope(t *testing.T) {
	root := filepath.Join(t.TempDir(), "void")
	cases := []struct {
		name     string
		event    normalizedSessionEvent
		inside   bool
		explicit bool
	}{
		{
			name:     "outside workdir",
			event:    normalizedSessionEvent{WorkingDirectories: []string{filepath.Join(filepath.Dir(root), "muninn")}},
			explicit: true,
		},
		{
			name:     "one nested call inside",
			event:    normalizedSessionEvent{WorkingDirectories: []string{"/elsewhere", root}},
			inside:   true,
			explicit: true,
		},
		{
			name:     "relative workdir",
			event:    normalizedSessionEvent{WorkingDirectories: []string{"subdir"}},
			inside:   true,
			explicit: true,
		},
		{
			name:     "exec defaults to session cwd",
			event:    normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "exec_command"},
			inside:   true,
			explicit: true,
		},
		{
			name:  "unscoped connector preserves state",
			event: normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "web"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			inside, explicit := eventRepositoryScope(test.event, root, root)
			if inside != test.inside || explicit != test.explicit {
				t.Fatalf("scope=(%t,%t) want (%t,%t)", inside, explicit, test.inside, test.explicit)
			}
		})
	}
}

func TestSessionRecordExcludesWorkInAnotherRepository(t *testing.T) {
	root := t.TempDir()
	startedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	outside := filepath.Join(filepath.Dir(root), "muninn")
	session := normalizedSession{
		Provider: "codex",
		CWD:      root,
		Events: []normalizedSessionEvent{
			{Sequence: 1, OccurredAt: startedAt, Kind: sessionEventToolCall, ToolName: "exec_command"},
			{Sequence: 2, OccurredAt: startedAt.Add(time.Second), CallOccurredAt: startedAt, Kind: sessionEventToolOutput, ToolName: "exec_command", OutputBytes: 10},
			{Sequence: 3, OccurredAt: startedAt.Add(2 * time.Second), Kind: sessionEventToken, Tokens: normalizedTokenUsage{TotalTokens: 100}},
			{Sequence: 4, OccurredAt: startedAt.Add(3 * time.Second), Kind: sessionEventToolCall, ToolName: "exec_command", WorkingDirectories: []string{outside}},
			{Sequence: 5, OccurredAt: startedAt.Add(4 * time.Second), CallOccurredAt: startedAt.Add(3 * time.Second), Kind: sessionEventToolOutput, ToolName: "exec_command", WorkingDirectories: []string{outside}, Failed: true, OutputBytes: 1000},
			{Sequence: 6, OccurredAt: startedAt.Add(5 * time.Second), Kind: sessionEventToken, Tokens: normalizedTokenUsage{TotalTokens: 500}},
			{Sequence: 7, OccurredAt: startedAt.Add(6 * time.Second), Kind: sessionEventComplete},
			{Sequence: 8, OccurredAt: startedAt.Add(7 * time.Second), Kind: sessionEventToolCall, ToolName: "exec_command"},
			{Sequence: 9, OccurredAt: startedAt.Add(8 * time.Second), CallOccurredAt: startedAt.Add(7 * time.Second), Kind: sessionEventToolOutput, ToolName: "exec_command", OutputBytes: 20},
			{Sequence: 10, OccurredAt: startedAt.Add(9 * time.Second), Kind: sessionEventToken, Tokens: normalizedTokenUsage{TotalTokens: 600}},
			{Sequence: 11, OccurredAt: startedAt.Add(10 * time.Second), Kind: sessionEventComplete},
		},
	}

	record, err := sessionRecordFromNormalized(
		session,
		root,
		startedAt.Add(-time.Minute),
		startedAt.Add(time.Hour),
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("sessionRecordFromNormalized: %v", err)
	}
	if record.ToolCalls != 2 || record.FailedToolCalls != 0 || record.ToolOutputBytes != 30 {
		t.Fatalf("tool metrics calls=%d failures=%d bytes=%d", record.ToolCalls, record.FailedToolCalls, record.ToolOutputBytes)
	}
	if record.Tokens.TotalTokens != 200 {
		t.Fatalf("total tokens=%d want 200", record.Tokens.TotalTokens)
	}
	if !record.Completed {
		t.Fatal("final in-repository completion was not retained")
	}
}
