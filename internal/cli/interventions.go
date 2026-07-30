package cli

import (
	"fmt"
	"sort"
	"strings"
)

type sessionIntervention struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	Evidence          string   `json:"evidence"`
	Action            string   `json:"action"`
	Lever             string   `json:"lever"`
	Confidence        string   `json:"confidence"`
	Priority          string   `json:"priority"`
	Focus             string   `json:"focus"`
	PrimarySignal     string   `json:"primarySignal"`
	SupportingSignals []string `json:"supportingSignals,omitempty"`
	FindingCount      int      `json:"findingCount"`
	LastSeen          string   `json:"lastSeen,omitempty"`
	score             int
	priority          int
}

func buildSessionInterventions(findings []sessionFinding) []sessionIntervention {
	dominantPhases := map[string]bool{}
	for _, finding := range findings {
		if phase, ok := compactionPhaseFinding(finding); ok {
			dominantPhases[phase] = true
		}
	}
	groups := map[string][]sessionFinding{}
	var order []string
	for _, finding := range findings {
		key := sessionInterventionKey(finding, dominantPhases)
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], finding)
	}

	interventions := make([]sessionIntervention, 0, len(groups))
	for _, key := range order {
		members := groups[key]
		primary := members[0]
		maxScore := primary.score
		maxPriority := interventionFindingPriority(primary)
		for _, candidate := range members[1:] {
			maxScore = max(maxScore, candidate.score)
			maxPriority = max(maxPriority, interventionFindingPriority(candidate))
			candidatePriority := interventionFindingPriority(candidate)
			primaryPriority := interventionFindingPriority(primary)
			if candidatePriority > primaryPriority ||
				(candidatePriority == primaryPriority &&
					(interventionPrimaryPriority(candidate) > interventionPrimaryPriority(primary) ||
						(interventionPrimaryPriority(candidate) == interventionPrimaryPriority(primary) &&
							sessionFindingHigherImpact(candidate, primary)))) {
				primary = candidate
			}
		}
		signals := map[string]bool{primary.Signal: true}
		for _, member := range members {
			signals[member.Signal] = true
			for _, signal := range member.Supporting {
				signals[signal] = true
			}
		}
		delete(signals, primary.Signal)
		supporting := make([]string, 0, len(signals))
		for signal := range signals {
			supporting = append(supporting, signal)
		}
		sort.Strings(supporting)
		evidence := primary.Evidence
		if len(members) > 1 {
			evidence += fmt.Sprintf("; corroborated by %s additional findings", formatCodexCount(int64(len(members)-1)))
		}
		interventions = append(interventions, sessionIntervention{
			ID:                key,
			Title:             primary.Title,
			Evidence:          evidence,
			Action:            primary.Action,
			Lever:             primary.Lever,
			Confidence:        primary.Confidence,
			Priority:          interventionPriorityLabel(maxPriority),
			Focus:             sessionInterventionFocus(key, primary),
			PrimarySignal:     primary.Signal,
			SupportingSignals: supporting,
			FindingCount:      len(members),
			LastSeen:          latestInterventionFinding(members),
			score:             maxScore + 75*(len(members)-1),
			priority:          maxPriority,
		})
	}
	sort.Slice(interventions, func(i, j int) bool {
		if interventions[i].priority != interventions[j].priority {
			return interventions[i].priority > interventions[j].priority
		}
		if interventions[i].score != interventions[j].score {
			return interventions[i].score > interventions[j].score
		}
		if interventions[i].FindingCount != interventions[j].FindingCount {
			return interventions[i].FindingCount > interventions[j].FindingCount
		}
		if interventions[i].LastSeen != interventions[j].LastSeen {
			return interventions[i].LastSeen > interventions[j].LastSeen
		}
		return interventions[i].ID < interventions[j].ID
	})
	return diversifySessionInterventions(interventions)
}

func sessionInterventionFocus(id string, primary sessionFinding) string {
	switch id {
	case "intervention/workflow/discovery":
		return "discovery"
	case "intervention/workflow/verification":
		return "loops"
	default:
		return sessionFindingFocus(primary)
	}
}

func diversifySessionInterventions(interventions []sessionIntervention) []sessionIntervention {
	result := make([]sessionIntervention, 0, len(interventions))
	deferred := make([]sessionIntervention, 0)
	ownerSelected := false
	selectedTools := map[string]bool{}
	preferredOperation := map[string]string{}
	for _, intervention := range interventions {
		if !strings.HasPrefix(intervention.ID, "intervention/operation/") {
			continue
		}
		tool := sessionInterventionTool(intervention.ID)
		if preferredOperation[tool] == "" {
			preferredOperation[tool] = intervention.ID
		}
	}
	for _, intervention := range interventions {
		if ownerIntervention(intervention.ID) {
			if ownerSelected {
				deferred = append(deferred, intervention)
				continue
			}
			ownerSelected = true
		}
		if tool := sessionInterventionTool(intervention.ID); tool != "" {
			if preferred := preferredOperation[tool]; preferred != "" && intervention.ID != preferred {
				deferred = append(deferred, intervention)
				continue
			}
			if selectedTools[tool] {
				deferred = append(deferred, intervention)
				continue
			}
			selectedTools[tool] = true
		}
		result = append(result, intervention)
	}
	return append(result, deferred...)
}

