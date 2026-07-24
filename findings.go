package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type sessionFinding struct {
	Category string `json:"category"`
	Control  string `json:"control"`
	Title    string `json:"title"`
	Evidence string `json:"evidence"`
	Action   string `json:"action"`
	Count    int    `json:"count,omitempty"`
	Sessions int    `json:"sessions,omitempty"`
	Target   string `json:"target,omitempty"`
	score    int
}

func buildSessionFindings(report codexSessionInsightsReport, config repositoryConfig) []sessionFinding {
	summary := report.Summary
	var findings []sessionFinding

	for _, feedback := range report.Feedback {
		findings = append(findings, sessionFinding{
			Category: directFeedbackFindingCategory(feedback.Category),
			Control:  feedback.Control,
			Title:    "direct feedback: " + feedback.Signal,
			Evidence: fmt.Sprintf("%s explicitly reported occurrences; category %s; sources %s; last seen %s",
				formatCodexCount(int64(feedback.Occurrences)),
				feedback.Category,
				strings.Join(feedback.Sources, ", "),
				feedback.LastSeen,
			),
			Action: directFeedbackAction(feedback),
			Count:  feedback.Occurrences,
			Target: feedback.Target,
			score:  800 + feedback.Occurrences*10,
		})
	}

	ownedConfigByID := map[string]ownedToolConfig{}
	for _, owned := range config.OwnedTools {
		ownedConfigByID[owned.ID] = owned
	}
	for operation, metrics := range summary.OwnedOperations {
		if metrics.Sessions < 2 {
			continue
		}
		actionableFailures, expectedFailures := ownedOperationFailureCounts(summary.OwnedOperationFailureReasons[operation])
		if actionableFailures < 2 && metrics.TruncatedCalls < 3 &&
			metrics.EstimatedOutputTokens < 10_000 && metrics.Calls < 20 {
			continue
		}
		toolID, _, _ := strings.Cut(operation, "/")
		owned := ownedConfigByID[toolID]
		action := strings.TrimSpace(owned.Recommendation)
		if action == "" {
			action = "Improve this locally controlled operation or its defaults before documenting another agent workaround."
		}
		title := "high-cost locally controlled operation: " + operation
		if actionableFailures >= 2 || metrics.TruncatedCalls >= 3 {
			title = "locally controlled operation has recurring friction: " + operation
		}
		findings = append(findings, sessionFinding{
			Category: "owned-operation",
			Control:  "local",
			Title:    title,
			Evidence: fmt.Sprintf("%s calls across %s sessions, %s bundled calls, %s actionable failures, %s expected/product failures, %s ambiguous bundled failures, %s truncations, ~%s attributed output tokens, ~%s ambiguous bundled output tokens",
				formatCodexCount(int64(metrics.Calls)),
				formatCodexCount(int64(metrics.Sessions)),
				formatCodexCount(int64(metrics.AmbiguousCalls)),
				formatCodexCount(int64(actionableFailures)),
				formatCodexCount(int64(expectedFailures)),
				formatCodexCount(int64(metrics.AmbiguousFailedCalls)),
				formatCodexCount(int64(metrics.TruncatedCalls)),
				formatCodexCount(metrics.EstimatedOutputTokens),
				formatCodexCount(metrics.EstimatedAmbiguousOutputTokens),
			),
			Action:   action,
			Count:    metrics.Calls,
			Sessions: metrics.Sessions,
			score:    650 + metrics.Sessions*20 + actionableFailures*30 + metrics.TruncatedCalls*10 + int(metrics.EstimatedOutputTokens/5_000),
		})
	}

	for _, owned := range config.OwnedTools {
		metrics := summary.OwnedTooling[owned.ID]
		outputTokens := estimatedTokens(metrics.OutputBytes)
		if metrics.FailedCalls < 2 && metrics.TruncatedCalls < 3 && outputTokens < 50_000 {
			continue
		}
		action := strings.TrimSpace(owned.Recommendation)
		if action == "" {
			action = "Improve the locally controlled CLI or its defaults before working around the behavior in agent instructions."
		}
		findings = append(findings, sessionFinding{
			Category: "owned-tool",
			Control:  "local",
			Title:    "locally controlled tooling has recurring friction: " + owned.ID,
			Evidence: fmt.Sprintf("%s calls, %s failures, %s truncations, ~%s visible output tokens",
				formatCodexCount(int64(metrics.Calls)),
				formatCodexCount(int64(metrics.FailedCalls)),
				formatCodexCount(int64(metrics.TruncatedCalls)),
				formatCodexCount(outputTokens),
			),
			Action: action,
			Count:  metrics.Calls,
			score:  500 + metrics.FailedCalls*20 + metrics.TruncatedCalls*5 + int(outputTokens/10_000),
		})
	}

	for reason, contexts := range summary.FailureContexts {
		for context, metrics := range contexts {
			if metrics.Sessions < 2 || metrics.Count < 2 {
				continue
			}
			findings = append(findings, sessionFinding{
				Category: "recurring-failure",
				Control:  "repository",
				Title:    reason + " recurs across sessions",
				Evidence: fmt.Sprintf("%s calls in %s sessions; context: %s",
					formatCodexCount(int64(metrics.Count)),
					formatCodexCount(int64(metrics.Sessions)),
					context,
				),
				Action:   config.Actions.RecurringFailure,
				Count:    metrics.Count,
				Sessions: metrics.Sessions,
				score:    400 + metrics.Sessions*30 + metrics.Count,
			})
		}
	}

	type workflowEvidence struct {
		Count    int
		Sessions int
	}
	workflows := map[string]workflowEvidence{}
	for transition, metrics := range summary.CrossCallTransitions {
		workflow := agentInterfaceWorkflow(transition)
		if workflow == "" {
			continue
		}
		evidence := workflows[workflow]
		evidence.Count += metrics.Count
		if metrics.Sessions > evidence.Sessions {
			evidence.Sessions = metrics.Sessions
		}
		workflows[workflow] = evidence
	}
	for workflow, metrics := range workflows {
		if metrics.Sessions < 3 || metrics.Count < 5 {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "agent-interface",
			Control:  "repository",
			Title:    "repeated cross-call workflow: " + workflow,
			Evidence: fmt.Sprintf("%s transitions across %s sessions",
				formatCodexCount(int64(metrics.Count)),
				formatCodexCount(int64(metrics.Sessions)),
			),
			Action:   config.Actions.AgentInterface,
			Count:    metrics.Count,
			Sessions: metrics.Sessions,
			score:    300 + metrics.Sessions*10 + metrics.Count,
		})
	}

	for target, metrics := range summary.ReadTargets {
		if metrics.Sessions < 2 || metrics.Reads < 4 {
			continue
		}
		path := filepath.Join(report.WorkspaceRoot, filepath.FromSlash(target))
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		const boundedOwnerBytes = 12 * 1024
		if info.Size() <= boundedOwnerBytes || filepath.Base(target) == "AGENTS.md" || repositoryManifestTarget(target) {
			if metrics.SearchReadLoops < 5 {
				continue
			}
			action := "Prefer the repository's bounded context/index surface or clarify the injected guidance so agents do not repeatedly reopen this small owner."
			if filepath.Base(target) == "AGENTS.md" {
				action = "AGENTS.md is normally injected into the session; clarify the relevant rule or tooling entry point, then avoid rereading the file."
			} else if repositoryManifestTarget(target) {
				action = "Add or use a bounded repository command for dependency/script discovery instead of repeatedly rereading the full manifest."
			}
			findings = append(findings, sessionFinding{
				Category: "instruction-discovery",
				Control:  "repository",
				Title:    "a small current owner is repeatedly reread",
				Evidence: fmt.Sprintf("%s reads and %s search/read loops across %s sessions; current size %s bytes",
					formatCodexCount(int64(metrics.Reads)),
					formatCodexCount(int64(metrics.SearchReadLoops)),
					formatCodexCount(int64(metrics.Sessions)),
					formatCodexCount(info.Size()),
				),
				Action:   action,
				Count:    metrics.Reads,
				Sessions: metrics.Sessions,
				Target:   target,
				score:    320 + metrics.Sessions*15 + metrics.SearchReadLoops*10 + metrics.Reads,
			})
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "code-structure",
			Control:  "repository",
			Title:    "repeated navigation into a current source owner",
			Evidence: fmt.Sprintf("%s reads and %s search/read loops across %s sessions; current size %s bytes",
				formatCodexCount(int64(metrics.Reads)),
				formatCodexCount(int64(metrics.SearchReadLoops)),
				formatCodexCount(int64(metrics.Sessions)),
				formatCodexCount(info.Size()),
			),
			Action:   config.Actions.CodeStructure,
			Count:    metrics.Reads,
			Sessions: metrics.Sessions,
			Target:   target,
			score:    350 + metrics.Sessions*15 + metrics.SearchReadLoops*10 + metrics.Reads,
		})
	}

	if summary.InlineOrchestrationCalls >= 2 || summary.InlineOrchestrationBytes >= 16*1024 || summary.InlineOrchestrationMaxBytes >= 8*1024 {
		title := "long inline code is carrying orchestration inside a tool call"
		if summary.InlineOrchestrationCalls >= 2 {
			title = "repeated inline code is rebuilding a workflow inside tool calls"
		}
		findings = append(findings, sessionFinding{
			Category: "agent-interface",
			Control:  "repository",
			Title:    title,
			Evidence: fmt.Sprintf("%s large inline calls across %s sessions; %s total input bytes; largest call %s bytes; tool sources: %s",
				formatCodexCount(int64(summary.InlineOrchestrationCalls)),
				formatCodexCount(int64(summary.InlineOrchestrationSessions)),
				formatCodexCount(summary.InlineOrchestrationBytes),
				formatCodexCount(summary.InlineOrchestrationMaxBytes),
				inlineToolEvidence(summary.InlineOrchestrationByTool),
			),
			Action:   config.Actions.InlineOrchestration,
			Count:    summary.InlineOrchestrationCalls,
			Sessions: summary.InlineOrchestrationSessions,
			score:    450 + summary.InlineOrchestrationSessions*20 + summary.InlineOrchestrationCalls,
		})
	}

	if summary.Compactions >= 3 || summary.SessionsWithCompactions >= 2 {
		findings = append(findings, sessionFinding{
			Category: "session-loop",
			Control:  "instructions",
			Title:    "context compactions indicate long or looping sessions",
			Evidence: fmt.Sprintf("%s compactions across %s sessions with %.0f%% cached input",
				formatCodexCount(int64(summary.Compactions)),
				formatCodexCount(int64(summary.SessionsWithCompactions)),
				100*ratio(float64(summary.Tokens.CachedInputTokens), float64(summary.Tokens.InputTokens)),
			),
			Action:   config.Actions.SessionLoop,
			Count:    summary.Compactions,
			Sessions: summary.SessionsWithCompactions,
			score:    420 + summary.SessionsWithCompactions*20 + summary.Compactions,
		})
	}

	searchRead := codexMixedSearchReadMetrics(summary.MixedShellShapes)
	if searchRead.Calls >= 10 && searchRead.EstimatedOutputTokens >= 50_000 {
		findings = append(findings, sessionFinding{
			Category: "discovery",
			Control:  "repository",
			Title:    "bundled search/read discovery remains output-heavy",
			Evidence: fmt.Sprintf("%s calls returned ~%s visible output tokens",
				formatCodexCount(int64(searchRead.Calls)),
				formatCodexCount(searchRead.EstimatedOutputTokens),
			),
			Action: config.Actions.SourceContext,
			Count:  searchRead.Calls,
			score:  250 + searchRead.Calls + int(searchRead.EstimatedOutputTokens/10_000),
		})
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].score != findings[j].score {
			return findings[i].score > findings[j].score
		}
		if findings[i].Sessions != findings[j].Sessions {
			return findings[i].Sessions > findings[j].Sessions
		}
		return findings[i].Title < findings[j].Title
	})
	return diversifySessionFindings(findings)
}

