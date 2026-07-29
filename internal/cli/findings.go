package cli

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type sessionFinding struct {
	Category   string   `json:"category"`
	Control    string   `json:"control"`
	Signal     string   `json:"signal"`
	Title      string   `json:"title"`
	Evidence   string   `json:"evidence"`
	Action     string   `json:"action"`
	Count      int      `json:"count,omitempty"`
	Sessions   int      `json:"sessions,omitempty"`
	Repository string   `json:"repository,omitempty"`
	Target     string   `json:"target,omitempty"`
	LastSeen   string   `json:"lastSeen,omitempty"`
	Lever      string   `json:"lever"`
	Confidence string   `json:"confidence"`
	Why        string   `json:"why,omitempty"`
	Supporting []string `json:"supportingSignals,omitempty"`
	score      int
}

func buildSessionFindings(report codexSessionInsightsReport, config repositoryConfig) []sessionFinding {
	summary := report.Summary
	ownership := newOwnershipCatalog(config.OwnedTools)
	var findings []sessionFinding

	const largeRootInstructionBytes = 16 * 1024
	if report.Instructions.RootBytes >= largeRootInstructionBytes {
		findings = append(findings, sessionFinding{
			Category: "instruction-footprint",
			Control:  "repository",
			Title:    "large always-on repository instruction footprint",
			Evidence: fmt.Sprintf("%s root instruction files total %s bytes (~%s tokens per session before provider/system context)",
				formatCodexCount(int64(report.Instructions.RootFiles)),
				formatCodexCount(report.Instructions.RootBytes),
				formatCodexCount(report.Instructions.RootEstimatedTokens),
			),
			Action:     "Keep behavioral and safety rules injected, but move stable command syntax and long reference examples behind repository help or bounded documentation surfaces.",
			Count:      report.Instructions.RootFiles,
			LastSeen:   report.GeneratedAt,
			Lever:      "instructions/docs",
			Confidence: "high",
			score:      620 + int(report.Instructions.RootEstimatedTokens/100),
		})
	}

	ownedConfigByID := map[string]ownedToolConfig{}
	for _, owned := range config.OwnedTools {
		ownedConfigByID[owned.ID] = owned
	}
	for target, metrics := range summary.OwnedFlags {
		separator := strings.LastIndex(target, "/")
		if separator <= 0 || separator == len(target)-1 {
			continue
		}
		scope := target[:separator]
		flag := target[separator+1:]
		scopeDisplay := strings.ReplaceAll(scope, "/", " ")
		eligibleCalls := summary.OwnedFlagEligibleCalls[scope]
		if metrics.Count < 5 || metrics.Sessions < 2 || eligibleCalls.Count < 5 {
			continue
		}
		frequency := ratio(float64(metrics.Count), float64(eligibleCalls.Count))
		if frequency < 0.8 {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "default-candidate",
			Control:  "local",
			Title:    "frequently repeated CLI flag may belong in the default: " + scopeDisplay + " --" + flag,
			Evidence: fmt.Sprintf("%s of %s definitely attributed %s calls (%.0f%%) supplied --%s across %s sessions",
				formatCodexCount(int64(metrics.Count)),
				formatCodexCount(int64(eligibleCalls.Count)),
				scopeDisplay,
				100*frequency,
				flag,
				formatCodexCount(int64(metrics.Sessions)),
			),
			Action:     "Review whether this option should become the default or be inferred from repository context; if the human default must remain different, provide a compact agent-facing mode for the repeated workflow.",
			Count:      metrics.Count,
			Sessions:   metrics.Sessions,
			Target:     target,
			LastSeen:   sessionFindingLastSeen(report, "owned-flag", target),
			Lever:      "tooling",
			Confidence: "medium",
			Why:        "Repeatedly spelling the same option adds command complexity and leaves room for inconsistent invocations.",
			score:      700 + metrics.Sessions*20 + metrics.Count,
		})
	}
	for operation, metrics := range summary.OwnedOperations {
		definiteCalls := max(metrics.Calls-metrics.AmbiguousCalls, 0)
		definiteSuccessfulCalls := max(definiteCalls-metrics.FailedCalls, 0)
		repeated := verificationOwnedOperation(operation) &&
			metrics.Sessions > 0 &&
			definiteSuccessfulCalls >= 40 &&
			definiteSuccessfulCalls >= metrics.Sessions*15
		reasons := summary.OwnedOperationFailureReasons[operation]
		actionableFailures, expectedFailures := ownedOperationFailureCounts(ownership, operation, reasons)
		failureSessions, truncationSessions := ownedOperationFrictionSessions(report, ownership, operation)
		recurringFailures := actionableFailures >= 2 && failureSessions >= 2
		recurringTruncations := metrics.TruncatedCalls >= 3 && truncationSessions >= 2
		concentratedFailures := actionableFailures >= 5 && failureSessions == 1
		concentratedTruncations := metrics.TruncatedCalls >= 5 && truncationSessions == 1
		outputThreshold := max(int64(50_000), summary.ToolOutputTokens/50)
		outputHeavy := metrics.Sessions >= 2 &&
			metrics.EstimatedOutputTokens >= outputThreshold &&
			ratio(float64(metrics.EstimatedOutputTokens), float64(metrics.Calls)) >= 500
		if !recurringFailures && !recurringTruncations &&
			!concentratedFailures && !concentratedTruncations &&
			!outputHeavy && !repeated {
			continue
		}
		toolID, _, _ := strings.Cut(operation, "/")
		owned := ownedConfigByID[toolID]
		action := strings.TrimSpace(owned.Recommendation)
		if action == "" {
			action = "Improve this locally controlled operation or its defaults before documenting another agent workaround."
		}
		title := "high-cost locally controlled operation: " + operation
		if repeated {
			title = "locally controlled operation is repeated excessively: " + operation
			action = "Batch related edits, then run this operation once at the verification boundary; use a faster focused mode only to narrow a reported failure."
		}
		if recurringFailures || recurringTruncations {
			title = "locally controlled operation has recurring friction: " + operation
		} else if concentratedFailures || concentratedTruncations {
			title = "locally controlled operation has concentrated single-session friction: " + operation
		}
		evidence := fmt.Sprintf("%s calls across %s sessions, %s bundled calls, %s actionable failures across %s failure sessions, %s expected/product failures, %s ambiguous bundled failures, %s truncations across %s truncation sessions, ~%s attributed output tokens, ~%s ambiguous bundled output tokens",
			formatCodexCount(int64(metrics.Calls)),
			formatCodexCount(int64(metrics.Sessions)),
			formatCodexCount(int64(metrics.AmbiguousCalls)),
			formatCodexCount(int64(actionableFailures)),
			formatCodexCount(int64(failureSessions)),
			formatCodexCount(int64(expectedFailures)),
			formatCodexCount(int64(metrics.AmbiguousFailedCalls)),
			formatCodexCount(int64(metrics.TruncatedCalls)),
			formatCodexCount(int64(truncationSessions)),
			formatCodexCount(metrics.EstimatedOutputTokens),
			formatCodexCount(metrics.EstimatedAmbiguousOutputTokens),
		)
		if repeated {
			evidence += fmt.Sprintf(
				"; %.1f definitely attributed successful calls per session",
				ratio(float64(definiteSuccessfulCalls), float64(metrics.Sessions)),
			)
		}
		if formatted := formatOwnedOperationActionableReasons(ownership, operation, reasons); formatted != "" {
			evidence += "; actionable reasons: " + formatted
		}
		evidence += recentOwnedOperationEvidence(report, ownership, operation)
		confidence := "high"
		if !repeated && !recurringFailures && !recurringTruncations &&
			(outputHeavy || concentratedFailures || concentratedTruncations) {
			confidence = "medium"
		}
		findings = append(findings, sessionFinding{
			Category: "owned-operation",
			Control:  "local",
			Title:    title,
			Evidence: evidence,
			Action:   action,
			Count:    metrics.Calls,
			Sessions: metrics.Sessions,
			Target:   operation,
			LastSeen: ownedOperationFindingLastSeen(
				report,
				operation,
				recurringFailures || recurringTruncations ||
					concentratedFailures || concentratedTruncations,
			),
			Lever:      "tooling",
			Confidence: confidence,
			score:      650 + metrics.Sessions*20 + actionableFailures*30 + metrics.TruncatedCalls*10 + min(metrics.Calls, 200) + int(metrics.EstimatedOutputTokens/5_000),
		})
	}

	verificationName, verification := dominantVerificationRepairLoop(
		summary.DeliveryRework.VerificationChecks,
	)
	if verificationName != "" {
		findings = append(findings, sessionFinding{
			Category: "verification-loop",
			Control:  "repository",
			Title:    "verification required an expensive repair loop: " + verificationName,
			Evidence: fmt.Sprintf(
				"%s failed runs before %s fail-fix-pass deliveries; the check covered %s deliveries",
				formatCodexCount(int64(verification.FailedRuns)),
				formatCodexCount(int64(verification.FailFixPassDeliveries)),
				formatCodexCount(int64(verification.Deliveries)),
			),
			Action:     "Inspect the bounded failure sequence for this check. If failures repeat one boundary, improve its diagnostic or test helper; if they expose different invariants after edits, strengthen the earliest source or focused-test boundary that should have caught them.",
			Count:      verification.FailedRuns,
			Sessions:   summary.DeliveryRework.Sessions,
			Target:     verificationName,
			LastSeen:   report.GeneratedAt,
			Lever:      "unknown",
			Confidence: "medium",
			score:      610 + verification.FailedRuns*20,
		})
	}

	findings = append(findings, buildCausalFindings(report, config)...)
	findings = append(findings, buildOutputCostFindings(report, config)...)

	for _, owned := range config.OwnedTools {
		metrics := summary.OwnedTooling[owned.ID]
		definiteCalls := max(metrics.Calls-metrics.AmbiguousCalls, 0)
		failedCalls := max(metrics.FailedCalls-metrics.AmbiguousFailedCalls, 0)
		truncatedCalls := max(metrics.TruncatedCalls-metrics.AmbiguousTruncatedCalls, 0)
		outputBytes := max(metrics.OutputBytes-metrics.AmbiguousOutputBytes, 0)
		outputTokens := estimatedTokens(outputBytes)
		failureSessions, truncationSessions := ownedToolFrictionSessions(report, owned.ID)
		recurringFailures := failedCalls >= 2 && failureSessions >= 2
		recurringTruncations := truncatedCalls >= 3 && truncationSessions >= 2
		concentratedFailures := failedCalls >= 5 && failureSessions == 1
		concentratedTruncations := truncatedCalls >= 5 && truncationSessions == 1
		outputThreshold := max(int64(50_000), summary.ToolOutputTokens/50)
		outputHeavy := metrics.Sessions >= 2 &&
			outputTokens >= outputThreshold &&
			ratio(float64(outputTokens), float64(definiteCalls)) >= 500
		if !recurringFailures && !recurringTruncations &&
			!concentratedFailures && !concentratedTruncations &&
			!outputHeavy {
			continue
		}
		action := strings.TrimSpace(owned.Recommendation)
		if action == "" {
			action = "Improve the locally controlled CLI or its defaults before working around the behavior in agent instructions."
		}
		title := "locally controlled tooling has recurring friction: " + owned.ID
		confidence := "high"
		if !recurringFailures && !recurringTruncations {
			confidence = "medium"
			if concentratedFailures || concentratedTruncations {
				title = "locally controlled tooling has concentrated single-session friction: " + owned.ID
			}
		}
		findings = append(findings, sessionFinding{
			Category: "owned-tool",
			Control:  "local",
			Title:    title,
			Evidence: fmt.Sprintf("%s calls across %s sessions, %s bundled calls, %s attributable failures across %s failure sessions, %s attributable truncations across %s truncation sessions, ~%s attributable output tokens, ~%s ambiguous bundled output tokens",
				formatCodexCount(int64(metrics.Calls)),
				formatCodexCount(int64(metrics.Sessions)),
				formatCodexCount(int64(metrics.AmbiguousCalls)),
				formatCodexCount(int64(failedCalls)),
				formatCodexCount(int64(failureSessions)),
				formatCodexCount(int64(truncatedCalls)),
				formatCodexCount(int64(truncationSessions)),
				formatCodexCount(outputTokens),
				formatCodexCount(estimatedTokens(metrics.AmbiguousOutputBytes)),
			),
			Action:     action,
			Count:      metrics.Calls,
			Target:     owned.ID,
			LastSeen:   sessionFindingLastSeen(report, "owned-tool", owned.ID),
			Confidence: confidence,
			score:      500 + failedCalls*20 + truncatedCalls*5 + int(outputTokens/10_000),
		})
	}

	for reason, contexts := range summary.FailureContexts {
		for context, metrics := range contexts {
			if metrics.Sessions < 2 || metrics.Count < 2 {
				continue
			}
			locallyControlled := locallyControlledToolContext(context, config.OwnedTools)
			actionableTestFailure := reason == "test failure" &&
				strings.Contains(context, "tests")
			if !locallyControlled && !actionableTestFailure {
				continue
			}
			control := "repository"
			action := config.Actions.RecurringFailure
			if locallyControlled {
				control = "local"
				action = "Inspect this operation with `muninn failures " + context + "`, fix the dominant owned failure boundary, and keep one focused regression check."
			}
			findings = append(findings, sessionFinding{
				Category: "recurring-failure",
				Control:  control,
				Title:    reason + " recurs in " + context,
				Evidence: fmt.Sprintf("%s calls in %s sessions",
					formatCodexCount(int64(metrics.Count)),
					formatCodexCount(int64(metrics.Sessions)),
				),
				Action:   action,
				Count:    metrics.Count,
				Sessions: metrics.Sessions,
				Target:   context,
				LastSeen: sessionFindingLastSeen(report, "failure", reason+"\x00"+context),
				score:    400 + metrics.Sessions*30 + metrics.Count,
			})
		}
	}

	for _, failure := range report.Diagnostics.Failures {
		if failure.Occurrences < 2 || failure.Sessions < 2 {
			continue
		}
		action := "Fix the repeated failure in its owning source-code boundary and keep the focused regression evidence."
		if failure.Lever == "tooling" {
			action = "Reproduce the startup boundary once, then fix the owned fixture or browser tooling instead of repeating session workarounds."
		} else if failure.Lever == "tests/instructions" {
			action = "Repair test selection or concise workflow guidance so the intended behavior runs and produces evidence."
		}
		targetEvidence := ""
		if len(failure.TargetLabels) > 0 {
			targetEvidence = "; target " + formatDiagnosticTargetLabels(failure.TargetLabels)
		}
		findings = append(findings, sessionFinding{
			Category: "diagnostic-failure",
			Control:  "repository",
			Title:    failure.Classification + " failure fingerprint recurs in " + failure.Source,
			Evidence: fmt.Sprintf(
				"%s occurrences across %s sessions; phase %s; source %s; diagnostic %s%s; post-failure cost %s tool calls, %s fresh tokens, %s",
				formatCodexCount(int64(failure.Occurrences)),
				formatCodexCount(int64(failure.Sessions)),
				failure.FixturePhase,
				failure.FailureSource,
				failure.DiagnosticStatus,
				targetEvidence,
				formatCodexCount(int64(failure.PostFailureCalls)),
				formatCodexCount(diagnosticFreshTokens(failure.PostFailureTokens)),
				formatDurationSeconds(failure.PostFailureSecs),
			),
			Action:     action,
			Count:      failure.Occurrences,
			Sessions:   failure.Sessions,
			Target:     failure.Fingerprint,
			LastSeen:   sessionFindingLastSeen(report, "diagnostic-failure", failure.Fingerprint),
			Lever:      failure.Lever,
			Confidence: "high",
			score:      700 + failure.Occurrences*30 + int(diagnosticFreshTokens(failure.PostFailureTokens)/1_000),
		})
	}

	findings = append(findings, buildAgentInterfaceFindings(report, config)...)

	findings = append(findings, ownerRediscoveryFindings(report, config)...)

	compactionBurst := summary.SessionsWithCompactions >= 2 &&
		summary.Compactions >= 2*summary.SessionsWithCompactions
	compactionWidespread := summary.SessionsWithCompactions >= 5 &&
		summary.SessionsWithCompactions*5 >= summary.Sessions
	singleSessionCompactionBurst := summary.SessionsWithCompactions == 1 &&
		summary.Compactions >= 5
	if compactionBurst || compactionWidespread || singleSessionCompactionBurst {
		compactionTokens := compactionCohortTokens(report)
		findings = append(findings, sessionFinding{
			Category: "session-loop",
			Control:  "instructions",
			Title:    "context compactions indicate long or looping sessions",
			Evidence: fmt.Sprintf("%s compactions across %s sessions; affected sessions used %s fresh tokens per session with %.0f%% cached input",
				formatCodexCount(int64(summary.Compactions)),
				formatCodexCount(int64(summary.SessionsWithCompactions)),
				formatCodexCount(perSessionTokens(
					compactionTokens.UncachedInputTokens+compactionTokens.OutputTokens,
					summary.SessionsWithCompactions,
				)),
				100*ratio(float64(compactionTokens.CachedInputTokens), float64(compactionTokens.InputTokens)),
			),
			Action:   config.Actions.SessionLoop,
			Count:    summary.Compactions,
			Sessions: summary.SessionsWithCompactions,
			LastSeen: sessionFindingLastSeen(report, "compaction", ""),
			Confidence: recurringPatternConfidence(
				summary.SessionsWithCompactions,
			),
			score: 420 + summary.SessionsWithCompactions*20 + summary.Compactions,
		})
	}

	outcomes := report.Outcomes
	if outcomes.ToolUsingCompleted >= 20 &&
		(outcomes.TopDecileFreshTokenShare >= 0.35 ||
			outcomes.FreshTokens.Max >= 5*outcomes.FreshTokens.P90) {
		driverKind, driver := dominantTaskCostTailDriver(outcomes.TailDrivers)
		driverEvidence := ""
		phaseEvidence := ""
		target := ""
		action := "Compare high-tail episodes with ordinary completed tasks by operation, rework, compaction, and target cohort before changing tooling, guidance, or source."
		if driver.Name != "" {
			driverEvidence = fmt.Sprintf("; strongest observed %s association %s appeared in %s/%s tail episodes versus %s/%s ordinary episodes (+%.0f percentage points, %.1fx prevalence; %s calls in tail episodes)",
				driverKind,
				driver.Name,
				formatCodexCount(int64(driver.TailEpisodes)),
				formatCodexCount(int64(outcomes.TailDrivers.TailEpisodes)),
				formatCodexCount(int64(driver.OrdinaryEpisodes)),
				formatCodexCount(int64(outcomes.TailDrivers.OrdinaryEpisodes)),
				100*driver.PrevalenceDelta,
				driver.PrevalenceLift,
				formatCodexCount(int64(driver.TailCalls)),
			)
			target = driver.Name
			switch driverKind {
			case "cohort":
				action = "Compare the calls and output inside this repository cohort between high-tail and ordinary completed tasks, then reduce its dominant navigation, validation, or source-boundary cost."
			case "operation":
				if driver.TailCalls <= driver.TailEpisodes {
					action = "Treat this one-call operation as a task-class marker, then compare high-tail and ordinary tasks that both use it before attributing overhead to the operation."
				} else {
					action = "Inspect why this owned operation is repeatedly used in high-tail tasks, then reduce redundant calls, output, or prerequisite discovery at that operation boundary."
				}
			case "family":
				action = "Inspect the repeated command shapes and output in this family inside high-tail tasks, then replace the dominant loop with a bounded repository interface."
			}
		}
		if phase, ok := dominantTaskPhaseTailAssociation(outcomes.TailPhases); ok &&
			phase.ShareDelta >= 0.05 {
			phaseEvidence = fmt.Sprintf(
				"; %s accounted for %.0f%% of high-tail fresh tokens versus %.0f%% in ordinary completed tasks (+%.0f percentage points)",
				phase.Phase,
				100*phase.TailShare,
				100*phase.OrdinaryShare,
				100*phase.ShareDelta,
			)
			switch phase.Phase {
			case "discovery":
				action = "Compare discovery calls, output, and repeated targets in high-tail versus ordinary tasks, then improve the bounded context or ownership surface."
			case "editing":
				action = "Compare edited cohorts in high-tail versus ordinary tasks, then simplify the source boundary responsible for repeated implementation work."
			case "verification":
				action = "Compare checks, failures, and output in high-tail versus ordinary tasks, then reduce redundant validation or strengthen the narrow check boundary."
			case "delivery":
				action = "Compare delivery operations and waits in high-tail versus ordinary tasks, then remove repeated handoff or publication work."
			case "rework":
				action = "Compare review-to-edit cycles and exact targets in high-tail versus ordinary tasks, then strengthen the pre-delivery source or verification boundary."
			}
		}
		findings = append(findings, sessionFinding{
			Category: "task-cost",
			Control:  "repository",
			Title:    "completed tool-task cost is concentrated in a high tail",
			Evidence: fmt.Sprintf("%s fully observed tool tasks; fresh-token p50 %s, p90 %s, max %s; the highest-cost 10%% account for %.0f%% of fresh tokens%s%s",
				formatCodexCount(int64(outcomes.ToolUsingCompleted)),
				formatCodexCount(outcomes.FreshTokens.P50),
				formatCodexCount(outcomes.FreshTokens.P90),
				formatCodexCount(outcomes.FreshTokens.Max),
				100*outcomes.TopDecileFreshTokenShare,
				driverEvidence,
				phaseEvidence,
			),
			Action:     action,
			Count:      outcomes.ToolUsingCompleted,
			Sessions:   summary.Sessions,
			Target:     target,
			LastSeen:   sessionFindingLastSeen(report, "completion", ""),
			Lever:      "unknown",
			Confidence: "low",
			score:      360 + int(100*outcomes.TopDecileFreshTokenShare),
		})
	}

	inputTasks := append([]codexTaskInsights(nil), report.Tasks...)
	sort.Slice(inputTasks, func(i, j int) bool {
		if inputTasks[i].Tokens.UncachedInputTokens != inputTasks[j].Tokens.UncachedInputTokens {
			return inputTasks[i].Tokens.UncachedInputTokens > inputTasks[j].Tokens.UncachedInputTokens
		}
		return inputTasks[i].Task < inputTasks[j].Task
	})
	if len(inputTasks) > 3 {
		inputTasks = inputTasks[:3]
	}
	for _, task := range inputTasks {
		if task.Task == "(root)" || strings.TrimSpace(task.Task) == "" {
			continue
		}
		uncachedPerSession := perSessionTokens(task.Tokens.UncachedInputTokens, task.Sessions)
		if task.Sessions < 2 || task.Tokens.UncachedInputTokens < 500_000 || uncachedPerSession < 50_000 {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "session-loop",
			Control:  "repository",
			Title:    "high input-token cost in task: " + task.Task,
			Evidence: fmt.Sprintf("%s total input tokens (%s uncached, %s cached), %s uncached input and %s fresh tokens per session across %s sessions; %s compactions",
				formatCodexCount(task.Tokens.InputTokens),
				formatCodexCount(task.Tokens.UncachedInputTokens),
				formatCodexCount(task.Tokens.CachedInputTokens),
				formatCodexCount(uncachedPerSession),
				formatCodexCount(perSessionTokens(task.FreshTokens, task.Sessions)),
				formatCodexCount(int64(task.Sessions)),
				formatCodexCount(int64(task.Compactions)),
			),
			Action:   "Reduce injected guidance, repeated source reads, and rediscovery for this task family; compare the same per-session input metrics after the tooling or structure change.",
			Count:    task.Sessions,
			Sessions: task.Sessions,
			Target:   task.Task,
			LastSeen: sessionFindingLastSeen(report, "task", task.Task),
			score:    430 + task.Sessions*10 + task.Compactions*5 + int(uncachedPerSession/25_000),
		})
	}

	delivery := summary.DeliveryRework
	if delivery.Deliveries >= 2 &&
		(delivery.DeliveriesWithRework >= 2 || delivery.ReviewToEditCycles >= 2) {
		lever := "unknown"
		confidence := "medium"
		action := "Classify a bounded sample of review feedback as tooling, instructions/docs, or source code, then strengthen the owning pre-delivery boundary."
		dominantLever, dominantLeverCount, leverTotal := dominantMetricDimension(delivery.ReworkLevers)
		if dominantLeverCount >= 2 && dominantLeverCount*2 > leverTotal {
			lever = dominantLever
			switch lever {
			case "tooling":
				action = "Strengthen the pre-delivery review/validation tooling for this cohort, then compare its post-delivery rework rate."
			case "instructions/docs":
				action = "Move recurring review requirements into the owning concise guidance or contract before delivery, then compare rework."
			case "source code":
				action = "Strengthen the source boundary and focused tests that should catch this review class before delivery."
			}
		} else if delivery.DeliveriesWithPreReview == 0 {
			lever = "tooling"
			confidence = "high"
			action = "Add or strengthen a bounded pre-delivery review gate before push, then compare post-delivery rework on the same repository cohort."
		}
		dominantScope, _, _ := dominantMetricDimension(delivery.ReworkScopes)
		target := ""
		if dominantScope != "" && dominantScope != "(root)" && dominantScope != "(unknown)" {
			target = dominantScope
		}
		cohortName, cohort := dominantDeliveryReworkCohort(delivery.Cohorts)
		cohortEvidence := ""
		if cohortName != "" {
			target = cohortName
			cohortEvidence = fmt.Sprintf("; top cohort %s: %s observed deliveries, %s with rework, %s review-to-edit cycles, tests after the latest edit on %s deliveries, and review after the latest edit on %s deliveries",
				cohortName,
				formatCodexCount(int64(cohort.Deliveries)),
				formatCodexCount(int64(cohort.DeliveriesWithRework)),
				formatCodexCount(int64(cohort.ReviewToEditCycles)),
				formatCodexCount(int64(cohort.DeliveriesWithPreTests)),
				formatCodexCount(int64(cohort.DeliveriesWithPreReview)),
			)
			if comparison := deliveryTestComparison(cohort); comparison != "" {
				cohortEvidence += "; " + comparison
			}
			checkName, check := dominantVerificationCheck(cohort.VerificationChecks)
			if checkName != "" {
				cohortEvidence += "; check " + checkName + ": " +
					verificationCheckComparison(cohort.Deliveries, cohort.DeliveriesWithRework, check)
				if check.Deliveries < cohort.Deliveries &&
					check.DeliveriesWithRework*cohort.Deliveries <
						cohort.DeliveriesWithRework*check.Deliveries {
					action = "Run " + checkName + " after the latest edit for this cohort before delivery, then compare its rework rate."
				} else if check.DeliveriesWithRework >= 2 {
					action = "Deliveries verified by " + checkName + " still required repeated rework; strengthen that check or the source boundary it should protect."
				}
			}
			if lever == "source code" && cohort.Deliveries >= 2 {
				if checkName == "" && cohort.DeliveriesWithPreTests*2 < cohort.Deliveries {
					action = "Add a focused test for this cohort and require it after its latest edit before delivery, then compare its rework rate."
				} else if checkName == "" && cohort.ReworkedDeliveriesWithPreTests > 0 {
					action = "Tests already ran after edits on reworked deliveries in this cohort; strengthen their assertions or the source boundary they should protect."
				}
			}
		}
		targetEvidence := ""
		dominantRawTarget, dominantTargetCount, targetTotal := dominantMetricDimension(delivery.ReworkTargets)
		dominantTarget := deliveryTargetLabel(dominantRawTarget)
		if dominantTargetCount >= 2 {
			targetEvidence = fmt.Sprintf("; top exact rework target %s: %s cycles",
				dominantTarget,
				formatCodexCount(int64(dominantTargetCount)),
			)
			if size, lines, ok := repositoryTargetSize(report.WorkspaceRoot, dominantRawTarget); ok {
				targetEvidence += fmt.Sprintf(", current file %s lines and %s bytes",
					formatCodexCount(int64(lines)),
					formatCodexCount(size),
				)
			}
			dominatesCohort := cohortName != "" &&
				strings.HasPrefix(dominantTarget, cohortName+"/") &&
				dominantTargetCount*2 > cohort.ReviewToEditCycles
			if dominantTargetCount*2 > targetTotal || dominatesCohort {
				target = dominantTarget
				if lever == "source code" {
					action = "Inspect this repeated exact target for an oversized source boundary or missing focused contract, strengthen the smallest justified boundary, then compare its rework."
				}
			}
		}
		findings = append(findings, sessionFinding{
			Category: "delivery-quality",
			Control:  "repository",
			Title:    "review after delivery is causing repeated rework",
			Evidence: fmt.Sprintf("%s deliveries, %s with post-delivery edits, %s review-to-edit cycles, review after the latest edit on %s deliveries, and %s post-delivery review checks across %s sessions; edit levers %s; edit scopes %s%s%s",
				formatCodexCount(int64(delivery.Deliveries)),
				formatCodexCount(int64(delivery.DeliveriesWithRework)),
				formatCodexCount(int64(delivery.ReviewToEditCycles)),
				formatCodexCount(int64(delivery.DeliveriesWithPreReview)),
				formatCodexCount(int64(delivery.PostDeliveryReviewChecks)),
				formatCodexCount(int64(delivery.Sessions)),
				formatMetricDimensions(delivery.ReworkLevers),
				formatMetricDimensions(delivery.ReworkScopes),
				cohortEvidence,
				targetEvidence,
			),
			Action:     action,
			Count:      delivery.ReviewToEditCycles,
			Sessions:   delivery.Sessions,
			Target:     target,
			LastSeen:   sessionFindingLastSeen(report, "delivery-rework", ""),
			Lever:      lever,
			Confidence: confidence,
			score:      700 + delivery.DeliveriesWithRework*30 + delivery.ReviewToEditCycles*40,
		})
	}
	if delivery.Deliveries >= 2 &&
		delivery.PostDeliveryReviewChecks >= 20 &&
		delivery.PostDeliveryReviewChecks >= delivery.Deliveries*3 {
		findings = append(findings, sessionFinding{
			Category: "delivery-quality",
			Control:  "local",
			Title:    "post-delivery review requires repeated checks",
			Evidence: fmt.Sprintf("%s post-delivery review checks for %s deliveries across %s sessions, or %.1f checks per delivery",
				formatCodexCount(int64(delivery.PostDeliveryReviewChecks)),
				formatCodexCount(int64(delivery.Deliveries)),
				formatCodexCount(int64(delivery.Sessions)),
				ratio(float64(delivery.PostDeliveryReviewChecks), float64(delivery.Deliveries)),
			),
			Action:     "Make review feedback retrieval one bounded wait/resume operation that returns only new actionable state and a stable completion token.",
			Count:      delivery.PostDeliveryReviewChecks,
			Sessions:   delivery.Sessions,
			LastSeen:   sessionFindingLastSeen(report, "delivery-review", ""),
			Lever:      "tooling",
			Confidence: "high",
			score:      680 + delivery.PostDeliveryReviewChecks,
		})
	}

	downstream := summary.DownstreamQuality
	if downstream.Deliveries >= 2 &&
		(downstream.DeliveriesWithFailure >= 2 || downstream.Reverts > 0) {
		lever := "source code"
		control := "repository"
		confidence := "medium"
		title := "delivered changes repeatedly fail downstream checks"
		why := ""
		action := "Strengthen the focused source boundary and check that should prevent this failure before delivery, then compare the downstream failure rate."
		if downstream.FailedDeliveriesWithPreTests*2 < downstream.DeliveriesWithFailure {
			lever = "tooling"
			confidence = "high"
			action = "Run the relevant fresh verification after the latest edit and before delivery, then compare the downstream failure rate."
		}
		cohortName, cohort := dominantDownstreamCohort(downstream.Cohorts)
		target := cohortName
		cohortEvidence := ""
		if cohortName != "" {
			cohortEvidence = fmt.Sprintf(
				"; top cohort %s: %s/%s deliveries failed downstream, %s failure runs, %s recovery redeliveries, and fresh tests before %s/%s failures",
				cohortName,
				formatCodexCount(int64(cohort.DeliveriesWithFailure)),
				formatCodexCount(int64(cohort.Deliveries)),
				formatCodexCount(int64(cohort.FailureRuns)),
				formatCodexCount(int64(cohort.RecoveredDeliveries)),
				formatCodexCount(int64(cohort.FailedDeliveriesWithPreTests)),
				formatCodexCount(int64(cohort.DeliveriesWithFailure)),
			)
		}
		check, checkCount, _ := dominantMetricDimension(downstream.FailureChecks)
		checkEvidence := ""
		if check != "" {
			checkEvidence = fmt.Sprintf(
				"; top downstream check %s: %s failures",
				check,
				formatCodexCount(int64(checkCount)),
			)
			if lever == "tooling" {
				target = check
				action = "Run " + check + " after the latest relevant edit and before delivery, then compare the downstream failure rate for this check."
				if locallyControlledToolContext(check, config.OwnedTools) {
					control = "local"
				}
			}
		}
		if downstream.RedeliveryAttempts > downstream.RecoveredDeliveries {
			if check != "" {
				target = check
				action = "Fix the recurring downstream failure at its owning source or verification boundary and require " + check + " to pass before redelivery."
			} else {
				action = "Fix the recurring downstream failure at its owning source or verification boundary and require the failed check to pass before redelivery."
			}
		}
		correctionObservations := downstream.FollowUpEditCycles +
			downstream.RedeliveryAttempts +
			downstream.Reverts
		observedCorrection := correctionObservations >= 2 &&
			correctionObservations*5 >= downstream.DeliveriesWithFailure
		if !observedCorrection {
			title = "post-delivery check failures have limited correction evidence"
			confidence = "medium"
			why = fmt.Sprintf(
				"Only %s matching follow-up edit, redelivery, or revert observations were found for %s affected deliveries, so the aggregate is not yet confirmed as recurring delivery escapes.",
				formatCodexCount(int64(correctionObservations)),
				formatCodexCount(int64(downstream.DeliveriesWithFailure)),
			)
			if check != "" {
				action = "Confirm that the next post-delivery " + check + " failure belongs to the preceding change before changing verification policy."
			} else {
				action = "Confirm that the next post-delivery check failure belongs to the preceding change before changing verification policy."
			}
		}
		lastSeen := sessionFindingLastSeen(report, "downstream-failure", "")
		if lastSeen == "" {
			lastSeen = sessionFindingLastSeen(report, "downstream-revert", "")
		}
		findings = append(findings, sessionFinding{
			Category: "delivery-quality",
			Control:  control,
			Title:    title,
			Evidence: fmt.Sprintf(
				"%s/%s deliveries failed downstream across %s sessions, with %s failure runs, %s follow-up edit cycles, %s/%s recovery redeliveries, %s reverts, and fresh tests before %s/%s failures%s%s",
				formatCodexCount(int64(downstream.DeliveriesWithFailure)),
				formatCodexCount(int64(downstream.Deliveries)),
				formatCodexCount(int64(downstream.FailureSessions)),
				formatCodexCount(int64(downstream.FailureRuns)),
				formatCodexCount(int64(downstream.FollowUpEditCycles)),
				formatCodexCount(int64(downstream.RecoveredDeliveries)),
				formatCodexCount(int64(downstream.RedeliveryAttempts)),
				formatCodexCount(int64(downstream.Reverts)),
				formatCodexCount(int64(downstream.FailedDeliveriesWithPreTests)),
				formatCodexCount(int64(downstream.DeliveriesWithFailure)),
				cohortEvidence,
				checkEvidence,
			),
			Action:     action,
			Why:        why,
			Count:      downstream.DeliveriesWithFailure,
			Sessions:   downstream.FailureSessions,
			Target:     target,
			LastSeen:   lastSeen,
			Lever:      lever,
			Confidence: confidence,
			score: 760 + downstream.DeliveriesWithFailure*40 +
				downstream.Reverts*80 + downstream.FollowUpEditCycles*20,
		})
	}

	searchRead := codexMixedSearchReadMetrics(summary.MixedShellShapes)
	if searchRead.Calls >= 10 && searchRead.EstimatedOutputTokens >= 50_000 {
		confidence := "medium"
		if searchRead.Sessions < 2 {
			confidence = "low"
		}
		evidence := fmt.Sprintf("%s calls returned ~%s visible output tokens across at least %s",
			formatCodexCount(int64(searchRead.Calls)),
			formatCodexCount(searchRead.EstimatedOutputTokens),
			formatCodexCountNoun(int64(searchRead.Sessions), "session"),
		)
		action := config.Actions.SourceContext
		inspectFallback := boundedInspectFallbackMetrics(summary.CrossCallTransitions)
		if inspectFallback.Count >= 10 && inspectFallback.Sessions >= 2 {
			evidence += fmt.Sprintf(
				"; bounded source inspection was followed by raw search or file reads %s times across at least %s",
				formatCodexCount(int64(inspectFallback.Count)),
				formatCodexCountNoun(int64(inspectFallback.Sessions), "session"),
			)
			action = "The bounded repository source-context surface is already used; improve its result or continuation boundary so agents do not immediately fall back to raw search or file reads."
		}
		findings = append(findings, sessionFinding{
			Category:   "discovery",
			Control:    "repository",
			Title:      "bundled search/read discovery remains output-heavy",
			Evidence:   evidence,
			Action:     action,
			Count:      searchRead.Calls,
			Sessions:   searchRead.Sessions,
			LastSeen:   latestSearchReadActivity(report),
			Confidence: confidence,
			score:      250 + searchRead.Calls + int(searchRead.EstimatedOutputTokens/10_000),
		})
	}

	delegation := report.Delegation
	totalFreshTokens := delegation.ParentFreshTokens + delegation.SubagentFreshTokens
	if delegation.CoordinationAvailable &&
		delegation.SubagentSessions >= 3 &&
		delegation.CoordinationCalls >= delegation.SubagentSessions*3 &&
		delegation.CoordinationFreshTokens >= 50_000 &&
		delegation.CoordinationFreshTokens*10 >= totalFreshTokens {
		findings = append(findings, sessionFinding{
			Category: "delegation-cost",
			Control:  "agent",
			Title:    "delegation coordination consumes material task cost",
			Evidence: fmt.Sprintf(
				"%s coordination calls for %s subagent sessions used ~%s fresh tokens (%.1f%% of all fresh tokens); actions: %s",
				formatCodexCount(int64(delegation.CoordinationCalls)),
				formatCodexCount(int64(delegation.SubagentSessions)),
				formatCodexCount(delegation.CoordinationFreshTokens),
				100*ratio(float64(delegation.CoordinationFreshTokens), float64(totalFreshTokens)),
				formatMetricDimensions(delegation.CoordinationCallsByAction),
			),
			Action:     "Give each subagent one bounded outcome and complete handoff contract; reduce polling and follow-up rounds, then compare coordination tokens per completed task.",
			Count:      delegation.CoordinationCalls,
			Sessions:   delegation.DelegatingParents,
			LastSeen:   sessionFindingLastSeen(report, "delegation", ""),
			Lever:      "instructions/docs",
			Confidence: "high",
			score: 680 + delegation.CoordinationCalls*3 +
				int(delegation.CoordinationFreshTokens/5_000),
		})
	}
	if delegation.ChildrenWithEditOverlap >= 2 &&
		delegation.OverlappingChildFreshTokens >= 50_000 &&
		delegation.OverlappingChildFreshTokens*4 >= delegation.SubagentFreshTokens {
		findings = append(findings, sessionFinding{
			Category: "delegation-cost",
			Control:  "agent",
			Title:    "delegated work repeatedly overlaps parent edits",
			Evidence: fmt.Sprintf(
				"%s linked children edited %s targets also edited by their parent; those children used ~%s fresh tokens (%.1f%% of subagent fresh tokens)",
				formatCodexCount(int64(delegation.ChildrenWithEditOverlap)),
				formatCodexCount(int64(delegation.EditOverlapTargets)),
				formatCodexCount(delegation.OverlappingChildFreshTokens),
				100*ratio(float64(delegation.OverlappingChildFreshTokens), float64(delegation.SubagentFreshTokens)),
			),
			Action:     "Delegate disjoint owned files or an evidence-only review; make the parent integrate instead of independently reworking the same targets.",
			Count:      delegation.ChildrenWithEditOverlap,
			Sessions:   delegation.DelegatingParents,
			LastSeen:   sessionFindingLastSeen(report, "delegation", ""),
			Lever:      "instructions/docs",
			Confidence: "high",
			score: 700 + delegation.ChildrenWithEditOverlap*30 +
				int(delegation.OverlappingChildFreshTokens/5_000),
		})
	}
	findings = append(findings, fileHotspotFindings(report)...)
	findings = assignAndSuppressSessionFindingSignals(findings, config.SuppressSignals)
	findings = consolidateOwnerFindings(findings)
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].LastSeen != findings[j].LastSeen {
			return findings[i].LastSeen > findings[j].LastSeen
		}
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

