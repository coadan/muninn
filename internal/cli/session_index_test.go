package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionStoreReusesUnchangedSourcesAndMatchesDirectAnalysis(t *testing.T) {
	sessionsDir := t.TempDir()
	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	sessionPath := writeCodexSessionFixture(t, sessionsDir, "indexed", []any{
		map[string]any{
			"timestamp": "2026-07-23T08:00:00Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{"total_token_usage": map[string]any{
					"input_tokens": 60, "cached_input_tokens": 40,
					"output_tokens": 5, "total_tokens": 65,
				}},
			},
		},
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
				"type":    "function_call_output",
				"call_id": "search",
				"output":  "exit code: 1",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:03Z",
			"type":      "event_msg",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{"total_token_usage": map[string]any{
					"input_tokens": 80, "cached_input_tokens": 50,
					"output_tokens": 10, "total_tokens": 90,
				}},
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:03.500Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "custom_tool_call",
				"call_id": "edit",
				"name":    "apply_patch",
				"input":   "*** Begin Patch\n*** Update File: src/example.go\n@@\n-old\n+new\n*** End Patch\n",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:04Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "delivery",
				"name":      "exec_command",
				"arguments": `{"cmd":"git push origin feature"}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:05Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "delivery",
				"output":  "exit code: 0",
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:06Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "downstream-test",
				"name":      "exec_command",
				"arguments": `{"cmd":"go test ./..."}`,
			},
		},
		map[string]any{
			"timestamp": "2026-07-24T08:00:07Z",
			"type":      "response_item",
			"payload": map[string]any{
				"type":    "function_call_output",
				"call_id": "downstream-test",
				"output":  "exit code: 1",
			},
		},
	})

	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	discovery, err := discoverCodexSessions([]string{sessionsDir})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.refresh(ctx, "codex", discovery, repositoryRoot, codexSessionProvider, ownershipCatalog{}, false)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if first.FilesIndexed != 1 || first.FilesReused != 0 {
		t.Fatalf("unexpected first refresh: %#v", first)
	}
	second, err := store.refresh(ctx, "codex", discovery, repositoryRoot, codexSessionProvider, ownershipCatalog{}, false)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if second.FilesIndexed != 0 || second.FilesReused != 1 {
		t.Fatalf("unchanged source was not reused: %#v", second)
	}

	generatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	since := generatedAt.Add(-24 * time.Hour)
	metadata := map[string]normalizedSessionMetadata{
		filepath.Clean(sessionPath): {
			Model:           "gpt-5.6-sol",
			ReasoningEffort: "xhigh",
			AgentKind:       "root",
			LineageKey:      "privacy-safe-lineage",
		},
	}
	indexed, err := store.analyze(ctx, "codex", []string{sessionsDir}, repositoryRoot, since, generatedAt, "", ownershipCatalog{}, second, metadata)
	if err != nil {
		t.Fatalf("indexed analysis: %v", err)
	}
	direct, err := analyzeCodexSessionsFilteredWithMetadata(
		[]string{sessionsDir},
		repositoryRoot,
		since,
		generatedAt,
		"",
		ownershipCatalog{},
		metadata,
	)
	if err != nil {
		t.Fatalf("direct analysis: %v", err)
	}
	indexedJSON, _ := json.Marshal(indexed.Summary)
	directJSON, _ := json.Marshal(direct.Summary)
	if string(indexedJSON) != string(directJSON) {
		t.Fatalf("indexed and direct summaries differ:\nindexed=%s\ndirect=%s", indexedJSON, directJSON)
	}
	indexedOutcomesJSON, _ := json.Marshal(indexed.Outcomes)
	directOutcomesJSON, _ := json.Marshal(direct.Outcomes)
	if string(indexedOutcomesJSON) != string(directOutcomesJSON) {
		t.Fatalf("indexed and direct outcomes differ:\nindexed=%s\ndirect=%s", indexedOutcomesJSON, directOutcomesJSON)
	}
	indexedProfilesJSON, _ := json.Marshal(indexed.Profiles)
	directProfilesJSON, _ := json.Marshal(direct.Profiles)
	if string(indexedProfilesJSON) != string(directProfilesJSON) {
		t.Fatalf("indexed and direct profiles differ:\nindexed=%s\ndirect=%s", indexedProfilesJSON, directProfilesJSON)
	}
	if got := indexed.Summary.DownstreamQuality; got.Deliveries != 1 ||
		got.DeliveriesWithFailure != 1 || got.FailureRuns != 1 {
		t.Fatalf("indexed downstream quality mismatch: %#v", got)
	}
	if got := indexed.Summary.Tokens; got.InputTokens != 20 ||
		got.CachedInputTokens != 10 ||
		got.UncachedInputTokens != 10 ||
		got.OutputTokens != 5 ||
		got.TotalTokens != 25 {
		t.Fatalf("indexed window token delta mismatch: %#v", got)
	}
}

func TestSessionStoreIsolatesRepositoryDerivedState(t *testing.T) {
	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	root := t.TempDir()
	repositoryA := filepath.Join(root, "a")
	repositoryB := filepath.Join(root, "b")
	sessionPath := filepath.Join(root, "session.fake")
	if err := os.WriteFile(sessionPath, []byte("session"), 0o600); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	invocationA := []ownedCommandInvocation{{Executable: "runner", Args: []string{"a", "--json"}}}
	invocationB := []ownedCommandInvocation{{Executable: "runner", Args: []string{"b", "--json"}}}
	session := normalizedSession{
		CWD: repositoryA,
		Events: []normalizedSessionEvent{
			{
				Sequence: 1, OccurredAt: startedAt, Kind: sessionEventToolCall,
				ToolName: "exec_command", WorkingDirectories: []string{repositoryA},
				CommandCandidates: invocationA,
			},
			{
				Sequence: 2, OccurredAt: startedAt.Add(time.Second),
				CallOccurredAt: startedAt, Kind: sessionEventToolOutput,
				ToolName: "exec_command", WorkingDirectories: []string{repositoryA},
				CommandCandidates: invocationA,
			},
			{
				Sequence: 3, OccurredAt: startedAt.Add(2 * time.Second),
				Kind: sessionEventToolCall, ToolName: "exec_command",
				WorkingDirectories: []string{repositoryB}, CommandCandidates: invocationB,
			},
			{
				Sequence: 4, OccurredAt: startedAt.Add(3 * time.Second),
				CallOccurredAt: startedAt.Add(2 * time.Second), Kind: sessionEventToolOutput,
				ToolName: "exec_command", WorkingDirectories: []string{repositoryB},
				CommandCandidates: invocationB,
			},
		},
	}
	normalizer := sessionProviderAdapter{
		name: "fake",
		normalize: func(path string) (normalizedSession, error) {
			if path != sessionPath {
				t.Fatalf("normalize path=%q", path)
			}
			copySession := session
			copySession.Events = append([]normalizedSessionEvent(nil), session.Events...)
			return copySession, nil
		},
	}
	discovery := sessionDiscovery{Dirs: []string{root}, Files: []string{sessionPath}}
	ownershipA := newOwnershipCatalog([]ownedToolConfig{{
		ID: "tool-a", Executables: []string{"runner"},
		Operations: []ownedOperationConfig{{ID: "check", Args: []string{"a"}}},
	}})
	ownershipB := newOwnershipCatalog([]ownedToolConfig{{
		ID: "tool-b", Executables: []string{"runner"},
		Operations: []ownedOperationConfig{{ID: "check", Args: []string{"b"}}},
	}})
	ctx := context.Background()
	for _, scope := range []struct {
		root      string
		ownership ownershipCatalog
	}{
		{repositoryA, ownershipA},
		{repositoryB, ownershipB},
	} {
		stats, err := store.refresh(ctx, "fake", discovery, scope.root, normalizer, scope.ownership, false)
		if err != nil {
			t.Fatalf("refresh %s: %v", scope.root, err)
		}
		if stats.FilesIndexed != 1 {
			t.Fatalf("refresh %s stats=%#v", scope.root, stats)
		}
	}
	repositoryC := filepath.Join(root, "c")
	for attempt := 0; attempt < 2; attempt++ {
		stats, err := store.refresh(
			ctx,
			"fake",
			discovery,
			repositoryC,
			normalizer,
			ownershipCatalog{},
			false,
		)
		if err != nil {
			t.Fatalf("refresh unrelated scope: %v", err)
		}
		if attempt == 0 && (stats.FilesIndexed != 0 || stats.FilesReused != 0) {
			t.Fatalf("initial unrelated refresh=%#v", stats)
		}
		if attempt == 1 && stats.FilesReused != 1 {
			t.Fatalf("unrelated source was not negatively cached: %#v", stats)
		}
	}
	ownershipAChanged := newOwnershipCatalog([]ownedToolConfig{{
		ID: "tool-a-v2", Executables: []string{"runner"},
		Operations: []ownedOperationConfig{{ID: "check", Args: []string{"a"}}},
	}})
	changedStats, err := store.refresh(
		ctx,
		"fake",
		discovery,
		repositoryA,
		normalizer,
		ownershipAChanged,
		false,
	)
	if err != nil {
		t.Fatalf("refresh changed ownership config: %v", err)
	}
	if changedStats.FilesIndexed != 1 || changedStats.FilesReused != 0 {
		t.Fatalf("changed ownership config reused stale state: %#v", changedStats)
	}

	var sources int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM sources WHERE provider = 'fake'`,
	).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if sources != 4 {
		t.Fatalf("derived source views=%d want repository, negative, and config scopes", sources)
	}
	for _, want := range []struct {
		root      string
		ownership ownershipCatalog
		operation string
	}{
		{repositoryA, ownershipA, "tool-a/check"},
		{repositoryB, ownershipB, "tool-b/check"},
		{repositoryA, ownershipAChanged, "tool-a-v2/check"},
	} {
		report, err := store.analyze(
			ctx,
			"fake",
			[]string{root},
			want.root,
			startedAt.Add(-time.Minute),
			startedAt.Add(time.Hour),
			"",
			want.ownership,
			sessionRefreshStats{},
		)
		if err != nil {
			t.Fatalf("analyze %s: %v", want.root, err)
		}
		if report.Summary.Sessions != 1 || report.Summary.ToolCalls != 1 ||
			report.Summary.OwnedOperations[want.operation].Calls != 1 ||
			len(report.Summary.OwnedOperations) != 1 {
			t.Fatalf("scope %s report=%#v", want.root, report.Summary)
		}
		toolID, _, _ := strings.Cut(want.operation, "/")
		if got := report.Summary.OwnedFlags[toolID+"/json"]; got.Count != 1 || got.Sessions != 1 {
			t.Fatalf("scope %s owned flag=%#v", want.root, got)
		}
		if got := report.Summary.OwnedFlagCalls[toolID]; got.Count != 1 || got.Sessions != 1 {
			t.Fatalf("scope %s owned flag calls=%#v", want.root, got)
		}
	}
}

