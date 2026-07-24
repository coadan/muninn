package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type sessionFinding struct {
	Category   string `json:"category"`
	Control    string `json:"control"`
	Signal     string `json:"signal"`
	Title      string `json:"title"`
	Evidence   string `json:"evidence"`
	Action     string `json:"action"`
	Count      int    `json:"count,omitempty"`
	Sessions   int    `json:"sessions,omitempty"`
	Target     string `json:"target,omitempty"`
	LastSeen   string `json:"lastSeen,omitempty"`
	Lever      string `json:"lever"`
	Confidence string `json:"confidence"`
	score      int
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
			Action:   directFeedbackAction(feedback),
			Count:    feedback.Occurrences,
			Target:   feedback.Target,
			LastSeen: feedback.LastSeen,
			score:    800 + feedback.Occurrences*10,
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
		evidence := fmt.Sprintf("%s calls across %s sessions, %s bundled calls, %s actionable failures, %s expected/product failures, %s ambiguous bundled failures, %s truncations, ~%s attributed output tokens, ~%s ambiguous bundled output tokens",
			formatCodexCount(int64(metrics.Calls)),
			formatCodexCount(int64(metrics.Sessions)),
			formatCodexCount(int64(metrics.AmbiguousCalls)),
			formatCodexCount(int64(actionableFailures)),
			formatCodexCount(int64(expectedFailures)),
			formatCodexCount(int64(metrics.AmbiguousFailedCalls)),
			formatCodexCount(int64(metrics.TruncatedCalls)),
			formatCodexCount(metrics.EstimatedOutputTokens),
			formatCodexCount(metrics.EstimatedAmbiguousOutputTokens),
		)
		if reasons := formatOwnedOperationActionableReasons(summary.OwnedOperationFailureReasons[operation]); reasons != "" {
			evidence += "; actionable reasons: " + reasons
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
			LastSeen: sessionFindingLastSeen(report, "owned-operation", operation),
			score:    650 + metrics.Sessions*20 + actionableFailures*30 + metrics.TruncatedCalls*10 + int(metrics.EstimatedOutputTokens/5_000),
		})
	}

	for context, metrics := range summary.OversizedOutputs {
		control := "repository"
		if locallyControlledOutputContext(context, config.OwnedTools) {
			control = "local"
		}
		findings = append(findings, sessionFinding{
			Category: "output-cost",
			Control:  control,
			Title:    "individual tool calls return oversized output: " + context,
			Evidence: fmt.Sprintf("%s calls returned %s bytes (~%s visible tokens) across %s sessions; largest call %s bytes",
				formatCodexCount(int64(metrics.Calls)),
				formatCodexCount(metrics.OutputBytes),
				formatCodexCount(estimatedTokens(metrics.OutputBytes)),
				formatCodexCount(int64(metrics.Sessions)),
				formatCodexCount(metrics.MaxOutputBytes),
			),
			Action:   oversizedOutputAction(context, control),
			Count:    metrics.Calls,
			Sessions: metrics.Sessions,
			Target:   context,
			LastSeen: sessionFindingLastSeen(report, "oversized-output", context),
			score:    600 + metrics.Calls + int(metrics.OutputBytes/10_000),
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
			Action:   action,
			Count:    metrics.Calls,
			Target:   owned.ID,
			LastSeen: sessionFindingLastSeen(report, "owned-tool", owned.ID),
			score:    500 + metrics.FailedCalls*20 + metrics.TruncatedCalls*5 + int(outputTokens/10_000),
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
				LastSeen: sessionFindingLastSeen(report, "failure", reason+"\x00"+context),
				score:    400 + metrics.Sessions*30 + metrics.Count,
			})
		}
	}

	type workflowEvidence struct {
		Count    int
		Sessions int
		LastSeen time.Time
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
		if seen := sessionFindingActivity(report, "transition", transition); seen.After(evidence.LastSeen) {
			evidence.LastSeen = seen
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
			LastSeen: formatSessionFindingTime(metrics.LastSeen),
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
				LastSeen: sessionFindingLastSeen(report, "read", target),
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
			LastSeen: sessionFindingLastSeen(report, "read", target),
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
			LastSeen: sessionFindingLastSeen(report, "inline", ""),
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
			LastSeen: sessionFindingLastSeen(report, "compaction", ""),
			score:    420 + summary.SessionsWithCompactions*20 + summary.Compactions,
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
		uncachedPerSession := perSessionTokens(task.Tokens.UncachedInputTokens, task.Sessions)
		if task.Sessions < 2 || task.Tokens.UncachedInputTokens < 500_000 || uncachedPerSession < 50_000 {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "session-loop",
			Control:  "repository",
			Title:    "input-token cost is concentrated in task: " + task.Task,
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

	for context, metrics := range summary.ProgressStalls {
		if metrics.Calls < 2 ||
			metrics.Seconds < 40 ||
			!locallyControlledOutputContext(context, config.OwnedTools) {
			continue
		}
		findings = append(findings, sessionFinding{
			Category: "session-loop",
			Control:  "local",
			Title:    "progress stalls while waiting on: " + context,
			Evidence: fmt.Sprintf("%s low-output waits consumed %s across %s sessions; waits for tests, builds, local reviews, and remote GitHub review were classified separately",
				formatCodexCount(int64(metrics.Calls)),
				formatDurationSeconds(metrics.Seconds),
				formatCodexCount(int64(metrics.Sessions)),
			),
			Action:   "Remove redundant polling, emit useful bounded progress, or make this operation asynchronous/resumable when the wait is not intrinsically required.",
			Count:    metrics.Calls,
			Sessions: metrics.Sessions,
			Target:   context,
			LastSeen: sessionFindingLastSeen(report, "progress-stall", context),
			score:    440 + metrics.Sessions*20 + metrics.Calls*5 + int(metrics.Seconds/10),
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
		confidence := "medium"
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
		}
		if downstream.RedeliveryAttempts > downstream.RecoveredDeliveries {
			action = "Fix the recurring downstream failure at its owning source or verification boundary and require the failed check to pass before redelivery."
		}
		lastSeen := sessionFindingLastSeen(report, "downstream-failure", "")
		if lastSeen == "" {
			lastSeen = sessionFindingLastSeen(report, "downstream-revert", "")
		}
		findings = append(findings, sessionFinding{
			Category: "delivery-quality",
			Control:  "repository",
			Title:    "delivered changes repeatedly fail downstream checks",
			Evidence: fmt.Sprintf(
				"%s/%s deliveries failed downstream across %s sessions, with %s failure runs, %s follow-up edit cycles, %s/%s recovery redeliveries, %s reverts, and fresh tests before %s/%s failures%s%s",
				formatCodexCount(int64(downstream.DeliveriesWithFailure)),
				formatCodexCount(int64(downstream.Deliveries)),
				formatCodexCount(int64(downstream.Sessions)),
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
			Count:      downstream.DeliveriesWithFailure,
			Sessions:   downstream.Sessions,
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
		findings = append(findings, sessionFinding{
			Category: "discovery",
			Control:  "repository",
			Title:    "bundled search/read discovery remains output-heavy",
			Evidence: fmt.Sprintf("%s calls returned ~%s visible output tokens",
				formatCodexCount(int64(searchRead.Calls)),
				formatCodexCount(searchRead.EstimatedOutputTokens),
			),
			Action:   config.Actions.SourceContext,
			Count:    searchRead.Calls,
			LastSeen: latestSearchReadActivity(report),
			score:    250 + searchRead.Calls + int(searchRead.EstimatedOutputTokens/10_000),
		})
	}

	findings = assignAndSuppressSessionFindingSignals(findings, config.SuppressSignals)
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

func repositoryTargetSize(repositoryRoot, target string) (size int64, lines int, ok bool) {
	path := filepath.Join(repositoryRoot, filepath.FromSlash(target))
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	lines = strings.Count(string(content), "\n")
	if len(content) > 0 && content[len(content)-1] != '\n' {
		lines++
	}
	return int64(len(content)), lines, true
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
		if finding.Lever == "" {
			finding.Lever, finding.Confidence = sessionFindingLever(finding)
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
		case repositoryManifestTarget(target):
			return "tooling", "high"
		default:
			return "source code", "medium"
		}
	case "discovery", "agent-interface", "output-cost":
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

func sessionFindingSignal(finding sessionFinding) string {
	target := strings.TrimSpace(finding.Target)
	switch {
	case strings.HasPrefix(finding.Title, "direct feedback: "):
		feedbackSignal := strings.TrimPrefix(finding.Title, "direct feedback: ")
		return signalID(finding.Category, "direct-feedback", target, feedbackSignal)
	case strings.HasPrefix(finding.Title, "input-token cost is concentrated in task: "):
		return signalID("session-loop", "input-cost", target)
	case strings.HasPrefix(finding.Title, "progress stalls while waiting on: "):
		return signalID("session-loop", "progress-stall", target)
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

func locallyControlledOutputContext(context string, ownedTools []ownedToolConfig) bool {
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

func oversizedOutputAction(context, control string) string {
	if control == "local" {
		return "Lower this locally controlled operation's default output and return a compact summary with explicit focused follow-ups."
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

func formatOwnedOperationActionableReasons(reasons map[string]codexOccurrenceMetrics) string {
	type row struct {
		reason   string
		count    int
		sessions int
	}
	rows := make([]row, 0, len(reasons))
	for reason, metrics := range reasons {
		if reason == "test failure" || reason == "search no match" || metrics.Count <= 0 {
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
			"output-cost":       true,
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
		"output": {
			"output-cost": true,
		},
		"quality": {
			"delivery-quality": true,
			"task-cost":        true,
		},
	}
	categories, ok := allowed[focus]
	if !ok {
		return nil, fmt.Errorf("unsupported --focus %q (available: tooling, instructions, interface, structure, discovery, failures, loops, output, quality)", focus)
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
		"session-loop":          6,
		"output-cost":           3,
		"delivery-quality":      3,
		"task-cost":             2,
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
		target := sessionFindingDisplayTarget(finding)
		fmt.Printf("- [%s/%s] %s%s\n", finding.Category, finding.Control, finding.Title, target)
		fmt.Printf("  Signal: %s\n", finding.Signal)
		fmt.Printf("  Evidence: %s.\n", strings.TrimSuffix(finding.Evidence, "."))
		if finding.LastSeen != "" {
			fmt.Printf("  Last seen: %s\n", formatSessionFindingAge(finding.LastSeen, time.Now().UTC()))
		}
		fmt.Printf("  Likely lever: %s (%s confidence)\n", finding.Lever, finding.Confidence)
		fmt.Printf("  Next: %s\n", finding.Action)
	}
	if len(rows) < len(findings) {
		fmt.Printf("... %d more findings; use --limit 0 or --details.\n", len(findings)-len(rows))
	}
}

func sessionFindingDisplayTarget(finding sessionFinding) string {
	if finding.Target == "" || strings.Contains(finding.Title, finding.Target) {
		return ""
	}
	return " · " + finding.Target
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