func recurringPatternConfidence(sessions int) string {
	if sessions >= 2 {
		return "medium"
	}
	return "low"
}

func verificationOwnedOperation(operation string) bool {
	normalized := strings.NewReplacer("/", "-", "_", "-").Replace(strings.ToLower(operation))
	for _, part := range strings.Split(normalized, "-") {
		switch part {
		case "check", "test", "verify", "verification", "lint", "typecheck", "review":
			return true
		}
	}
	return false
}

func dominantTaskPhaseTailAssociation(
	associations []taskPhaseTailAssociation,
) (taskPhaseTailAssociation, bool) {
	for _, association := range associations {
		if association.ShareDelta > 0 {
			return association, true
		}
	}
	return taskPhaseTailAssociation{}, false
}

func dominantDeliveryReworkCohort(cohorts map[string]deliveryCohortMetrics) (string, deliveryCohortMetrics) {
	name := ""
	metrics := deliveryCohortMetrics{}
	for candidate, value := range cohorts {
		if candidate == "(unknown)" || value.ReviewToEditCycles == 0 {
			continue
		}
		if value.ReviewToEditCycles > metrics.ReviewToEditCycles ||
			(value.ReviewToEditCycles == metrics.ReviewToEditCycles &&
				(value.DeliveriesWithRework > metrics.DeliveriesWithRework ||
					(value.DeliveriesWithRework == metrics.DeliveriesWithRework && candidate < name))) {
			name = candidate
			metrics = value
		}
	}
	return name, metrics
}

