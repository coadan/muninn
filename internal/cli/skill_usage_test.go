package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func skillUsageCall(round int, id string) normalizedSessionEvent {
	return normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "Skill", ToolRound: round,
		GuidanceArtifacts: []normalizedGuidanceArtifact{{Kind: "skill", ID: id}}}
}

func TestCodexSkillUsageReportsTelemetryOrderIncludingDelayedInvocation(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	at := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	row := func(second int, kind string, payload map[string]any) any {
		return map[string]any{"timestamp": at.Add(time.Duration(second) * time.Second).Format(time.RFC3339), "type": kind, "payload": payload}
	}
	tokens := func(n int) map[string]any {
		return map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": map[string]any{"input_tokens": n, "output_tokens": 0, "total_tokens": n}}}
	}
	path := writeCodexSessionFixture(t, sources, "delayed-skill-tokens", []any{
		row(0, "session_meta", map[string]any{"cwd": root}),
		row(1, "response_item", map[string]any{"type": "function_call", "call_id": "s1", "name": "exec_command", "arguments": `{"cmd":"cat .codex/skills/browser-qa/SKILL.md"}`}),
		row(2, "response_item", map[string]any{"type": "function_call_output", "call_id": "s1", "output": "exit code: 0"}),
		row(3, "event_msg", tokens(90)),
		row(4, "response_item", map[string]any{"type": "function_call", "call_id": "g1", "name": "exec_command", "arguments": `{"cmd":"git status --short"}`}),
		row(5, "response_item", map[string]any{"type": "function_call_output", "call_id": "g1", "output": "exit code: 0"}),
		row(6, "event_msg", tokens(140)),
		row(7, "event_msg", map[string]any{"type": "task_complete"}),
	})
	provider, err := resolveSessionSource("codex")
	if err != nil {
		t.Fatal(err)
	}
	discovery := sessionDiscovery{Dirs: []string{sources}, Files: []string{path}}
	direct, err := analyzeProviderSessions(provider, discovery, root, at, at.Add(time.Hour), "", ownershipCatalog{}, nil)
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
	indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, at, at.Add(time.Hour), "", ownershipCatalog{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range []codexSessionInsightsReport{direct, indexed} {
		got := report.Summary.SkillLoads["browser-qa"].UsageAfterLoad
		// Both snapshots follow the load: 90 invocation + 50 later tokens.
		if got.TokenBasis != "telemetry-after-load-result" || got.Tokens.TotalTokens != 140 || got.ToolCalls != 1 || got.CompletedTurns != 1 {
			t.Fatalf("observed telemetry order=%#v", got)
		}
	}
}

func TestSkillUsageDoesNotAssumeNestedOrCompoundReadSuccess(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		wrapped, compound, failed, claude bool
		commented                         bool
		native                            bool
		outputMode                        string
		commandOverride                   string
		loads, unconfirmed                int
	}{
		{name: "wrapped failure", wrapped: true, failed: true, unconfirmed: 1},
		{name: "wrapped success is unproven", wrapped: true, unconfirmed: 1},
		{name: "compound read", compound: true, unconfirmed: 1},
		{name: "Claude compound read", compound: true, claude: true, unconfirmed: 1},
		{name: "direct success", loads: 1},
		{name: "direct failure", failed: true},
		{name: "missing output", outputMode: "missing", unconfirmed: 1},
		{name: "null output", outputMode: "null", unconfirmed: 1},
		{name: "empty output", outputMode: "empty", unconfirmed: 1},
		{name: "unknown status", outputMode: "unknown", unconfirmed: 1},
		{name: "Claude missing content", claude: true, outputMode: "missing", unconfirmed: 1},
		{name: "structured missing exit", outputMode: "structured missing", unconfirmed: 1},
		{name: "structured null exit", outputMode: "structured null", unconfirmed: 1},
		{name: "encoded structured null exit", outputMode: "encoded structured null", unconfirmed: 1},
		{name: "structured success", outputMode: "structured success", loads: 1},
		{name: "commented skill", commented: true, unconfirmed: 1},
		{name: "Claude commented skill", commented: true, claude: true, unconfirmed: 1},
		{name: "cat help does not read", commandOverride: "cat --help .codex/skills/browser-qa/SKILL.md", unconfirmed: 1},
		{name: "expanded arguments unproven", commandOverride: "cat $ARGS .codex/skills/browser-qa/SKILL.md", unconfirmed: 1},
		{name: "reader options unproven", commandOverride: "head -n 0 .codex/skills/browser-qa/SKILL.md", unconfirmed: 1},
		{name: "continued comment", commandOverride: "cat /dev/null \\\n# .codex/skills/browser-qa/SKILL.md", unconfirmed: 1},
		{name: "Claude continued comment", commandOverride: "cat /dev/null \\\n# .codex/skills/browser-qa/SKILL.md", claude: true, unconfirmed: 1},
		{name: "brace options unproven", commandOverride: "cat {--help,} .codex/skills/browser-qa/SKILL.md", unconfirmed: 1},
		{name: "glob operands unproven", commandOverride: "cat * .codex/skills/browser-qa/SKILL.md", unconfirmed: 1},
		{name: "tilde operands unproven", commandOverride: "cat ~/extra .codex/skills/browser-qa/SKILL.md", unconfirmed: 1},
		{name: "stdout without status header", outputMode: "stdout only", unconfirmed: 1},
		{name: "final stdout without status header", outputMode: "final stdout only", unconfirmed: 1},
		{name: "Claude omitted error flag", claude: true, native: true, outputMode: "omitted error flag", loads: 1},
		{name: "Claude null error flag", claude: true, native: true, outputMode: "null error flag", unconfirmed: 1},
		{name: "structured success with failure example", outputMode: "structured success failure example", loads: 1},
		{name: "encoded success with failure example", outputMode: "encoded success failure example", loads: 1},
		{name: "textual success with failure example", outputMode: "textual success failure example", loads: 1},
		{name: "structured failure", outputMode: "structured failure", failed: true},
		{name: "array success", outputMode: "array success", loads: 1},
		{name: "array failure", outputMode: "array failure", failed: true},
		{name: "text block success", outputMode: "text block success", loads: 1},
		{name: "text block failure", outputMode: "text block failure", failed: true},
		{name: "array structured success", outputMode: "array structured success", loads: 1},
		{name: "text block null", outputMode: "text block null", unconfirmed: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, sources := t.TempDir(), t.TempDir()
			at := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
			command := "cat .codex/skills/browser-qa/SKILL.md"
			if tc.commandOverride != "" {
				command = tc.commandOverride
			}
			if tc.commented {
				command = "cat /dev/null # .codex/skills/browser-qa/SKILL.md"
			}
			if tc.compound {
				command += " || true"
			}
			arguments := string(mustJSON(t, map[string]any{"cmd": command}))
			call := map[string]any{"type": "function_call", "name": "exec_command", "call_id": "s1", "arguments": arguments}
			status := "Process exited with code 0"
			if tc.failed {
				status = "Process exited with code 1"
			}
			var output any = status
			outputType := "function_call_output"
			if tc.wrapped {
				call = map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "s1", "input": "text(await tools.exec_command(" + arguments + "));"}
				outputType = "custom_tool_call_output"
				output = []any{map[string]any{"type": "input_text", "text": "Script completed\nWall time 0.1 seconds"}, map[string]any{"type": "input_text", "text": status}}
			}
			row := func(second int, kind string, payload map[string]any) any {
				return map[string]any{"timestamp": at.Add(time.Duration(second) * time.Second).Format(time.RFC3339), "type": kind, "payload": payload}
			}
			providerName := "codex"
			result := map[string]any{"type": outputType, "call_id": "s1", "output": output}
			switch tc.outputMode {
			case "missing":
				delete(result, "output")
			case "null":
				result["output"] = nil
			case "empty":
				result["output"] = ""
			case "unknown":
				result["output"] = "No terminal process status available"
			case "structured missing":
				result["output"] = map[string]any{"output": "exit code: 0"}
			case "structured null":
				result["output"] = map[string]any{"exit_code": nil, "output": "exit code: 0"}
			case "encoded structured null":
				result["output"] = `{"exit_code":null,"output":"exit code: 0"}`
			case "structured success":
				result["output"] = map[string]any{"exit_code": 0, "output": "skill instructions"}
			case "stdout only":
				result["output"] = "Output:\nexit code: 0"
			case "final stdout only":
				result["output"] = "Final output:\nexit code: 0"
			case "structured success failure example":
				result["output"] = map[string]any{"exit_code": 0, "output": "command timed out"}
			case "encoded success failure example":
				result["output"] = `{"exit_code":0,"output":"command timed out"}`
			case "textual success failure example":
				result["output"] = "Process exited with code 0\nOutput:\ncommand timed out"
			case "structured failure":
				result["output"] = map[string]any{"exit_code": 2, "output": "skill instructions"}
			case "array success":
				result["output"] = []any{map[string]any{"type": "input_text", "text": "Process exited with code 0"}, map[string]any{"type": "input_text", "text": "command timed out"}}
			case "array failure":
				result["output"] = []any{map[string]any{"type": "input_text", "text": "Process exited with code 2"}, map[string]any{"type": "input_text", "text": "skill instructions"}}
			case "text block success":
				result["output"] = map[string]any{"type": "input_text", "text": "Process exited with code 0\nOutput:\ncommand timed out"}
			case "text block failure":
				result["output"] = map[string]any{"type": "input_text", "text": "Process exited with code 2\nOutput:\nskill instructions"}
			case "array structured success":
				result["output"] = []any{map[string]any{"type": "input_text", "text": `{"exit_code":0,"output":"command timed out"}`}}
			case "text block null":
				result["output"] = map[string]any{"type": "input_text", "text": nil}
			}
			path := writeCodexSessionFixture(t, sources, "read-status", []any{
				row(0, "session_meta", map[string]any{"cwd": root}), row(1, "response_item", call),
				row(2, "response_item", result),
				row(3, "event_msg", map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": map[string]any{"input_tokens": 80, "total_tokens": 80}}}),
				row(4, "event_msg", map[string]any{"type": "task_complete"}),
			})
			if tc.claude {
				providerName = "claude"
				at = time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
				records := []any{claudeFixtureAssistant(1, "m1", "tool_use", nil, claudeFixtureToolUse("s1", "Bash", map[string]any{"command": command})), claudeFixtureToolResult(2, "s1", false, "ok"), claudeFixtureAssistant(3, "m2", "end_turn", map[string]any{"input_tokens": 80})}
				if tc.native {
					records[0] = claudeFixtureAssistant(1, "m1", "tool_use", nil, claudeFixtureToolUse("s1", "Skill", map[string]any{"skill": "browser-qa"}))
				}
				block := records[1].(map[string]any)["message"].(map[string]any)["content"].([]any)[0].(map[string]any)
				if tc.outputMode == "omitted error flag" {
					delete(block, "is_error")
				} else if tc.outputMode == "null error flag" {
					block["is_error"] = nil
				}
				if tc.outputMode == "missing" {
					message := records[1].(map[string]any)["message"].(map[string]any)
					delete(message["content"].([]any)[0].(map[string]any), "content")
				}
				for _, raw := range records {
					raw.(map[string]any)["cwd"] = root
				}
				path = writeClaudeSessionFixture(t, sources, "claude-read-status", records)
			}
			provider, err := resolveSessionSource(providerName)
			if err != nil {
				t.Fatal(err)
			}
			discovery := sessionDiscovery{Dirs: []string{sources}, Files: []string{path}}
			direct, err := analyzeProviderSessions(provider, discovery, root, at, at.Add(time.Hour), "", ownershipCatalog{}, nil)
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
			indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, at, at.Add(time.Hour), "", ownershipCatalog{}, stats)
			if err != nil {
				t.Fatal(err)
			}
			for _, report := range []codexSessionInsightsReport{direct, indexed} {
				got := report.Summary.SkillLoads["browser-qa"].UsageAfterLoad
				wantTokens := int64(0)
				if tc.loads == 1 {
					wantTokens = 80
				}
				if got.SuccessfulLoads != tc.loads || got.UnconfirmedLoadAttempts != tc.unconfirmed || got.Tokens.TotalTokens != wantTokens {
					t.Fatalf("load confirmation=%#v want loads=%d unconfirmed=%d tokens=%d", got, tc.loads, tc.unconfirmed, wantTokens)
				}
				if tc.loads == 1 && report.Summary.FailedToolCalls != 0 {
					t.Fatalf("successful read reported %d failures", report.Summary.FailedToolCalls)
				}
				if (tc.outputMode == "structured failure" || tc.outputMode == "array failure" || tc.outputMode == "text block failure") && report.Summary.FailedToolCalls != 1 {
					t.Fatalf("failed process reported %d failures", report.Summary.FailedToolCalls)
				}
			}
		})
	}
}