func directFeedbackFindingCategory(category string) string {
	switch category {
	case "failure":
		return "recurring-failure"
	case "structure":
		return "code-structure"
	case "instructions":
		return "instruction-discovery"
	case "loop":
		return "session-loop"
	case "discovery":
		return "discovery"
	default:
		return "agent-interface"
	}
}

func directFeedbackAction(feedback agentFeedbackAggregate) string {
	switch feedback.Control {
	case "local":
		return fmt.Sprintf("Improve the locally controlled %s surface, then resolve this feedback with `muninn feedback resolve`.", feedback.Target)
	case "repository":
		return fmt.Sprintf("Improve the %s repository interface or guidance, then resolve this feedback with `muninn feedback resolve`.", feedback.Target)
	case "third-party":
		return "Prefer a bounded local adapter or workaround and track the longer upstream path separately."
	default:
		return "Identify whether this belongs to local tooling, repository structure, or an upstream dependency before choosing the fix path."
	}
}

func inlineToolEvidence(metrics map[string]codexInlineMetrics) string {
	type row struct {
		tool  string
		bytes int64
		calls int
	}
	rows := make([]row, 0, len(metrics))
	for tool, value := range metrics {
		rows = append(rows, row{tool: tool, bytes: value.Bytes, calls: value.Calls})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].bytes != rows[j].bytes {
			return rows[i].bytes > rows[j].bytes
		}
		return rows[i].tool < rows[j].tool
	})
	if len(rows) > 3 {
		rows = rows[:3]
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprintf("%s %s calls/%s bytes",
			row.tool,
			formatCodexCount(int64(row.calls)),
			formatCodexCount(row.bytes),
		))
	}
	if len(parts) == 0 {
		return "(unknown)"
	}
	return strings.Join(parts, ", ")
}