func ownerIntervention(id string) bool {
	return strings.HasPrefix(id, "intervention/owner/") ||
		strings.HasPrefix(id, "intervention/code-structure/file-cost/")
}

func sessionInterventionTool(id string) string {
	for _, prefix := range []string{"intervention/tool/", "intervention/operation/"} {
		if target, found := strings.CutPrefix(id, prefix); found {
			tool, _, _ := strings.Cut(target, "/")
			return tool
		}
	}
	return ""
}

func interventionFindingPriority(finding sessionFinding) int {
	switch {
	case finding.Category == "code-structure" &&
		strings.HasPrefix(finding.Signal, "code-structure/file-cost/"):
		return 1
	case finding.Category == "owned-operation" &&
		strings.HasPrefix(finding.Title, "high-cost locally controlled operation: "):
		return 2
	case (finding.Category == "owned-operation" || finding.Category == "owned-tool") &&
		strings.Contains(finding.Title, "concentrated single-session friction"):
		return 2
	case finding.Control == "local" && finding.Confidence == "high":
		return 4
	case finding.Confidence == "high",
		finding.Control == "local" && finding.Confidence == "medium":
		return 3
	case finding.Confidence == "medium":
		return 2
	default:
		return 1
	}
}

func interventionPriorityLabel(priority int) string {
	switch priority {
	case 4:
		return "highest"
	case 3:
		return "high"
	case 2:
		return "medium"
	default:
		return "low"
	}
}

func sessionInterventionKey(finding sessionFinding, dominantPhases map[string]bool) string {
	if strings.HasPrefix(finding.Title, "recurring owned-operation chain: ") {
		return signalID("intervention", "workflow", "owned-operations", finding.Target)
	}
	if strings.HasPrefix(finding.Title, "frequently repeated CLI flag may belong in the default: ") {
		return signalID("intervention", "default", finding.Target)
	}
	if finding.Category == "owned-tool" {
		return signalID("intervention", "tool", finding.Target)
	}
	if finding.Category == "owned-operation" {
		return signalID("intervention", "operation", finding.Target)
	}
	if finding.Control == "local" && finding.Lever == "tooling" {
		tool, operation, found := strings.Cut(finding.Target, "/")
		if found && strings.TrimSpace(tool) != "" && strings.TrimSpace(operation) != "" {
			return signalID("intervention", "operation", finding.Target)
		}
	}
	if phase, ok := compactionPhaseFinding(finding); ok {
		return signalID("intervention", "workflow", phase)
	}
	if finding.Signal == "session-loop/context-compactions-indicate-long-or-looping-sessions" {
		for phase := range dominantPhases {
			return signalID("intervention", "workflow", phase)
		}
		return "intervention/session/compaction"
	}
	if finding.Category == "discovery" ||
		finding.Signal == "agent-interface/repeated-cross-call-workflow-source-discovery-and-navigation" ||
		finding.Signal == "output-cost/file-reads" {
		return "intervention/workflow/discovery"
	}
	if finding.Category == "verification-loop" ||
		finding.Signal == "agent-interface/repeated-cross-call-workflow-verification" {
		return "intervention/workflow/verification"
	}
	return "intervention/" + finding.Signal
}

func compactionPhaseFinding(finding sessionFinding) (string, bool) {
	const prefix = "context compactions concentrate in phase: "
	if finding.Category != "session-loop" || !strings.HasPrefix(finding.Title, prefix) {
		return "", false
	}
	phase := strings.TrimSpace(strings.TrimPrefix(finding.Title, prefix))
	return phase, phase != ""
}

func interventionPrimaryPriority(finding sessionFinding) int {
	switch {
	case finding.Category == "owned-operation", finding.Category == "verification-loop":
		return 6
	case finding.Category == "owned-tool":
		return 5
	case finding.Category == "default-candidate":
		return 5
	case strings.HasPrefix(finding.Title, "context compactions concentrate in phase: "):
		return 5
	case finding.Category == "discovery":
		return 4
	case finding.Category == "agent-interface":
		return 3
	case finding.Category == "output-cost":
		return 2
	default:
		return 1
	}
}

func latestInterventionFinding(findings []sessionFinding) string {
	latest := ""
	for _, finding := range findings {
		if finding.LastSeen > latest {
			latest = finding.LastSeen
		}
	}
	return latest
}