func dominantDownstreamCohort(cohorts map[string]downstreamCohortMetrics) (string, downstreamCohortMetrics) {
	name := ""
	metrics := downstreamCohortMetrics{}
	for candidate, value := range cohorts {
		if candidate == "(unknown)" || value.DeliveriesWithFailure == 0 {
			continue
		}
		if value.DeliveriesWithFailure > metrics.DeliveriesWithFailure ||
			(value.DeliveriesWithFailure == metrics.DeliveriesWithFailure &&
				value.FailureRuns > metrics.FailureRuns) ||
			(value.DeliveriesWithFailure == metrics.DeliveriesWithFailure &&
				value.FailureRuns == metrics.FailureRuns && (name == "" || candidate < name)) {
			name = candidate
			metrics = value
		}
	}
	return name, metrics
}

func deliveryTestComparison(metrics deliveryCohortMetrics) string {
	testedDeliveries := metrics.DeliveriesWithPreTests
	untestedDeliveries := metrics.Deliveries - testedDeliveries
	testedRework := metrics.ReworkedDeliveriesWithPreTests
	untestedRework := metrics.DeliveriesWithRework - testedRework
	if testedDeliveries == 0 || untestedDeliveries == 0 ||
		testedRework < 0 || untestedRework < 0 {
		return ""
	}
	return fmt.Sprintf(
		"rework occurred on %s/%s tested deliveries versus %s/%s without a post-edit test",
		formatCodexCount(int64(testedRework)),
		formatCodexCount(int64(testedDeliveries)),
		formatCodexCount(int64(untestedRework)),
		formatCodexCount(int64(untestedDeliveries)),
	)
}

