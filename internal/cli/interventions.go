package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type sessionIntervention struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	Evidence          string   `json:"evidence"`
	Action            string   `json:"action"`
	Lever             string   `json:"lever"`
	Confidence        string   `json:"confidence"`
	Priority          string   `json:"priority"`
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
	return interventions
}

func interventionFindingPriority(finding sessionFinding) int {
	switch {
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
	if finding.Category == "owned-tool" {
		return signalID("intervention", "tool", finding.Target)
	}
	if finding.Category == "owned-operation" {
		tool, _, _ := strings.Cut(finding.Target, "/")
		return signalID("intervention", "tool", tool)
	}
	if finding.Control == "local" && finding.Lever == "tooling" {
		tool, operation, found := strings.Cut(finding.Target, "/")
		if found && strings.TrimSpace(tool) != "" && strings.TrimSpace(operation) != "" {
			return signalID("intervention", "tool", tool)
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

func printSessionInterventions(interventions []sessionIntervention, limit int) {
	fmt.Println("\nInterventions:")
	if len(interventions) == 0 {
		fmt.Println("- No current findings met the recurrence and impact thresholds.")
		return
	}
	rows := interventions
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	for _, intervention := range rows {
		fmt.Printf("- [%s priority · %s/%s] %s\n", intervention.Priority, intervention.Lever, intervention.Confidence, intervention.Title)
		fmt.Printf("  Intervention: %s\n", intervention.ID)
		fmt.Printf("  Primary signal: %s\n", intervention.PrimarySignal)
		fmt.Printf("  Evidence: %s.\n", strings.TrimSuffix(intervention.Evidence, "."))
		if len(intervention.SupportingSignals) > 0 {
			fmt.Printf("  Supporting signals: %s\n", strings.Join(intervention.SupportingSignals, ", "))
		}
		if intervention.LastSeen != "" {
			fmt.Printf("  Last seen: %s\n", formatSessionFindingAge(intervention.LastSeen, time.Now().UTC()))
		}
		fmt.Printf("  Next: %s\n", intervention.Action)
	}
	if len(rows) < len(interventions) {
		fmt.Printf("... %d more interventions; use --limit 0 for the full queue or --details for constituent findings.\n", len(interventions)-len(rows))
	}
}
