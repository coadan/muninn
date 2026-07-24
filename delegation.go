package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type modelEffortAnalysis struct {
	Available bool                 `json:"available"`
	Profiles  []modelEffortProfile `json:"profiles,omitempty"`
}

type modelEffortProfile struct {
	AgentKind                    string              `json:"agentKind"`
	Model                        string              `json:"model"`
	ReasoningEffort              string              `json:"reasoningEffort"`
	Sessions                     int                 `json:"sessions"`
	CompletedSessions            int                 `json:"completedSessions"`
	CompletedToolTasks           int                 `json:"completedToolTasks"`
	FreshTokens                  int64               `json:"freshTokens"`
	FreshTokenShare              float64             `json:"freshTokenShare"`
	CompletedTaskFreshTokens     int64               `json:"completedTaskFreshTokens"`
	ToolCalls                    int                 `json:"toolCalls"`
	CompletedTaskToolCalls       int                 `json:"completedTaskToolCalls"`
	FailedCalls                  int                 `json:"failedCalls"`
	VisibleOutputTokens          int64               `json:"visibleOutputTokens"`
	FreshTokensPerSession        int64               `json:"freshTokensPerSession"`
	FreshTokensPerCompletedTask  int64               `json:"freshTokensPerCompletedTask"`
	ToolCallsPerCompletedTask    float64             `json:"toolCallsPerCompletedTask"`
	CompletedToolTasksPerHour    float64             `json:"completedToolTasksPerHour"`
	SessionDurationSeconds       outcomeDistribution `json:"sessionDurationSeconds"`
	CompletedTaskDurationSeconds outcomeDistribution `json:"completedTaskDurationSeconds"`
}

type delegationAnalysis struct {
	Available                           bool           `json:"available"`
	ParentSessions                      int            `json:"parentSessions"`
	DelegatingParents                   int            `json:"delegatingParents"`
	SubagentSessions                    int            `json:"subagentSessions"`
	LinkedSubagents                     int            `json:"linkedSubagents"`
	OrphanSubagents                     int            `json:"orphanSubagents"`
	CompletedSubagents                  int            `json:"completedSubagents"`
	IncompleteSubagents                 int            `json:"incompleteSubagents"`
	ParentFreshTokens                   int64          `json:"parentFreshTokens"`
	SubagentFreshTokens                 int64          `json:"subagentFreshTokens"`
	SubagentFreshTokenShare             float64        `json:"subagentFreshTokenShare"`
	ParentToolCalls                     int            `json:"parentToolCalls"`
	SubagentToolCalls                   int            `json:"subagentToolCalls"`
	CoordinationCalls                   int            `json:"coordinationCalls"`
	CoordinationFreshTokens             int64          `json:"coordinationFreshTokens"`
	CoordinationOutputTokens            int64          `json:"coordinationOutputTokens"`
	CoordinationCallsByAction           map[string]int `json:"coordinationCallsByAction,omitempty"`
	ChildrenWithUniqueEdits             int            `json:"childrenWithUniqueEdits"`
	ChildrenWithEditOverlap             int            `json:"childrenWithEditOverlap"`
	EditOverlapTargets                  int            `json:"editOverlapTargets"`
	ChildrenWithReadOverlap             int            `json:"childrenWithReadOverlap"`
	ReadOverlapTargets                  int            `json:"readOverlapTargets"`
	OverlappingChildFreshTokens         int64          `json:"overlappingChildFreshTokens"`
	NoObservableContributionSubagents   int            `json:"noObservableContributionSubagents"`
	NoObservableContributionFreshTokens int64          `json:"noObservableContributionFreshTokens"`
	SummedSubagentDurationSeconds       int64          `json:"summedSubagentDurationSeconds"`
	DelegatedWallSeconds                int64          `json:"delegatedWallSeconds"`
	SubagentConcurrencyFactor           float64        `json:"subagentConcurrencyFactor"`
}

type modelEffortProfileAccumulator struct {
	profile      modelEffortProfile
	sessionTimes []int64
	taskTimes    []int64
	taskSeconds  int64
}

