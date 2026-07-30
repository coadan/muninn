package cli

import "fmt"

const genericOwnedOperationFailureReason = "other non-zero exit"

func buildDiagnosticContractFindings(
	report codexSessionInsightsReport,
	ownership ownershipCatalog,
) []sessionFinding {
	var findings []sessionFinding
	for operation, reasons := range report.Summary.OwnedOperationFailureReasons {
		generic := reasons[genericOwnedOperationFailureReason]
		if ownership.operationFailureExpected(operation, genericOwnedOperationFailureReason) {
			continue
		}
		if generic.Count < 3 || generic.Sessions < 2 {
			continue
		}
		actionable := 0
		for reason, metrics := range reasons {
			if !ownership.operationFailureExpected(operation, reason) {
				actionable += metrics.Count
			}
		}
		if actionable == 0 || ratio(float64(generic.Count), float64(actionable)) < 0.5 {
			continue
		}
		genericShare := 100 * ratio(float64(generic.Count), float64(actionable))
		findings = append(findings, sessionFinding{
			Category: "diagnostic-contract",
			Control:  "local",
			Title:    "owned operation lacks an actionable failure contract: " + operation,
			Evidence: fmt.Sprintf(
				"%s of %s actionable failures (%.0f%%) across %s collapsed to the generic %q label",
				formatCodexCount(int64(generic.Count)),
				formatCodexCount(int64(actionable)),
				genericShare,
				formatCodexCountNoun(int64(generic.Sessions), "session"),
				genericOwnedOperationFailureReason,
			),
			Action: fmt.Sprintf(
				"Make %s emit a stable machine-readable failure class and concise next-action metadata; preserve the non-zero exit while removing dependence on prose parsing.",
				operation,
			),
			Count:      generic.Count,
			Sessions:   generic.Sessions,
			Target:     operation,
			LastSeen:   sessionFindingLastSeen(report, "owned-operation-friction", operation),
			Lever:      "tooling",
			Confidence: "high",
			Why:        "A recurring generic exit identifies the failing operation but cannot distinguish one reusable fix from another.",
			score:      820 + generic.Sessions*30 + generic.Count*10,
		})
	}
	return findings
}
