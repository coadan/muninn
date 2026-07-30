package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type workflowTransitionEvidence struct {
	Transition string
	Count      int
	Sessions   int
}

type workflowEvidence struct {
	Count       int
	Sessions    int
	LastSeen    time.Time
	Transitions []workflowTransitionEvidence
}

func buildAgentInterfaceFindings(
	report codexSessionInsightsReport,
	config repositoryConfig,
) []sessionFinding {
	summary := report.Summary
	var findings []sessionFinding

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
		if seen := sessionFindingActivity(report, "transition", transition); seen.After(evidence.LastSeen) {
			evidence.LastSeen = seen
		}
		evidence.Transitions = append(evidence.Transitions, workflowTransitionEvidence{
			Transition: transition,
			Count:      metrics.Count,
			Sessions:   metrics.Sessions,
		})
		workflows[workflow] = evidence
	}
	for workflow, metrics := range workflows {
		if metrics.Sessions < 3 || metrics.Count < max(5, metrics.Sessions*3) {
			continue
		}
		evidence := fmt.Sprintf("%s transitions across at least %s sessions",
			formatCodexCount(int64(metrics.Count)),
			formatCodexCount(int64(metrics.Sessions)),
		)
		if transitions := formatAgentInterfaceTransitions(metrics.Transitions, 3); transitions != "" {
			evidence += "; dominant transitions: " + transitions
		}
		findings = append(findings, sessionFinding{
			Category: "agent-interface",
			Control:  "repository",
			Title:    "repeated cross-call workflow: " + workflow,
			Evidence: evidence,
			Action:   config.Actions.AgentInterface,
			Count:    metrics.Count,
			Sessions: metrics.Sessions,
			LastSeen: formatSessionFindingTime(metrics.LastSeen),
			score:    300 + metrics.Sessions*10 + metrics.Count,
		})
	}
	findings = append(findings, buildOwnedOperationChainFindings(report)...)

	if summary.InlineOrchestrationCalls > 0 {
		title := "long inline code is carrying orchestration inside a tool call"
		if summary.InlineOrchestrationByTool["exec_command"].Calls > 0 {
			title = "very long CLI input is rebuilding an inspection workflow"
		}
		if summary.InlineOrchestrationCalls >= 2 {
			title = "repeated inline code is rebuilding a workflow inside tool calls"
		}
		family, familyMetrics := dominantInlineMetric(summary.InlineOrchestrationByFamily)
		owner, _ := dominantInlineMetric(summary.InlineOrchestrationByOwner)
		task, taskMetrics := dominantInlineTask(report.Tasks)
		control := "repository"
		lever := "unknown"
		confidence := "medium"
		target := family
		action := config.Actions.InlineOrchestration
		if owner != "" && owner != "(unowned)" {
			control = "local"
			lever = "tooling"
			confidence = "high"
			target = owner
			action = "Improve the locally owned " + owner + " surface so this inspection is one bounded operation, then compare oversized-input recurrence."
		} else if family == "inline runtime" || family == "database query CLI" {
			lever = "tooling"
			target = family
			action = "Add a compact, tested repository inspection command for this workflow; if one already exists, route agents to it with one concise pointer."
		}
		dimensions := fmt.Sprintf("; families: %s; ownership: %s",
			inlineToolEvidence(summary.InlineOrchestrationByFamily),
			inlineToolEvidence(summary.InlineOrchestrationByOwner),
		)
		if task != "" {
			dimensions += fmt.Sprintf("; top task cohort %s: %s calls/%s bytes",
				task,
				formatCodexCount(int64(taskMetrics.Calls)),
				formatCodexCount(taskMetrics.Bytes),
			)
		}
		findings = append(findings, sessionFinding{
			Category: "agent-interface",
			Control:  control,
			Title:    title,
			Evidence: fmt.Sprintf("%s large inline calls across %s sessions; %s total input bytes; largest call %s bytes; tool sources: %s%s",
				formatCodexCount(int64(summary.InlineOrchestrationCalls)),
				formatCodexCount(int64(summary.InlineOrchestrationSessions)),
				formatCodexCount(summary.InlineOrchestrationBytes),
				formatCodexCount(summary.InlineOrchestrationMaxBytes),
				inlineToolEvidence(summary.InlineOrchestrationByTool),
				dimensions,
			),
			Action:     action,
			Count:      summary.InlineOrchestrationCalls,
			Sessions:   summary.InlineOrchestrationSessions,
			Target:     target,
			LastSeen:   sessionFindingLastSeen(report, "inline", ""),
			Lever:      lever,
			Confidence: confidence,
			score:      450 + summary.InlineOrchestrationSessions*20 + summary.InlineOrchestrationCalls + familyMetrics.Calls,
		})
	}

	for context, metrics := range summary.ProgressStalls {
		if metrics.Calls < 2 ||
			metrics.Seconds < 40 ||
			!locallyControlledToolContext(context, config.OwnedTools) {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "session-loop",
			Control:  "local",
			Title:    "progress stalls while waiting on: " + context,
			Evidence: fmt.Sprintf("%s low-output waits consumed %s across %s; waits for tests, builds, local reviews, and remote GitHub review were classified separately",
				formatCodexCount(int64(metrics.Calls)),
				formatDurationSeconds(metrics.Seconds),
				formatCodexCountNoun(int64(metrics.Sessions), "session"),
			),
			Action:     "Remove redundant polling, emit useful bounded progress, or make this operation asynchronous/resumable when the wait is not intrinsically required.",
			Count:      metrics.Calls,
			Sessions:   metrics.Sessions,
			Target:     context,
			LastSeen:   sessionFindingLastSeen(report, "progress-stall", context),
			Confidence: recurringPatternConfidence(metrics.Sessions),
			score:      440 + metrics.Sessions*20 + metrics.Calls*5 + int(metrics.Seconds/10),
		})
	}

	for context, metrics := range summary.RapidPolls {
		if metrics.Sessions < 1 || metrics.Calls < max(5, metrics.Sessions*3) {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "agent-interface",
			Control:  "repository",
			Title:    "rapid continuation polling: " + context,
			Evidence: fmt.Sprintf("%s continuation polls returned within %s across %s sessions",
				formatCodexCount(int64(metrics.Calls)),
				formatDurationSeconds(metrics.Seconds),
				formatCodexCount(int64(metrics.Sessions)),
			),
			Action:   "Resume yielded commands with a 30-second wait or the command's documented heartbeat interval; keep necessary work running without a model roundtrip for each short poll.",
			Count:    metrics.Calls,
			Sessions: metrics.Sessions,
			Target:   context,
			LastSeen: sessionFindingLastSeen(report, "rapid-poll", context),
			score:    445 + metrics.Sessions*20 + metrics.Calls*5,
		})
	}

	for context, metrics := range summary.AbandonedContinuations {
		local := locallyControlledToolContext(context, config.OwnedTools)
		if metrics.Count < 2 ||
			metrics.Sessions < 2 ||
			(!local && !expectedContinuationContext(context)) {
			continue
		}
		control := "repository"
		action := config.Actions.YieldedOperation
		if local {
			control = "local"
			action = "Resume yielded operations to a terminal result, explicitly terminate them, or make the owning command self-finalizing with bounded progress."
		}
		findings = append(findings, sessionFinding{
			Category: "agent-interface",
			Control:  control,
			Title:    "yielded operations never reached a terminal result: " + context,
			Evidence: fmt.Sprintf("%s yielded operations remained pending after session completion or 30 minutes of inactivity across %s sessions",
				formatCodexCount(int64(metrics.Count)),
				formatCodexCount(int64(metrics.Sessions)),
			),
			Action:   action,
			Count:    metrics.Count,
			Sessions: metrics.Sessions,
			Target:   context,
			LastSeen: sessionFindingLastSeen(report, "abandoned-continuation", context),
			score:    500 + metrics.Sessions*25 + metrics.Count*10,
		})
	}

	return findings
}

