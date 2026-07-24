package main

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A completion episode is the privacy-safe analytical proxy for one task. It
// starts after the previous completion marker and ends at the next marker.
type codexTaskEpisode struct {
	StartedAt       time.Time
	EndedAt         time.Time
	Completed       bool
	LeftCensored    bool
	Tokens          codexTokenUsage
	ToolCalls       int
	FailedCalls     int
	Compactions     int
	ToolOutputBytes int64
	Families        map[string]int
	OwnedOperations map[string]int
	TargetCohorts   map[string]int
}

type deliveryReworkMetrics struct {
	Deliveries                      int                              `json:"deliveries"`
	DeliveriesWithPreTests          int                              `json:"deliveriesWithPreTests"`
	DeliveriesWithPreReview         int                              `json:"deliveriesWithPreReview"`
	PostDeliveryReviewChecks        int                              `json:"postDeliveryReviewChecks"`
	ReviewToEditCycles              int                              `json:"reviewToEditCycles"`
	DeliveriesWithRework            int                              `json:"deliveriesWithRework"`
	ReworkedDeliveriesWithPreTests  int                              `json:"reworkedDeliveriesWithPreTests"`
	ReworkedDeliveriesWithPreReview int                              `json:"reworkedDeliveriesWithPreReview"`
	PostDeliveryEditCalls           int                              `json:"postDeliveryEditCalls"`
	ReviewOutputBytes               int64                            `json:"reviewOutputBytes"`
	Sessions                        int                              `json:"sessions"`
	ReworkLevers                    map[string]int                   `json:"reworkLevers,omitempty"`
	ReworkScopes                    map[string]int                   `json:"reworkScopes,omitempty"`
	ReworkTargets                   map[string]int                   `json:"reworkTargets,omitempty"`
	Cohorts                         map[string]deliveryCohortMetrics `json:"cohorts,omitempty"`
	VerificationChecks              map[string]verificationMetrics   `json:"verificationChecks,omitempty"`
}

type deliveryCohortMetrics struct {
	Deliveries                      int                            `json:"deliveries"`
	DeliveriesWithPreTests          int                            `json:"deliveriesWithPreTests"`
	DeliveriesWithPreReview         int                            `json:"deliveriesWithPreReview"`
	DeliveriesWithRework            int                            `json:"deliveriesWithRework"`
	ReworkedDeliveriesWithPreTests  int                            `json:"reworkedDeliveriesWithPreTests"`
	ReworkedDeliveriesWithPreReview int                            `json:"reworkedDeliveriesWithPreReview"`
	ReviewToEditCycles              int                            `json:"reviewToEditCycles"`
	VerificationChecks              map[string]verificationMetrics `json:"verificationChecks,omitempty"`
}

type verificationMetrics struct {
	Deliveries            int `json:"deliveries"`
	DeliveriesWithRework  int `json:"deliveriesWithRework"`
	FailedRuns            int `json:"failedRuns"`
	FailFixPassDeliveries int `json:"failFixPassDeliveries"`
}

type deliveryReworkTracker struct {
	metrics                  deliveryReworkMetrics
	delivered                bool
	deliveryHadRework        bool
	reviewAwaitingEdit       bool
	pendingEditCohorts       map[string]struct{}
	pendingEditTargets       map[string]struct{}
	testsAfterLatestEdit     bool
	reviewAfterLatestEdit    bool
	currentDeliveryPreTests  bool
	currentDeliveryPreReview bool
	currentDeliveryCohorts   map[string]struct{}
	currentDeliveryTargets   map[string]struct{}
	reworkedDeliveryCohorts  map[string]struct{}
	passedChecksAfterEdit    map[string]struct{}
	failedChecksAwaitingEdit map[string]struct{}
	repairCandidateChecks    map[string]struct{}
	repairedChecksAfterEdit  map[string]struct{}
	currentDeliveryChecks    map[string]struct{}
}

func (tracker *deliveryReworkTracker) observe(event normalizedSessionEvent, operations []string) {
	if event.Kind == sessionEventToolCall && event.ToolName == "apply_patch" {
		tracker.observeEdit(event)
		return
	}
	if event.Kind == sessionEventToolOutput && !event.OperationContinues &&
		len(tracker.pendingEditCohorts) > 0 &&
		testOperation(event, operations) {
		tracker.observeTest(event, operations)
	}
	if event.Kind == sessionEventToolOutput && !event.Failed && !event.OperationContinues &&
		len(tracker.pendingEditCohorts) > 0 {
		if reviewOperation(event, operations) {
			tracker.reviewAfterLatestEdit = true
		}
	}
	if event.Kind == sessionEventToolOutput && !event.Failed && !event.OperationContinues &&
		deliveryOperation(event, operations) {
		tracker.observeDelivery()
		return
	}
	if reviewStart(event, operations) {
		if tracker.delivered {
			tracker.metrics.PostDeliveryReviewChecks++
			tracker.reviewAwaitingEdit = true
		}
		return
	}
	if event.Kind == sessionEventToolOutput && tracker.delivered && reviewOperation(event, operations) {
		tracker.metrics.ReviewOutputBytes += event.OutputBytes
		return
	}
}

