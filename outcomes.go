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
	StartedAt         time.Time
	EndedAt           time.Time
	Completed         bool
	LeftCensored      bool
	Tokens            codexTokenUsage
	ToolCalls         int
	FailedCalls       int
	Compactions       int
	ToolOutputBytes   int64
	Families          map[string]int
	OwnedOperations   map[string]int
	Targets           map[string]int
	TargetCohorts     map[string]int
	Phases            map[string]taskPhaseCost
	currentPhase      string
	lastObservedAt    time.Time
	delivered         bool
	reworkActive      bool
	editSinceDelivery bool
}

type taskPhaseCost struct {
	Tokens          codexTokenUsage
	ToolCalls       int
	FailedCalls     int
	Compactions     int
	ToolOutputBytes int64
	DurationSeconds int64
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

type downstreamQualityMetrics struct {
	Deliveries                        int                                `json:"deliveries"`
	DeliveriesWithPreTests            int                                `json:"deliveriesWithPreTests"`
	DeliveriesWithFailure             int                                `json:"deliveriesWithFailure"`
	FailedDeliveriesWithPreTests      int                                `json:"failedDeliveriesWithPreTests"`
	FailureRuns                       int                                `json:"failureRuns"`
	FollowUpEditCycles                int                                `json:"followUpEditCycles"`
	RedeliveryAttempts                int                                `json:"redeliveryAttempts"`
	RecoveredDeliveries               int                                `json:"recoveredDeliveries"`
	Reverts                           int                                `json:"reverts"`
	Sessions                          int                                `json:"sessions"`
	FailureChecks                     map[string]int                     `json:"failureChecks,omitempty"`
	PreDeliveryChecks                 map[string]downstreamCheckMetrics  `json:"preDeliveryChecks,omitempty"`
	Cohorts                           map[string]downstreamCohortMetrics `json:"cohorts,omitempty"`
	TimeToFailureSeconds              outcomeDistribution                `json:"timeToFailureSeconds"`
	TimeToRecoverySeconds             outcomeDistribution                `json:"timeToRecoverySeconds"`
	timeToFailureSecondsObservations  []int64
	timeToRecoverySecondsObservations []int64
}

type downstreamCheckMetrics struct {
	Deliveries            int `json:"deliveries"`
	DeliveriesWithFailure int `json:"deliveriesWithFailure"`
}

type downstreamCohortMetrics struct {
	Deliveries                   int `json:"deliveries"`
	DeliveriesWithPreTests       int `json:"deliveriesWithPreTests"`
	DeliveriesWithFailure        int `json:"deliveriesWithFailure"`
	FailedDeliveriesWithPreTests int `json:"failedDeliveriesWithPreTests"`
	FailureRuns                  int `json:"failureRuns"`
	FollowUpEditCycles           int `json:"followUpEditCycles"`
	RedeliveryAttempts           int `json:"redeliveryAttempts"`
	RecoveredDeliveries          int `json:"recoveredDeliveries"`
	Reverts                      int `json:"reverts"`
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

type downstreamQualityTracker struct {
	metrics                 downstreamQualityMetrics
	delivered               bool
	pendingEditCohorts      map[string]struct{}
	pendingEditTargets      map[string]struct{}
	testsAfterLatestEdit    bool
	passedChecksAfterEdit   map[string]struct{}
	currentDeliveryAt       time.Time
	currentDeliveryCohorts  map[string]struct{}
	currentDeliveryTargets  map[string]struct{}
	currentDeliveryPreTests bool
	currentDeliveryChecks   map[string]struct{}
	editSinceDelivery       bool
	currentDeliveryFailed   bool
	failureActive           bool
	failureAt               time.Time
	failureChecks           map[string]struct{}
	recoveryEditSeen        bool
	recoveryPassedChecks    map[string]struct{}
	recoveryCohorts         map[string]struct{}
}

func (tracker *downstreamQualityTracker) observe(event normalizedSessionEvent, operations []string) {
	if event.Kind == sessionEventToolCall && event.ToolName == "apply_patch" {
		tracker.observeEdit(event)
		return
	}
	if event.Kind != sessionEventToolOutput || event.OperationContinues {
		return
	}
	if !event.Failed && revertOperation(event) {
		tracker.observeRevert(event)
		return
	}
	if checks := downstreamCheckIDs(event, operations); len(checks) > 0 {
		tracker.observeCheck(event, checks)
	}
	if !event.Failed && deliveryOperation(event, operations) {
		tracker.observeDelivery(event)
	}
}

func (tracker *downstreamQualityTracker) observeEdit(event normalizedSessionEvent) {
	if tracker.delivered {
		tracker.editSinceDelivery = true
	}
	matchedTargets := matchingTargets(event.Targets, tracker.currentDeliveryTargets)
	if tracker.failureActive && len(matchedTargets) > 0 {
		tracker.metrics.FollowUpEditCycles++
		tracker.recoveryEditSeen = true
		tracker.recoveryPassedChecks = nil
		if tracker.recoveryCohorts == nil {
			tracker.recoveryCohorts = map[string]struct{}{}
		}
		for cohort := range eventTargetCohorts(matchedTargets) {
			tracker.recoveryCohorts[cohort] = struct{}{}
			metrics := tracker.cohort(cohort)
			metrics.FollowUpEditCycles++
			tracker.metrics.Cohorts[cohort] = metrics
		}
	}
	if tracker.pendingEditCohorts == nil {
		tracker.pendingEditCohorts = map[string]struct{}{}
	}
	for cohort := range eventTargetCohorts(event.Targets) {
		tracker.pendingEditCohorts[cohort] = struct{}{}
	}
	if tracker.pendingEditTargets == nil {
		tracker.pendingEditTargets = map[string]struct{}{}
	}
	for _, target := range event.Targets {
		tracker.pendingEditTargets[target] = struct{}{}
	}
	tracker.testsAfterLatestEdit = false
	tracker.passedChecksAfterEdit = nil
}

func (tracker *downstreamQualityTracker) observeCheck(event normalizedSessionEvent, checks []string) {
	if event.Failed {
		if tracker.delivered && (!tracker.editSinceDelivery || tracker.failureActive) {
			tracker.observeFailure(event, checks)
		}
		return
	}
	if len(tracker.pendingEditTargets) > 0 {
		tracker.testsAfterLatestEdit = true
		if tracker.passedChecksAfterEdit == nil {
			tracker.passedChecksAfterEdit = map[string]struct{}{}
		}
		for _, check := range checks {
			tracker.passedChecksAfterEdit[check] = struct{}{}
		}
	}
	if !tracker.failureActive || !tracker.recoveryEditSeen {
		return
	}
	if tracker.recoveryPassedChecks == nil {
		tracker.recoveryPassedChecks = map[string]struct{}{}
	}
	for _, check := range checks {
		if _, failed := tracker.failureChecks[check]; failed {
			tracker.recoveryPassedChecks[check] = struct{}{}
		}
	}
}

func (tracker *downstreamQualityTracker) observeFailure(event normalizedSessionEvent, checks []string) {
	tracker.metrics.FailureRuns++
	for _, check := range checks {
		if tracker.metrics.FailureChecks == nil {
			tracker.metrics.FailureChecks = map[string]int{}
		}
		tracker.metrics.FailureChecks[check]++
	}
	for cohort := range tracker.currentDeliveryCohorts {
		metrics := tracker.cohort(cohort)
		metrics.FailureRuns++
		tracker.metrics.Cohorts[cohort] = metrics
	}
	if !tracker.currentDeliveryFailed {
		tracker.currentDeliveryFailed = true
		tracker.metrics.DeliveriesWithFailure++
		if tracker.currentDeliveryPreTests {
			tracker.metrics.FailedDeliveriesWithPreTests++
		}
		for check := range tracker.currentDeliveryChecks {
			metrics := tracker.preDeliveryCheck(check)
			metrics.DeliveriesWithFailure++
			tracker.metrics.PreDeliveryChecks[check] = metrics
		}
		for cohort := range tracker.currentDeliveryCohorts {
			metrics := tracker.cohort(cohort)
			metrics.DeliveriesWithFailure++
			if tracker.currentDeliveryPreTests {
				metrics.FailedDeliveriesWithPreTests++
			}
			tracker.metrics.Cohorts[cohort] = metrics
		}
		if !tracker.currentDeliveryAt.IsZero() && event.OccurredAt.After(tracker.currentDeliveryAt) {
			tracker.metrics.timeToFailureSecondsObservations = append(
				tracker.metrics.timeToFailureSecondsObservations,
				int64(event.OccurredAt.Sub(tracker.currentDeliveryAt).Seconds()),
			)
		}
	}
	if !tracker.failureActive {
		tracker.failureAt = event.OccurredAt
		tracker.failureChecks = map[string]struct{}{}
	}
	for _, check := range checks {
		tracker.failureChecks[check] = struct{}{}
	}
	tracker.failureActive = true
}

func (tracker *downstreamQualityTracker) observeRevert(event normalizedSessionEvent) {
	if !tracker.delivered || tracker.editSinceDelivery {
		return
	}
	tracker.metrics.Reverts++
	for cohort := range tracker.currentDeliveryCohorts {
		metrics := tracker.cohort(cohort)
		metrics.Reverts++
		tracker.metrics.Cohorts[cohort] = metrics
	}
	if tracker.currentDeliveryFailed {
		return
	}
	tracker.currentDeliveryFailed = true
	tracker.metrics.DeliveriesWithFailure++
	if tracker.currentDeliveryPreTests {
		tracker.metrics.FailedDeliveriesWithPreTests++
	}
	for check := range tracker.currentDeliveryChecks {
		metrics := tracker.preDeliveryCheck(check)
		metrics.DeliveriesWithFailure++
		tracker.metrics.PreDeliveryChecks[check] = metrics
	}
	for cohort := range tracker.currentDeliveryCohorts {
		metrics := tracker.cohort(cohort)
		metrics.DeliveriesWithFailure++
		if tracker.currentDeliveryPreTests {
			metrics.FailedDeliveriesWithPreTests++
		}
		tracker.metrics.Cohorts[cohort] = metrics
	}
	if !tracker.currentDeliveryAt.IsZero() && event.OccurredAt.After(tracker.currentDeliveryAt) {
		tracker.metrics.timeToFailureSecondsObservations = append(
			tracker.metrics.timeToFailureSecondsObservations,
			int64(event.OccurredAt.Sub(tracker.currentDeliveryAt).Seconds()),
		)
	}
}

func (tracker *downstreamQualityTracker) observeDelivery(event normalizedSessionEvent) {
	if tracker.failureActive && tracker.recoveryEditSeen {
		tracker.metrics.RedeliveryAttempts++
		for cohort := range tracker.recoveryCohorts {
			metrics := tracker.cohort(cohort)
			metrics.RedeliveryAttempts++
			tracker.metrics.Cohorts[cohort] = metrics
		}
		if setContainsAll(tracker.recoveryPassedChecks, tracker.failureChecks) {
			tracker.metrics.RecoveredDeliveries++
			for cohort := range tracker.recoveryCohorts {
				metrics := tracker.cohort(cohort)
				metrics.RecoveredDeliveries++
				tracker.metrics.Cohorts[cohort] = metrics
			}
			if !tracker.failureAt.IsZero() && event.OccurredAt.After(tracker.failureAt) {
				tracker.metrics.timeToRecoverySecondsObservations = append(
					tracker.metrics.timeToRecoverySecondsObservations,
					int64(event.OccurredAt.Sub(tracker.failureAt).Seconds()),
				)
			}
		}
	}

	tracker.metrics.Deliveries++
	if tracker.testsAfterLatestEdit {
		tracker.metrics.DeliveriesWithPreTests++
	}
	for cohort := range tracker.pendingEditCohorts {
		metrics := tracker.cohort(cohort)
		metrics.Deliveries++
		if tracker.testsAfterLatestEdit {
			metrics.DeliveriesWithPreTests++
		}
		tracker.metrics.Cohorts[cohort] = metrics
	}
	for check := range tracker.passedChecksAfterEdit {
		metrics := tracker.preDeliveryCheck(check)
		metrics.Deliveries++
		tracker.metrics.PreDeliveryChecks[check] = metrics
	}

	tracker.delivered = true
	tracker.currentDeliveryAt = event.OccurredAt
	tracker.currentDeliveryCohorts = cloneStringSet(tracker.pendingEditCohorts)
	tracker.currentDeliveryTargets = cloneStringSet(tracker.pendingEditTargets)
	tracker.currentDeliveryPreTests = tracker.testsAfterLatestEdit
	tracker.currentDeliveryChecks = cloneStringSet(tracker.passedChecksAfterEdit)
	tracker.pendingEditCohorts = nil
	tracker.pendingEditTargets = nil
	tracker.testsAfterLatestEdit = false
	tracker.passedChecksAfterEdit = nil
	tracker.editSinceDelivery = false
	tracker.currentDeliveryFailed = false
	tracker.failureActive = false
	tracker.failureAt = time.Time{}
	tracker.failureChecks = nil
	tracker.recoveryEditSeen = false
	tracker.recoveryPassedChecks = nil
	tracker.recoveryCohorts = nil
}

func (tracker *downstreamQualityTracker) cohort(name string) downstreamCohortMetrics {
	if tracker.metrics.Cohorts == nil {
		tracker.metrics.Cohorts = map[string]downstreamCohortMetrics{}
	}
	return tracker.metrics.Cohorts[name]
}

func (tracker *downstreamQualityTracker) preDeliveryCheck(name string) downstreamCheckMetrics {
	if tracker.metrics.PreDeliveryChecks == nil {
		tracker.metrics.PreDeliveryChecks = map[string]downstreamCheckMetrics{}
	}
	return tracker.metrics.PreDeliveryChecks[name]
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

func matchingTargets(targets []string, candidates map[string]struct{}) []string {
	var matched []string
	for _, target := range targets {
		if _, ok := candidates[target]; ok {
			matched = append(matched, target)
		}
	}
	return matched
}

func setContainsAll(values, required map[string]struct{}) bool {
	if len(required) == 0 {
		return false
	}
	for value := range required {
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
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

func downstreamCheckIDs(event normalizedSessionEvent, operations []string) []string {
	if event.Family == "review" || reviewOperation(event, operations) {
		return nil
	}
	var checks []string
	for _, operation := range operations {
		name := strings.ToLower(operation[strings.LastIndex(operation, "/")+1:])
		if downstreamCheckName(name) {
			checks = appendUniqueString(checks, operation)
		}
	}
	if len(checks) == 0 {
		switch event.Family {
		case "tests":
			checks = append(checks, "tests")
		case "build, lint, or install":
			checks = append(checks, "build/lint")
		}
	}
	sort.Strings(checks)
	return checks
}

func downstreamCheckName(name string) bool {
	for _, prefix := range []string{"test", "build", "lint", "check", "verify", "ci"} {
		if name == prefix || strings.HasPrefix(name, prefix+"-") {
			return true
		}
	}
	return false
}

func revertOperation(event normalizedSessionEvent) bool {
	if event.Family == "revert" || event.FirstFamily == "revert" || event.LastFamily == "revert" {
		return true
	}
	for _, family := range strings.Split(event.Shape, " -> ") {
		if family == "revert" {
			return true
		}
	}
	return false
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

func addDownstreamQualityMetrics(target *downstreamQualityMetrics, addition downstreamQualityMetrics) {
	target.Deliveries += addition.Deliveries
	target.DeliveriesWithPreTests += addition.DeliveriesWithPreTests
	target.DeliveriesWithFailure += addition.DeliveriesWithFailure
	target.FailedDeliveriesWithPreTests += addition.FailedDeliveriesWithPreTests
	target.FailureRuns += addition.FailureRuns
	target.FollowUpEditCycles += addition.FollowUpEditCycles
	target.RedeliveryAttempts += addition.RedeliveryAttempts
	target.RecoveredDeliveries += addition.RecoveredDeliveries
	target.Reverts += addition.Reverts
	target.Sessions += addition.Sessions
	if target.FailureChecks == nil {
		target.FailureChecks = map[string]int{}
	}
	for check, count := range addition.FailureChecks {
		target.FailureChecks[check] += count
	}
	if target.PreDeliveryChecks == nil {
		target.PreDeliveryChecks = map[string]downstreamCheckMetrics{}
	}
	for check, metrics := range addition.PreDeliveryChecks {
		current := target.PreDeliveryChecks[check]
		current.Deliveries += metrics.Deliveries
		current.DeliveriesWithFailure += metrics.DeliveriesWithFailure
		target.PreDeliveryChecks[check] = current
	}
	if target.Cohorts == nil {
		target.Cohorts = map[string]downstreamCohortMetrics{}
	}
	for cohort, metrics := range addition.Cohorts {
		current := target.Cohorts[cohort]
		current.Deliveries += metrics.Deliveries
		current.DeliveriesWithPreTests += metrics.DeliveriesWithPreTests
		current.DeliveriesWithFailure += metrics.DeliveriesWithFailure
		current.FailedDeliveriesWithPreTests += metrics.FailedDeliveriesWithPreTests
		current.FailureRuns += metrics.FailureRuns
		current.FollowUpEditCycles += metrics.FollowUpEditCycles
		current.RedeliveryAttempts += metrics.RedeliveryAttempts
		current.RecoveredDeliveries += metrics.RecoveredDeliveries
		current.Reverts += metrics.Reverts
		target.Cohorts[cohort] = current
	}
	target.timeToFailureSecondsObservations = append(
		target.timeToFailureSecondsObservations,
		addition.timeToFailureSecondsObservations...,
	)
	target.timeToRecoverySecondsObservations = append(
		target.timeToRecoverySecondsObservations,
		addition.timeToRecoverySecondsObservations...,
	)
	finalizeDownstreamQualityMetrics(target)
}

func finalizeDownstreamQualityMetrics(metrics *downstreamQualityMetrics) {
	metrics.TimeToFailureSeconds = summarizeOutcomeDistribution(metrics.timeToFailureSecondsObservations)
	metrics.TimeToRecoverySeconds = summarizeOutcomeDistribution(metrics.timeToRecoverySecondsObservations)
}

func (episode *codexTaskEpisode) observe(event normalizedSessionEvent, tokenIncrement codexTokenUsage, operations []string) {
	if episode.StartedAt.IsZero() || event.OccurredAt.Before(episode.StartedAt) {
		episode.StartedAt = event.OccurredAt
	}
	if episode.EndedAt.IsZero() || event.OccurredAt.After(episode.EndedAt) {
		episode.EndedAt = event.OccurredAt
	}
	episode.observePhase(event, tokenIncrement, operations)
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
			if episode.Targets == nil {
				episode.Targets = map[string]int{}
			}
			if episode.TargetCohorts == nil {
				episode.TargetCohorts = map[string]int{}
			}
			var sourceTargets []string
			for _, target := range event.Targets {
				if taskCostSourceTarget(target) {
					sourceTargets = append(sourceTargets, target)
					episode.Targets[target]++
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

func (episode *codexTaskEpisode) observePhase(
	event normalizedSessionEvent,
	tokenIncrement codexTokenUsage,
	operations []string,
) {
	if episode.Phases == nil {
		episode.Phases = map[string]taskPhaseCost{}
	}
	if episode.currentPhase == "" {
		episode.currentPhase = "discovery"
	}
	if !episode.lastObservedAt.IsZero() && event.OccurredAt.After(episode.lastObservedAt) {
		metrics := episode.Phases[episode.currentPhase]
		metrics.DurationSeconds += int64(event.OccurredAt.Sub(episode.lastObservedAt).Seconds())
		episode.Phases[episode.currentPhase] = metrics
	}
	episode.lastObservedAt = event.OccurredAt

	phase := episode.currentPhase
	if event.Kind == sessionEventToolCall {
		phase = episode.phaseForToolCall(event, operations)
		episode.currentPhase = phase
	}
	if event.Kind == sessionEventToolOutput {
		phase = episode.phaseForToolOutput(event, operations)
	}
	metrics := episode.Phases[phase]
	switch event.Kind {
	case sessionEventToken:
		addCodexTokenUsage(&metrics.Tokens, tokenIncrement)
	case sessionEventToolCall:
		metrics.ToolCalls++
	case sessionEventToolOutput:
		metrics.ToolOutputBytes += event.OutputBytes
		if event.Failed && !event.OperationContinues {
			metrics.FailedCalls++
		}
		if event.Failed && !event.OperationContinues && episode.delivered &&
			!episode.editSinceDelivery &&
			len(downstreamCheckIDs(event, operations)) > 0 {
			episode.reworkActive = true
		}
		if !event.Failed && !event.OperationContinues && deliveryOperation(event, operations) {
			episode.delivered = true
			episode.reworkActive = false
			episode.editSinceDelivery = false
		}
	case sessionEventCompaction:
		metrics.Compactions++
	}
	episode.Phases[phase] = metrics
}

func (episode *codexTaskEpisode) phaseForToolCall(
	event normalizedSessionEvent,
	operations []string,
) string {
	if deliveryOperation(event, operations) {
		return "delivery"
	}
	if episode.delivered && revertOperation(event) {
		episode.reworkActive = true
	}
	if episode.delivered && reviewStart(event, operations) {
		episode.reworkActive = true
	}
	if episode.delivered && event.ToolName == "apply_patch" && !episode.reworkActive {
		episode.editSinceDelivery = true
	}
	if episode.reworkActive {
		return "rework"
	}
	if delegationOperation(event) {
		return "delegation"
	}
	if event.ToolName == "apply_patch" {
		return "editing"
	}
	if verificationOperation(event, operations) {
		return "verification"
	}
	if discoveryOperation(event, operations) {
		return "discovery"
	}
	return episode.currentPhase
}

func (episode codexTaskEpisode) phaseForToolOutput(
	event normalizedSessionEvent,
	operations []string,
) string {
	if deliveryOperation(event, operations) {
		return "delivery"
	}
	if episode.reworkActive || episode.delivered && revertOperation(event) {
		return "rework"
	}
	if delegationOperation(event) {
		return "delegation"
	}
	if verificationOperation(event, operations) {
		return "verification"
	}
	if discoveryOperation(event, operations) {
		return "discovery"
	}
	return episode.currentPhase
}

func verificationOperation(event normalizedSessionEvent, operations []string) bool {
	switch event.Family {
	case "tests", "build, lint, or install", "review":
		return true
	}
	if reviewOperation(event, operations) {
		return true
	}
	for _, operation := range operations {
		name := operation[strings.LastIndex(operation, "/")+1:]
		if name == "validate" || name == "ready" || name == "plan-check" {
			return true
		}
	}
	return false
}

func delegationOperation(event normalizedSessionEvent) bool {
	return delegationToolName(event.ToolName)
}

func discoveryOperation(event normalizedSessionEvent, operations []string) bool {
	switch event.Family {
	case "search", "file reads", "git inspect", "bounded task inspect":
		return true
	}
	for _, operation := range operations {
		name := operation[strings.LastIndex(operation, "/")+1:]
		if strings.HasPrefix(name, "inspect") || strings.HasPrefix(name, "status") {
			return true
		}
	}
	return false
}

type outcomeDistribution struct {
	Count int   `json:"count"`
	P50   int64 `json:"p50"`
	P75   int64 `json:"p75"`
	P90   int64 `json:"p90"`
	Max   int64 `json:"max"`
}

type completionEpisodeAnalysis struct {
	Completed                int                          `json:"completed"`
	FullyObservedCompleted   int                          `json:"fullyObservedCompleted"`
	LeftCensoredCompleted    int                          `json:"leftCensoredCompleted"`
	ToolUsingCompleted       int                          `json:"toolUsingCompleted"`
	ResponseOnlyCompleted    int                          `json:"responseOnlyCompleted"`
	Incomplete               int                          `json:"incomplete"`
	CachedInputTokens        outcomeDistribution          `json:"cachedInputTokens"`
	UncachedInputTokens      outcomeDistribution          `json:"uncachedInputTokens"`
	ModelOutputTokens        outcomeDistribution          `json:"modelOutputTokens"`
	FreshTokens              outcomeDistribution          `json:"freshTokens"`
	ToolCalls                outcomeDistribution          `json:"toolCalls"`
	VisibleOutputTokens      outcomeDistribution          `json:"visibleOutputTokens"`
	DurationSeconds          outcomeDistribution          `json:"durationSeconds"`
	FailedCalls              outcomeDistribution          `json:"failedCalls"`
	Compactions              outcomeDistribution          `json:"compactions"`
	TopDecileFreshTokenShare float64                      `json:"topDecileFreshTokenShare"`
	TailDrivers              taskCostTailDrivers          `json:"tailDrivers"`
	Phases                   map[string]taskPhaseAnalysis `json:"phases,omitempty"`
	TailPhases               []taskPhaseTailAssociation   `json:"tailPhases,omitempty"`
	FileHotspots             []fileHotspotMetrics         `json:"fileHotspots,omitempty"`
}

type fileHotspotMetrics struct {
	Target              string              `json:"target"`
	CompletedTasks      int                 `json:"completedTasks"`
	EditCalls           int                 `json:"editCalls"`
	TaskShare           float64             `json:"taskShare"`
	ToolRoundtrips      outcomeDistribution `json:"toolRoundtrips"`
	DurationSeconds     outcomeDistribution `json:"durationSeconds"`
	PostReviewEditCalls int                 `json:"postReviewEditCalls"`
}

type taskPhaseAnalysis struct {
	Episodes             int                 `json:"episodes"`
	TotalFreshTokens     int64               `json:"totalFreshTokens"`
	TotalToolCalls       int64               `json:"totalToolCalls"`
	TotalOutputTokens    int64               `json:"totalOutputTokens"`
	TotalDurationSeconds int64               `json:"totalDurationSeconds"`
	FreshTokens          outcomeDistribution `json:"freshTokens"`
	ToolCalls            outcomeDistribution `json:"toolCalls"`
	VisibleOutputTokens  outcomeDistribution `json:"visibleOutputTokens"`
	DurationSeconds      outcomeDistribution `json:"durationSeconds"`
	FailedCalls          outcomeDistribution `json:"failedCalls"`
	Compactions          outcomeDistribution `json:"compactions"`
}

type taskPhaseTailAssociation struct {
	Phase               string  `json:"phase"`
	TailFreshTokens     int64   `json:"tailFreshTokens"`
	OrdinaryFreshTokens int64   `json:"ordinaryFreshTokens"`
	TailShare           float64 `json:"tailShare"`
	OrdinaryShare       float64 `json:"ordinaryShare"`
	ShareDelta          float64 `json:"shareDelta"`
}

type taskCostTailDrivers struct {
	TailEpisodes     int                  `json:"tailEpisodes"`
	OrdinaryEpisodes int                  `json:"ordinaryEpisodes"`
	Families         []taskCostTailDriver `json:"families,omitempty"`
	OwnedOperations  []taskCostTailDriver `json:"ownedOperations,omitempty"`
	Targets          []taskCostTailDriver `json:"targets,omitempty"`
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
	var cachedInputTokens, uncachedInputTokens, modelOutputTokens []int64
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
		cachedInputTokens = append(cachedInputTokens, episode.Tokens.CachedInputTokens)
		uncachedInputTokens = append(uncachedInputTokens, episode.Tokens.UncachedInputTokens)
		modelOutputTokens = append(modelOutputTokens, episode.Tokens.OutputTokens)
		freshTokens = append(freshTokens, episode.Tokens.UncachedInputTokens+episode.Tokens.OutputTokens)
		toolCalls = append(toolCalls, int64(episode.ToolCalls))
		outputTokens = append(outputTokens, estimatedTokens(episode.ToolOutputBytes))
		durations = append(durations, taskEpisodeDuration(episode))
		failures = append(failures, int64(episode.FailedCalls))
		compactions = append(compactions, int64(episode.Compactions))
	}
	analysis.CachedInputTokens = summarizeOutcomeDistribution(cachedInputTokens)
	analysis.UncachedInputTokens = summarizeOutcomeDistribution(uncachedInputTokens)
	analysis.ModelOutputTokens = summarizeOutcomeDistribution(modelOutputTokens)
	analysis.FreshTokens = summarizeOutcomeDistribution(freshTokens)
	analysis.ToolCalls = summarizeOutcomeDistribution(toolCalls)
	analysis.VisibleOutputTokens = summarizeOutcomeDistribution(outputTokens)
	analysis.DurationSeconds = summarizeOutcomeDistribution(durations)
	analysis.FailedCalls = summarizeOutcomeDistribution(failures)
	analysis.Compactions = summarizeOutcomeDistribution(compactions)
	analysis.TopDecileFreshTokenShare = topOutcomeShare(freshTokens, 0.10)
	analysis.TailDrivers = analyzeTaskCostTailDrivers(eligible)
	analysis.Phases = analyzeTaskPhases(eligible)
	analysis.TailPhases = analyzeTaskPhaseTailAssociations(eligible)
	return analysis
}

func analyzeFileHotspots(
	episodes []codexTaskEpisode,
	reworkTargets map[string]int,
) []fileHotspotMetrics {
	type observations struct {
		tasks      int
		editCalls  int
		roundtrips []int64
		durations  []int64
	}
	eligibleTasks := 0
	byTarget := map[string]*observations{}
	for _, episode := range episodes {
		if !episode.Completed || episode.LeftCensored || episode.ToolCalls == 0 {
			continue
		}
		eligibleTasks++
		for target, calls := range episode.Targets {
			if calls <= 0 {
				continue
			}
			label := deliveryTargetLabel(target)
			current := byTarget[label]
			if current == nil {
				current = &observations{}
				byTarget[label] = current
			}
			current.tasks++
			current.editCalls += calls
			current.roundtrips = append(current.roundtrips, int64(episode.ToolCalls))
			current.durations = append(current.durations, taskEpisodeDuration(episode))
		}
	}
	reworkByTarget := map[string]int{}
	for target, calls := range reworkTargets {
		reworkByTarget[deliveryTargetLabel(target)] += calls
	}
	var hotspots []fileHotspotMetrics
	for target, current := range byTarget {
		if current.tasks < 2 {
			continue
		}
		hotspots = append(hotspots, fileHotspotMetrics{
			Target:              target,
			CompletedTasks:      current.tasks,
			EditCalls:           current.editCalls,
			TaskShare:           ratio(float64(current.tasks), float64(eligibleTasks)),
			ToolRoundtrips:      summarizeOutcomeDistribution(current.roundtrips),
			DurationSeconds:     summarizeOutcomeDistribution(current.durations),
			PostReviewEditCalls: reworkByTarget[target],
		})
	}
	sort.Slice(hotspots, func(i, j int) bool {
		if hotspots[i].CompletedTasks != hotspots[j].CompletedTasks {
			return hotspots[i].CompletedTasks > hotspots[j].CompletedTasks
		}
		if hotspots[i].PostReviewEditCalls != hotspots[j].PostReviewEditCalls {
			return hotspots[i].PostReviewEditCalls > hotspots[j].PostReviewEditCalls
		}
		if hotspots[i].EditCalls != hotspots[j].EditCalls {
			return hotspots[i].EditCalls > hotspots[j].EditCalls
		}
		return hotspots[i].Target < hotspots[j].Target
	})
	return hotspots
}

func printFileHotspots(hotspots []fileHotspotMetrics, limit int) {
	if len(hotspots) == 0 {
		return
	}
	if limit > 0 && len(hotspots) > limit {
		hotspots = hotspots[:limit]
	}
	fmt.Println("\nFile edit hotspots (frequency is demand; correlate cost and post-review edits before acting):")
	fmt.Printf(
		"%-48s %7s %7s %7s %13s %15s %10s\n",
		"TARGET",
		"TASKS",
		"SHARE",
		"EDITS",
		"RT P50/P90",
		"TIME P50/P90",
		"POST-REVIEW",
	)
	for _, hotspot := range hotspots {
		fmt.Printf(
			"%-48s %7s %6.0f%% %7s %6s/%-6s %7s/%-7s %10s\n",
			truncateCodexLabel(hotspot.Target, 48),
			formatCodexCount(int64(hotspot.CompletedTasks)),
			100*hotspot.TaskShare,
			formatCodexCount(int64(hotspot.EditCalls)),
			formatCodexCount(hotspot.ToolRoundtrips.P50),
			formatCodexCount(hotspot.ToolRoundtrips.P90),
			formatDurationSeconds(hotspot.DurationSeconds.P50),
			formatDurationSeconds(hotspot.DurationSeconds.P90),
			formatCodexCount(int64(hotspot.PostReviewEditCalls)),
		)
	}
}

func analyzeTaskPhases(episodes []codexTaskEpisode) map[string]taskPhaseAnalysis {
	type phaseValues struct {
		freshTokens, toolCalls, outputTokens, durations, failures, compactions []int64
	}
	values := map[string]*phaseValues{}
	for _, episode := range episodes {
		for phase, metrics := range episode.Phases {
			current := values[phase]
			if current == nil {
				current = &phaseValues{}
				values[phase] = current
			}
			current.freshTokens = append(current.freshTokens, phaseFreshTokens(metrics))
			current.toolCalls = append(current.toolCalls, int64(metrics.ToolCalls))
			current.outputTokens = append(current.outputTokens, estimatedTokens(metrics.ToolOutputBytes))
			current.durations = append(current.durations, metrics.DurationSeconds)
			current.failures = append(current.failures, int64(metrics.FailedCalls))
			current.compactions = append(current.compactions, int64(metrics.Compactions))
		}
	}
	analysis := make(map[string]taskPhaseAnalysis, len(values))
	for phase, current := range values {
		analysis[phase] = taskPhaseAnalysis{
			Episodes:             len(current.freshTokens),
			TotalFreshTokens:     sumInt64(current.freshTokens),
			TotalToolCalls:       sumInt64(current.toolCalls),
			TotalOutputTokens:    sumInt64(current.outputTokens),
			TotalDurationSeconds: sumInt64(current.durations),
			FreshTokens:          summarizeOutcomeDistribution(current.freshTokens),
			ToolCalls:            summarizeOutcomeDistribution(current.toolCalls),
			VisibleOutputTokens:  summarizeOutcomeDistribution(current.outputTokens),
			DurationSeconds:      summarizeOutcomeDistribution(current.durations),
			FailedCalls:          summarizeOutcomeDistribution(current.failures),
			Compactions:          summarizeOutcomeDistribution(current.compactions),
		}
	}
	return analysis
}

func analyzeTaskPhaseTailAssociations(episodes []codexTaskEpisode) []taskPhaseTailAssociation {
	if len(episodes) < 10 {
		return nil
	}
	ranked := append([]codexTaskEpisode(nil), episodes...)
	sort.Slice(ranked, func(i, j int) bool {
		return episodeFreshTokens(ranked[i]) > episodeFreshTokens(ranked[j])
	})
	tailCount := max(1, int(math.Ceil(0.10*float64(len(ranked)))))
	tail := aggregatePhaseFreshTokens(ranked[:tailCount])
	ordinary := aggregatePhaseFreshTokens(ranked[tailCount:])
	var tailTotal, ordinaryTotal int64
	for _, value := range tail {
		tailTotal += value
	}
	for _, value := range ordinary {
		ordinaryTotal += value
	}
	names := map[string]struct{}{}
	for phase := range tail {
		names[phase] = struct{}{}
	}
	for phase := range ordinary {
		names[phase] = struct{}{}
	}
	associations := make([]taskPhaseTailAssociation, 0, len(names))
	for phase := range names {
		tailShare := ratio(float64(tail[phase]), float64(tailTotal))
		ordinaryShare := ratio(float64(ordinary[phase]), float64(ordinaryTotal))
		associations = append(associations, taskPhaseTailAssociation{
			Phase:               phase,
			TailFreshTokens:     tail[phase],
			OrdinaryFreshTokens: ordinary[phase],
			TailShare:           tailShare,
			OrdinaryShare:       ordinaryShare,
			ShareDelta:          tailShare - ordinaryShare,
		})
	}
	sort.Slice(associations, func(i, j int) bool {
		if associations[i].ShareDelta != associations[j].ShareDelta {
			return associations[i].ShareDelta > associations[j].ShareDelta
		}
		return associations[i].Phase < associations[j].Phase
	})
	return associations
}

func aggregatePhaseFreshTokens(episodes []codexTaskEpisode) map[string]int64 {
	totals := map[string]int64{}
	for _, episode := range episodes {
		for phase, metrics := range episode.Phases {
			totals[phase] += phaseFreshTokens(metrics)
		}
	}
	return totals
}

func phaseFreshTokens(metrics taskPhaseCost) int64 {
	return metrics.Tokens.UncachedInputTokens + metrics.Tokens.OutputTokens
}

func episodeFreshTokens(episode codexTaskEpisode) int64 {
	return episode.Tokens.UncachedInputTokens + episode.Tokens.OutputTokens
}

func sumInt64(values []int64) int64 {
	var total int64
	for _, value := range values {
		total += value
	}
	return total
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
		return taskCostDiagnosticOperations(episode.OwnedOperations)
	})
	drivers.TargetCohorts = taskCostTailDimension(tail, ordinary, func(episode codexTaskEpisode) map[string]int {
		return episode.TargetCohorts
	})
	drivers.Targets = taskCostTailTargetContributors(tail, ordinary, drivers.TargetCohorts)
	return drivers
}

func taskCostDiagnosticOperations(operations map[string]int) map[string]int {
	filtered := make(map[string]int, len(operations))
	for operation, calls := range operations {
		name := operation[strings.LastIndex(operation, "/")+1:]
		name = strings.TrimSuffix(name, "-here")
		switch name {
		case "git-add", "git-commit", "git-push", "pr", "publish", "worktree-land":
			continue
		}
		filtered[operation] = calls
	}
	return filtered
}

func taskCostTailTargetContributors(
	tail,
	ordinary []codexTaskEpisode,
	cohorts []taskCostTailDriver,
) []taskCostTailDriver {
	allowedCohorts := map[string]struct{}{}
	for _, cohort := range cohorts {
		allowedCohorts[cohort.Name] = struct{}{}
	}
	if len(allowedCohorts) == 0 {
		return nil
	}
	type counts struct {
		tailEpisodes     int
		ordinaryEpisodes int
		tailCalls        int
		ordinaryCalls    int
	}
	byName := map[string]counts{}
	collect := func(episodes []codexTaskEpisode, tailCohort bool) {
		for _, episode := range episodes {
			seen := map[string]struct{}{}
			for target, calls := range episode.Targets {
				if _, allowed := allowedCohorts[deliveryTargetCohort(target)]; !allowed {
					continue
				}
				name := deliveryTargetLabel(target)
				current := byName[name]
				if tailCohort {
					current.tailCalls += calls
					if _, exists := seen[name]; !exists {
						current.tailEpisodes++
					}
				} else {
					current.ordinaryCalls += calls
					if _, exists := seen[name]; !exists {
						current.ordinaryEpisodes++
					}
				}
				seen[name] = struct{}{}
				byName[name] = current
			}
		}
	}
	collect(tail, true)
	collect(ordinary, false)

	var targets []taskCostTailDriver
	for name, current := range byName {
		if current.tailEpisodes < 2 {
			continue
		}
		tailRate := ratio(float64(current.tailEpisodes), float64(len(tail)))
		ordinaryRate := ratio(float64(current.ordinaryEpisodes), float64(len(ordinary)))
		targets = append(targets, taskCostTailDriver{
			Name:             name,
			TailEpisodes:     current.tailEpisodes,
			OrdinaryEpisodes: current.ordinaryEpisodes,
			TailCalls:        current.tailCalls,
			OrdinaryCalls:    current.ordinaryCalls,
			PrevalenceDelta:  tailRate - ordinaryRate,
			PrevalenceLift:   tailRate / math.Max(ordinaryRate, 1/float64(max(1, len(ordinary)))),
		})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].TailEpisodes != targets[j].TailEpisodes {
			return targets[i].TailEpisodes > targets[j].TailEpisodes
		}
		if targets[i].TailCalls != targets[j].TailCalls {
			return targets[i].TailCalls > targets[j].TailCalls
		}
		return targets[i].Name < targets[j].Name
	})
	if len(targets) > 3 {
		targets = targets[:3]
	}
	return targets
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
		"Completed tool-task outcomes p50/p75/p90: fresh tokens %s/%s/%s; tool roundtrips %s/%s/%s; duration %s/%s/%s\n",
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
	fmt.Printf(
		"Completed task token split p50/p90: cached input %s/%s; uncached input %s/%s; model output %s/%s\n",
		formatCodexCount(analysis.CachedInputTokens.P50),
		formatCodexCount(analysis.CachedInputTokens.P90),
		formatCodexCount(analysis.UncachedInputTokens.P50),
		formatCodexCount(analysis.UncachedInputTokens.P90),
		formatCodexCount(analysis.ModelOutputTokens.P50),
		formatCodexCount(analysis.ModelOutputTokens.P90),
	)
	if phases := formatTaskPhaseAnalysis(analysis.Phases); phases != "" {
		fmt.Printf("Task phase outcomes: %s\n", phases)
	}
	if phases := formatTaskPhaseTailAssociations(analysis.TailPhases, 3); phases != "" {
		fmt.Printf("High-tail phase mix: %s\n", phases)
	}
	if drivers := formatTaskCostTailDrivers(analysis.TailDrivers); drivers != "" {
		fmt.Printf("Fresh-token tail associations: %s\n", drivers)
	}
}

func formatTaskPhaseAnalysis(phases map[string]taskPhaseAnalysis) string {
	order := []string{"discovery", "editing", "verification", "delivery", "rework", "delegation"}
	var parts []string
	for _, phase := range order {
		metrics, ok := phases[phase]
		if !ok || metrics.Episodes == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"%s %s episodes, fresh p50/p90 %s/%s, calls %s/%s",
			phase,
			formatCodexCount(int64(metrics.Episodes)),
			formatCodexCount(metrics.FreshTokens.P50),
			formatCodexCount(metrics.FreshTokens.P90),
			formatCodexCount(metrics.ToolCalls.P50),
			formatCodexCount(metrics.ToolCalls.P90),
		))
	}
	return strings.Join(parts, "; ")
}

func formatTaskPhaseTailAssociations(associations []taskPhaseTailAssociation, limit int) string {
	var parts []string
	for _, association := range associations {
		if association.ShareDelta <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"%s %.0f%% tail vs %.0f%% ordinary (+%.0fpp)",
			association.Phase,
			100*association.TailShare,
			100*association.OrdinaryShare,
			100*association.ShareDelta,
		))
		if limit > 0 && len(parts) >= limit {
			break
		}
	}
	return strings.Join(parts, "; ")
}

