package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Synthetic fixtures for Claude Code telemetry beyond tool events: cost-state
// records, thinking tokens, per-turn effort, and unrecognized record and
// content-block types.

func claudeFixtureCostState(cost float64, apiMs, toolMs int64) map[string]any {
	// cost-state records carry no timestamp and lead with their type.
	return map[string]any{
		"type": "cost-state", "sessionId": claudeFixtureSession,
		"totalCostUSD": cost, "totalAPIDuration": apiMs,
		"totalAPIDurationWithoutRetries": apiMs, "totalToolDuration": toolMs,
		"totalDuration": apiMs + toolMs + 1000, "startTime": 1, "totalLinesAdded": 0,
		"totalLinesRemoved": 0, "hasUnknownModelCost": false,
		"modelUsage": map[string]any{"claude-test-model": map[string]any{
			"inputTokens": 1, "outputTokens": 1, "thinkingTokens": 0, "costUSD": cost,
		}},
	}
}

func claudeFixtureEffortTurn(second int, id, perTurn string, usage map[string]any) map[string]any {
	record := claudeFixtureAssistant(second, id, "end_turn", usage, map[string]any{"type": "text", "text": "ASSISTANT-SECRET"})
	record["perTurnEffort"] = perTurn
	return record
}

func claudeTelemetryConversation() []any {
	usage := func(output, thinking int) map[string]any {
		return map[string]any{
			"input_tokens": 1, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
			"output_tokens": output, "output_tokens_details": map[string]any{"thinking_tokens": thinking},
		}
	}
	return []any{
		// A cost-state before any timestamped record has no time to land at.
		claudeFixtureCostState(0.01, 10, 0),
		claudeFixtureRecord("user", 0, map[string]any{"message": map[string]any{"role": "user", "content": claudeFixturePrompt}}),
		claudeFixtureEffortTurn(1, "msg_1", "medium", usage(100, 40)),
		claudeFixtureCostState(0.50, 2000, 300),
		// The same cost again is not a new observation.
		claudeFixtureCostState(0.50, 2000, 300),
		claudeFixtureEffortTurn(60, "msg_2", "high", usage(50, 30)),
		// A second block of msg_2 must not count its effort twice.
		claudeFixtureEffortTurn(60, "msg_2", "high", usage(50, 30)),
		claudeFixtureEffortTurn(61, "msg_3", "high", usage(10, 0)),
		claudeFixtureCostState(1.25, 5000, 700),
	}
}

func TestParseClaudeSessionEmitsCumulativeProviderCostEvents(t *testing.T) {
	path := writeClaudeSessionFixture(t, t.TempDir(), "main", claudeTelemetryConversation())
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	var costs []normalizedProviderCost
	var times []time.Time
	for _, event := range session.Events {
		switch event.Kind {
		case sessionEventProviderCost:
			costs = append(costs, event.Cost)
			times = append(times, event.OccurredAt)
		case sessionEventToken:
			if event.Cost != (normalizedProviderCost{}) {
				t.Fatalf("token event carries cost: %#v", event)
			}
		}
	}
	// The cost-state before any timestamped record and the repeated one are
	// dropped; each kept one lands at the preceding record's time.
	want := []normalizedProviderCost{
		{CostUSD: 0.50, APIDurationMs: 2000, ToolDurationMs: 300},
		{CostUSD: 1.25, APIDurationMs: 5000, ToolDurationMs: 700},
	}
	wantTimes := []time.Time{
		time.Date(2026, 7, 29, 8, 0, 1, 0, time.UTC),
		time.Date(2026, 7, 29, 8, 1, 1, 0, time.UTC),
	}
	if len(costs) != len(want) {
		t.Fatalf("provider cost events=%v want %v", costs, want)
	}
	for index := range want {
		if costs[index] != want[index] || !times[index].Equal(wantTimes[index]) {
			t.Fatalf("cost event %d = %v at %v, want %v at %v", index, costs[index], times[index], want[index], wantTimes[index])
		}
	}
}