func TestSessionStorePreservesScopeAcrossAnalysisBoundary(t *testing.T) {
	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	root := t.TempDir()
	repositoryRoot := filepath.Join(root, "repository")
	otherRoot := filepath.Join(root, "other")
	sessionPath := filepath.Join(root, "session.fake")
	if err := os.WriteFile(sessionPath, []byte("session"), 0o600); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	session := normalizedSession{
		CWD: repositoryRoot,
		Events: []normalizedSessionEvent{
			{
				Sequence:         1,
				OccurredAt:       since.Add(-time.Minute),
				Kind:             sessionEventToolCall,
				ToolName:         "apply_patch",
				TargetCandidates: []string{filepath.Join(otherRoot, "file.go")},
			},
			{
				Sequence:   2,
				OccurredAt: since.Add(time.Minute),
				Kind:       sessionEventToolCall,
				ToolName:   "read_file",
			},
		},
	}
	normalizer := sessionProviderAdapter{
		name: "fake",
		normalize: func(string) (normalizedSession, error) {
			return session, nil
		},
	}
	discovery := sessionDiscovery{Dirs: []string{root}, Files: []string{sessionPath}}
	ctx := context.Background()
	stats, err := store.refresh(
		ctx,
		"fake",
		discovery,
		repositoryRoot,
		normalizer,
		ownershipCatalog{},
		false,
	)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	report, err := store.analyze(
		ctx,
		"fake",
		[]string{root},
		repositoryRoot,
		since,
		since.Add(time.Hour),
		"",
		ownershipCatalog{},
		stats,
	)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if report.Summary.ToolCalls != 0 {
		t.Fatalf("indexed out-of-scope tool calls=%d want 0", report.Summary.ToolCalls)
	}
}

