package cli

import (
	"fmt"
	"strings"
)

func buildOutputCostFindings(
	report codexSessionInsightsReport,
	config repositoryConfig,
) []sessionFinding {
	var findings []sessionFinding
	for context, metrics := range report.Summary.OversizedOutputs {
		if metrics.Calls < 2 && metrics.MaxOutputBytes < 2*oversizedOutputMinimumBytes {
			continue
		}
		control := "repository"
		if locallyControlledToolContext(context, config.OwnedTools) {
			control = "local"
		}
		title := "individual tool calls return oversized output: " + context
		if context == "concurrent tool batch" {
			title = "concurrent tool batches exceed the shared output budget"
		}
		evidence := fmt.Sprintf("%s returned %s bytes (~%s visible tokens) across %s; largest call %s bytes",
			formatCodexCountNoun(int64(metrics.Calls), "call"),
			formatCodexCount(metrics.OutputBytes),
			formatCodexCount(estimatedTokens(metrics.OutputBytes)),
			formatCodexCountNoun(int64(metrics.Sessions), "session"),
			formatCodexCount(metrics.MaxOutputBytes),
		)
		if context == "concurrent tool batch" && metrics.NestedCalls > 0 {
			evidence += fmt.Sprintf("; %s nested calls averaged ~%s visible output tokens each; largest batch contained %s calls",
				formatCodexCount(int64(metrics.NestedCalls)),
				formatCodexCount(estimatedTokens(metrics.OutputBytes)/int64(metrics.NestedCalls)),
				formatCodexCount(int64(metrics.MaxNestedCalls)),
			)
		}
		action := oversizedOutputAction(context, control)
		if context == "concurrent tool batch" && metrics.MaxNestedCalls > 0 {
			action = fmt.Sprintf(
				"Keep independent calls concurrent, but keep the combined stage below ~%s visible tokens; for the largest %s-call batch, budget about %s tokens per result if divided evenly, and inspect every partial result.",
				formatCodexCount(concurrentBatchOutputBudgetTokens),
				formatCodexCount(int64(metrics.MaxNestedCalls)),
				formatCodexCount(concurrentBatchOutputBudgetTokens/int64(metrics.MaxNestedCalls)),
			)
		}
		findings = append(findings, sessionFinding{
			Category:   "output-cost",
			Control:    control,
			Title:      title,
			Evidence:   evidence,
			Action:     action,
			Count:      metrics.Calls,
			Sessions:   metrics.Sessions,
			Target:     context,
			LastSeen:   sessionFindingLastSeen(report, "oversized-output", context),
			Confidence: oversizedOutputFindingConfidence(metrics),
			score:      600 + metrics.Calls + int(metrics.OutputBytes/10_000),
		})
	}
	return findings
}

func oversizedOutputFindingConfidence(metrics codexOversizedOutputMetrics) string {
	if metrics.Sessions >= 2 || metrics.MaxOutputBytes >= 4*oversizedOutputMinimumBytes {
		return "medium"
	}
	return "low"
}

func oversizedOutputAction(context, control string) string {
	if context == "concurrent tool batch" {
		return "Keep independent calls concurrent, but lower each nested call's output limit so the combined stage stays below the shared output budget; inspect every partial result."
	}
	if control == "local" {
		return "Lower this locally controlled operation's default output and return a compact summary with explicit focused follow-ups."
	}
	if strings.HasPrefix(context, "nested tool") {
		if strings.Contains(context, "exec_command") {
			return "Set a bounded max_output_tokens on each nested exec_command, then narrow or page only the result that reaches that limit."
		}
		return "Request bounded output from the named nested tool and inspect only the focused follow-up needed for the task."
	}
	if strings.Contains(context, " -> ") {
		return "Keep the workflow bundled, but cap each output-heavy stage and return one compact summary with explicit focused follow-ups."
	}
	switch context {
	case "file reads":
		return "Read the exact owner, symbol, heading, or bounded line window instead of returning the whole file."
	case "search":
		return "Narrow the search scope and cap matches or excerpts before retrying."
	case "git inspect":
		return "Use a diff summary or path-scoped inspection first, then load only the changed hunk that needs attention."
	case "tests":
		return "Keep successful test output compact and route complete failure diagnostics to a log or explicit focused follow-up."
	case "review":
		return "Use the compact review summary first and retrieve details only for the selected finding."
	default:
		return "Narrow the command, lower its output limit, or add a compact repository-owned surface that returns focused follow-ups."
	}
}