func dominantVerificationCheck(checks map[string]verificationMetrics) (string, verificationMetrics) {
	name := ""
	metrics := verificationMetrics{}
	for candidate, value := range checks {
		if value.Deliveries > metrics.Deliveries ||
			(value.Deliveries == metrics.Deliveries &&
				(value.FailFixPassDeliveries > metrics.FailFixPassDeliveries ||
					(value.FailFixPassDeliveries == metrics.FailFixPassDeliveries && candidate < name))) {
			name = candidate
			metrics = value
		}
	}
	return name, metrics
}

func dominantVerificationRepairLoop(
	checks map[string]verificationMetrics,
) (string, verificationMetrics) {
	name := ""
	metrics := verificationMetrics{}
	for candidate, value := range checks {
		if value.FailedRuns < 4 || value.FailFixPassDeliveries < 1 {
			continue
		}
		if value.FailedRuns > metrics.FailedRuns ||
			(value.FailedRuns == metrics.FailedRuns && candidate < name) {
			name = candidate
			metrics = value
		}
	}
	return name, metrics
}

func verificationCheckComparison(totalDeliveries, totalRework int, check verificationMetrics) string {
	withoutDeliveries := totalDeliveries - check.Deliveries
	withoutRework := totalRework - check.DeliveriesWithRework
	comparison := fmt.Sprintf(
		"%s/%s verified deliveries reworked",
		formatCodexCount(int64(check.DeliveriesWithRework)),
		formatCodexCount(int64(check.Deliveries)),
	)
	if withoutDeliveries > 0 && withoutRework >= 0 {
		comparison += fmt.Sprintf(
			" versus %s/%s without it",
			formatCodexCount(int64(withoutRework)),
			formatCodexCount(int64(withoutDeliveries)),
		)
	}
	if check.FailFixPassDeliveries > 0 {
		comparison += fmt.Sprintf(
			"; %s fail-fix-pass deliveries",
			formatCodexCount(int64(check.FailFixPassDeliveries)),
		)
	}
	if check.FailedRuns > 0 {
		comparison += fmt.Sprintf(
			"; %s failed runs",
			formatCodexCount(int64(check.FailedRuns)),
		)
	}
	if check.RepeatedRuns > 0 {
		comparison += fmt.Sprintf(
			"; %s/%s runs repeated without an intervening edit",
			formatCodexCount(int64(check.RepeatedRuns)),
			formatCodexCount(int64(check.Runs)),
		)
	}
	return comparison
}

