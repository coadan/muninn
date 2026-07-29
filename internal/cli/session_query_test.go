package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStoreRestoresRepositoryScopeAtAnalysisBoundary(t *testing.T) {
	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	root := t.TempDir()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	session := normalizedSession{
		Provider:   "codex",
		SourcePath: "scope.jsonl",
		CWD:        root,
		Events: []normalizedSessionEvent{
			{Sequence: 1, OccurredAt: now.Add(-3 * time.Minute), Kind: sessionEventToolCall, ToolName: "exec_command", WorkingDirectories: []string{filepath.Join(filepath.Dir(root), "muninn")}},
			{Sequence: 2, OccurredAt: now.Add(-2 * time.Minute), Kind: sessionEventToken, Tokens: normalizedTokenUsage{TotalTokens: 500}},
			{Sequence: 3, OccurredAt: now.Add(-time.Minute), Kind: sessionEventToolOutput, ToolName: "exec_command", Failed: true, OutputBytes: 1000},
		},
	}
	markRepositoryEventScope(&session, root)
	if err := store.replaceSession(
		context.Background(),
		repositoryStoreScopeKey(root, ownershipCatalog{}),
		session,
		1,
		1,
		true,
	); err != nil {
		t.Fatalf("replace session: %v", err)
	}

	boundaries, err := store.analysisBoundaries(context.Background(), 1, now.Add(-90*time.Second))
	if err != nil {
		t.Fatalf("analysis boundaries: %v", err)
	}
	record, err := sessionRecordFromNormalized(
		normalizedSession{
			Provider: "codex",
			CWD:      root,
			Events: append(boundaries, normalizedSessionEvent{
				Sequence:      3,
				OccurredAt:    now.Add(-time.Minute),
				Kind:          sessionEventToolOutput,
				ToolName:      "exec_command",
				Failed:        true,
				OutputBytes:   1000,
				FailureReason: "test failure",
			}),
		},
		root,
		now.Add(-90*time.Second),
		now,
		ownershipCatalog{},
	)
	if err != nil {
		t.Fatalf("normalize bounded session: %v", err)
	}
	if record.FailedToolCalls != 0 || record.ToolOutputBytes != 0 {
		t.Fatalf("outside boundary leaked failures=%d bytes=%d", record.FailedToolCalls, record.ToolOutputBytes)
	}
}