func analyzeModelEffortProfiles(records []codexSessionRecord) modelEffortAnalysis {
	analysis := modelEffortAnalysis{}
	accumulators := map[string]*modelEffortProfileAccumulator{}
	var totalFreshTokens int64
	for _, record := range records {
		if record.Model != "" || record.ReasoningEffort != "" || record.AgentKind != "" {
			analysis.Available = true
		}
		agentKind := nonemptyProfileLabel(record.AgentKind, "(unknown)")
		model := nonemptyProfileLabel(record.Model, "(unknown)")
		effort := nonemptyProfileLabel(record.ReasoningEffort, "(unknown)")
		key := strings.Join([]string{agentKind, model, effort}, "\x00")
		current := accumulators[key]
		if current == nil {
			current = &modelEffortProfileAccumulator{profile: modelEffortProfile{
				AgentKind:       agentKind,
				Model:           model,
				ReasoningEffort: effort,
			}}
			accumulators[key] = current
		}
		current.profile.Sessions++
		if record.Completed {
			current.profile.CompletedSessions++
		}
		fresh := recordFreshTokens(record)
		totalFreshTokens += fresh
		current.profile.FreshTokens += fresh
		current.profile.ToolCalls += record.ToolCalls
		current.profile.FailedCalls += record.FailedToolCalls
		current.profile.VisibleOutputTokens += estimatedTokens(record.ToolOutputBytes)
		duration := codexSessionDuration(record)
		current.sessionTimes = append(current.sessionTimes, duration)
		for _, episode := range record.TaskEpisodes {
			if !episode.Completed || episode.LeftCensored || episode.ToolCalls == 0 {
				continue
			}
			current.profile.CompletedToolTasks++
			current.profile.CompletedTaskFreshTokens += episodeFreshTokens(episode)
			current.profile.CompletedTaskToolCalls += episode.ToolCalls
			taskDuration := taskEpisodeDuration(episode)
			current.taskTimes = append(current.taskTimes, taskDuration)
			current.taskSeconds += taskDuration
		}
	}
	if !analysis.Available {
		return modelEffortAnalysis{}
	}
	for _, current := range accumulators {
		current.profile.FreshTokenShare = ratio(
			float64(current.profile.FreshTokens),
			float64(totalFreshTokens),
		)
		current.profile.FreshTokensPerSession = perSessionTokens(
			current.profile.FreshTokens,
			current.profile.Sessions,
		)
		current.profile.FreshTokensPerCompletedTask = perSessionTokens(
			current.profile.CompletedTaskFreshTokens,
			current.profile.CompletedToolTasks,
		)
		if current.profile.CompletedToolTasks > 0 {
			current.profile.ToolCallsPerCompletedTask = ratio(
				float64(current.profile.CompletedTaskToolCalls),
				float64(current.profile.CompletedToolTasks),
			)
		}
		if current.taskSeconds > 0 {
			current.profile.CompletedToolTasksPerHour = ratio(
				3600*float64(current.profile.CompletedToolTasks),
				float64(current.taskSeconds),
			)
		}
		current.profile.SessionDurationSeconds = summarizeOutcomeDistribution(current.sessionTimes)
		current.profile.CompletedTaskDurationSeconds = summarizeOutcomeDistribution(current.taskTimes)
		analysis.Profiles = append(analysis.Profiles, current.profile)
	}
	sort.Slice(analysis.Profiles, func(i, j int) bool {
		if analysis.Profiles[i].FreshTokens != analysis.Profiles[j].FreshTokens {
			return analysis.Profiles[i].FreshTokens > analysis.Profiles[j].FreshTokens
		}
		if analysis.Profiles[i].Sessions != analysis.Profiles[j].Sessions {
			return analysis.Profiles[i].Sessions > analysis.Profiles[j].Sessions
		}
		left := analysis.Profiles[i]
		right := analysis.Profiles[j]
		return strings.Join([]string{left.AgentKind, left.Model, left.ReasoningEffort}, "\x00") <
			strings.Join([]string{right.AgentKind, right.Model, right.ReasoningEffort}, "\x00")
	})
	return analysis
}

