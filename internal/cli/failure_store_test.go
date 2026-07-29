package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionStoreOwnedOperationFailuresAreBoundedAndRepositoryScoped(t *testing.T) {
	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	root := filepath.Join(t.TempDir(), "repository")
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	sessions := []normalizedSession{
		{
			Provider:   "codex",
			SourcePath: "inside.jsonl",
			CWD:        filepath.Join(root, "worktree"),
			Events: []normalizedSessionEvent{
				{
					Sequence:        1,
					OccurredAt:      now.Add(-3 * time.Hour),
					Kind:            sessionEventToolOutput,
					Family:          "tests",
					Failed:          true,
					OutputBytes:     120,
					FailureReason:   "test failure",
					OwnedOperations: []string{"bwb/test-nses"},
				},
				{
					Sequence:                      2,
					OccurredAt:                    now.Add(-2 * time.Hour),
					Kind:                          sessionEventToolOutput,
					Family:                        "mixed shell",
					Failed:                        true,
					OutputBytes:                   480,
					FailureReason:                 "test harness protocol",
					OwnedOperations:               []string{"bwb/test-nses"},
					OperationAttributionAmbiguous: true,
				},
				{
					Sequence:        3,
					OccurredAt:      now.Add(-time.Hour),
					Kind:            sessionEventToolOutput,
					Family:          "tests",
					Failed:          false,
					OutputBytes:     80,
					OwnedOperations: []string{"bwb/test-nses"},
				},
			},
		},
		{
			Provider:   "codex",
			SourcePath: "outside.jsonl",
			CWD:        root + "-other",
			Events: []normalizedSessionEvent{{
				Sequence:        1,
				OccurredAt:      now.Add(-30 * time.Minute),
				Kind:            sessionEventToolOutput,
				Family:          "tests",
				Failed:          true,
				OutputBytes:     999,
				FailureReason:   "test failure",
				OwnedOperations: []string{"bwb/test-nses"},
			}},
		},
		{
			Provider:   "codex",
			SourcePath: "task.jsonl",
			CWD:        filepath.Join(root, "worktree"),
			Events: []normalizedSessionEvent{{
				Sequence:        1,
				OccurredAt:      now.Add(-4 * time.Hour),
				Kind:            sessionEventToolOutput,
				Family:          "tests",
				Failed:          true,
				OutputBytes:     240,
				FailureReason:   "test failure",
				OwnedOperations: []string{"bwb/test-nses"},
				OperationTask:   "cost-task",
			}},
		},
	}
	for index, session := range sessions {
		markRepositoryEventScope(&session, root)
		if err := store.replaceSession(
			context.Background(),
			repositoryStoreScopeKey(root, ownershipCatalog{}),
			session,
			int64(index+1),
			int64(index+1),
			true,
		); err != nil {
			t.Fatalf("replace session %d: %v", index, err)
		}
	}

	events, err := store.ownedOperationFailures(
		context.Background(),
		"codex",
		root,
		ownershipCatalog{},
		now.Add(-24*time.Hour),
		"bwb/test-nses",
		"",
		"",
		1,
	)
	if err != nil {
		t.Fatalf("query failures: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d want 1", len(events))
	}
	if events[0].Reason != "test harness protocol" || events[0].OutputBytes != 480 || !events[0].AttributionAmbiguous {
		t.Fatalf("unexpected event: %#v", events[0])
	}
	if events[0].Operation != "bwb/test-nses" || events[0].Family != "mixed shell" {
		t.Fatalf("missing safe event labels: %#v", events[0])
	}
	if events[0].Task != "(root)" {
		t.Fatalf("task=%q want root attribution", events[0].Task)
	}

	events, err = store.ownedOperationFailures(
		context.Background(),
		"codex",
		root,
		ownershipCatalog{},
		now.Add(-24*time.Hour),
		"bwb/test-nses",
		"test failure",
		"",
		10,
	)
	if err != nil {
		t.Fatalf("query reason-filtered failures: %v", err)
	}
	if len(events) != 2 || events[0].Reason != "test failure" || events[1].Task != "cost-task" {
		t.Fatalf("reason-filtered events=%#v", events)
	}

	events, err = store.ownedOperationFailures(
		context.Background(),
		"codex",
		root,
		ownershipCatalog{},
		now.Add(-24*time.Hour),
		"bwb/test-nses",
		"test failure",
		"cost-task",
		10,
	)
	if err != nil {
		t.Fatalf("query task-filtered failures: %v", err)
	}
	if len(events) != 1 || events[0].Task != "cost-task" || events[0].OutputBytes != 240 {
		t.Fatalf("task-filtered events=%#v", events)
	}
}