func (tracker *deliveryReworkTracker) observeEdit(event normalizedSessionEvent) {
	cohorts := eventTargetCohorts(event.Targets)
	if tracker.delivered && tracker.reviewAwaitingEdit {
		tracker.observeReviewDrivenEdit(event)
	}
	if tracker.pendingEditCohorts == nil {
		tracker.pendingEditCohorts = map[string]struct{}{}
	}
	for cohort := range cohorts {
		tracker.pendingEditCohorts[cohort] = struct{}{}
	}
	if tracker.pendingEditTargets == nil {
		tracker.pendingEditTargets = map[string]struct{}{}
	}
	for _, target := range event.Targets {
		tracker.pendingEditTargets[target] = struct{}{}
	}
	if tracker.repairCandidateChecks == nil {
		tracker.repairCandidateChecks = map[string]struct{}{}
	}
	for check := range tracker.failedChecksAwaitingEdit {
		tracker.repairCandidateChecks[check] = struct{}{}
	}
	tracker.failedChecksAwaitingEdit = nil
	tracker.testsAfterLatestEdit = false
	tracker.reviewAfterLatestEdit = false
	tracker.passedChecksAfterEdit = nil
	tracker.repairedChecksAfterEdit = nil
}

func (tracker *deliveryReworkTracker) observeTest(event normalizedSessionEvent, operations []string) {
	checks := verificationCheckIDs(event, operations)
	if event.Failed {
		if tracker.failedChecksAwaitingEdit == nil {
			tracker.failedChecksAwaitingEdit = map[string]struct{}{}
		}
		for _, check := range checks {
			tracker.failedChecksAwaitingEdit[check] = struct{}{}
			delete(tracker.repairCandidateChecks, check)
			delete(tracker.passedChecksAfterEdit, check)
			delete(tracker.repairedChecksAfterEdit, check)
			metrics := tracker.verification(check)
			metrics.FailedRuns++
			tracker.metrics.VerificationChecks[check] = metrics
			for cohort := range tracker.pendingEditCohorts {
				cohortMetrics := tracker.cohort(cohort)
				checkMetrics := cohortVerification(&cohortMetrics, check)
				checkMetrics.FailedRuns++
				cohortMetrics.VerificationChecks[check] = checkMetrics
				tracker.metrics.Cohorts[cohort] = cohortMetrics
			}
		}
		tracker.testsAfterLatestEdit = len(tracker.passedChecksAfterEdit) > 0
		return
	}
	tracker.testsAfterLatestEdit = true
	if tracker.passedChecksAfterEdit == nil {
		tracker.passedChecksAfterEdit = map[string]struct{}{}
	}
	if tracker.repairedChecksAfterEdit == nil {
		tracker.repairedChecksAfterEdit = map[string]struct{}{}
	}
	for _, check := range checks {
		tracker.passedChecksAfterEdit[check] = struct{}{}
		if _, repaired := tracker.repairCandidateChecks[check]; repaired {
			tracker.repairedChecksAfterEdit[check] = struct{}{}
		}
	}
}

func (tracker *deliveryReworkTracker) observeDelivery() {
	tracker.metrics.Deliveries++
	if tracker.testsAfterLatestEdit {
		tracker.metrics.DeliveriesWithPreTests++
	}
	if tracker.reviewAfterLatestEdit {
		tracker.metrics.DeliveriesWithPreReview++
	}
	for cohort := range tracker.pendingEditCohorts {
		metrics := tracker.cohort(cohort)
		metrics.Deliveries++
		if tracker.testsAfterLatestEdit {
			metrics.DeliveriesWithPreTests++
		}
		if tracker.reviewAfterLatestEdit {
			metrics.DeliveriesWithPreReview++
		}
		for check := range tracker.passedChecksAfterEdit {
			checkMetrics := cohortVerification(&metrics, check)
			checkMetrics.Deliveries++
			if _, repaired := tracker.repairedChecksAfterEdit[check]; repaired {
				checkMetrics.FailFixPassDeliveries++
			}
			metrics.VerificationChecks[check] = checkMetrics
		}
		tracker.metrics.Cohorts[cohort] = metrics
	}
	for check := range tracker.passedChecksAfterEdit {
		metrics := tracker.verification(check)
		metrics.Deliveries++
		if _, repaired := tracker.repairedChecksAfterEdit[check]; repaired {
			metrics.FailFixPassDeliveries++
		}
		tracker.metrics.VerificationChecks[check] = metrics
	}
	tracker.delivered = true
	tracker.deliveryHadRework = false
	tracker.reviewAwaitingEdit = false
	tracker.currentDeliveryPreTests = tracker.testsAfterLatestEdit
	tracker.currentDeliveryPreReview = tracker.reviewAfterLatestEdit
	tracker.currentDeliveryCohorts = cloneStringSet(tracker.pendingEditCohorts)
	tracker.currentDeliveryTargets = cloneStringSet(tracker.pendingEditTargets)
	tracker.currentDeliveryChecks = cloneStringSet(tracker.passedChecksAfterEdit)
	tracker.reworkedDeliveryCohorts = map[string]struct{}{}
	tracker.pendingEditCohorts = nil
	tracker.pendingEditTargets = nil
	tracker.testsAfterLatestEdit = false
	tracker.reviewAfterLatestEdit = false
	tracker.passedChecksAfterEdit = nil
	tracker.failedChecksAwaitingEdit = nil
	tracker.repairCandidateChecks = nil
	tracker.repairedChecksAfterEdit = nil
}