func TestProviderCostEventsDoNotChangeTurnsOrEpisodes(t *testing.T) {
	root := t.TempDir()
	conversation := func(withCost bool) []any {
		usage := map[string]any{"input_tokens": 1, "output_tokens": 10}
		records := []any{
			claudeFixtureRecord("user", 0, map[string]any{"message": map[string]any{"role": "user", "content": claudeFixturePrompt}}),
			claudeFixtureAssistant(1, "msg_1", "tool_use", usage, claudeFixtureToolUse("t1", "Bash", map[string]any{"command": "ls"})),
			claudeFixtureToolResult(2, "t1", false, "ok"),
			claudeFixtureAssistant(3, "msg_2", "end_turn", usage, map[string]any{"type": "text", "text": "x"}),
		}
		if withCost {
			records = append(records, claudeFixtureCostState(0.40, 1000, 100))
		}
		records = append(records,
			claudeFixtureRecord("user", 600, map[string]any{"message": map[string]any{"role": "user", "content": claudeFixturePrompt}}),
			claudeFixtureAssistant(601, "msg_3", "tool_use", usage, claudeFixtureToolUse("t2", "Bash", map[string]any{"command": "ls"})),
			claudeFixtureToolResult(602, "t2", false, "ok"),
			claudeFixtureAssistant(603, "msg_4", "end_turn", usage, map[string]any{"type": "text", "text": "x"}),
		)
		if withCost {
			// Claude Code writes cost-state after a turn, often as the last record.
			records = append(records, claudeFixtureCostState(0.90, 2000, 200))
		}
		for _, record := range records {
			if fields := record.(map[string]any); fields["timestamp"] != nil {
				fields["cwd"] = root
			}
		}
		return records
	}
	generatedAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	records := map[bool]codexSessionRecord{}
	for _, withCost := range []bool{false, true} {
		path := writeClaudeSessionFixture(t, t.TempDir(), "main", conversation(withCost))
		session, err := parseClaudeNormalizedSession(path)
		if err != nil {
			t.Fatal(err)
		}
		records[withCost], err = sessionRecordFromNormalized(session, root, generatedAt.Add(-24*time.Hour), generatedAt, ownershipCatalog{})
		if err != nil {
			t.Fatal(err)
		}
	}
	plain, costly := records[false], records[true]
	if !costly.ProviderCostReported || costly.ProviderCost.CostUSD < 0.9-1e-9 || costly.ProviderCost.CostUSD > 0.9+1e-9 {
		t.Fatalf("cost=%#v", costly.ProviderCost)
	}
	if len(plain.TaskEpisodes) != 2 || !plain.Completed {
		t.Fatalf("baseline episodes=%d completed=%v", len(plain.TaskEpisodes), plain.Completed)
	}
	if len(costly.TaskEpisodes) != len(plain.TaskEpisodes) || costly.Completed != plain.Completed ||
		!costly.EndedAt.Equal(plain.EndedAt) {
		t.Fatalf("cost events changed turns: episodes %d vs %d, completed %v vs %v, ended %v vs %v",
			len(costly.TaskEpisodes), len(plain.TaskEpisodes), costly.Completed, plain.Completed, costly.EndedAt, plain.EndedAt)
	}
	for index := range plain.TaskEpisodes {
		if !costly.TaskEpisodes[index].StartedAt.Equal(plain.TaskEpisodes[index].StartedAt) ||
			costly.TaskEpisodes[index].Completed != plain.TaskEpisodes[index].Completed {
			t.Fatalf("episode %d differs: %#v vs %#v", index, costly.TaskEpisodes[index], plain.TaskEpisodes[index])
		}
	}
}

