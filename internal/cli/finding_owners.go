package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func materialOwnerRediscovery(metrics codexTargetMetrics) bool {
	return metrics.Sessions >= 2 &&
		metrics.RediscoverySessions >= 3 &&
		metrics.RediscoverySessions*5 >= metrics.Sessions
}

func ownerRediscoveryConfidence(metrics codexTargetMetrics) string {
	if metrics.RediscoverySessions >= 5 &&
		metrics.RediscoverySessions*2 >= metrics.Sessions {
		return "high"
	}
	return "medium"
}

func consolidateOwnerFindings(findings []sessionFinding) []sessionFinding {
	groups := map[string][]int{}
	for index, finding := range findings {
		if !consolidatableOwnerFinding(finding) {
			continue
		}
		groups[finding.Target] = append(groups[finding.Target], index)
	}
	removed := map[int]bool{}
	for target, indices := range groups {
		if len(indices) < 2 {
			index := indices[0]
			if findings[index].Title == "repeated navigation into a current source owner" {
				findings[index].Confidence = "medium"
				findings[index].score = max(0, findings[index].score-75)
				findings[index].Why = "This is repeated discovery evidence only; treat it as a navigation candidate until cost or quality signals corroborate it."
			}
			continue
		}
		primaryIndex := indices[0]
		for _, index := range indices[1:] {
			if ownerFindingPriority(findings[index]) > ownerFindingPriority(findings[primaryIndex]) ||
				(ownerFindingPriority(findings[index]) == ownerFindingPriority(findings[primaryIndex]) &&
					sessionFindingHigherImpact(findings[index], findings[primaryIndex])) {
				primaryIndex = index
			}
		}
		primary := findings[primaryIndex]
		var supportingSignals, supportingTitles []string
		for _, index := range indices {
			member := findings[index]
			primary.Count = max(primary.Count, member.Count)
			primary.Sessions = max(primary.Sessions, member.Sessions)
			if member.LastSeen > primary.LastSeen {
				primary.LastSeen = member.LastSeen
			}
			supportingSignals = append(supportingSignals, member.Signal)
			if index == primaryIndex {
				continue
			}
			removed[index] = true
			supportingTitles = append(supportingTitles, member.Title)
		}
		sort.Strings(supportingSignals)
		sort.Strings(supportingTitles)
		primary.Signal = signalID("owner", target)
		primary.Supporting = supportingSignals
		primary.Evidence += "; corroborating signals: " + strings.Join(supportingTitles, "; ")
		primary.Why = ownerFindingWhy(indices, findings)
		primary.score += 100 * (len(indices) - 1)
		if len(indices) >= 3 || ownerFindingsIncludeCategory(indices, findings, "delivery-quality") {
			primary.Confidence = "high"
		} else if primary.Confidence == "low" {
			primary.Confidence = "medium"
		}
		findings[primaryIndex] = primary
	}
	result := make([]sessionFinding, 0, len(findings)-len(removed))
	for index, finding := range findings {
		if !removed[index] {
			result = append(result, finding)
		}
	}
	return result
}

func consolidatableOwnerFinding(finding sessionFinding) bool {
	if finding.Target == "" || filepath.Ext(filepath.Base(finding.Target)) == "" {
		return false
	}
	switch finding.Category {
	case "code-structure", "instruction-discovery", "delivery-quality":
		return true
	default:
		return false
	}
}

func ownerFindingPriority(finding sessionFinding) int {
	switch {
	case finding.Category == "delivery-quality":
		return 4
	case finding.Title == "repeated edits correlate with high task cost":
		return 3
	case finding.Category == "code-structure":
		return 2
	default:
		return 1
	}
}

func ownerFindingWhy(indices []int, findings []sessionFinding) string {
	hasQuality := ownerFindingsIncludeCategory(indices, findings, "delivery-quality")
	hasCost := false
	hasNavigation := false
	for _, index := range indices {
		finding := findings[index]
		hasCost = hasCost || finding.Title == "repeated edits correlate with high task cost"
		hasNavigation = hasNavigation ||
			finding.Title == "repeated navigation into a current source owner" ||
			finding.Category == "instruction-discovery"
	}
	switch {
	case hasQuality && hasCost:
		return "This owner combines high agent task cost with observed delivery-quality exposure, making it a stronger improvement candidate than either signal alone."
	case hasQuality && hasNavigation:
		return "Agents repeatedly rediscover this owner and observed corrections or failures survive delivery, linking navigation friction with quality exposure."
	case hasCost && hasNavigation:
		return "Agents repeatedly rediscover this owner and spend materially high fresh tokens or roundtrips changing it, so the navigation signal has direct task-cost support."
	default:
		return "Multiple independent findings point to the same owner, increasing confidence that the reported friction is actionable."
	}
}

func ownerFindingsIncludeCategory(indices []int, findings []sessionFinding, category string) bool {
	for _, index := range indices {
		if findings[index].Category == category {
			return true
		}
	}
	return false
}

func hotspotFindingConfidence(observations int) string {
	if observations >= 3 {
		return "high"
	}
	return "medium"
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
