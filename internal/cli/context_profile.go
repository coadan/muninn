package cli

import "time"

// Context profiles describe observations, not optimal context budgets. A first
// observed request can already include task input, resumed history, and guidance.
type contextProfileAnalysis struct {
	Sessions                 int                     `json:"sessions"`
	SessionsWithSamples      int                     `json:"sessionsWithSamples"`
	SessionsWithoutSamples   int                     `json:"sessionsWithoutSamples"`
	ObservedInputTokens      outcomeDistribution     `json:"observedInputTokens"`
	FirstObservedInputTokens outcomeDistribution     `json:"firstObservedInputTokens"`
	PeakInputTokens          outcomeDistribution     `json:"peakInputTokens"`
	SegmentGrowthTokens      outcomeDistribution     `json:"segmentGrowthTokens"`
	ReportedWindowTokens     outcomeDistribution     `json:"reportedWindowTokens"`
	CompactionRecovery       contextRecoveryAnalysis `json:"compactionRecovery"`
}

type contextRecoveryAnalysis struct {
	Boundaries                  int                 `json:"boundaries"`
	Triggers                    map[string]int      `json:"triggers"`
	WithBeforeSample            int                 `json:"withBeforeSample"`
	WithReportedBeforeTokens    int                 `json:"withReportedBeforeTokens"`
	WithAfterSample             int                 `json:"withAfterSample"`
	PairedSamples               int                 `json:"pairedSamples"`
	BeforeInputTokens           outcomeDistribution `json:"beforeInputTokens"`
	AfterInputTokens            outcomeDistribution `json:"afterInputTokens"`
	InputReductionTokens        outcomeDistribution `json:"inputReductionTokens"`
	CompletedWindows            int                 `json:"completedWindows"`
	CensoredWindows             int                 `json:"censoredWindows"`
	RequestSamplesToCompletion  outcomeDistribution `json:"requestSamplesToCompletion"`
	ToolCallsToCompletion       outcomeDistribution `json:"toolCallsToCompletion"`
	ReadSearchCallsToCompletion outcomeDistribution `json:"readSearchCallsToCompletion"`
	FailedCallsToCompletion     outcomeDistribution `json:"failedCallsToCompletion"`
	ElapsedSecondsToCompletion  outcomeDistribution `json:"elapsedSecondsToCompletion"`
}

type sessionContextProfile struct {
	FirstInput int64
	Inputs     []int64
	Windows    []int64
	Growth     []int64
	Recoveries []*contextRecoveryWindow
}

type contextRecoveryWindow struct {
	StartedAt      time.Time
	Trigger        string
	Before         int64
	BeforeReported bool
	After          int64
	Completed      bool
	Requests       int64
	ToolCalls      int64
	ReadSearch     int64
	FailedCalls    int64
	ElapsedSeconds int64
}

func normalizedCompactionTrigger(trigger string) string {
	switch trigger {
	case "auto", "manual":
		return trigger
	default:
		return ""
	}
}

func profileSessionContext(session normalizedSession, root string, since, until time.Time) sessionContextProfile {
	var profile sessionContextProfile
	contextScope, _ := pathInsideRoot(root, session.CWD)
	inside := contextScope
	firstSeen := false
	var previousInput, segmentFirst, segmentPeak int64
	segmentSamples := 0
	var recovery *contextRecoveryWindow
	recoveryCalls := map[int]bool{}
	finishSegment := func() {
		if segmentSamples >= 2 {
			profile.Growth = append(profile.Growth, segmentPeak-segmentFirst)
		}
		segmentFirst, segmentPeak, segmentSamples = 0, 0, 0
	}
	for _, event := range session.Events {
		if event.OccurredAt.After(until) {
			continue
		}
		inside = repositoryScopeForEvent(event, session.CWD, root, &contextScope)
		isSample := event.Kind == sessionEventToken && event.ContextTokens > 0
		isFirst := isSample && !firstSeen
		firstSeen = firstSeen || isSample
		if !inside {
			// Only leaving the foreground repository ends a segment; a late
			// result from elsewhere is skipped without breaking continuity.
			if !contextScope {
				finishSegment()
				previousInput, recovery = 0, nil
			}
			continue
		}
		if event.OccurredAt.Before(since) {
			if isSample {
				previousInput = event.ContextTokens
			} else if event.Kind == sessionEventCompaction {
				previousInput = 0
			}
			continue
		}
		switch event.Kind {
		case sessionEventCompaction:
			finishSegment()
			recovery = &contextRecoveryWindow{
				StartedAt: event.OccurredAt, Trigger: normalizedCompactionTrigger(event.CompactionTrigger),
				Before: previousInput,
			}
			if event.ContextTokens > 0 {
				recovery.Before, recovery.BeforeReported = event.ContextTokens, true
			}
			profile.Recoveries = append(profile.Recoveries, recovery)
			recoveryCalls = map[int]bool{}
			previousInput = 0
		case sessionEventToken:
			if !isSample {
				continue
			}
			input := event.ContextTokens
			profile.Inputs = append(profile.Inputs, input)
			if isFirst {
				profile.FirstInput = input
			}
			if event.ContextWindowTokens > 0 {
				profile.Windows = append(profile.Windows, event.ContextWindowTokens)
			}
			if segmentSamples == 0 {
				segmentFirst = input
			}
			segmentSamples++
			segmentPeak = max(segmentPeak, input)
			previousInput = input
			if recovery != nil {
				if recovery.After == 0 {
					recovery.After = input
				}
				recovery.Requests++
			}
		case sessionEventToolCall:
			if recovery != nil {
				recovery.ToolCalls++
				recoveryCalls[event.ToolRound] = true
				if callIncludesReadSearch(event) {
					recovery.ReadSearch++
				}
			}
		case sessionEventToolOutput:
			if recovery != nil && recoveryCalls[event.ToolRound] && event.Failed && event.Refusal == "" {
				recovery.FailedCalls++
			}
		case sessionEventComplete:
			// Compaction machinery can emit a synthetic completion before
			// any resumed work. It is not evidence of a recovery window.
			if recovery != nil && (recovery.Requests > 0 || recovery.ToolCalls > 0) {
				recovery.Completed = true
				recovery.ElapsedSeconds = max(int64(0), int64(event.OccurredAt.Sub(recovery.StartedAt).Seconds()))
				recovery = nil
			}
		}
	}
	finishSegment()
	return profile
}