func (tracker *deliveryReworkTracker) observeReviewDrivenEdit(event normalizedSessionEvent) {
	matchedTargets := make([]string, 0, len(event.Targets))
	for _, target := range event.Targets {
		if _, delivered := tracker.currentDeliveryTargets[target]; delivered {
			matchedTargets = append(matchedTargets, target)
		}
	}
	tracker.reviewAwaitingEdit = false
	if len(matchedTargets) == 0 {
		return
	}
	cohorts := eventTargetCohorts(matchedTargets)
	tracker.metrics.PostDeliveryEditCalls++
	if tracker.metrics.ReworkLevers == nil {
		tracker.metrics.ReworkLevers = map[string]int{}
	}
	if tracker.metrics.ReworkScopes == nil {
		tracker.metrics.ReworkScopes = map[string]int{}
	}
	if tracker.metrics.ReworkTargets == nil {
		tracker.metrics.ReworkTargets = map[string]int{}
	}
	levers := map[string]struct{}{}
	scopes := map[string]struct{}{}
	for _, target := range matchedTargets {
		levers[reworkTargetLever(target)] = struct{}{}
		scopes[reworkTargetScope(target)] = struct{}{}
		tracker.metrics.ReworkTargets[target]++
	}
	if len(levers) == 0 {
		levers["unknown"] = struct{}{}
	}
	if len(scopes) == 0 {
		scopes["(unknown)"] = struct{}{}
	}
	for lever := range levers {
		tracker.metrics.ReworkLevers[lever]++
	}
	for scope := range scopes {
		tracker.metrics.ReworkScopes[scope]++
	}
	if !tracker.deliveryHadRework {
		tracker.metrics.DeliveriesWithRework++
		if tracker.currentDeliveryPreTests {
			tracker.metrics.ReworkedDeliveriesWithPreTests++
		}
		if tracker.currentDeliveryPreReview {
			tracker.metrics.ReworkedDeliveriesWithPreReview++
		}
		for check := range tracker.currentDeliveryChecks {
			metrics := tracker.verification(check)
			metrics.DeliveriesWithRework++
			tracker.metrics.VerificationChecks[check] = metrics
		}
		tracker.deliveryHadRework = true
	}
	for cohort := range cohorts {
		metrics := tracker.cohort(cohort)
		metrics.ReviewToEditCycles++
		_, belongedToDelivery := tracker.currentDeliveryCohorts[cohort]
		if _, seen := tracker.reworkedDeliveryCohorts[cohort]; belongedToDelivery && !seen {
			metrics.DeliveriesWithRework++
			if tracker.currentDeliveryPreTests {
				metrics.ReworkedDeliveriesWithPreTests++
			}
			if tracker.currentDeliveryPreReview {
				metrics.ReworkedDeliveriesWithPreReview++
			}
			for check := range tracker.currentDeliveryChecks {
				checkMetrics := cohortVerification(&metrics, check)
				checkMetrics.DeliveriesWithRework++
				metrics.VerificationChecks[check] = checkMetrics
			}
			tracker.reworkedDeliveryCohorts[cohort] = struct{}{}
		}
		tracker.metrics.Cohorts[cohort] = metrics
	}
	tracker.metrics.ReviewToEditCycles++
}

func cloneStringSet(values map[string]struct{}) map[string]struct{} {
	cloned := make(map[string]struct{}, len(values))
	for value := range values {
		cloned[value] = struct{}{}
	}
	return cloned
}

func (tracker *deliveryReworkTracker) cohort(name string) deliveryCohortMetrics {
	if tracker.metrics.Cohorts == nil {
		tracker.metrics.Cohorts = map[string]deliveryCohortMetrics{}
	}
	return tracker.metrics.Cohorts[name]
}

func (tracker *deliveryReworkTracker) verification(name string) verificationMetrics {
	if tracker.metrics.VerificationChecks == nil {
		tracker.metrics.VerificationChecks = map[string]verificationMetrics{}
	}
	return tracker.metrics.VerificationChecks[name]
}

func cohortVerification(cohort *deliveryCohortMetrics, name string) verificationMetrics {
	if cohort.VerificationChecks == nil {
		cohort.VerificationChecks = map[string]verificationMetrics{}
	}
	return cohort.VerificationChecks[name]
}

func eventTargetCohorts(targets []string) map[string]struct{} {
	cohorts := map[string]struct{}{}
	for _, target := range targets {
		cohorts[deliveryTargetCohort(target)] = struct{}{}
	}
	if len(cohorts) == 0 {
		cohorts["(unknown)"] = struct{}{}
	}
	return cohorts
}

