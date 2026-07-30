package cli

import "fmt"

func recordToolingBypasses(
	record *codexSessionRecord,
	event normalizedSessionEvent,
	ownedOperations,
	bypassedOperations []string,
) {
	if event.OperationAttributionAmbiguous || len(bypassedOperations) == 0 {
		return
	}
	used := map[string]bool{}
	for _, operation := range ownedOperations {
		used[operation] = true
	}
	for _, preferred := range bypassedOperations {
		if used[preferred] {
			continue
		}
		record.ToolingBypasses[preferred]++
		touchSessionActivity(record.Activity, "tooling-bypass", preferred, event.OccurredAt)
	}
}

func buildToolingBypassFindings(report codexSessionInsightsReport) []sessionFinding {
	var findings []sessionFinding
	for operation, metrics := range report.Summary.ToolingBypasses {
		if metrics.Count < 5 || metrics.Sessions < 2 {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "tooling-bypass",
			Control:  "local",
			Title:    "owned operation is repeatedly bypassed: " + operation,
			Evidence: fmt.Sprintf(
				"%s definitely attributed configured bypass invocations across %s",
				formatCodexCount(int64(metrics.Count)),
				formatCodexCountNoun(int64(metrics.Sessions), "session"),
			),
			Action: fmt.Sprintf(
				"Inspect why agents bypass %s, then close the missing capability, reliability, or discoverability gap in that owned operation; remove the bypass pattern only if the lower-level path is intentionally supported.",
				operation,
			),
			Count:      metrics.Count,
			Sessions:   metrics.Sessions,
			Target:     operation,
			LastSeen:   sessionFindingLastSeen(report, "tooling-bypass", operation),
			Lever:      "tooling",
			Confidence: "high",
			Why:        "The repository explicitly declares the lower-level invocation as a bypass of this preferred operation.",
			score:      880 + metrics.Sessions*35 + metrics.Count*5,
		})
	}
	return findings
}
