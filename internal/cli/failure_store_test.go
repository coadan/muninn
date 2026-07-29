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

	timeline, err := store.ownedOperationFailures(
		context.Background(),
		"codex",
		root,
		ownershipCatalog{},
		now.Add(-24*time.Hour),
		"bwb/test-nses",
		"",
		"",
		1,
		1,
	)
	if err != nil {
		t.Fatalf("query failures: %v", err)
	}
	if timeline.TotalDefinite != 2 || timeline.TotalAmbiguous != 1 ||
		len(timeline.Definite) != 1 || len(timeline.Ambiguous) != 1 {
		t.Fatalf("timeline bounds mismatch: %#v", timeline)
	}
	if timeline.Definite[0].Reason != "test failure" ||
		timeline.Definite[0].OutputBytes != 120 ||
		timeline.Definite[0].AttributionAmbiguous {
		t.Fatalf("unexpected definite event: %#v", timeline.Definite[0])
	}
	if timeline.Ambiguous[0].Reason != "test harness protocol" ||
		timeline.Ambiguous[0].OutputBytes != 480 ||
		!timeline.Ambiguous[0].AttributionAmbiguous {
		t.Fatalf("unexpected ambiguous event: %#v", timeline.Ambiguous[0])
	}
	if timeline.Definite[0].Operation != "bwb/test-nses" ||
		timeline.Definite[0].Family != "tests" ||
		timeline.Definite[0].Task != "(root)" {
		t.Fatalf("missing safe event labels: %#v", timeline.Definite[0])
	}

	timeline, err = store.ownedOperationFailures(
		context.Background(),
		"codex",
		root,
		ownershipCatalog{},
		now.Add(-24*time.Hour),
		"bwb/test-nses",
		"test failure",
		"",
		10,
		5,
	)
	if err != nil {
		t.Fatalf("query reason-filtered failures: %v", err)
	}
	if timeline.TotalDefinite != 2 ||
		timeline.TotalAmbiguous != 0 ||
		len(timeline.Definite) != 2 ||
		timeline.Definite[1].Task != "cost-task" {
		t.Fatalf("reason-filtered timeline=%#v", timeline)
	}

	timeline, err = store.ownedOperationFailures(
		context.Background(),
		"codex",
		root,
		ownershipCatalog{},
		now.Add(-24*time.Hour),
		"bwb/test-nses",
		"test failure",
		"cost-task",
		10,
		5,
	)
	if err != nil {
		t.Fatalf("query task-filtered failures: %v", err)
	}
	if timeline.TotalDefinite != 1 ||
		len(timeline.Definite) != 1 ||
		timeline.Definite[0].Task != "cost-task" ||
		timeline.Definite[0].OutputBytes != 240 {
		t.Fatalf("task-filtered timeline=%#v", timeline)
	}
}