func TestParseClaudeSessionReportsThinkingAsReasoningWithinOutput(t *testing.T) {
	path := writeClaudeSessionFixture(t, t.TempDir(), "main", claudeTelemetryConversation())
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	var tokens normalizedTokenUsage
	for _, event := range session.Events {
		if event.Kind == sessionEventToken {
			tokens = event.Tokens
		}
	}
	// Output is 100+50+10; thinking is 40+30+0 and is part of output, as
	// reasoning is in Codex, so totals are not inflated.
	if tokens.OutputTokens != 160 || tokens.ReasoningTokens != 70 || tokens.TotalTokens != 163 {
		t.Fatalf("tokens=%#v", tokens)
	}

	// A provider value above output is clamped so reasoning stays a subset.
	odd := claudeFixtureAssistant(1, "msg_odd", "end_turn", map[string]any{
		"input_tokens": 1, "output_tokens": 5, "output_tokens_details": map[string]any{"thinking_tokens": 9},
	})
	path = writeClaudeSessionFixture(t, t.TempDir(), "odd", []any{odd})
	session, err = parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range session.Events {
		if event.Kind == sessionEventToken && event.Tokens.ReasoningTokens != 5 {
			t.Fatalf("unclamped reasoning tokens: %#v", event.Tokens)
		}
	}
}

func TestParseClaudeSessionUsesDominantPerTurnEffort(t *testing.T) {
	path := writeClaudeSessionFixture(t, t.TempDir(), "main", claudeTelemetryConversation())
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	// The first turn ran at medium, but two of three API messages ran at high.
	if session.ReasoningEffort != "high" {
		t.Fatalf("effort=%q want high", session.ReasoningEffort)
	}

	// Without perTurnEffort the record's effort is used; an unsafe label is
	// reported as "(unknown)", as for Codex, and is never echoed.
	plain := claudeFixtureAssistant(1, "msg_a", "end_turn", map[string]any{"input_tokens": 1, "output_tokens": 1})
	unsafe := claudeFixtureAssistant(2, "msg_b", "end_turn", map[string]any{"input_tokens": 1, "output_tokens": 1})
	unsafe["effort"] = "not a label/../x"
	path = writeClaudeSessionFixture(t, t.TempDir(), "plain", []any{plain})
	if session, err = parseClaudeNormalizedSession(path); err != nil || session.ReasoningEffort != "high" {
		t.Fatalf("fallback effort=%q err=%v", session.ReasoningEffort, err)
	}
	unsafeAgain := claudeFixtureAssistant(3, "msg_c", "end_turn", map[string]any{"input_tokens": 1, "output_tokens": 1})
	unsafeAgain["effort"] = unsafe["effort"]
	path = writeClaudeSessionFixture(t, t.TempDir(), "unsafe", []any{unsafe, unsafeAgain, plain})
	if session, err = parseClaudeNormalizedSession(path); err != nil || session.ReasoningEffort != "(unknown)" {
		t.Fatalf("unsafe effort=%q err=%v", session.ReasoningEffort, err)
	}
}

func claudeDriftConversation() []any {
	records := claudeFixtureConversation("/workspace/app")
	unknownLeading := map[string]any{"type": "future-record-TYPESECRET", "sessionId": claudeFixtureSession}
	unknownTrailing := map[string]any{"parentUuid": "p", "type": "future-event-TYPESECRET", "timestamp": claudeFixtureTime(6)}
	known := map[string]any{"type": "custom-title", "sessionId": claudeFixtureSession, "customTitle": "TITLE-SECRET"}
	attachment := map[string]any{"parentUuid": "p", "attachment": map[string]any{"type": "skill_listing"}, "type": "attachment"}
	unknownBlocks := claudeFixtureAssistant(7, "msg_new", "end_turn", map[string]any{"input_tokens": 1, "output_tokens": 1},
		map[string]any{"type": "future_block_TYPESECRET"}, map[string]any{"type": "thinking", "thinking": "THINKING-SECRET"})
	unknownResult := claudeFixtureRecord("user", 8, map[string]any{"message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "future_result_TYPESECRET"},
	}}})
	return append(records, unknownLeading, unknownTrailing, known, attachment, unknownBlocks, unknownResult)
}