func formatTaskCostTailDrivers(drivers taskCostTailDrivers) string {
	var parts []string
	if len(drivers.Targets) > 0 {
		rendered := make([]string, 0, len(drivers.Targets))
		for _, target := range drivers.Targets {
			rendered = append(rendered, fmt.Sprintf(
				"%s %s calls across %s tail episodes (%s ordinary episodes)",
				target.Name,
				formatCodexCount(int64(target.TailCalls)),
				formatCodexCount(int64(target.TailEpisodes)),
				formatCodexCount(int64(target.OrdinaryEpisodes)),
			))
		}
		parts = append(parts, "cohort targets "+strings.Join(rendered, ", "))
	}
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

func printDownstreamQualityAnalysis(metrics downstreamQualityMetrics) {
	if metrics.Deliveries == 0 && metrics.DeliveriesWithFailure == 0 && metrics.Reverts == 0 {
		return
	}
	fmt.Printf(
		"Downstream quality: %s deliveries, %s failed downstream (%.0f%%), %s failure runs, %s follow-up edit cycles, %s/%s recovery redeliveries, %s reverts\n",
		formatCodexCount(int64(metrics.Deliveries)),
		formatCodexCount(int64(metrics.DeliveriesWithFailure)),
		100*ratio(float64(metrics.DeliveriesWithFailure), float64(metrics.Deliveries)),
		formatCodexCount(int64(metrics.FailureRuns)),
		formatCodexCount(int64(metrics.FollowUpEditCycles)),
		formatCodexCount(int64(metrics.RecoveredDeliveries)),
		formatCodexCount(int64(metrics.RedeliveryAttempts)),
		formatCodexCount(int64(metrics.Reverts)),
	)
	if metrics.DeliveriesWithFailure > 0 {
		fmt.Printf(
			"Fresh verification before downstream failure: %s/%s affected deliveries; failure checks %s\n",
			formatCodexCount(int64(metrics.FailedDeliveriesWithPreTests)),
			formatCodexCount(int64(metrics.DeliveriesWithFailure)),
			formatMetricDimensions(metrics.FailureChecks),
		)
	}
	var timings []string
	if metrics.TimeToFailureSeconds.Count > 0 {
		timings = append(timings, fmt.Sprintf(
			"failure p50/p90 %s/%s",
			formatDurationSeconds(metrics.TimeToFailureSeconds.P50),
			formatDurationSeconds(metrics.TimeToFailureSeconds.P90),
		))
	}
	if metrics.TimeToRecoverySeconds.Count > 0 {
		timings = append(timings, fmt.Sprintf(
			"recovery p50/p90 %s/%s",
			formatDurationSeconds(metrics.TimeToRecoverySeconds.P50),
			formatDurationSeconds(metrics.TimeToRecoverySeconds.P90),
		))
	}
	if len(timings) > 0 {
		fmt.Printf("Downstream timing: %s\n", strings.Join(timings, "; "))
	}
	if checks := formatDownstreamPreDeliveryChecks(metrics); checks != "" {
		fmt.Printf("Downstream rate by pre-delivery check: %s\n", checks)
	}
	if cohorts := formatDownstreamCohorts(metrics.Cohorts, 3); cohorts != "" {
		fmt.Printf("Downstream rate by cohort: %s\n", cohorts)
	}
}

func formatDownstreamPreDeliveryChecks(metrics downstreamQualityMetrics) string {
	type row struct {
		name    string
		metrics downstreamCheckMetrics
	}
	rows := make([]row, 0, len(metrics.PreDeliveryChecks))
	for name, check := range metrics.PreDeliveryChecks {
		if check.Deliveries == 0 {
			continue
		}
		rows = append(rows, row{name: name, metrics: check})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].metrics.Deliveries != rows[j].metrics.Deliveries {
			return rows[i].metrics.Deliveries > rows[j].metrics.Deliveries
		}
		return rows[i].name < rows[j].name
	})
	if len(rows) > 3 {
		rows = rows[:3]
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		withoutDeliveries := metrics.Deliveries - row.metrics.Deliveries
		withoutFailures := metrics.DeliveriesWithFailure - row.metrics.DeliveriesWithFailure
		parts = append(parts, fmt.Sprintf(
			"%s %s/%s with failure vs %s/%s without",
			row.name,
			formatCodexCount(int64(row.metrics.DeliveriesWithFailure)),
			formatCodexCount(int64(row.metrics.Deliveries)),
			formatCodexCount(int64(withoutFailures)),
			formatCodexCount(int64(withoutDeliveries)),
		))
	}
	return strings.Join(parts, "; ")
}

