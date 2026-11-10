package cli

import (
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeLatePhysicalResultKeepsItsRepositoryInDirectAndIndexedAnalysis(t *testing.T) {
	root, other, sources := t.TempDir(), t.TempDir(), t.TempDir()
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	records := []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": "go test ./...", "run_in_background": true})),
		claudeFixtureBackgroundResult(2, "b1", "bgtask015", true),
		claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("p1", "TaskOutput", map[string]any{"task_id": "bgtask015"})),
		claudeFixtureNotification(4, "bgtask015", "completed"),
		claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("a1", "Bash", map[string]any{"command": "printf ready"})),
		claudeFixtureToolResult(6, "a1", false, "ready"),
		claudeFixtureToolResult(7, "p1", true, "unexpected output"),
	}
	for i, raw := range records {
		cwd := root
		if i >= 4 {
			cwd = other
		}
		raw.(map[string]any)["cwd"] = cwd
	}
	path := writeClaudeSessionFixture(t, sources, "late-scope", records)
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery := sessionDiscovery{Dirs: []string{sources}, Files: []string{path}}
	store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, repository := range []string{root, other} {
		at := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
		direct, err := analyzeProviderSessions(provider, discovery, repository, at, at.Add(time.Hour), "", ownershipCatalog{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		stats, err := store.refresh(t.Context(), provider.Name(), discovery, repository, provider, ownershipCatalog{}, false)
		if err != nil {
			t.Fatal(err)
		}
		indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, repository, at, at.Add(time.Hour), "", ownershipCatalog{}, stats)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if repository == root {
			want = 1
		}
		for _, report := range []codexSessionInsightsReport{direct, indexed} {
			if report.Summary.FailedToolCalls != want {
				t.Fatalf("repository=%s failures=%d want=%d", repository, report.Summary.FailedToolCalls, want)
			}
		}
	}
}

func TestClaudeContinuationDoesNotRepeatIndexedFlagsOrBypasses(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	records := []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": "runner build --json", "run_in_background": true})),
		claudeFixtureBackgroundResult(2, "b1", "bgtask016", true),
		claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("p1", "Read", map[string]any{"file_path": "/tmp/bgtask016.output"})),
		claudeFixtureToolResult(4, "p1", false, "running"),
		claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("p2", "TaskOutput", map[string]any{"task_id": "bgtask016"})),
		claudeFixtureToolResult(6, "p2", false, "<retrieval_status>success</retrieval_status>\n<status>completed</status>\n<output>done</output>"),
		claudeFixtureAssistant(7, "m4", "end_turn", usage),
	}
	for _, raw := range records {
		raw.(map[string]any)["cwd"] = root
	}
	path := writeClaudeSessionFixture(t, sources, "continuation-flags", records)
	catalog := newOwnershipCatalog([]ownedToolConfig{
		{ID: "runner", Executables: []string{"runner"}, Operations: []ownedOperationConfig{{ID: "build", Args: []string{"build"}}}},
		{ID: "workflow", Executables: []string{"workflow"}, Operations: []ownedOperationConfig{{ID: "verify", Args: []string{"verify"}, BypassPatterns: []ownedBypassPatternConfig{{Executable: "runner", Args: []string{"build", "**"}}}}}},
	})
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery := sessionDiscovery{Dirs: []string{sources}, Files: []string{path}}
	at := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	direct, err := analyzeProviderSessions(provider, discovery, root, at, at.Add(time.Hour), "", catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stats, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, catalog, false)
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, at, at.Add(time.Hour), "", catalog, stats)
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []codexSessionInsightsReport{direct, indexed} {
		if report.Summary.OwnedFlags["runner/build/json"].Count != 1 || report.Summary.OwnedFlagEligibleCalls["runner/build"].Count != 1 || report.Summary.ToolingBypasses["workflow/verify"].Count != 1 || report.Summary.OwnedOperations["runner/build"].Calls != 1 {
			t.Fatalf("continuation duplicates command metadata: flags=%#v scopes=%#v bypasses=%#v operations=%#v", report.Summary.OwnedFlags, report.Summary.OwnedFlagEligibleCalls, report.Summary.ToolingBypasses, report.Summary.OwnedOperations)
		}
	}
}