func TestParseClaudeSessionCountsUnrecognizedTypes(t *testing.T) {
	path := writeClaudeSessionFixture(t, t.TempDir(), "main", claudeDriftConversation())
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if session.UnrecognizedRecords != 2 || session.UnrecognizedContentBlocks != 2 {
		t.Fatalf("unrecognized records=%d blocks=%d, want 2 and 2", session.UnrecognizedRecords, session.UnrecognizedContentBlocks)
	}
	// Types Claude Code writes but this corpus rarely shows are known too.
	clean := append(claudeFixtureConversation("/workspace/app"),
		map[string]any{"type": "summary", "summary": "TITLE-SECRET", "leafUuid": "u"},
		map[string]any{"parentUuid": "p", "type": "progress", "timestamp": claudeFixtureTime(6)},
		claudeFixtureAssistant(7, "msg_more", "end_turn", map[string]any{"input_tokens": 1, "output_tokens": 1},
			map[string]any{"type": "redacted_thinking"}, map[string]any{"type": "server_tool_use"},
			map[string]any{"type": "web_search_tool_result"}, map[string]any{"type": "web_fetch_tool_result"}),
		claudeFixtureRecord("user", 8, map[string]any{"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "document"},
		}}}),
	)
	path = writeClaudeSessionFixture(t, t.TempDir(), "clean", clean)
	if session, err = parseClaudeNormalizedSession(path); err != nil || session.UnrecognizedRecords != 0 || session.UnrecognizedContentBlocks != 0 {
		t.Fatalf("known shapes counted as drift: %#v err=%v", session, err)
	}
}

func claudeTelemetryWorkspace(t *testing.T) (root, sessionsDir string) {
	t.Helper()
	root, sessionsDir = t.TempDir(), t.TempDir()
	withRoot := func(records []any) []any {
		for _, record := range records {
			if fields := record.(map[string]any); fields["timestamp"] != nil {
				fields["cwd"] = root
			}
		}
		return records
	}
	writeClaudeSessionFixture(t, sessionsDir, "costly", withRoot(claudeTelemetryConversation()))
	drift := withRoot(claudeDriftConversation())
	for _, record := range drift {
		if fields := record.(map[string]any); fields["sessionId"] != nil {
			fields["sessionId"] = "22222222-aaaa-bbbb-cccc-000000000002"
		}
	}
	writeClaudeSessionFixture(t, sessionsDir, "drift", drift)
	return root, sessionsDir
}

func TestClaudeTelemetryReachesReportsAndMatchesTheIndex(t *testing.T) {
	root, sessionsDir := claudeTelemetryWorkspace(t)
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := provider.Discover(sessionsDir, false)
	if err != nil || len(discovery.Files) != 2 {
		t.Fatalf("discovery=%#v err=%v", discovery, err)
	}
	generatedAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	since := generatedAt.Add(-24 * time.Hour)
	direct, err := analyzeProviderSessions(provider, discovery, root, since, generatedAt, "", ownershipCatalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stats, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, ownershipCatalog{}, false)
	if err != nil || stats.FilesIndexed != 2 {
		t.Fatalf("stats=%#v err=%v", stats, err)
	}
	indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, since, generatedAt, "", ownershipCatalog{}, stats)
	if err != nil {
		t.Fatal(err)
	}

	wantCost := providerCostMetrics{Sessions: 1, normalizedProviderCost: normalizedProviderCost{CostUSD: 1.25, APIDurationMs: 5000, ToolDurationMs: 700}}
	wantDrift := providerFormatDriftMetrics{Sessions: 1, UnrecognizedRecords: 2, UnrecognizedContentBlocks: 2}
	for name, report := range map[string]codexSessionInsightsReport{"direct": direct, "indexed": indexed} {
		if report.Summary.ProviderCost != wantCost || report.Summary.ProviderFormatDrift != wantDrift {
			t.Fatalf("%s summary cost=%#v drift=%#v", name, report.Summary.ProviderCost, report.Summary.ProviderFormatDrift)
		}
		if len(report.Tasks) != 1 || report.Tasks[0].ProviderCost != wantCost {
			t.Fatalf("%s task cost=%#v", name, report.Tasks)
		}
		// The index keeps the session's model and effort, so profiles agree.
		profiles, _ := json.Marshal(report.Profiles)
		if !report.Profiles.Available || !strings.Contains(string(profiles), `"reasoningEffort":"high"`) {
			t.Fatalf("%s profiles=%s", name, profiles)
		}
		compact, err := json.Marshal(analysisJSONPayload(report, false))
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{`"providerCost":{"sessions":1,"costUSD":1.25,"apiDurationMs":5000,"toolDurationMs":700}`, `"providerFormatDrift":{"sessions":1,"unrecognizedRecords":2,"unrecognizedContentBlocks":2}`} {
			if !strings.Contains(string(compact), field) {
				t.Fatalf("%s compact report lacks %s: %s", name, field, compact)
			}
		}
		full, _ := json.Marshal(report)
		for _, forbidden := range []string{"TYPESECRET", "future", "TITLE-SECRET", "THINKING-SECRET", "PROMPT-SECRET", claudeFixtureSession} {
			if strings.Contains(string(full), forbidden) {
				t.Fatalf("%s report exposes %q", name, forbidden)
			}
		}
	}
	directProfiles, _ := json.Marshal(direct.Profiles)
	indexedProfiles, _ := json.Marshal(indexed.Profiles)
	if string(directProfiles) != string(indexedProfiles) {
		t.Fatalf("profiles differ:\ndirect  %s\nindexed %s", directProfiles, indexedProfiles)
	}
}