func deliveryTargetCohort(target string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(target)), "/")
	prefix := []string{}
	switch {
	case len(parts) >= 4 && parts[0] == ".workbench" && parts[1] == "repos":
		prefix = append(prefix, parts[2])
		parts = parts[3:]
	case len(parts) >= 3 && parts[0] == ".worktrees":
		parts = parts[2:]
	case len(parts) >= 5 && parts[0] == ".workbench" && parts[1] == "worktrees":
		prefix = append(prefix, parts[3])
		parts = parts[4:]
	}
	if len(parts) == 0 || parts[0] == "." {
		return strings.Join(append(prefix, "(root)"), "/")
	}
	if len(parts) == 1 {
		return strings.Join(append(prefix, "(root)"), "/")
	}
	depth := 1
	if len(parts) >= 3 {
		depth = 2
	}
	return strings.Join(append(prefix, parts[:depth]...), "/")
}

func deliveryTargetLabel(target string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(target)), "/")
	switch {
	case len(parts) >= 4 && parts[0] == ".workbench" && parts[1] == "repos":
		parts = parts[2:]
	case len(parts) >= 4 && parts[0] == ".worktrees":
		parts = parts[2:]
	case len(parts) >= 5 && parts[0] == ".workbench" && parts[1] == "worktrees":
		parts = parts[3:]
	}
	return strings.Join(parts, "/")
}

func testOperation(event normalizedSessionEvent, operations []string) bool {
	if event.Family == "tests" {
		return true
	}
	for _, operation := range operations {
		name := operation[strings.LastIndex(operation, "/")+1:]
		if name == "test" || strings.HasPrefix(name, "test-") {
			return true
		}
	}
	return false
}

func verificationCheckIDs(event normalizedSessionEvent, operations []string) []string {
	var checks []string
	for _, operation := range operations {
		name := operation[strings.LastIndex(operation, "/")+1:]
		if name == "test" || strings.HasPrefix(name, "test-") {
			checks = appendUniqueString(checks, operation)
		}
	}
	if len(checks) == 0 && event.Family == "tests" {
		checks = append(checks, "tests")
	}
	sort.Strings(checks)
	return checks
}

func reworkTargetLever(target string) string {
	target = leverTargetPath(target)
	switch {
	case instructionTarget(target):
		return "instructions/docs"
	case toolingTarget(target):
		return "tooling"
	default:
		return "source code"
	}
}

func reworkTargetScope(target string) string {
	parts := strings.Split(filepath.ToSlash(target), "/")
	if len(parts) >= 3 && parts[0] == ".workbench" && parts[1] == "repos" {
		return parts[2]
	}
	return "(root)"
}

func deliveryOperation(event normalizedSessionEvent, operations []string) bool {
	if event.Family == "delivery" {
		return true
	}
	for _, operation := range operations {
		if strings.HasSuffix(operation, "/pr") || strings.HasSuffix(operation, "/worktree-land") {
			return true
		}
	}
	return false
}

func reviewStart(event normalizedSessionEvent, operations []string) bool {
	if event.Kind != sessionEventToolCall || event.ToolName == "wait" || event.ToolName == "write_stdin" {
		return false
	}
	if event.Family == "review" {
		return true
	}
	return reviewOperation(event, operations)
}

func reviewOperation(event normalizedSessionEvent, operations []string) bool {
	if event.Family == "review" {
		return true
	}
	for _, operation := range operations {
		if strings.HasSuffix(operation, "/comments-resolve") {
			return false
		}
	}
	for _, operation := range operations {
		if strings.HasSuffix(operation, "/comments") || strings.HasSuffix(operation, "/comments-wait") {
			return true
		}
	}
	return false
}

func addDeliveryReworkMetrics(target *deliveryReworkMetrics, addition deliveryReworkMetrics) {
	target.Deliveries += addition.Deliveries
	target.DeliveriesWithPreTests += addition.DeliveriesWithPreTests
	target.DeliveriesWithPreReview += addition.DeliveriesWithPreReview
	target.PostDeliveryReviewChecks += addition.PostDeliveryReviewChecks
	target.ReviewToEditCycles += addition.ReviewToEditCycles
	target.DeliveriesWithRework += addition.DeliveriesWithRework
	target.ReworkedDeliveriesWithPreTests += addition.ReworkedDeliveriesWithPreTests
	target.ReworkedDeliveriesWithPreReview += addition.ReworkedDeliveriesWithPreReview
	target.PostDeliveryEditCalls += addition.PostDeliveryEditCalls
	target.ReviewOutputBytes += addition.ReviewOutputBytes
	target.Sessions += addition.Sessions
	if target.ReworkLevers == nil {
		target.ReworkLevers = map[string]int{}
	}
	for lever, count := range addition.ReworkLevers {
		target.ReworkLevers[lever] += count
	}
	if target.ReworkScopes == nil {
		target.ReworkScopes = map[string]int{}
	}
	for scope, count := range addition.ReworkScopes {
		target.ReworkScopes[scope] += count
	}
	if target.ReworkTargets == nil {
		target.ReworkTargets = map[string]int{}
	}
	for name, count := range addition.ReworkTargets {
		target.ReworkTargets[name] += count
	}
	if target.Cohorts == nil {
		target.Cohorts = map[string]deliveryCohortMetrics{}
	}
	for cohort, metrics := range addition.Cohorts {
		current := target.Cohorts[cohort]
		addDeliveryCohortMetrics(&current, metrics)
		target.Cohorts[cohort] = current
	}
	addVerificationMetricsMap(&target.VerificationChecks, addition.VerificationChecks)
}