func expectedContinuationContext(context string) bool {
	switch context {
	case "tests", "build, lint, or install", "review":
		return true
	default:
		return false
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

func dominantInlineMetric(metrics map[string]codexInlineMetrics) (string, codexInlineMetrics) {
	name := ""
	value := codexInlineMetrics{}
	for candidate, metrics := range metrics {
		if metrics.Bytes > value.Bytes ||
			(metrics.Bytes == value.Bytes && metrics.Calls > value.Calls) ||
			(metrics.Bytes == value.Bytes && metrics.Calls == value.Calls && candidate < name) {
			name = candidate
			value = metrics
		}
	}
	return name, value
}

func dominantInlineTask(tasks []codexTaskInsights) (string, codexInlineMetrics) {
	name := ""
	value := codexInlineMetrics{}
	for _, task := range tasks {
		metrics := codexInlineMetrics{
			Calls:    task.InlineOrchestrationCalls,
			Sessions: task.InlineOrchestrationSessions,
			Bytes:    task.InlineOrchestrationBytes,
			MaxBytes: task.InlineOrchestrationMaxBytes,
		}
		if metrics.Bytes > value.Bytes ||
			(metrics.Bytes == value.Bytes && metrics.Calls > value.Calls) ||
			(metrics.Bytes == value.Bytes && metrics.Calls == value.Calls && task.Task < name) {
			name = task.Task
			value = metrics
		}
	}
	if value.Calls == 0 {
		return "", codexInlineMetrics{}
	}
	return name, value
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
	case gitInspectionTransitionContext(from) && gitInspectionTransitionContext(to):
		return "change inspection"
	case (from == "tests" || from == "build, lint, or install") &&
		(to == "tests" || to == "build, lint, or install"):
		return "verification"
	default:
		return ""
	}
}

func gitInspectionTransitionContext(value string) bool {
	return value == "git inspect" || strings.HasPrefix(value, "git inspect/")
}

func formatAgentInterfaceTransitions(transitions []workflowTransitionEvidence, limit int) string {
	rows := append([]workflowTransitionEvidence(nil), transitions...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		if rows[i].Sessions != rows[j].Sessions {
			return rows[i].Sessions > rows[j].Sessions
		}
		return rows[i].Transition < rows[j].Transition
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		sessionLabel := "sessions"
		if row.Sessions == 1 {
			sessionLabel = "session"
		}
		parts = append(parts, fmt.Sprintf(
			"%s: %s transitions/%s %s",
			row.Transition,
			formatCodexCount(int64(row.Count)),
			formatCodexCount(int64(row.Sessions)),
			sessionLabel,
		))
	}
	return strings.Join(parts, ", ")
}