func TestSessionStorePrunesSourcesMissingFromCompleteDiscovery(t *testing.T) {
	store, err := openSessionStore(filepath.Join(t.TempDir(), "muninn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	root := t.TempDir()
	sessionPath := filepath.Join(root, "session.fake")
	if err := os.WriteFile(sessionPath, []byte("session"), 0o600); err != nil {
		t.Fatal(err)
	}
	normalizer := sessionProviderAdapter{
		name: "fake",
		normalize: func(string) (normalizedSession, error) {
			return normalizedSession{
				CWD: root,
				Events: []normalizedSessionEvent{{
					Sequence:   1,
					OccurredAt: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
					Kind:       sessionEventToolCall,
					ToolName:   "exec_command",
				}},
			}, nil
		},
	}
	ctx := context.Background()
	initial := sessionDiscovery{Dirs: []string{root}, Files: []string{sessionPath}}
	stats, err := store.refresh(ctx, "fake", initial, root, normalizer, ownershipCatalog{}, false)
	if err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	if stats.FilesIndexed != 1 {
		t.Fatalf("initial refresh stats=%#v", stats)
	}
	if err := os.Remove(sessionPath); err != nil {
		t.Fatal(err)
	}

	incomplete := sessionDiscovery{Dirs: []string{root}, FilesUnreadable: 1}
	stats, err = store.refresh(ctx, "fake", incomplete, root, normalizer, ownershipCatalog{}, false)
	if err != nil {
		t.Fatalf("incomplete refresh: %v", err)
	}
	if stats.FilesPruned != 0 {
		t.Fatalf("incomplete discovery pruned sources: %#v", stats)
	}
	var sources int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if sources != 1 {
		t.Fatalf("incomplete discovery retained %d sources, want 1", sources)
	}

	complete := sessionDiscovery{Dirs: []string{root}}
	stats, err = store.refresh(ctx, "fake", complete, root, normalizer, ownershipCatalog{}, false)
	if err != nil {
		t.Fatalf("complete refresh: %v", err)
	}
	if stats.FilesPruned != 1 {
		t.Fatalf("complete discovery pruning stats=%#v", stats)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if sources != 0 {
		t.Fatalf("complete discovery retained %d stale sources", sources)
	}
}