func TestProviderCostCountsOnlyGrowthInsideTheWindow(t *testing.T) {
	root, sessionsDir := claudeTelemetryWorkspace(t)
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := provider.Discover(sessionsDir, false)
	if err != nil {
		t.Fatal(err)
	}
	generatedAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	// The window opens after the 0.50 cost-state (08:00:01) and before the
	// 1.25 one (08:01:01), so only the growth between them counts.
	since := time.Date(2026, 7, 29, 8, 0, 30, 0, time.UTC)
	direct, err := analyzeProviderSessions(provider, discovery, root, since, generatedAt, "", ownershipCatalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stats, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, ownershipCatalog{}, false)
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, since, generatedAt, "", ownershipCatalog{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	// A cost-state just before the window must not hide the completed turn
	// that precedes it, so both paths agree on left-censoring.
	directOutcomes, _ := json.Marshal(direct.Outcomes)
	indexedOutcomes, _ := json.Marshal(indexed.Outcomes)
	if string(directOutcomes) != string(indexedOutcomes) {
		t.Fatalf("outcomes differ:\ndirect  %s\nindexed %s", directOutcomes, indexedOutcomes)
	}
	if direct.Outcomes.LeftCensoredCompleted != 0 {
		t.Fatalf("turn after a completed one is left-censored: %#v", direct.Outcomes)
	}
	want := normalizedProviderCost{CostUSD: 0.75, APIDurationMs: 3000, ToolDurationMs: 400}
	for name, report := range map[string]codexSessionInsightsReport{"direct": direct, "indexed": indexed} {
		got := report.Summary.ProviderCost
		if got.Sessions != 1 || got.APIDurationMs != want.APIDurationMs || got.ToolDurationMs != want.ToolDurationMs ||
			got.CostUSD < want.CostUSD-1e-9 || got.CostUSD > want.CostUSD+1e-9 {
			t.Fatalf("%s windowed cost=%#v want %#v", name, got, want)
		}
	}
}

func TestProviderCostIsOmittedWhenNoSessionReportsIt(t *testing.T) {
	root, sessionsDir := t.TempDir(), t.TempDir()
	conversation := claudeFixtureConversation(root)
	for _, record := range conversation {
		record.(map[string]any)["cwd"] = root
	}
	writeClaudeSessionFixture(t, sessionsDir, "main", conversation)
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := provider.Discover(sessionsDir, false)
	if err != nil {
		t.Fatal(err)
	}
	generatedAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	report, err := analyzeProviderSessions(provider, discovery, root, generatedAt.Add(-24*time.Hour), generatedAt, "", ownershipCatalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(report)
	compact, _ := json.Marshal(analysisJSONPayload(report, false))
	for _, text := range []string{string(raw), string(compact)} {
		if strings.Contains(text, "providerCost") || strings.Contains(text, "providerFormatDrift") {
			t.Fatalf("absent provider telemetry is reported: %s", text)
		}
	}
}

func TestClaudeIndexStoresNoUnrecognizedTypeNames(t *testing.T) {
	root, sessionsDir := claudeTelemetryWorkspace(t)
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := provider.Discover(sessionsDir, false)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "sessions.sqlite")
	store, err := openSessionStore(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, ownershipCatalog{}, false); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "-wal"} {
		raw, err := os.ReadFile(databasePath + name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"TYPESECRET", "TITLE-SECRET", "THINKING-SECRET", claudeFixtureSession} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("index%s stores %q", name, forbidden)
			}
		}
	}
}