func addDeliveryCohortMetrics(target *deliveryCohortMetrics, addition deliveryCohortMetrics) {
	target.Deliveries += addition.Deliveries
	target.DeliveriesWithPreTests += addition.DeliveriesWithPreTests
	target.DeliveriesWithPreReview += addition.DeliveriesWithPreReview
	target.DeliveriesWithRework += addition.DeliveriesWithRework
	target.ReworkedDeliveriesWithPreTests += addition.ReworkedDeliveriesWithPreTests
	target.ReworkedDeliveriesWithPreReview += addition.ReworkedDeliveriesWithPreReview
	target.ReviewToEditCycles += addition.ReviewToEditCycles
	addVerificationMetricsMap(&target.VerificationChecks, addition.VerificationChecks)
}

func addVerificationMetricsMap(target *map[string]verificationMetrics, addition map[string]verificationMetrics) {
	if *target == nil {
		*target = map[string]verificationMetrics{}
	}
	for check, metrics := range addition {
		current := (*target)[check]
		current.Deliveries += metrics.Deliveries
		current.DeliveriesWithRework += metrics.DeliveriesWithRework
		current.FailedRuns += metrics.FailedRuns
		current.FailFixPassDeliveries += metrics.FailFixPassDeliveries
		(*target)[check] = current
	}
}

func (episode *codexTaskEpisode) observe(event normalizedSessionEvent, tokenIncrement codexTokenUsage, operations []string) {
	if episode.StartedAt.IsZero() || event.OccurredAt.Before(episode.StartedAt) {
		episode.StartedAt = event.OccurredAt
	}
	if episode.EndedAt.IsZero() || event.OccurredAt.After(episode.EndedAt) {
		episode.EndedAt = event.OccurredAt
	}
	switch event.Kind {
	case sessionEventToken:
		addCodexTokenUsage(&episode.Tokens, tokenIncrement)
	case sessionEventToolCall:
		episode.ToolCalls++
		if event.Family != "" {
			if episode.Families == nil {
				episode.Families = map[string]int{}
			}
			episode.Families[event.Family]++
		}
		if len(operations) > 0 {
			if episode.OwnedOperations == nil {
				episode.OwnedOperations = map[string]int{}
			}
			for _, operation := range operations {
				episode.OwnedOperations[operation]++
			}
		}
		if len(event.Targets) > 0 {
			if episode.TargetCohorts == nil {
				episode.TargetCohorts = map[string]int{}
			}
			var sourceTargets []string
			for _, target := range event.Targets {
				if taskCostSourceTarget(target) {
					sourceTargets = append(sourceTargets, target)
				}
			}
			for cohort := range eventTargetCohorts(sourceTargets) {
				if cohort != "(unknown)" {
					episode.TargetCohorts[cohort]++
				}
			}
		}
	case sessionEventToolOutput:
		episode.ToolOutputBytes += event.OutputBytes
		if event.Failed {
			episode.FailedCalls++
		}
	case sessionEventCompaction:
		episode.Compactions++
	case sessionEventComplete:
		episode.Completed = true
	}
}

type outcomeDistribution struct {
	Count int   `json:"count"`
	P50   int64 `json:"p50"`
	P75   int64 `json:"p75"`
	P90   int64 `json:"p90"`
	Max   int64 `json:"max"`
}

type completionEpisodeAnalysis struct {
	Completed                int                 `json:"completed"`
	FullyObservedCompleted   int                 `json:"fullyObservedCompleted"`
	LeftCensoredCompleted    int                 `json:"leftCensoredCompleted"`
	ToolUsingCompleted       int                 `json:"toolUsingCompleted"`
	ResponseOnlyCompleted    int                 `json:"responseOnlyCompleted"`
	Incomplete               int                 `json:"incomplete"`
	FreshTokens              outcomeDistribution `json:"freshTokens"`
	ToolCalls                outcomeDistribution `json:"toolCalls"`
	VisibleOutputTokens      outcomeDistribution `json:"visibleOutputTokens"`
	DurationSeconds          outcomeDistribution `json:"durationSeconds"`
	FailedCalls              outcomeDistribution `json:"failedCalls"`
	Compactions              outcomeDistribution `json:"compactions"`
	TopDecileFreshTokenShare float64             `json:"topDecileFreshTokenShare"`
	TailDrivers              taskCostTailDrivers `json:"tailDrivers"`
}

type taskCostTailDrivers struct {
	TailEpisodes     int                  `json:"tailEpisodes"`
	OrdinaryEpisodes int                  `json:"ordinaryEpisodes"`
	Families         []taskCostTailDriver `json:"families,omitempty"`
	OwnedOperations  []taskCostTailDriver `json:"ownedOperations,omitempty"`
	TargetCohorts    []taskCostTailDriver `json:"targetCohorts,omitempty"`
}

type taskCostTailDriver struct {
	Name             string  `json:"name"`
	TailEpisodes     int     `json:"tailEpisodes"`
	OrdinaryEpisodes int     `json:"ordinaryEpisodes"`
	TailCalls        int     `json:"tailCalls"`
	OrdinaryCalls    int     `json:"ordinaryCalls"`
	PrevalenceDelta  float64 `json:"prevalenceDelta"`
	PrevalenceLift   float64 `json:"prevalenceLift"`
}