func dominantMetricDimension(values map[string]int) (name string, count, total int) {
	for candidate, value := range values {
		total += value
		if value > count || (value == count && candidate < name) {
			name = candidate
			count = value
		}
	}
	return name, count, total
}

func formatMetricDimensions(values map[string]int) string {
	type row struct {
		name  string
		count int
	}
	rows := make([]row, 0, len(values))
	for name, count := range values {
		rows = append(rows, row{name: name, count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].name < rows[j].name
	})
	if len(rows) > 3 {
		rows = rows[:3]
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprintf("%s %s", row.name, formatCodexCount(int64(row.count))))
	}
	if len(parts) == 0 {
		return "(unknown)"
	}
	return strings.Join(parts, ", ")
}

var signalSlugSeparators = regexp.MustCompile(`[^a-z0-9._/-]+`)

func assignAndSuppressSessionFindingSignals(findings []sessionFinding, suppressions []string) []sessionFinding {
	suppressed := map[string]struct{}{}
	for _, signal := range suppressions {
		suppressed[signal] = struct{}{}
	}
	result := make([]sessionFinding, 0, len(findings))
	for _, finding := range findings {
		finding.Signal = sessionFindingSignal(finding)
		defaultLever, defaultConfidence := sessionFindingLever(finding)
		if finding.Lever == "" {
			finding.Lever = defaultLever
		}
		if finding.Confidence == "" {
			finding.Confidence = defaultConfidence
		}
		if _, hidden := suppressed[finding.Signal]; hidden {
			continue
		}
		result = append(result, finding)
	}
	return result
}

