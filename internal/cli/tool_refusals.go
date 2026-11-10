package cli

import "fmt"

// A tool refusal is a tool call that never ran because the user declined it, a
// permission policy denied it, or a pre-tool hook blocked it. It is workflow
// friction, not a tool failure: the repair belongs in instructions, permission
// configuration, or the hook's contract rather than in the tool. Providers map
// their own result text onto these fixed labels; the index, analysis, and
// reports see only the label and the normalized tool name.
const (
	toolRefusalDeclinedByUser     = "declined-by-user"
	toolRefusalDeniedByPermission = "denied-by-permission"
	toolRefusalBlockedByHook      = "blocked-by-hook"
)

// knownToolRefusal keeps anything but a fixed label out of storage and reports.
func knownToolRefusal(label string) string {
	switch label {
	case toolRefusalDeclinedByUser, toolRefusalDeniedByPermission, toolRefusalBlockedByHook:
		return label
	}
	return ""
}

func recordToolRefusal(record *codexSessionRecord, event normalizedSessionEvent) {
	label := knownToolRefusal(event.Refusal)
	if label == "" {
		return
	}
	tool := event.ToolName
	if tool == "" {
		tool = "(unknown)"
	}
	addCodexFailureContext(record.ToolRefusals, label, tool)
	touchSessionActivity(record.Activity, "tool-refusal", label+"/"+tool, event.OccurredAt)
}

// buildToolRefusalFindings reports a refusal of the same tool that recurs
// across sessions: at least three refusals in at least two sessions.
func buildToolRefusalFindings(report codexSessionInsightsReport) []sessionFinding {
	var findings []sessionFinding
	for label, tools := range report.Summary.ToolRefusals {
		for tool, metrics := range tools {
			if metrics.Count < 3 || metrics.Sessions < 2 {
				continue
			}
			target := label + "/" + tool
			finding := sessionFinding{
				Category: "tool-refusal",
				Control:  "repository",
				Title:    label + " recurs for tool: " + tool,
				Evidence: fmt.Sprintf("%s refused calls across %s; refused calls never ran and are not counted as tool failures",
					formatCodexCount(int64(metrics.Count)),
					formatCodexCountNoun(int64(metrics.Sessions), "session"),
				),
				Count:      metrics.Count,
				Sessions:   metrics.Sessions,
				Target:     target,
				LastSeen:   sessionFindingLastSeen(report, "tool-refusal", target),
				Lever:      "instructions/docs",
				Confidence: "medium",
				score:      450 + metrics.Sessions*30 + metrics.Count*5,
			}
			switch label {
			case toolRefusalBlockedByHook:
				finding.Lever = "tooling"
				finding.Action = "State the rule this hook enforces where the agent decides (repository instructions or the owning skill), and make the hook's message name the allowed alternative, so the agent stops attempting the blocked call."
				finding.Why = "A pre-tool hook refused the same tool repeatedly, so the agent keeps proposing calls the workflow forbids."
			case toolRefusalDeniedByPermission:
				finding.Action = "Name the denied action and its allowed alternative in repository instructions, or change the permission rules if the action is intended, so the agent stops spending turns on calls that cannot run."
				finding.Why = "A permission rule or policy denied the same tool repeatedly, so the agent keeps proposing calls that cannot run."
			default:
				finding.Confidence = "low"
				finding.Action = "Review whether instructions should route this kind of call to an approved alternative or ask before it; repeated declines mean the agent proposes calls the user does not want."
				finding.Why = "The user declined the same tool repeatedly; a decline can be a one-off judgement, so this stays low confidence."
			}
			findings = append(findings, finding)
		}
	}
	return findings
}