func analyzeCompletionEpisodes(episodes []codexTaskEpisode) completionEpisodeAnalysis {
	analysis := completionEpisodeAnalysis{}
	var freshTokens, toolCalls, outputTokens, durations, failures, compactions []int64
	var eligible []codexTaskEpisode
	for _, episode := range episodes {
		if !episode.Completed {
			analysis.Incomplete++
			continue
		}
		analysis.Completed++
		if episode.LeftCensored {
			analysis.LeftCensoredCompleted++
			continue
		}
		analysis.FullyObservedCompleted++
		if episode.ToolCalls == 0 {
			analysis.ResponseOnlyCompleted++
			continue
		}
		analysis.ToolUsingCompleted++
		eligible = append(eligible, episode)
		freshTokens = append(freshTokens, episode.Tokens.UncachedInputTokens+episode.Tokens.OutputTokens)
		toolCalls = append(toolCalls, int64(episode.ToolCalls))
		outputTokens = append(outputTokens, estimatedTokens(episode.ToolOutputBytes))
		durations = append(durations, taskEpisodeDuration(episode))
		failures = append(failures, int64(episode.FailedCalls))
		compactions = append(compactions, int64(episode.Compactions))
	}
	analysis.FreshTokens = summarizeOutcomeDistribution(freshTokens)
	analysis.ToolCalls = summarizeOutcomeDistribution(toolCalls)
	analysis.VisibleOutputTokens = summarizeOutcomeDistribution(outputTokens)
	analysis.DurationSeconds = summarizeOutcomeDistribution(durations)
	analysis.FailedCalls = summarizeOutcomeDistribution(failures)
	analysis.Compactions = summarizeOutcomeDistribution(compactions)
	analysis.TopDecileFreshTokenShare = topOutcomeShare(freshTokens, 0.10)
	analysis.TailDrivers = analyzeTaskCostTailDrivers(eligible)
	return analysis
}

func analyzeTaskCostTailDrivers(episodes []codexTaskEpisode) taskCostTailDrivers {
	drivers := taskCostTailDrivers{}
	if len(episodes) < 10 {
		return drivers
	}
	ranked := append([]codexTaskEpisode(nil), episodes...)
	sort.Slice(ranked, func(i, j int) bool {
		iTokens := ranked[i].Tokens.UncachedInputTokens + ranked[i].Tokens.OutputTokens
		jTokens := ranked[j].Tokens.UncachedInputTokens + ranked[j].Tokens.OutputTokens
		return iTokens > jTokens
	})
	drivers.TailEpisodes = max(1, int(math.Ceil(0.10*float64(len(ranked)))))
	drivers.OrdinaryEpisodes = len(ranked) - drivers.TailEpisodes
	tail := ranked[:drivers.TailEpisodes]
	ordinary := ranked[drivers.TailEpisodes:]
	drivers.Families = taskCostTailDimension(tail, ordinary, func(episode codexTaskEpisode) map[string]int {
		return episode.Families
	})
	drivers.OwnedOperations = taskCostTailDimension(tail, ordinary, func(episode codexTaskEpisode) map[string]int {
		return episode.OwnedOperations
	})
	drivers.TargetCohorts = taskCostTailDimension(tail, ordinary, func(episode codexTaskEpisode) map[string]int {
		return episode.TargetCohorts
	})
	return drivers
}

func taskCostTailDimension(
	tail,
	ordinary []codexTaskEpisode,
	values func(codexTaskEpisode) map[string]int,
) []taskCostTailDriver {
	type counts struct {
		tailEpisodes     int
		ordinaryEpisodes int
		tailCalls        int
		ordinaryCalls    int
	}
	byName := map[string]counts{}
	for _, episode := range tail {
		for name, calls := range values(episode) {
			current := byName[name]
			current.tailEpisodes++
			current.tailCalls += calls
			byName[name] = current
		}
	}
	for _, episode := range ordinary {
		for name, calls := range values(episode) {
			current := byName[name]
			current.ordinaryEpisodes++
			current.ordinaryCalls += calls
			byName[name] = current
		}
	}
	var drivers []taskCostTailDriver
	for name, current := range byName {
		if current.tailEpisodes < 3 {
			continue
		}
		tailRate := ratio(float64(current.tailEpisodes), float64(len(tail)))
		ordinaryRate := ratio(float64(current.ordinaryEpisodes), float64(len(ordinary)))
		if tailRate-ordinaryRate < 0.10 {
			continue
		}
		lift := tailRate / math.Max(ordinaryRate, 1/float64(max(1, len(ordinary))))
		if lift < 1.5 {
			continue
		}
		drivers = append(drivers, taskCostTailDriver{
			Name:             name,
			TailEpisodes:     current.tailEpisodes,
			OrdinaryEpisodes: current.ordinaryEpisodes,
			TailCalls:        current.tailCalls,
			OrdinaryCalls:    current.ordinaryCalls,
			PrevalenceDelta:  tailRate - ordinaryRate,
			PrevalenceLift:   lift,
		})
	}
	sort.Slice(drivers, func(i, j int) bool {
		if drivers[i].PrevalenceDelta != drivers[j].PrevalenceDelta {
			return drivers[i].PrevalenceDelta > drivers[j].PrevalenceDelta
		}
		if drivers[i].PrevalenceLift != drivers[j].PrevalenceLift {
			return drivers[i].PrevalenceLift > drivers[j].PrevalenceLift
		}
		return drivers[i].Name < drivers[j].Name
	})
	if len(drivers) > 3 {
		drivers = drivers[:3]
	}
	return drivers
}