func ownedOperationFailureCounts(reasons map[string]codexOccurrenceMetrics) (actionable int, expected int) {
	for reason, metrics := range reasons {
		switch reason {
		case "test failure", "search no match":
			expected += metrics.Count
		default:
			actionable += metrics.Count
		}
	}
	return actionable, expected
}

func repositoryManifestTarget(target string) bool {
	switch strings.ToLower(filepath.Base(target)) {
	case "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock",
		"go.mod", "go.sum", "cargo.toml", "cargo.lock", "deps.edn",
		"pyproject.toml", "poetry.lock", "requirements.txt":
		return true
	default:
		return false
	}
}

func filterSessionFindings(findings []sessionFinding, focus string) ([]sessionFinding, error) {
	focus = strings.ToLower(strings.TrimSpace(focus))
	if focus == "" {
		return findings, nil
	}
	allowed := map[string]map[string]bool{
		"tooling": {
			"owned-tool":        true,
			"owned-operation":   true,
			"recurring-failure": true,
		},
		"instructions": {
			"instruction-discovery": true,
			"session-loop":          true,
		},
		"interface": {
			"agent-interface": true,
		},
		"structure": {
			"code-structure": true,
		},
		"discovery": {
			"discovery":             true,
			"instruction-discovery": true,
		},
		"failures": {
			"recurring-failure": true,
		},
		"loops": {
			"agent-interface": true,
			"session-loop":    true,
		},
	}
	categories, ok := allowed[focus]
	if !ok {
		return nil, fmt.Errorf("unsupported --focus %q (available: tooling, instructions, interface, structure, discovery, failures, loops)", focus)
	}
	filtered := make([]sessionFinding, 0, len(findings))
	for _, finding := range findings {
		if categories[finding.Category] {
			filtered = append(filtered, finding)
		}
	}
	return filtered, nil
}