func TestClaudeReplayedLaunchDoesNotReopenOrDuplicateBackgroundTasks(t *testing.T) {
	for _, settled := range []bool{false, true} {
		root, sources := t.TempDir(), t.TempDir()
		usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
		records := []any{
			claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": "go test ./...", "run_in_background": true})),
			claudeFixtureBackgroundResult(2, "b1", "bgtask017", true),
		}
		if settled {
			records = append(records, claudeFixtureNotification(3, "bgtask017", "completed"))
		}
		records = append(records, claudeFixtureBackgroundResult(4, "b1", "bgtask017", true))
		if !settled {
			records = append(records, claudeFixtureNotification(5, "bgtask017", "completed"))
		}
		records = append(records, claudeFixtureAssistant(7, "m2", "end_turn", usage))
		for _, raw := range records {
			raw.(map[string]any)["cwd"] = root
		}
		path := writeClaudeSessionFixture(t, sources, "replayed-launch", records)
		provider, err := resolveSessionSource("claude")
		if err != nil {
			t.Fatal(err)
		}
		discovery := sessionDiscovery{Dirs: []string{sources}, Files: []string{path}}
		at := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
		direct, err := analyzeProviderSessions(provider, discovery, root, at, at.Add(time.Hour), "", ownershipCatalog{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		stats, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, ownershipCatalog{}, false)
		if err != nil {
			t.Fatal(err)
		}
		indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, at, at.Add(time.Hour), "", ownershipCatalog{}, stats)
		if err != nil {
			t.Fatal(err)
		}
		for _, report := range []codexSessionInsightsReport{direct, indexed} {
			if report.Summary.AbandonedContinuations["tests"].Count != 0 || report.Summary.ShellCommandsByFamily["tests"].Calls != 1 {
				t.Fatalf("settled=%v abandoned=%#v commands=%#v", settled, report.Summary.AbandonedContinuations, report.Summary.ShellCommandsByFamily)
			}
		}
	}
}

func TestClaudeBackgroundCompletionDoesNotMoveFollowingActivityAcrossRepositories(t *testing.T) {
	for _, surface := range []string{"notification", "TaskOutput", "TaskStop"} {
		t.Run(surface, func(t *testing.T) {
			root, other, sources := t.TempDir(), t.TempDir(), t.TempDir()
			records := []any{
				claudeFixtureAssistant(1, "m1", "tool_use", nil, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": "go test ./...", "run_in_background": true})),
				claudeFixtureBackgroundResult(2, "b1", "bgtask018", true),
				claudeFixtureAssistant(3, "m2", "tool_use", nil, claudeFixtureToolUse("c1", "Bash", map[string]any{"command": "printf ready"})),
				claudeFixtureToolResult(4, "c1", false, "ready"),
			}
			if surface == "notification" {
				records = append(records, claudeFixtureNotification(5, "bgtask018", "completed"))
			} else {
				records = append(records, claudeFixtureAssistant(5, "m-poll", "tool_use", nil, claudeFixtureToolUse("p1", surface, map[string]any{"task_id": "bgtask018"})), claudeFixtureToolResult(6, "p1", false, "<retrieval_status>success</retrieval_status>\n<status>completed</status>\n<output>done</output>"))
			}
			records = append(records,
				claudeFixtureAssistant(7, "m3", "tool_use", nil, claudeFixtureToolUse("s1", "Skill", map[string]any{"skill": "planning"})),
				claudeFixtureToolResult(8, "s1", false, "loaded"),
				claudeFixtureAssistant(9, "m4", "tool_use", nil, claudeFixtureToolUse("r1", "Read", map[string]any{"file_path": filepath.Join(other, "README.md")})),
				claudeFixtureToolResult(10, "r1", false, "read"),
				claudeFixtureAssistant(11, "m5", "end_turn", map[string]any{"input_tokens": 80}))
			for i, raw := range records {
				cwd := other
				if i < 2 {
					cwd = root
				}
				raw.(map[string]any)["cwd"] = cwd
			}
			path := writeClaudeSessionFixture(t, sources, "completion-scope", records)
			provider, err := resolveSessionSource("claude")
			if err != nil {
				t.Fatal(err)
			}
			discovery := sessionDiscovery{Dirs: []string{sources}, Files: []string{path}}
			store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			at := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
			for _, repository := range []string{root, other} {
				direct, err := analyzeProviderSessions(provider, discovery, repository, at, at.Add(time.Hour), "", ownershipCatalog{}, nil)
				if err != nil {
					t.Fatal(err)
				}
				stats, err := store.refresh(t.Context(), provider.Name(), discovery, repository, provider, ownershipCatalog{}, false)
				if err != nil {
					t.Fatal(err)
				}
				indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, repository, at, at.Add(time.Hour), "", ownershipCatalog{}, stats)
				if err != nil {
					t.Fatal(err)
				}
				wantLoads, wantTokens := 0, int64(0)
				if repository == other {
					wantLoads = 1
					wantTokens = 80
				}
				for _, report := range []codexSessionInsightsReport{direct, indexed} {
					if report.Summary.SkillLoads["planning"].Loads != wantLoads || report.Summary.ToolMetricsByName["Read"].Calls != wantLoads || report.Summary.Tokens.TotalTokens != wantTokens {
						t.Fatalf("loads=%#v reads=%#v tokens=%#v want loads/reads=%d tokens=%d", report.Summary.SkillLoads, report.Summary.ToolMetricsByName["Read"], report.Summary.Tokens, wantLoads, wantTokens)
					}
				}
			}
		})
	}
}