func TestClaudeYieldedSkillReadRequiresSuccessfulTerminalResult(t *testing.T) {
	for _, completion := range []string{"poll completed", "notification completed", "poll failed", "cancelled", "still running", "replay failed", "replay completed", "replay pending"} {
		t.Run(completion, func(t *testing.T) {
			root, sources := t.TempDir(), t.TempDir()
			records := []any{
				claudeFixtureAssistant(1, "m1", "tool_use", nil, claudeFixtureToolUse("s1", "Bash", map[string]any{"command": "cat .claude/skills/planning/SKILL.md", "run_in_background": true})),
				claudeFixtureBackgroundResult(2, "s1", "bgtask019", true),
			}
			if completion == "replay failed" || completion == "replay completed" || completion == "replay pending" {
				records = append(records, claudeFixtureBackgroundResult(3, "s1", "bgtask019", true),
					claudeFixtureAssistant(4, "m-before-completion", "end_turn", map[string]any{"input_tokens": 40}))
				if completion == "replay completed" {
					records = append(records, claudeFixtureNotification(5, "bgtask019", "completed"))
				} else if completion == "replay failed" {
					records = append(records, claudeFixtureNotification(5, "bgtask019", "failed"))
				}
			} else if completion == "notification completed" {
				records = append(records, claudeFixtureNotification(4, "bgtask019", "completed"))
			} else {
				tool, status := "TaskOutput", "completed"
				if completion == "cancelled" {
					tool = "TaskStop"
				} else if completion == "poll failed" {
					status = "failed"
				} else if completion == "still running" {
					status = "running"
				}
				records = append(records,
					claudeFixtureAssistant(3, "m2", "tool_use", nil, claudeFixtureToolUse("p1", tool, map[string]any{"task_id": "bgtask019"})),
					claudeFixtureToolResult(4, "p1", false, "<retrieval_status>success</retrieval_status>\n<status>"+status+"</status>\n<output>skill instructions</output>"))
			}
			records = append(records, claudeFixtureAssistant(6, "m3", "end_turn", map[string]any{"input_tokens": 80}))
			for _, raw := range records {
				raw.(map[string]any)["cwd"] = root
			}
			path := writeClaudeSessionFixture(t, sources, "yielded-read", records)
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
			defer store.Close()
			stats, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, ownershipCatalog{}, false)
			if err != nil {
				t.Fatal(err)
			}
			indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, at, at.Add(time.Hour), "", ownershipCatalog{}, stats)
			if err != nil {
				t.Fatal(err)
			}
			for _, report := range []codexSessionInsightsReport{direct, indexed} {
				got := report.Summary.SkillLoads["planning"].UsageAfterLoad
				wantLoads, wantTokens, wantUnconfirmed := 0, int64(0), 0
				if completion == "poll completed" || completion == "notification completed" || completion == "replay completed" {
					wantLoads, wantTokens = 1, 80
				} else if completion == "still running" || completion == "replay pending" {
					wantUnconfirmed = 1
				}
				if got.SuccessfulLoads != wantLoads || got.Tokens.TotalTokens != wantTokens || got.UnconfirmedLoadAttempts != wantUnconfirmed || got.CompletedTurns != wantLoads {
					t.Fatalf("terminal skill usage=%#v want loads=%d tokens=%d unconfirmed=%d", got, wantLoads, wantTokens, wantUnconfirmed)
				}
			}
		})
	}
}