func sessionFindingLever(finding sessionFinding) (string, string) {
	target := leverTargetPath(finding.Target)
	switch finding.Category {
	case "owned-tool", "owned-operation":
		return "tooling", "high"
	case "code-structure":
		switch {
		case toolingTarget(target):
			return "tooling", "high"
		case instructionTarget(target):
			return "instructions/docs", "high"
		default:
			return "source code", "high"
		}
	case "instruction-discovery":
		switch {
		case instructionTarget(target):
			return "instructions/docs", "high"
		case repositoryManifestTarget(target), buildEntrypointTarget(target):
			return "tooling", "high"
		default:
			return "source code", "medium"
		}
	case "discovery", "agent-interface", "default-candidate", "output-cost":
		return "tooling", "medium"
	case "session-loop":
		switch {
		case strings.Contains(finding.Title, "progress stalls"):
			return "tooling", "medium"
		case strings.Contains(finding.Title, "input-token cost"),
			strings.Contains(finding.Title, "context compactions"):
			return "instructions/docs", "medium"
		default:
			return "unknown", "low"
		}
	case "recurring-failure":
		return "unknown", "low"
	case "delivery-quality":
		return "unknown", "medium"
	case "delegation-cost":
		return "instructions/docs", "medium"
	case "task-cost":
		return "unknown", "low"
	default:
		return "unknown", "low"
	}
}

func leverTargetPath(target string) string {
	target = strings.ToLower(filepath.ToSlash(target))
	parts := strings.Split(target, "/")
	if len(parts) >= 4 && parts[0] == ".workbench" && parts[1] == "repos" {
		return strings.Join(parts[3:], "/")
	}
	return target
}

