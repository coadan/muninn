package cli

// skillUsageMetrics describes observed work after a successful skill load,
// until compaction or the end of the session. Active skills overlap: these counters must
// not be summed across skills or interpreted as causal costs. Child sessions
// are not attributed to a parent's skills.
// Tokens follow telemetry order; delayed invocation telemetry can be included.
type skillUsageMetrics struct {
	TokenBasis       string               `json:"tokenBasis"`
	SuccessfulLoads  int                  `json:"successfulLoads"`
	Tokens           normalizedTokenUsage `json:"tokens"`
	FreshTokens      int64                `json:"freshTokens"`
	ToolCalls        int                  `json:"toolCalls"`
	FailedToolCalls  int                  `json:"failedToolCalls"`
	ReviewCalls      int                  `json:"reviewCalls"`
	ReviewEditCycles int                  `json:"reviewEditCycles"`
	CompletedTurns   int                  `json:"completedTurns"`
	IncompleteTurns  int                  `json:"incompleteTurns"`
}

type skillUsageState struct {
	pending            map[int][]string
	active             map[string]bool
	task               map[string]bool
	reviewAwaitingEdit map[string]bool
	metrics            map[string]skillUsageMetrics
}

func newSkillUsageState() skillUsageState {
	return skillUsageState{
		pending: map[int][]string{}, active: map[string]bool{},
		task: map[string]bool{}, metrics: map[string]skillUsageMetrics{}, reviewAwaitingEdit: map[string]bool{},
	}
}

func (state *skillUsageState) observe(event normalizedSessionEvent, tokens normalizedTokenUsage) {
	// Repeated cumulative telemetry is not a new turn of work.
	if event.Kind == sessionEventToken && tokens == (normalizedTokenUsage{}) {
		return
	}
	switch event.Kind {
	case sessionEventComplete:
		state.finish(true)
	case sessionEventCompaction:
		state.finish(false)
	case sessionEventToken, sessionEventToolCall, sessionEventToolOutput:
		for id := range state.active {
			state.task[id] = true
			metrics := state.metrics[id]
			switch event.Kind {
			case sessionEventToken:
				addNormalizedTokenUsage(&metrics.Tokens, tokens)
			case sessionEventToolCall:
				metrics.ToolCalls++
				if reviewStart(event, event.OwnedOperations) {
					metrics.ReviewCalls++
					state.reviewAwaitingEdit[id] = true
				}
				if event.ToolName == "apply_patch" && state.reviewAwaitingEdit[id] {
					metrics.ReviewEditCycles++
					delete(state.reviewAwaitingEdit, id)
				}
			case sessionEventToolOutput:
				if event.Failed {
					metrics.FailedToolCalls++
				}
			}
			state.metrics[id] = metrics
		}
		if event.Kind == sessionEventToolCall {
			for _, artifact := range event.GuidanceArtifacts {
				if artifact.Kind == "skill" && guidanceArtifactIDPattern.MatchString(artifact.ID) {
					state.pending[event.ToolRound] = appendUniqueString(state.pending[event.ToolRound], artifact.ID)
				}
			}
		}
		if event.Kind == sessionEventToolOutput {
			if event.OperationContinues {
				return
			}
			for _, id := range state.pending[event.ToolRound] {
				if event.Failed || event.Refusal != "" {
					continue
				}
				state.active[id], state.task[id] = true, true
				metrics := state.metrics[id]
				metrics.SuccessfulLoads++
				state.metrics[id] = metrics
			}
			delete(state.pending, event.ToolRound)
		}
	}
}

func (state *skillUsageState) finish(completed bool) {
	for id := range state.task {
		metrics := state.metrics[id]
		if completed {
			metrics.CompletedTurns++
		} else {
			metrics.IncompleteTurns++
		}
		state.metrics[id] = metrics
	}
	clear(state.task)
	if completed {
		// A provider end_turn is not the end of a multi-turn workflow.
		// Loaded skills remain in context for the following turn.
		return
	}
	clear(state.active)
	clear(state.pending)
	clear(state.reviewAwaitingEdit)
}

func addSkillUsageMetrics(total *skillUsageMetrics, addition skillUsageMetrics) {
	if addition.SuccessfulLoads > 0 {
		total.TokenBasis = "telemetry-after-load-result"
	}
	total.SuccessfulLoads += addition.SuccessfulLoads
	addNormalizedTokenUsage(&total.Tokens, addition.Tokens)
	total.FreshTokens = total.Tokens.UncachedInputTokens + total.Tokens.OutputTokens
	total.ToolCalls += addition.ToolCalls
	total.FailedToolCalls += addition.FailedToolCalls
	total.ReviewCalls += addition.ReviewCalls
	total.ReviewEditCycles += addition.ReviewEditCycles
	total.CompletedTurns += addition.CompletedTurns
	total.IncompleteTurns += addition.IncompleteTurns
}