func claudeBackgroundRecord(t *testing.T, records []any) codexSessionRecord {
	t.Helper()
	path := writeClaudeSessionFixture(t, t.TempDir(), "outcomes", records)
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	record, err := sessionRecordFromNormalized(session, "/workspace/app", at, at.Add(time.Hour), ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestClaudeStoppedBackgroundCommandIsNeverSuccessfulDeliveryOrVerification(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	for _, command := range []string{"go test ./...", "git push"} {
		for _, surface := range []string{"TaskStop", "TaskOutput", "notification"} {
			t.Run(command+"/"+surface, func(t *testing.T) {
				records := []any{
					claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("e1", "Edit", map[string]any{"file_path": "/workspace/app/main.go"})),
					claudeFixtureToolResult(2, "e1", false, "ok"),
					claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": command, "run_in_background": true})),
					claudeFixtureBackgroundResult(4, "b1", "bgtask011", true),
				}
				if surface == "notification" {
					records = append(records, claudeFixtureNotification(6, "bgtask011", "stopped"))
				} else {
					output := "stopped"
					if surface == "TaskOutput" {
						output = "<retrieval_status>success</retrieval_status>\n<status>stopped</status>\n<output>SECRET</output>"
					}
					records = append(records, claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("s1", surface, map[string]any{"task_id": "bgtask011"})), claudeFixtureToolResult(6, "s1", false, output))
					if surface == "TaskStop" {
						records = append(records, claudeFixtureToolResult(6, "s1", false, output))
					}
				}
				if command == "go test ./..." {
					records = append(records, claudeFixtureAssistant(7, "m4", "tool_use", usage, claudeFixtureToolUse("p1", "Bash", map[string]any{"command": "gh pr create --fill"})), claudeFixtureToolResult(8, "p1", false, claudeFixturePRURL), claudeFixturePRLink(8, claudeFixturePRURL))
				}
				records = append(records, claudeFixtureAssistant(9, "m5", "end_turn", usage))
				record := claudeBackgroundRecord(t, records)
				wantDeliveries := 0
				if command == "go test ./..." {
					wantDeliveries = 1
				}
				if record.DeliveryRework.Deliveries != wantDeliveries || record.DeliveryRework.DeliveriesWithPreTests != 0 || record.FailureReasons["interrupted process"] != 1 {
					t.Fatalf("stopped command outcomes: deliveries=%#v failures=%#v", record.DeliveryRework, record.FailureReasons)
				}
			})
		}
	}
}

func TestClaudeOutstandingResultsDoNotResolveAnotherBackgroundTask(t *testing.T) {
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	for _, surface := range []string{"TaskOutput", "TaskStop"} {
		t.Run(surface, func(t *testing.T) {
			records := []any{
				claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("b1", "Bash", map[string]any{"command": "go test ./...", "run_in_background": true})),
				claudeFixtureBackgroundResult(2, "b1", "bgtask012", true),
				claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("b2", "Bash", map[string]any{"command": "go test ./other", "run_in_background": true})),
				claudeFixtureBackgroundResult(4, "b2", "bgtask013", true),
				claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("p1", surface, map[string]any{"task_id": "bgtask012"}), claudeFixtureToolUse("p2", surface, map[string]any{"task_id": "bgtask012"})),
			}
			if surface == "TaskOutput" {
				records = append(records, claudeFixtureNotification(6, "bgtask012", "completed"))
			}
			records = append(records, claudeFixtureToolResult(7, "p1", false, "<retrieval_status>success</retrieval_status>\n<status>completed</status>\n<output>ok</output>"), claudeFixtureToolResult(8, "p2", false, "stopped"), claudeFixtureAssistant(9, "m4", "end_turn", usage))
			record := claudeBackgroundRecord(t, records)
			if record.AbandonedContinuations["tests"] != 1 {
				t.Fatalf("unresolved second task lost: %#v", record.AbandonedContinuations)
			}
		})
	}
}