func TestClaudeSkillSeparatesInstalledLoadingEstimateFromMeasuredUsage(t *testing.T) {
	root, sources, config := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	writeClaudeSkill(t, filepath.Join(config, "skills", "planning"), 6_000)
	records := []any{
		claudeFixtureAssistant(1, "m1", "tool_use", map[string]any{"input_tokens": 1, "output_tokens": 1}, claudeFixtureToolUse("s1", "Skill", map[string]any{"skill": "planning"})),
		claudeFixtureToolResult(2, "s1", false, "loaded"),
		claudeFixtureAssistant(3, "m2", "end_turn", map[string]any{"input_tokens": 200, "output_tokens": 10}),
	}
	for _, raw := range records {
		raw.(map[string]any)["cwd"] = root
	}
	path := writeClaudeSessionFixture(t, sources, "skill-cost", records)
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	report, err := analyzeProviderSessions(provider, sessionDiscovery{Dirs: []string{sources}, Files: []string{path}}, root, at.Add(-time.Hour), at, "", ownershipCatalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	refreshReportGuidanceSizes(&report, provider, root)
	got := report.Summary.SkillLoads["planning"]
	// 6000 bytes / 4 is the file estimate; 200 input + 10 output is recorded work.
	if got.EstimatedTokens != 1500 || got.EstimatedLoadedTokens != 1500 || got.UsageAfterLoad.Tokens.TotalTokens != 210 || got.UsageAfterLoad.SuccessfulLoads != 1 || got.UsageAfterLoad.CompletedTurns != 1 {
		t.Fatalf("loading estimate and measured work=%#v", got)
	}
}

func TestSkillUsageStartsAfterSuccessAndDoesNotMultiplyReloads(t *testing.T) {
	state := newSkillUsageState()
	call := func(round int, id string) { state.observe(skillUsageCall(round, id), normalizedTokenUsage{}) }
	result := func(round int) {
		state.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, ToolRound: round, GuidanceReadSucceeded: true}, normalizedTokenUsage{})
	}
	tokens := func(usage normalizedTokenUsage) {
		state.observe(normalizedSessionEvent{Kind: sessionEventToken}, usage)
	}
	call(1, "alpha")
	// This API request emitted the skill invocation, before the result.
	tokens(normalizedTokenUsage{InputTokens: 100, CachedInputTokens: 20, UncachedInputTokens: 80, OutputTokens: 10, ReasoningTokens: 2, TotalTokens: 110})
	result(1)
	tokens(normalizedTokenUsage{InputTokens: 60, CachedInputTokens: 20, UncachedInputTokens: 40, OutputTokens: 10, ReasoningTokens: 3, TotalTokens: 70})
	call(2, "alpha")
	result(2)
	tokens(normalizedTokenUsage{InputTokens: 80, CachedInputTokens: 30, UncachedInputTokens: 50, OutputTokens: 15, ReasoningTokens: 3, TotalTokens: 95})
	call(3, "beta")
	result(3)
	tokens(normalizedTokenUsage{InputTokens: 110, CachedInputTokens: 40, UncachedInputTokens: 70, OutputTokens: 20, ReasoningTokens: 4, TotalTokens: 130})
	state.observe(normalizedSessionEvent{Kind: sessionEventToolCall, ToolRound: 4}, normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, ToolRound: 4, Failed: true}, normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventComplete}, normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventCompaction}, normalizedTokenUsage{})
	tokens(normalizedTokenUsage{InputTokens: 999, TotalTokens: 999})
	state.finish(false)
	var alpha, beta skillUsageMetrics
	addSkillUsageMetrics(&alpha, state.metrics["alpha"])
	addSkillUsageMetrics(&beta, state.metrics["beta"])
	// Worked input deltas above: 60+80+110 input and 10+15+20 output.
	if alpha.Tokens != (normalizedTokenUsage{InputTokens: 250, CachedInputTokens: 90, UncachedInputTokens: 160, OutputTokens: 45, ReasoningTokens: 10, TotalTokens: 295}) || alpha.FreshTokens != 205 {
		t.Fatalf("alpha token usage=%#v", alpha)
	}
	if beta.Tokens != (normalizedTokenUsage{InputTokens: 110, CachedInputTokens: 40, UncachedInputTokens: 70, OutputTokens: 20, ReasoningTokens: 4, TotalTokens: 130}) || beta.FreshTokens != 90 {
		t.Fatalf("beta token usage=%#v", beta)
	}
	if alpha.SuccessfulLoads != 2 || alpha.ToolCalls != 3 || alpha.FailedToolCalls != 1 || alpha.CompletedTurns != 1 || alpha.IncompleteTurns != 0 {
		t.Fatalf("alpha outcomes=%#v", alpha)
	}
	if beta.SuccessfulLoads != 1 || beta.ToolCalls != 1 || beta.FailedToolCalls != 1 || beta.CompletedTurns != 1 {
		t.Fatalf("beta outcomes=%#v", beta)
	}
}

