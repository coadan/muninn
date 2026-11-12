package cli

import (
	"context"
	"encoding/json"
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

// A resumed Claude transcript can replay history dated before the window
// after in-window activity. Replayed usage still advances Claude's cumulative
// counters without being charged, only the prefix before the first in-window
// event decides censoring, and both analysis paths see the same stream.
func TestReplayedPreWindowHistoryKeepsAnalysisPathsAligned(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	bash := func(id string) map[string]any {
		return claudeFixtureToolUse(id, "Bash", map[string]any{"command": "true"})
	}
	done := map[string]any{"type": "text", "text": "done"}
	cases := []replayedHistoryCase{
		{
			name: "running task is not re-censored",
			records: []any{
				claudeFixtureAssistant(1, "m1", "tool_use", usage, bash("a")),
				claudeFixtureToolResult(2, "a", false, ""),
				claudeFixtureAssistant(3, "m2", "end_turn", usage, done),
				claudeFixtureAssistant(20, "m3", "tool_use", usage, bash("b")),
				claudeFixtureToolResult(21, "b", false, ""),
				// Replayed history: dated before the window, in the repository.
				claudeFixtureAssistant(2, "m4", "tool_use", usage, bash("c")),
				claudeFixtureToolResult(2, "c", false, ""),
				claudeFixtureAssistant(22, "m5", "end_turn", usage, done),
			},
			// m3 and m5 are charged; m4's replayed usage is not.
			wantTokens: 4,
		},
		{
			name: "replayed context sample is seen by both paths",
			records: []any{
				claudeFixtureRecord("system", 20, map[string]any{"subtype": "compact_boundary"}),
				claudeFixtureAssistant(2, "m1", "tool_use", usage, bash("a")),
				claudeFixtureToolResult(2, "a", false, ""),
				claudeFixtureAssistant(22, "m2", "end_turn", usage, done),
			},
			wantTokens: 2,
		},
		{
			name: "events past the cutoff do not start the window",
			records: []any{
				claudeFixtureAssistant(1, "m1", "tool_use", usage, bash("a")),
				claudeFixtureRecord("system", 7300, map[string]any{"subtype": "compact_boundary"}),
				claudeFixtureAssistant(3, "m2", "end_turn", usage, done),
				claudeFixtureAssistant(20, "m3", "tool_use", usage, bash("b")),
				claudeFixtureToolResult(21, "b", false, ""),
				claudeFixtureAssistant(22, "m4", "end_turn", usage, done),
			},
			wantTokens: 4,
		},
		{
			name: "a result whose call predates the window is not activity",
			records: []any{
				claudeFixtureAssistant(1, "m1", "tool_use", usage, bash("a")),
				claudeFixtureToolResult(15, "a", false, ""),
				claudeFixtureAssistant(3, "m2", "end_turn", usage, done),
				claudeFixtureAssistant(20, "m3", "tool_use", usage, bash("b")),
				claudeFixtureToolResult(21, "b", false, ""),
				claudeFixtureAssistant(22, "m4", "end_turn", usage, done),
			},
			wantTokens: 4,
		},
		{
			name: "activity outside the repository is not activity",
			records: []any{
				claudeFixtureAssistant(1, "m1", "tool_use", usage, bash("a")),
				claudeFixtureToolResult(2, "a", false, ""),
				withCWD(claudeFixtureAssistant(15, "m2", "tool_use", usage, bash("o")), outsideRepository),
				withCWD(claudeFixtureToolResult(16, "o", false, ""), outsideRepository),
				// The replayed history re-enters the repository and completes.
				claudeFixtureAssistant(3, "m3", "tool_use", usage, bash("r")),
				claudeFixtureToolResult(3, "r", false, ""),
				claudeFixtureAssistant(3, "m4", "end_turn", usage, done),
				claudeFixtureAssistant(20, "m5", "tool_use", usage, bash("b")),
				claudeFixtureToolResult(21, "b", false, ""),
				claudeFixtureAssistant(22, "m6", "end_turn", usage, done),
			},
			wantTokens: 4,
		},
		replayedCompletionAfterCost(false, usage, bash, done),
		replayedCompletionAfterCost(true, usage, bash, done),
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, sources, outside := t.TempDir(), t.TempDir(), t.TempDir()
			for _, raw := range tc.records {
				fields := raw.(map[string]any)
				switch {
				case fields["cwd"] == outsideRepository:
					fields["cwd"] = outside
				case fields["timestamp"] != nil:
					fields["cwd"] = root
				}
			}
			path := writeClaudeSessionFixture(t, sources, "replayed-history", tc.records)
			provider, err := resolveSessionSource("claude")
			if err != nil {
				t.Fatal(err)
			}
			discovery := sessionDiscovery{Dirs: []string{sources}, Files: []string{path}}
			since := time.Date(2026, 7, 29, 8, 0, 10, 0, time.UTC)
			until := since.Add(time.Hour)
			direct, err := analyzeProviderSessions(provider, discovery, root, since, until, "", ownershipCatalog{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			stats, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, ownershipCatalog{}, false)
			if err != nil {
				t.Fatal(err)
			}
			indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, since, until, "", ownershipCatalog{}, stats)
			if err != nil {
				t.Fatal(err)
			}
			for label, report := range map[string]codexSessionInsightsReport{"direct": direct, "indexed": indexed} {
				if got := report.Outcomes; got.FullyObservedCompleted != 1 || got.LeftCensoredCompleted != 0 {
					t.Fatalf("%s: fully observed=%d left-censored=%d want 1/0", label, got.FullyObservedCompleted, got.LeftCensoredCompleted)
				}
				if got := report.Summary.Tokens.TotalTokens; got != tc.wantTokens {
					t.Fatalf("%s: total tokens=%d want %d", label, got, tc.wantTokens)
				}
			}
			for _, part := range []struct {
				name            string
				direct, indexed any
			}{
				{"outcomes", direct.Outcomes, indexed.Outcomes},
				{"context profile", direct.ContextProfile, indexed.ContextProfile},
			} {
				d, _ := json.Marshal(part.direct)
				i, _ := json.Marshal(part.indexed)
				if string(d) != string(i) {
					t.Fatalf("%s: direct and indexed disagree:\n%s\n%s", part.name, d, i)
				}
			}
		})
	}
}

const outsideRepository = "<outside the repository>"

func withCWD(record map[string]any, cwd string) map[string]any {
	record["cwd"] = cwd
	return record
}

type replayedHistoryCase struct {
	name       string
	records    []any
	wantTokens int64
}

// Cost bookkeeping inside the window is not activity: with or without it, a
// replayed completion before the first in-window work clears censoring.
func replayedCompletionAfterCost(withCost bool, usage map[string]any, bash func(string) map[string]any, done map[string]any) replayedHistoryCase {
	records := []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, bash("a")),
		claudeFixtureRecord("user", 15, map[string]any{"message": map[string]any{"role": "user", "content": "next"}}),
	}
	name := "replayed completion without cost bookkeeping"
	if withCost {
		records = append(records, claudeFixtureCostState(0.40, 1000, 100))
		name = "replayed completion after cost bookkeeping"
	}
	records = append(records,
		claudeFixtureAssistant(3, "m2", "end_turn", usage, done),
		claudeFixtureAssistant(20, "m3", "tool_use", usage, bash("b")),
		claudeFixtureToolResult(21, "b", false, ""),
		claudeFixtureAssistant(22, "m4", "end_turn", usage, done),
	)
	return replayedHistoryCase{name: name, records: records, wantTokens: 4}
}