func taskCostSourceTarget(target string) bool {
	if reworkTargetLever(target) != "source code" {
		return false
	}
	switch strings.ToLower(filepath.Ext(target)) {
	case ".clj", ".cljc", ".cljs", ".go", ".java", ".kt", ".kts", ".py",
		".rb", ".rs", ".js", ".jsx", ".ts", ".tsx", ".vue", ".svelte",
		".css", ".scss", ".sql":
		return true
	default:
		return false
	}
}

func summarizeOutcomeDistribution(values []int64) outcomeDistribution {
	if len(values) == 0 {
		return outcomeDistribution{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return outcomeDistribution{
		Count: len(sorted),
		P50:   nearestRank(sorted, 0.50),
		P75:   nearestRank(sorted, 0.75),
		P90:   nearestRank(sorted, 0.90),
		Max:   sorted[len(sorted)-1],
	}
}

func nearestRank(sorted []int64, percentile float64) int64 {
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	index = max(0, min(index, len(sorted)-1))
	return sorted[index]
}

func topOutcomeShare(values []int64, fraction float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] > sorted[j] })
	count := max(1, int(math.Ceil(fraction*float64(len(sorted)))))
	var top, total int64
	for index, value := range sorted {
		total += value
		if index < count {
			top += value
		}
	}
	if total <= 0 {
		return 0
	}
	return float64(top) / float64(total)
}

func taskEpisodeDuration(episode codexTaskEpisode) int64 {
	if episode.StartedAt.IsZero() || episode.EndedAt.Before(episode.StartedAt) {
		return 0
	}
	return int64(episode.EndedAt.Sub(episode.StartedAt).Seconds())
}

func printCompletionEpisodeAnalysis(analysis completionEpisodeAnalysis) {
	if analysis.Completed == 0 && analysis.Incomplete == 0 {
		return
	}
	fmt.Printf(
		"Completion episodes: %s tool-using, %s response-only, %s left-censored, %s incomplete\n",
		formatCodexCount(int64(analysis.ToolUsingCompleted)),
		formatCodexCount(int64(analysis.ResponseOnlyCompleted)),
		formatCodexCount(int64(analysis.LeftCensoredCompleted)),
		formatCodexCount(int64(analysis.Incomplete)),
	)
	if analysis.ToolUsingCompleted == 0 {
		return
	}
	fmt.Printf(
		"Completed tool-task outcomes p50/p75/p90: fresh tokens %s/%s/%s; tool calls %s/%s/%s; duration %s/%s/%s\n",
		formatCodexCount(analysis.FreshTokens.P50),
		formatCodexCount(analysis.FreshTokens.P75),
		formatCodexCount(analysis.FreshTokens.P90),
		formatCodexCount(analysis.ToolCalls.P50),
		formatCodexCount(analysis.ToolCalls.P75),
		formatCodexCount(analysis.ToolCalls.P90),
		formatDurationSeconds(analysis.DurationSeconds.P50),
		formatDurationSeconds(analysis.DurationSeconds.P75),
		formatDurationSeconds(analysis.DurationSeconds.P90),
	)
	if drivers := formatTaskCostTailDrivers(analysis.TailDrivers); drivers != "" {
		fmt.Printf("Fresh-token tail associations: %s\n", drivers)
	}
}

func formatTaskCostTailDrivers(drivers taskCostTailDrivers) string {
	var parts []string
	for _, dimension := range []struct {
		label   string
		drivers []taskCostTailDriver
	}{
		{label: "cohorts", drivers: drivers.TargetCohorts},
		{label: "operations", drivers: drivers.OwnedOperations},
		{label: "families", drivers: drivers.Families},
	} {
		if len(dimension.drivers) == 0 {
			continue
		}
		rendered := make([]string, 0, len(dimension.drivers))
		for _, driver := range dimension.drivers {
			rendered = append(rendered, fmt.Sprintf(
				"%s %s/%s tail vs %s/%s ordinary (+%.0fpp, %.1fx; %s tail calls)",
				driver.Name,
				formatCodexCount(int64(driver.TailEpisodes)),
				formatCodexCount(int64(drivers.TailEpisodes)),
				formatCodexCount(int64(driver.OrdinaryEpisodes)),
				formatCodexCount(int64(drivers.OrdinaryEpisodes)),
				100*driver.PrevalenceDelta,
				driver.PrevalenceLift,
				formatCodexCount(int64(driver.TailCalls)),
			))
		}
		parts = append(parts, dimension.label+" "+strings.Join(rendered, ", "))
	}
	return strings.Join(parts, "; ")
}