func TestSkillUsageRejectsUnsuccessfulLoadsAndStopsAtCompaction(t *testing.T) {
	state := newSkillUsageState()
	for _, result := range []normalizedSessionEvent{
		{Kind: sessionEventToolOutput, ToolRound: 1, Failed: true},
		{Kind: sessionEventToolOutput, ToolRound: 1, Refusal: "denied-by-permission"},
	} {
		state.observe(skillUsageCall(1, "failed"), normalizedTokenUsage{})
		state.observe(result, normalizedTokenUsage{})
	}
	state.observe(normalizedSessionEvent{Kind: sessionEventToken}, normalizedTokenUsage{TotalTokens: 100})
	if len(state.metrics) != 0 {
		t.Fatalf("unsuccessful load activated a skill: %#v", state.metrics)
	}
	state.observe(skillUsageCall(2, "alpha"), normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, ToolRound: 2, GuidanceReadSucceeded: true}, normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventToken}, normalizedTokenUsage{TotalTokens: 20})
	state.observe(normalizedSessionEvent{Kind: sessionEventCompaction}, normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventToken}, normalizedTokenUsage{TotalTokens: 300})
	state.observe(normalizedSessionEvent{Kind: sessionEventComplete}, normalizedTokenUsage{})
	state.finish(false)
	if got := state.metrics["alpha"]; got.Tokens.TotalTokens != 20 || got.IncompleteTurns != 1 || got.CompletedTurns != 0 {
		t.Fatalf("compacted usage=%#v", got)
	}
}