func analyzeDelegation(records []codexSessionRecord) delegationAnalysis {
	analysis := delegationAnalysis{CoordinationCallsByAction: map[string]int{}}
	byLineage := map[string]*codexSessionRecord{}
	for index := range records {
		record := &records[index]
		if record.LineageKey != "" {
			analysis.Available = true
			byLineage[record.LineageKey] = record
		}
		if record.AgentKind == "subagent" {
			analysis.SubagentSessions++
			analysis.SubagentFreshTokens += recordFreshTokens(*record)
			analysis.SubagentToolCalls += record.ToolCalls
			if record.Completed || record.SpawnStatus == "completed" {
				analysis.CompletedSubagents++
			} else {
				analysis.IncompleteSubagents++
			}
		} else {
			analysis.ParentSessions++
			analysis.ParentFreshTokens += recordFreshTokens(*record)
			analysis.ParentToolCalls += record.ToolCalls
		}
		for action, calls := range record.ToolCallsByName {
			if !delegationToolName(action) {
				continue
			}
			analysis.CoordinationCalls += calls
			analysis.CoordinationCallsByAction[action] += calls
		}
		for _, episode := range record.TaskEpisodes {
			phase, ok := episode.Phases["delegation"]
			if !ok {
				continue
			}
			analysis.CoordinationFreshTokens += phaseFreshTokens(phase)
			analysis.CoordinationOutputTokens += estimatedTokens(phase.ToolOutputBytes)
		}
	}
	if !analysis.Available {
		return delegationAnalysis{}
	}
	totalFresh := analysis.ParentFreshTokens + analysis.SubagentFreshTokens
	analysis.SubagentFreshTokenShare = ratio(
		float64(analysis.SubagentFreshTokens),
		float64(totalFresh),
	)

	parentIntervals := map[string][]timeInterval{}
	delegatingParents := map[string]struct{}{}
	editOverlap := map[string]struct{}{}
	readOverlap := map[string]struct{}{}
	for index := range records {
		child := &records[index]
		if child.AgentKind != "subagent" {
			continue
		}
		parent := byLineage[child.ParentLineageKey]
		if parent == nil {
			analysis.OrphanSubagents++
			continue
		}
		analysis.LinkedSubagents++
		delegatingParents[parent.LineageKey] = struct{}{}
		if child.EndedAt.After(child.StartedAt) {
			parentIntervals[parent.LineageKey] = append(parentIntervals[parent.LineageKey], timeInterval{
				start: child.StartedAt,
				end:   child.EndedAt,
			})
			analysis.SummedSubagentDurationSeconds += int64(child.EndedAt.Sub(child.StartedAt).Seconds())
		}
		childUniqueEdits := false
		childEditOverlap := false
		for target := range child.EditTargets {
			if parent.EditTargets[target] > 0 {
				childEditOverlap = true
				editOverlap[target] = struct{}{}
			} else {
				childUniqueEdits = true
			}
		}
		if childUniqueEdits {
			analysis.ChildrenWithUniqueEdits++
		}
		if childEditOverlap {
			analysis.ChildrenWithEditOverlap++
			analysis.OverlappingChildFreshTokens += recordFreshTokens(*child)
		}
		childReadOverlap := false
		for target := range child.ReadTargets {
			if _, ok := parent.ReadTargets[target]; ok {
				childReadOverlap = true
				readOverlap[target] = struct{}{}
			}
		}
		if childReadOverlap {
			analysis.ChildrenWithReadOverlap++
		}
		if len(child.EditTargets) == 0 && child.DeliveryRework.Deliveries == 0 {
			analysis.NoObservableContributionSubagents++
			analysis.NoObservableContributionFreshTokens += recordFreshTokens(*child)
		}
	}
	analysis.DelegatingParents = len(delegatingParents)
	analysis.EditOverlapTargets = len(editOverlap)
	analysis.ReadOverlapTargets = len(readOverlap)
	for _, intervals := range parentIntervals {
		analysis.DelegatedWallSeconds += unionDurationSeconds(intervals)
	}
	analysis.SubagentConcurrencyFactor = ratio(
		float64(analysis.SummedSubagentDurationSeconds),
		float64(analysis.DelegatedWallSeconds),
	)
	return analysis
}