func instructionTarget(target string) bool {
	target = leverTargetPath(target)
	base := strings.ToLower(filepath.Base(target))
	return strings.HasPrefix(target, "docs/") ||
		base == "agents.md" ||
		base == "readme.md"
}

func toolingTarget(target string) bool {
	target = leverTargetPath(target)
	return strings.HasPrefix(target, "scripts/") ||
		strings.HasPrefix(target, "bwb-src/") ||
		strings.HasPrefix(target, "tools/") ||
		strings.HasPrefix(target, "tooling/") ||
		strings.HasPrefix(target, ".github/")
}

func sessionFindingActivity(report codexSessionInsightsReport, kind, target string) time.Time {
	return report.Summary.Activity[sessionActivityKey(kind, target)]
}

func sessionFindingLastSeen(report codexSessionInsightsReport, kind, target string) string {
	return formatSessionFindingTime(sessionFindingActivity(report, kind, target))
}

func formatSessionFindingTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func latestSearchReadActivity(report codexSessionInsightsReport) string {
	var latest time.Time
	for shape := range report.Summary.MixedShellShapes {
		if !strings.Contains(shape, "search") || !strings.Contains(shape, "file reads") {
			continue
		}
		if seen := sessionFindingActivity(report, "shape", shape); seen.After(latest) {
			latest = seen
		}
	}
	return formatSessionFindingTime(latest)
}

func boundedInspectFallbackMetrics(transitions map[string]codexTransitionMetrics) codexTransitionMetrics {
	var result codexTransitionMetrics
	for _, transition := range []string{
		"bounded task inspect -> file reads",
		"bounded task inspect -> search",
	} {
		metrics := transitions[transition]
		result.Count += metrics.Count
		result.Sessions = max(result.Sessions, metrics.Sessions)
	}
	return result
}

func sessionFindingSignal(finding sessionFinding) string {
	target := sessionFindingTargetIdentity(finding)
	switch {
	case strings.HasPrefix(finding.Title, "high input-token cost in task: "):
		return signalID("session-loop", "input-cost", target)
	case strings.HasPrefix(finding.Title, "progress stalls while waiting on: "):
		return signalID("session-loop", "progress-stall", target)
	case strings.HasPrefix(finding.Title, "rapid continuation polling: "):
		return signalID("agent-interface", "rapid-poll", target)
	case strings.HasPrefix(finding.Title, "yielded operations never reached a terminal result: "):
		return signalID("agent-interface", "abandoned-continuation", target)
	case finding.Title == "repeated edits correlate with high task cost":
		return signalID("code-structure", "file-cost", target)
	case finding.Title == "frequently edited target requires repeated rework":
		return signalID("delivery-quality", "file-rework", target)
	case finding.Title == "frequently edited target is associated with downstream failures":
		return signalID("delivery-quality", "file-failure", target)
	case finding.Category == "owned-operation":
		return signalID("owned-operation", target)
	case finding.Category == "owned-tool":
		return signalID("owned-tool", target)
	case target != "":
		return signalID(finding.Category, target)
	default:
		return signalID(finding.Category, finding.Title)
	}
}

func signalID(parts ...string) string {
	slugs := make([]string, 0, len(parts))
	for _, part := range parts {
		slug := strings.Trim(signalSlugSeparators.ReplaceAllString(strings.ToLower(strings.TrimSpace(part)), "-"), "-/")
		if slug != "" {
			slugs = append(slugs, slug)
		}
	}
	signal := strings.Join(slugs, "/")
	const maximumSignalLength = 200
	if len(signal) > maximumSignalLength {
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(signal)))
		const digestLength = 16
		prefixLength := maximumSignalLength - digestLength - 1
		signal = strings.TrimRight(signal[:prefixLength], "-/") + "-" + digest[:digestLength]
	}
	return signal
}

func locallyControlledToolContext(context string, ownedTools []ownedToolConfig) bool {
	toolID, _, hasOperation := strings.Cut(context, "/")
	if !hasOperation {
		return false
	}
	for _, owned := range ownedTools {
		if toolID == owned.ID {
			return true
		}
	}
	return false
}

func ownedOperationFailureCounts(ownership ownershipCatalog, operation string, reasons map[string]codexOccurrenceMetrics) (actionable int, expected int) {
	for reason, metrics := range reasons {
		if ownership.operationFailureExpected(operation, reason) {
			expected += metrics.Count
		} else {
			actionable += metrics.Count
		}
	}
	return actionable, expected
}

func ownedToolFrictionSessions(report codexSessionInsightsReport, tool string) (failureSessions, truncationSessions int) {
	for _, record := range report.sessionRecords {
		metrics := record.OwnedTooling[tool]
		if metrics.FailedCalls > metrics.AmbiguousFailedCalls {
			failureSessions++
		}
		if metrics.TruncatedCalls > metrics.AmbiguousTruncatedCalls {
			truncationSessions++
		}
	}
	return failureSessions, truncationSessions
}

func ownedOperationFrictionSessions(
	report codexSessionInsightsReport,
	ownership ownershipCatalog,
	operation string,
) (failureSessions, truncationSessions int) {
	for _, record := range report.sessionRecords {
		hasActionableFailure := false
		for reason, count := range record.OwnedOperationFailureReasons[operation] {
			if count > 0 && !ownership.operationFailureExpected(operation, reason) {
				hasActionableFailure = true
				break
			}
		}
		if hasActionableFailure {
			failureSessions++
		}
		if record.OwnedOperations[operation].TruncatedCalls > 0 {
			truncationSessions++
		}
	}
	for reason, metrics := range report.Summary.OwnedOperationFailureReasons[operation] {
		if !ownership.operationFailureExpected(operation, reason) {
			failureSessions = max(failureSessions, metrics.Sessions)
		}
	}
	return failureSessions, truncationSessions
}

func ownedOperationExpectedFailure(operation, reason string) bool {
	if reason == "test failure" || reason == "search no match" {
		return true
	}
	name := strings.ToLower(operation[strings.LastIndex(operation, "/")+1:])
	return reason == "other non-zero exit" && downstreamCheckName(name)
}

func ownedOperationFindingLastSeen(
	report codexSessionInsightsReport,
	operation string,
	recurringFriction bool,
) string {
	if recurringFriction {
		if friction := sessionFindingActivity(report, "owned-operation-friction", operation); !friction.IsZero() {
			return formatSessionFindingTime(friction)
		}
	}
	return sessionFindingLastSeen(report, "owned-operation", operation)
}

func recentOwnedOperationEvidence(
	report codexSessionInsightsReport,
	ownership ownershipCatalog,
	operation string,
) string {
	generatedAt, generatedErr := time.Parse(time.RFC3339, report.GeneratedAt)
	since, sinceErr := time.Parse(time.RFC3339, report.Since)
	const recentWindow = 24 * time.Hour
	if generatedErr != nil || sinceErr != nil || generatedAt.Sub(since) < 2*recentWindow {
		return ""
	}

	cutoff := generatedAt.Add(-recentWindow)
	calls := 0
	ambiguousCalls := 0
	sessions := 0
	actionableFailures := 0
	truncatedCalls := 0
	ambiguousTruncatedCalls := 0
	outputBytes := int64(0)
	ambiguousOutputBytes := int64(0)
	for _, record := range report.sessionRecords {
		activity := record.Activity[sessionActivityKey("owned-operation", operation)]
		if activity.IsZero() || activity.Before(cutoff) {
			continue
		}
		exact := record.OwnedOperations[operation]
		ambiguous := record.OwnedOperationAmbiguous[operation]
		recordCalls := exact.Calls + ambiguous.Calls
		if recordCalls == 0 {
			continue
		}
		calls += recordCalls
		ambiguousCalls += ambiguous.Calls
		sessions++
		truncatedCalls += exact.TruncatedCalls
		ambiguousTruncatedCalls += ambiguous.TruncatedCalls
		outputBytes += exact.OutputBytes
		ambiguousOutputBytes += ambiguous.OutputBytes
		for reason, count := range record.OwnedOperationFailureReasons[operation] {
			if !ownership.operationFailureExpected(operation, reason) {
				actionableFailures += count
			}
		}
	}
	if calls == 0 {
		return "; recent 24h sessions: no calls"
	}
	return fmt.Sprintf(
		"; recent 24h sessions: %s calls/%s sessions, %s bundled calls, %s actionable failures, %s truncations, %s ambiguous bundled truncations, ~%s attributed output tokens, ~%s ambiguous bundled output tokens",
		formatCodexCount(int64(calls)),
		formatCodexCount(int64(sessions)),
		formatCodexCount(int64(ambiguousCalls)),
		formatCodexCount(int64(actionableFailures)),
		formatCodexCount(int64(truncatedCalls)),
		formatCodexCount(int64(ambiguousTruncatedCalls)),
		formatCodexCount(estimatedTokens(outputBytes)),
		formatCodexCount(estimatedTokens(ambiguousOutputBytes)),
	)
}