func agentInterfaceWorkflow(transition string) string {
	from, to, ok := strings.Cut(transition, " -> ")
	if !ok {
		return ""
	}
	discovery := func(value string) bool {
		return value == "search" || value == "file reads"
	}
	switch {
	case discovery(from) && discovery(to):
		return "source discovery and navigation"
	case from == "browser QA" && to == "browser QA":
		return "browser QA control and recovery"
	case from == "git inspect" && to == "git inspect":
		return "change inspection"
	case (from == "tests" || from == "build, lint, or install") &&
		(to == "tests" || to == "build, lint, or install"):
		return "verification"
	default:
		return ""
	}
}

func diversifySessionFindings(findings []sessionFinding) []sessionFinding {
	limits := map[string]int{
		"agent-interface":       4,
		"code-structure":        6,
		"instruction-discovery": 4,
		"recurring-failure":     4,
		"owned-tool":            4,
		"owned-operation":       6,
	}
	counts := map[string]int{}
	result := make([]sessionFinding, 0, len(findings))
	for _, finding := range findings {
		if limit := limits[finding.Category]; limit > 0 && counts[finding.Category] >= limit {
			continue
		}
		counts[finding.Category]++
		result = append(result, finding)
	}
	return result
}

func printSessionFindings(findings []sessionFinding, limit int) {
	fmt.Println("\nFindings:")
	if len(findings) == 0 {
		fmt.Println("- No current findings met the recurrence and impact thresholds.")
		return
	}
	rows := findings
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	for _, finding := range rows {
		target := ""
		if finding.Target != "" {
			target = " · " + finding.Target
		}
		fmt.Printf("- [%s/%s] %s%s\n", finding.Category, finding.Control, finding.Title, target)
		fmt.Printf("  Evidence: %s.\n", strings.TrimSuffix(finding.Evidence, "."))
		fmt.Printf("  Next: %s\n", finding.Action)
	}
	if len(rows) < len(findings) {
		fmt.Printf("... %d more findings; use --limit 0 or --details.\n", len(findings)-len(rows))
	}
}