func TestProviderCostIsSplitByTheRepositoryShareOfTokens(t *testing.T) {
	root, elsewhere, sessionsDir := t.TempDir(), t.TempDir(), t.TempDir()
	output := func(tokens int) map[string]any {
		return map[string]any{"input_tokens": 0, "output_tokens": tokens}
	}
	inRepo := claudeFixtureAssistant(1, "msg_1", "end_turn", output(100), map[string]any{"type": "text", "text": "x"})
	inRepo["cwd"] = root
	// The agent moves to another directory; its next 300 tokens are not this
	// repository's.
	away := claudeFixtureAssistant(2, "msg_2", "tool_use", output(300),
		claudeFixtureToolUse("t1", "Bash", map[string]any{"command": "ls"}))
	away["cwd"] = elsewhere
	result := claudeFixtureToolResult(3, "t1", false, "ok")
	result["cwd"] = elsewhere
	writeClaudeSessionFixture(t, sessionsDir, "split", []any{
		inRepo, away, result,
		claudeFixtureCostState(1.00, 4000, 800),
		// Growth with no tokens in between follows the current scope: outside.
		claudeFixtureCostState(1.50, 5000, 900),
	})
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := provider.Discover(sessionsDir, false)
	if err != nil {
		t.Fatal(err)
	}
	generatedAt := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	report, err := analyzeProviderSessions(provider, discovery, root, generatedAt.Add(-24*time.Hour), generatedAt, "", ownershipCatalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := report.Summary.ProviderCost
	if got.Sessions != 1 || got.APIDurationMs != 1000 || got.ToolDurationMs != 200 ||
		got.CostUSD < 0.25-1e-9 || got.CostUSD > 0.25+1e-9 {
		t.Fatalf("cost=%#v want a quarter of the first observation only", got)
	}
}

func TestParseClaudeSessionThinkingOnlyUpdateAddsNoTokenEvent(t *testing.T) {
	usage := func(thinking int) map[string]any {
		return map[string]any{"input_tokens": 1, "output_tokens": 20, "output_tokens_details": map[string]any{"thinking_tokens": thinking}}
	}
	records := []any{
		claudeFixtureAssistant(10, "msg_1", "tool_use", usage(5), map[string]any{"type": "text", "text": "x"}),
		claudeFixtureAssistant(20, "msg_2", "end_turn", map[string]any{"input_tokens": 1, "output_tokens": 1}, map[string]any{"type": "text", "text": "x"}),
		// A later copy of msg_1, stamped earlier, differs only in thinking.
		claudeFixtureAssistant(5, "msg_1", "tool_use", usage(8), map[string]any{"type": "text", "text": "x"}),
	}
	path := writeClaudeSessionFixture(t, t.TempDir(), "main", records)
	session, err := parseClaudeNormalizedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	tokenEvents := 0
	for _, event := range session.Events {
		if event.Kind == sessionEventToken {
			tokenEvents++
			if event.OccurredAt.Before(time.Date(2026, 7, 29, 8, 0, 10, 0, time.UTC)) {
				t.Fatalf("out-of-order token event from a thinking-only update: %#v", event)
			}
		}
	}
	if tokenEvents != 2 {
		t.Fatalf("token events=%d want 2 (one per counted usage change)", tokenEvents)
	}
}