func formatOwnedOperationActionableReasons(ownership ownershipCatalog, operation string, reasons map[string]codexOccurrenceMetrics) string {
	type row struct {
		reason   string
		count    int
		sessions int
	}
	rows := make([]row, 0, len(reasons))
	for reason, metrics := range reasons {
		if ownership.operationFailureExpected(operation, reason) || metrics.Count <= 0 {
			continue
		}
		rows = append(rows, row{
			reason:   reason,
			count:    metrics.Count,
			sessions: metrics.Sessions,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		if rows[i].sessions != rows[j].sessions {
			return rows[i].sessions > rows[j].sessions
		}
		return rows[i].reason < rows[j].reason
	})
	if len(rows) > 3 {
		rows = rows[:3]
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		sessionLabel := "sessions"
		if row.sessions == 1 {
			sessionLabel = "session"
		}
		parts = append(parts, fmt.Sprintf(
			"%s %s calls/%s %s",
			row.reason,
			formatCodexCount(int64(row.count)),
			formatCodexCount(int64(row.sessions)),
			sessionLabel,
		))
	}
	return strings.Join(parts, ", ")
}

func perSessionTokens(total int64, sessions int) int64 {
	if sessions <= 0 {
		return 0
	}
	return total / int64(sessions)
}

func compactionCohortTokens(report codexSessionInsightsReport) normalizedTokenUsage {
	var tokens normalizedTokenUsage
	for _, record := range report.sessionRecords {
		if record.Compactions > 0 {
			addNormalizedTokenUsage(&tokens, record.Tokens)
		}
	}
	if tokens.InputTokens == 0 && tokens.OutputTokens == 0 {
		return report.Summary.Tokens
	}
	return tokens
}

func formatDurationSeconds(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	return (time.Duration(seconds) * time.Second).String()
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

func diversifySessionFindings(findings []sessionFinding) []sessionFinding {
	limits := map[string]int{
		"agent-interface":       4,
		"default-candidate":     6,
		"code-structure":        6,
		"instruction-discovery": 4,
		"instruction-footprint": 1,
		"recurring-failure":     4,
		"diagnostic-failure":    6,
		"owned-tool":            4,
		"owned-operation":       6,
		"session-loop":          6,
		"output-cost":           3,
		"delivery-quality":      3,
		"delegation-cost":       3,
		"task-cost":             2,
	}
	codeStructureSelections := selectRepositoryScopedCodeStructureFindings(
		findings,
		limits["code-structure"],
	)
	categorySelections := map[string]map[int]bool{}
	for category, limit := range limits {
		if category == "code-structure" {
			continue
		}
		categorySelections[category] = selectHighestImpactCategoryFindings(findings, category, limit)
	}
	result := make([]sessionFinding, 0, len(findings))
	for index, finding := range findings {
		if finding.Category == "code-structure" {
			if !codeStructureSelections[index] {
				continue
			}
			result = append(result, finding)
			continue
		}
		if selections, limited := categorySelections[finding.Category]; limited {
			if !selections[index] {
				continue
			}
		}
		result = append(result, finding)
	}
	return result
}

func selectHighestImpactCategoryFindings(findings []sessionFinding, category string, limit int) map[int]bool {
	selected := map[int]bool{}
	if limit <= 0 {
		return selected
	}
	var candidates []int
	for index, finding := range findings {
		if finding.Category == category {
			candidates = append(candidates, index)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := findings[candidates[i]]
		right := findings[candidates[j]]
		if sessionFindingHigherImpact(left, right) {
			return true
		}
		if sessionFindingHigherImpact(right, left) {
			return false
		}
		return candidates[i] < candidates[j]
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	for _, index := range candidates {
		selected[index] = true
	}
	return selected
}

func selectRepositoryScopedCodeStructureFindings(findings []sessionFinding, limit int) map[int]bool {
	selected := map[int]bool{}
	if limit <= 0 {
		return selected
	}

	bestByScope := map[string]int{}
	for index, finding := range findings {
		if finding.Category != "code-structure" {
			continue
		}
		scope := sessionFindingRepositoryScope(finding)
		best, exists := bestByScope[scope]
		if !exists || sessionFindingHigherImpact(finding, findings[best]) {
			bestByScope[scope] = index
		}
	}

	representatives := make([]int, 0, len(bestByScope))
	for _, index := range bestByScope {
		representatives = append(representatives, index)
	}
	sort.Slice(representatives, func(i, j int) bool {
		left := findings[representatives[i]]
		right := findings[representatives[j]]
		if sessionFindingHigherImpact(left, right) {
			return true
		}
		if sessionFindingHigherImpact(right, left) {
			return false
		}
		return representatives[i] < representatives[j]
	})
	if len(representatives) > limit {
		representatives = representatives[:limit]
	}
	for _, index := range representatives {
		selected[index] = true
	}

	for index, finding := range findings {
		if len(selected) >= limit {
			break
		}
		if finding.Category == "code-structure" {
			selected[index] = true
		}
	}
	return selected
}

func sessionFindingHigherImpact(left, right sessionFinding) bool {
	if left.score != right.score {
		return left.score > right.score
	}
	if left.Sessions != right.Sessions {
		return left.Sessions > right.Sessions
	}
	if left.Count != right.Count {
		return left.Count > right.Count
	}
	return left.LastSeen > right.LastSeen
}

func sessionFindingRepositoryScope(finding sessionFinding) string {
	if repository := strings.TrimSpace(finding.Repository); repository != "" {
		return repository
	}
	target := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(finding.Target)), "./")
	parts := strings.Split(target, "/")
	if len(parts) >= 3 && parts[0] == ".workbench" && parts[1] == "repos" {
		return parts[2]
	}
	if len(parts) >= 3 && parts[0] == ".worktrees" {
		return parts[2]
	}
	return "workspace"
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
		target := sessionFindingDisplayTarget(finding)
		fmt.Printf("- [%s/%s] %s%s\n", finding.Category, finding.Control, finding.Title, target)
		fmt.Printf("  Signal: %s\n", finding.Signal)
		fmt.Printf("  Evidence: %s.\n", strings.TrimSuffix(finding.Evidence, "."))
		if finding.Why != "" {
			fmt.Printf("  Why this matters: %s\n", finding.Why)
		}
		if len(finding.Supporting) > 0 {
			fmt.Printf("  Supporting signals: %s\n", strings.Join(finding.Supporting, ", "))
		}
		if finding.LastSeen != "" {
			fmt.Printf("  Last seen: %s\n", formatSessionFindingAge(finding.LastSeen, time.Now().UTC()))
		}
		fmt.Printf("  Likely lever: %s (%s confidence)\n", finding.Lever, finding.Confidence)
		fmt.Printf("  Next: %s\n", finding.Action)
	}
	if len(rows) < len(findings) {
		fmt.Printf("... %d more findings; use --limit 0 for the full list.\n", len(findings)-len(rows))
	}
}

func sessionFindingDisplayTarget(finding sessionFinding) string {
	target := sessionFindingTargetIdentity(finding)
	if target == "" || strings.Contains(finding.Title, target) {
		return ""
	}
	return " · " + target
}

func sessionFindingTargetIdentity(finding sessionFinding) string {
	target := strings.TrimSpace(finding.Target)
	repository := strings.TrimSpace(finding.Repository)
	if repository == "" {
		return target
	}
	if target == "" {
		return repository
	}
	return repository + "/" + target
}

func splitManagedRepositoryTarget(target string) (repository, relative string, ok bool) {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(target)), "/")
	if len(parts) < 4 || parts[0] != ".workbench" || parts[1] != "repos" {
		return "", target, false
	}
	return parts[2], strings.Join(parts[3:], "/"), true
}

func formatSessionFindingAge(lastSeen string, now time.Time) string {
	seen, err := time.Parse(time.RFC3339, lastSeen)
	if err != nil || seen.After(now) {
		return lastSeen
	}
	age := now.Sub(seen)
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age/time.Minute))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(age/(24*time.Hour)))
	}
}