func analyzeContextProfiles(records []codexSessionRecord) contextProfileAnalysis {
	profile := contextProfileAnalysis{Sessions: len(records)}
	profile.CompactionRecovery.Triggers = map[string]int{}
	var inputs, first, peaks, growth, windows []int64
	var before, after, reduction, requests, calls, reads, failures, elapsed []int64
	for _, record := range records {
		session := record.ContextProfile
		if len(session.Inputs) > 0 {
			profile.SessionsWithSamples++
			var peak int64
			for _, input := range session.Inputs {
				peak = max(peak, input)
			}
			peaks = append(peaks, peak)
		}
		inputs = append(inputs, session.Inputs...)
		growth = append(growth, session.Growth...)
		windows = append(windows, session.Windows...)
		if session.FirstInput > 0 {
			first = append(first, session.FirstInput)
		}
		for _, recovery := range session.Recoveries {
			metrics := &profile.CompactionRecovery
			metrics.Boundaries++
			trigger := recovery.Trigger
			if trigger == "" {
				trigger = "unknown"
			}
			metrics.Triggers[trigger]++
			if recovery.Before > 0 {
				before = append(before, recovery.Before)
				if recovery.BeforeReported {
					metrics.WithReportedBeforeTokens++
				} else {
					metrics.WithBeforeSample++
				}
			}
			if recovery.After > 0 {
				after = append(after, recovery.After)
				metrics.WithAfterSample++
			}
			if recovery.Before > 0 && recovery.After > 0 {
				reduction = append(reduction, recovery.Before-recovery.After)
				metrics.PairedSamples++
			}
			if recovery.Completed {
				metrics.CompletedWindows++
				requests = append(requests, recovery.Requests)
				calls = append(calls, recovery.ToolCalls)
				reads = append(reads, recovery.ReadSearch)
				failures = append(failures, recovery.FailedCalls)
				elapsed = append(elapsed, recovery.ElapsedSeconds)
			} else {
				metrics.CensoredWindows++
			}
		}
	}
	profile.SessionsWithoutSamples = profile.Sessions - profile.SessionsWithSamples
	profile.ObservedInputTokens = summarizeOutcomeDistribution(inputs)
	profile.FirstObservedInputTokens = summarizeOutcomeDistribution(first)
	profile.PeakInputTokens = summarizeOutcomeDistribution(peaks)
	profile.SegmentGrowthTokens = summarizeOutcomeDistribution(growth)
	profile.ReportedWindowTokens = summarizeOutcomeDistribution(windows)
	metrics := &profile.CompactionRecovery
	metrics.BeforeInputTokens = summarizeOutcomeDistribution(before)
	metrics.AfterInputTokens = summarizeOutcomeDistribution(after)
	metrics.InputReductionTokens = summarizeOutcomeDistribution(reduction)
	metrics.RequestSamplesToCompletion = summarizeOutcomeDistribution(requests)
	metrics.ToolCallsToCompletion = summarizeOutcomeDistribution(calls)
	metrics.ReadSearchCallsToCompletion = summarizeOutcomeDistribution(reads)
	metrics.FailedCallsToCompletion = summarizeOutcomeDistribution(failures)
	metrics.ElapsedSecondsToCompletion = summarizeOutcomeDistribution(elapsed)
	return profile
}

// callIncludesReadSearch counts an outer call once when its own command reads
// or searches at any step. A continuation call starts no command and carries
// neither flag nor endpoints, so the family it inherits does not count.
func callIncludesReadSearch(event normalizedSessionEvent) bool {
	if event.ReadSearch {
		return true
	}
	for _, family := range []string{event.FirstFamily, event.LastFamily} {
		if family == "search" || family == "file reads" {
			return true
		}
	}
	return false
}