func dominantTaskCostTailDriver(drivers taskCostTailDrivers) (kind string, driver taskCostTailDriver) {
	for _, dimension := range []struct {
		kind    string
		drivers []taskCostTailDriver
	}{
		{kind: "operation", drivers: drivers.OwnedOperations},
		{kind: "cohort", drivers: drivers.TargetCohorts},
		{kind: "family", drivers: drivers.Families},
	} {
		if len(dimension.drivers) > 0 {
			return dimension.kind, dimension.drivers[0]
		}
	}
	return "", taskCostTailDriver{}
}

func printDeliveryReworkAnalysis(metrics deliveryReworkMetrics) {
	if metrics.Deliveries == 0 && metrics.PostDeliveryReviewChecks == 0 {
		return
	}
	reworkRate := 100 * ratio(float64(metrics.DeliveriesWithRework), float64(metrics.Deliveries))
	fmt.Printf(
		"Delivery quality: %s deliveries, %s with post-delivery edits (%.0f%%), %s review→edit cycles, %s post-delivery review checks\n",
		formatCodexCount(int64(metrics.Deliveries)),
		formatCodexCount(int64(metrics.DeliveriesWithRework)),
		reworkRate,
		formatCodexCount(int64(metrics.ReviewToEditCycles)),
		formatCodexCount(int64(metrics.PostDeliveryReviewChecks)),
	)
	if metrics.Deliveries > 0 {
		fmt.Printf(
			"Pre-delivery evidence after latest edit: tests %s/%s deliveries; review %s/%s deliveries\n",
			formatCodexCount(int64(metrics.DeliveriesWithPreTests)),
			formatCodexCount(int64(metrics.Deliveries)),
			formatCodexCount(int64(metrics.DeliveriesWithPreReview)),
			formatCodexCount(int64(metrics.Deliveries)),
		)
	}
	if metrics.PostDeliveryEditCalls > 0 {
		fmt.Printf(
			"Post-delivery edit attribution: levers %s; scopes %s\n",
			formatMetricDimensions(metrics.ReworkLevers),
			formatMetricDimensions(metrics.ReworkScopes),
		)
		if cohorts := formatDeliveryReworkCohorts(metrics.Cohorts, 3); cohorts != "" {
			fmt.Printf("Top delivery cohorts: %s\n", cohorts)
		}
		if len(metrics.ReworkTargets) > 0 {
			fmt.Printf("Top rework targets: %s\n", formatDeliveryReworkTargets(metrics.ReworkTargets))
		}
	}
	if checks := formatVerificationChecks(
		metrics.Deliveries,
		metrics.DeliveriesWithRework,
		metrics.VerificationChecks,
		3,
	); checks != "" {
		fmt.Printf("Verification effectiveness: %s\n", checks)
	}
}

func formatVerificationChecks(
	totalDeliveries,
	totalRework int,
	checks map[string]verificationMetrics,
	limit int,
) string {
	type row struct {
		name    string
		metrics verificationMetrics
	}
	rows := make([]row, 0, len(checks))
	for name, metrics := range checks {
		if metrics.Deliveries == 0 {
			continue
		}
		rows = append(rows, row{name: name, metrics: metrics})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].metrics.Deliveries != rows[j].metrics.Deliveries {
			return rows[i].metrics.Deliveries > rows[j].metrics.Deliveries
		}
		if rows[i].metrics.FailFixPassDeliveries != rows[j].metrics.FailFixPassDeliveries {
			return rows[i].metrics.FailFixPassDeliveries > rows[j].metrics.FailFixPassDeliveries
		}
		return rows[i].name < rows[j].name
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, row.name+" "+verificationCheckComparison(
			totalDeliveries,
			totalRework,
			row.metrics,
		))
	}
	return strings.Join(parts, "; ")
}

func formatDeliveryReworkTargets(targets map[string]int) string {
	labels := make(map[string]int, len(targets))
	for target, count := range targets {
		labels[deliveryTargetLabel(target)] += count
	}
	return formatMetricDimensions(labels)
}

func formatDeliveryReworkCohorts(cohorts map[string]deliveryCohortMetrics, limit int) string {
	type row struct {
		name    string
		metrics deliveryCohortMetrics
	}
	rows := make([]row, 0, len(cohorts))
	for name, metrics := range cohorts {
		if metrics.ReviewToEditCycles == 0 || name == "(unknown)" {
			continue
		}
		rows = append(rows, row{name: name, metrics: metrics})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].metrics.ReviewToEditCycles != rows[j].metrics.ReviewToEditCycles {
			return rows[i].metrics.ReviewToEditCycles > rows[j].metrics.ReviewToEditCycles
		}
		return rows[i].name < rows[j].name
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		testEvidence := fmt.Sprintf(
			"tests %s/%s",
			formatCodexCount(int64(row.metrics.DeliveriesWithPreTests)),
			formatCodexCount(int64(row.metrics.Deliveries)),
		)
		if comparison := deliveryTestComparison(row.metrics); comparison != "" {
			testEvidence = comparison
		}
		parts = append(parts, fmt.Sprintf(
			"%s %s/%s reworked, %s cycles, %s",
			row.name,
			formatCodexCount(int64(row.metrics.DeliveriesWithRework)),
			formatCodexCount(int64(row.metrics.Deliveries)),
			formatCodexCount(int64(row.metrics.ReviewToEditCycles)),
			testEvidence,
		))
	}
	return strings.Join(parts, "; ")
}