type timeInterval struct {
	start time.Time
	end   time.Time
}

func unionDurationSeconds(intervals []timeInterval) int64 {
	if len(intervals) == 0 {
		return 0
	}
	sorted := append([]timeInterval(nil), intervals...)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].start.Equal(sorted[j].start) {
			return sorted[i].start.Before(sorted[j].start)
		}
		return sorted[i].end.Before(sorted[j].end)
	})
	start, end := sorted[0].start, sorted[0].end
	var total int64
	for _, interval := range sorted[1:] {
		if interval.start.After(end) {
			total += int64(end.Sub(start).Seconds())
			start, end = interval.start, interval.end
			continue
		}
		if interval.end.After(end) {
			end = interval.end
		}
	}
	return total + int64(end.Sub(start).Seconds())
}

func recordFreshTokens(record codexSessionRecord) int64 {
	return record.Tokens.UncachedInputTokens + record.Tokens.OutputTokens
}

func nonemptyProfileLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func delegationToolName(name string) bool {
	name = name[strings.LastIndex(name, ".")+1:]
	switch name {
	case "spawn_agent", "followup_task", "send_message", "wait_agent", "interrupt_agent", "list_agents":
		return true
	default:
		return false
	}
}

func printModelEffortAnalysis(analysis modelEffortAnalysis) {
	if !analysis.Available || len(analysis.Profiles) == 0 {
		return
	}
	fmt.Println("Model/effort cohorts:")
	for _, profile := range analysis.Profiles {
		fmt.Printf(
			"- %s %s/%s: %s sessions, %s total fresh tokens (%.1f%%, %s/session), %s completed tool tasks at %s fresh tokens/task, %.1f tool calls/task, %.2f tasks/hour; task duration p50/p90 %s/%s\n",
			profile.AgentKind,
			profile.Model,
			profile.ReasoningEffort,
			formatCodexCount(int64(profile.Sessions)),
			formatCodexCount(profile.FreshTokens),
			100*profile.FreshTokenShare,
			formatCodexCount(profile.FreshTokensPerSession),
			formatCodexCount(int64(profile.CompletedToolTasks)),
			formatCodexCount(profile.FreshTokensPerCompletedTask),
			profile.ToolCallsPerCompletedTask,
			profile.CompletedToolTasksPerHour,
			formatDurationSeconds(profile.CompletedTaskDurationSeconds.P50),
			formatDurationSeconds(profile.CompletedTaskDurationSeconds.P90),
		)
	}
}

func printDelegationAnalysis(analysis delegationAnalysis) {
	if !analysis.Available || analysis.SubagentSessions == 0 {
		return
	}
	fmt.Printf(
		"Delegation: %s subagent sessions across %s parents used %.1f%% of fresh tokens; %s coordination calls (~%s fresh tokens); %s children overlapped parent edits; observed concurrency %.2fx\n",
		formatCodexCount(int64(analysis.SubagentSessions)),
		formatCodexCount(int64(analysis.DelegatingParents)),
		100*analysis.SubagentFreshTokenShare,
		formatCodexCount(int64(analysis.CoordinationCalls)),
		formatCodexCount(analysis.CoordinationFreshTokens),
		formatCodexCount(int64(analysis.ChildrenWithEditOverlap)),
		analysis.SubagentConcurrencyFactor,
	)
	if analysis.NoObservableContributionSubagents > 0 {
		fmt.Printf(
			"Delegation attribution: %s subagents (~%s fresh tokens) had no observable edit or delivery; research/review contributions are not inferable from repository metadata.\n",
			formatCodexCount(int64(analysis.NoObservableContributionSubagents)),
			formatCodexCount(analysis.NoObservableContributionFreshTokens),
		)
	}
}