func TestSkillUsageInSessionIsLimitedToAnalysisWindow(t *testing.T) {
	at := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{
		skillUsageCall(1, "outside"),
		{Kind: sessionEventToolOutput, ToolRound: 1, GuidanceReadSucceeded: true},
		{Kind: sessionEventToken, Tokens: normalizedTokenUsage{InputTokens: 100, UncachedInputTokens: 100, TotalTokens: 100}},
		skillUsageCall(2, "inside"),
		{Kind: sessionEventToolOutput, ToolRound: 2, GuidanceReadSucceeded: true},
		{Kind: sessionEventToken, Tokens: normalizedTokenUsage{InputTokens: 120, UncachedInputTokens: 120, OutputTokens: 5, TotalTokens: 125}},
		{Kind: sessionEventComplete},
	}
	for i := range events {
		events[i].OccurredAt = at.Add(time.Duration(i) * time.Second)
		events[i].CallOccurredAt = events[i].OccurredAt
	}
	record, err := sessionRecordFromNormalized(normalizedSession{CWD: "/workspace/app", Events: events}, "/workspace/app", at.Add(2*time.Second), at.Add(6*time.Second), ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := record.SkillUsageAfterLoad["outside"]; ok {
		t.Fatal("load outside analysis window attributed")
	}
	got := record.SkillUsageAfterLoad["inside"]
	if got.Tokens.TotalTokens != 25 || got.CompletedTurns != 1 {
		t.Fatalf("window usage=%#v", got)
	}
}

func TestSkillUsageCountsReviewEditCyclesAcrossTurns(t *testing.T) {
	state := newSkillUsageState()
	for _, event := range []normalizedSessionEvent{
		skillUsageCall(1, "workflow"),
		{Kind: sessionEventToolOutput, ToolRound: 1, GuidanceReadSucceeded: true},
		{Kind: sessionEventToolCall, ToolRound: 2, OwnedOperations: []string{"workflow/review"}},
		{Kind: sessionEventComplete},
		{Kind: sessionEventToolCall, ToolRound: 3, ToolName: "apply_patch"},
		{Kind: sessionEventToolCall, ToolRound: 4, ToolName: "apply_patch"},
		{Kind: sessionEventToolCall, ToolRound: 5, ToolName: "wait", Family: "review"},
		{Kind: sessionEventToolCall, ToolRound: 6, Family: "review"},
		{Kind: sessionEventToolCall, ToolRound: 7, ToolName: "apply_patch"},
		{Kind: sessionEventComplete},
	} {
		state.observe(event, normalizedTokenUsage{})
	}
	state.finish(false)
	got := state.metrics["workflow"]
	if got.ReviewCalls != 2 || got.ReviewEditCycles != 2 || got.CompletedTurns != 2 || got.IncompleteTurns != 0 {
		t.Fatalf("workflow loop=%#v", got)
	}
}

func TestClaudeSkillUsageMatchesIndexedAnalysis(t *testing.T) {
	root, sessionsDir := t.TempDir(), t.TempDir()
	records := []any{
		claudeFixtureAssistant(1, "m1", "tool_use", map[string]any{"input_tokens": 100, "output_tokens": 10}, claudeFixtureToolUse("s1", "Skill", map[string]any{"skill": "workflow"})),
		claudeFixtureToolResult(2, "s1", false, "Launching skill: workflow"),
		claudeFixtureAssistant(3, "m2", "tool_use", map[string]any{"input_tokens": 200, "output_tokens": 20, "output_tokens_details": map[string]any{"thinking_tokens": 5}}, claudeFixtureToolUse("t1", "Bash", map[string]any{"command": "go test ./..."})),
		claudeFixtureToolResult(4, "t1", true, "tests failed"),
		claudeFixtureAssistant(5, "m3", "end_turn", map[string]any{"input_tokens": 50, "output_tokens": 5}),
	}
	for _, raw := range records {
		raw.(map[string]any)["cwd"] = root
	}
	writeClaudeSessionFixture(t, sessionsDir, "skill", records)
	provider, err := resolveSessionSource("claude")
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := provider.Discover(sessionsDir, false)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	direct, err := analyzeProviderSessions(provider, discovery, root, at.Add(-time.Hour), at, "", ownershipCatalog{}, nil)
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
	indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, at.Add(-time.Hour), at, "", ownershipCatalog{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	got := direct.Summary.SkillLoads["workflow"].UsageAfterLoad
	want := skillUsageMetrics{TokenBasis: "telemetry-after-load-result", SuccessfulLoads: 1, Tokens: normalizedTokenUsage{InputTokens: 250, UncachedInputTokens: 250, OutputTokens: 25, ReasoningTokens: 5, TotalTokens: 275}, FreshTokens: 275, ToolCalls: 1, FailedToolCalls: 1, CompletedTurns: 1}
	if got != want {
		t.Fatalf("direct usage=%#v, want %#v", got, want)
	}
	if !reflect.DeepEqual(direct.Summary.SkillLoads, indexed.Summary.SkillLoads) {
		t.Fatalf("indexed skill metrics differ: direct=%#v indexed=%#v", direct.Summary.SkillLoads, indexed.Summary.SkillLoads)
	}
}

func TestSkillUsageDoesNotReintroduceExcludedTokenTelemetry(t *testing.T) {
	at := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	events := []normalizedSessionEvent{skillUsageCall(1, "workflow"), {Kind: sessionEventToolOutput, ToolRound: 1, GuidanceReadSucceeded: true}}
	for i := 1; i <= 2_001; i++ {
		input := int64(i) * 1_000_000_000
		events = append(events, normalizedSessionEvent{Kind: sessionEventToken,
			OccurredAt: at.Add(time.Duration(i) * time.Millisecond),
			Tokens: normalizedTokenUsage{InputTokens: input, UncachedInputTokens: input,
				OutputTokens: 1_000_000_000, TotalTokens: input + 1_000_000_000}})
	}
	events[0].OccurredAt, events[1].OccurredAt = at, at
	events[1].CallOccurredAt = at
	record, err := sessionRecordFromNormalized(normalizedSession{CWD: "/workspace/app", Events: events},
		"/workspace/app", at, at.Add(time.Hour), ownershipCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	if !record.TokenTelemetryExcluded {
		t.Fatal("fixture did not exercise excluded telemetry")
	}
	metrics := record.SkillUsageAfterLoad["workflow"]
	if metrics.SuccessfulLoads != 1 || metrics.Tokens != (normalizedTokenUsage{}) {
		t.Fatalf("excluded stream leaked into skill usage: %#v", metrics)
	}
}

func TestSkillUsageLifecycleMatchesIndexedAnalysis(t *testing.T) {
	for _, name := range []string{"yielded load", "compaction outside repository", "duplicate snapshot after completion"} {
		t.Run(name, func(t *testing.T) {
			root, outside, sources := t.TempDir(), t.TempDir(), t.TempDir()
			events := []normalizedSessionEvent{skillUsageCall(1, "workflow")}
			wantTokens, wantCompleted, wantIncomplete := int64(40), 1, 0
			if name == "yielded load" {
				events = append(events,
					normalizedSessionEvent{Kind: sessionEventToolOutput, ToolRound: 1, OperationContinues: true},
					normalizedSessionEvent{Kind: sessionEventToken, Tokens: normalizedTokenUsage{InputTokens: 100, UncachedInputTokens: 100, TotalTokens: 100}},
					normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "wait", ToolRound: 1},
					normalizedSessionEvent{Kind: sessionEventToolOutput, ToolName: "wait", ToolRound: 1, GuidanceReadSucceeded: true},
				)
			} else if name == "duplicate snapshot after completion" {
				wantTokens = 140
				events = append(events, normalizedSessionEvent{Kind: sessionEventToolOutput, ToolRound: 1, GuidanceReadSucceeded: true})
			} else {
				wantTokens, wantCompleted, wantIncomplete = 100, 0, 1
				events = append(events,
					normalizedSessionEvent{Kind: sessionEventToolOutput, ToolRound: 1, GuidanceReadSucceeded: true},
					normalizedSessionEvent{Kind: sessionEventToken, Tokens: normalizedTokenUsage{InputTokens: 100, UncachedInputTokens: 100, TotalTokens: 100}},
					normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "exec_command", WorkingDirectories: []string{outside}},
					normalizedSessionEvent{Kind: sessionEventCompaction},
					normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "exec_command", WorkingDirectories: []string{root}},
				)
			}
			events = append(events,
				normalizedSessionEvent{Kind: sessionEventToken, Tokens: normalizedTokenUsage{InputTokens: 140, UncachedInputTokens: 140, TotalTokens: 140}},
				normalizedSessionEvent{Kind: sessionEventComplete},
			)
			if name == "duplicate snapshot after completion" {
				events = append(events, normalizedSessionEvent{Kind: sessionEventToken, Tokens: normalizedTokenUsage{InputTokens: 140, UncachedInputTokens: 140, TotalTokens: 140}})
			}
			at := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
			for i := range events {
				events[i].Sequence = i + 1
				events[i].OccurredAt = at.Add(time.Duration(i) * time.Second)
				events[i].CallOccurredAt = events[i].OccurredAt
			}
			path := filepath.Join(sources, "session.jsonl")
			if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			provider := sessionProviderAdapter{name: "fixture", normalize: func(string) (normalizedSession, error) {
				return normalizedSession{Provider: "fixture", SourcePath: path, CWD: root, Events: events}, nil
			}}
			discovery := sessionDiscovery{Dirs: []string{sources}, Files: []string{path}}
			direct, err := analyzeProviderSessions(provider, discovery, root, at.Add(-time.Second), at.Add(time.Hour), "", ownershipCatalog{}, nil)
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
			indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, at.Add(-time.Second), at.Add(time.Hour), "", ownershipCatalog{}, stats)
			if err != nil {
				t.Fatal(err)
			}
			for _, report := range []codexSessionInsightsReport{direct, indexed} {
				got := report.Summary.SkillLoads["workflow"].UsageAfterLoad
				if got.SuccessfulLoads != 1 || got.Tokens.TotalTokens != wantTokens || got.CompletedTurns != wantCompleted || got.IncompleteTurns != wantIncomplete {
					t.Fatalf("lifecycle usage=%#v, want tokens=%d completed=%d incomplete=%d", got, wantTokens, wantCompleted, wantIncomplete)
				}
			}
		})
	}
}

// A result after end_turn, such as a background task's notification, is no
// new turn of work, so it must not leave an incomplete turn behind.
func TestSkillUsageLateResultAfterEndTurnOpensNoTurn(t *testing.T) {
	state := newSkillUsageState()
	state.observe(skillUsageCall(1, "alpha"), normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, ToolRound: 1, GuidanceReadSucceeded: true}, normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventToolCall, ToolName: "exec_command", ToolRound: 2}, normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, ToolName: "exec_command", ToolRound: 2, OperationContinues: true}, normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventComplete}, normalizedTokenUsage{})
	state.observe(normalizedSessionEvent{Kind: sessionEventToolOutput, ToolName: "wait", ToolRound: 2}, normalizedTokenUsage{})
	state.finish(false)
	if got := state.metrics["alpha"]; got.CompletedTurns != 1 || got.IncompleteTurns != 0 {
		t.Fatalf("turns=%d completed, %d incomplete; want 1 and 0", got.CompletedTurns, got.IncompleteTurns)
	}
}