func formatDownstreamCohorts(cohorts map[string]downstreamCohortMetrics, limit int) string {
	type row struct {
		name    string
		metrics downstreamCohortMetrics
	}
	rows := make([]row, 0, len(cohorts))
	for name, metrics := range cohorts {
		if metrics.DeliveriesWithFailure == 0 || name == "(unknown)" {
			continue
		}
		rows = append(rows, row{name: name, metrics: metrics})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].metrics.DeliveriesWithFailure != rows[j].metrics.DeliveriesWithFailure {
			return rows[i].metrics.DeliveriesWithFailure > rows[j].metrics.DeliveriesWithFailure
		}
		if rows[i].metrics.Deliveries != rows[j].metrics.Deliveries {
			return rows[i].metrics.Deliveries > rows[j].metrics.Deliveries
		}
		return rows[i].name < rows[j].name
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, fmt.Sprintf(
			"%s %s/%s failed, %s recovered, tests before %s/%s failures",
			row.name,
			formatCodexCount(int64(row.metrics.DeliveriesWithFailure)),
			formatCodexCount(int64(row.metrics.Deliveries)),
			formatCodexCount(int64(row.metrics.RecoveredDeliveries)),
			formatCodexCount(int64(row.metrics.FailedDeliveriesWithPreTests)),
			formatCodexCount(int64(row.metrics.DeliveriesWithFailure)),
		))
	}
	return strings.Join(parts, "; ")
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
