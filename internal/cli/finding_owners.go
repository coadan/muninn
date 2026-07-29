package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func ownerRediscoveryFindings(report codexSessionInsightsReport, config repositoryConfig) []sessionFinding {
	var findings []sessionFinding
	for target, metrics := range report.Summary.ReadTargets {
		if metrics.Sessions < 2 || metrics.Reads < 4 {
			continue
		}
		path := filepath.Join(report.WorkspaceRoot, filepath.FromSlash(target))
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		const boundedOwnerBytes = 12 * 1024
		if info.Size() <= boundedOwnerBytes || strings.EqualFold(filepath.Base(target), "AGENTS.md") || repositoryManifestTarget(target) {
			if !materialOwnerRediscovery(metrics) {
				continue
			}
			title, action := ownerRediscoveryPolicy(target, config)
			findings = append(findings, sessionFinding{
				Category: "instruction-discovery",
				Control:  "repository",
				Title:    title,
				Evidence: fmt.Sprintf("%s reads and %s search/read loops across %s sessions; rediscovery affected %s sessions; current size %s bytes",
					formatCodexCount(int64(metrics.Reads)),
					formatCodexCount(int64(metrics.SearchReadLoops)),
					formatCodexCount(int64(metrics.Sessions)),
					formatCodexCount(int64(metrics.RediscoverySessions)),
					formatCodexCount(info.Size()),
				),
				Action:     action,
				Count:      metrics.Reads,
				Sessions:   metrics.Sessions,
				Target:     target,
				LastSeen:   sessionFindingLastSeen(report, "read", target),
				Confidence: ownerRediscoveryConfidence(metrics),
				score:      320 + metrics.RediscoverySessions*20 + metrics.SearchReadLoops*10 + metrics.Reads,
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
	return findings
}

func fileHotspotFindings(report codexSessionInsightsReport) []sessionFinding {
	var findings []sessionFinding
	for _, hotspot := range report.Outcomes.FileHotspots {
		if _, _, exists := repositoryTargetSize(report.WorkspaceRoot, hotspot.Target); !exists {
			continue
		}
		switch hotspot.Classification {
		case "expensive-owner":
			if hotspot.CompletedTasks < 3 || hotspot.ToolRoundtrips.P50 < 50 {
				continue
			}
			findings = append(findings, sessionFinding{
				Category: "code-structure",
				Control:  "repository",
				Title:    "repeated edits correlate with high task cost",
				Evidence: fmt.Sprintf(
					"%s completed tasks (%.1f%% of tool-using tasks) edited this target %s times; fresh tokens p50/p90 %s/%s and task roundtrips p50/p90 %s/%s",
					formatCodexCount(int64(hotspot.CompletedTasks)),
					100*hotspot.TaskShare,
					formatCodexCount(int64(hotspot.EditCalls)),
					formatCodexCount(hotspot.FreshTokens.P50),
					formatCodexCount(hotspot.FreshTokens.P90),
					formatCodexCount(hotspot.ToolRoundtrips.P50),
					formatCodexCount(hotspot.ToolRoundtrips.P90),
				),
				Action:     "Trace one representative change through this owner and its smallest verification. Improve navigation, inspection, or the focused test interface if the owner is cohesive; split only when the trace shows independent ownership seams.",
				Count:      hotspot.CompletedTasks,
				Target:     hotspot.Target,
				LastSeen:   hotspot.LastSeen,
				Lever:      reworkTargetLever(hotspot.Target),
				Confidence: "medium",
				Why:        "Repeated work on this owner consumes materially more fresh tokens and tool roundtrips than an ordinary edit target.",
				score: 520 + hotspot.CompletedTasks*5 +
					int(hotspot.ToolRoundtrips.P50) +
					int(hotspot.FreshTokens.P50/5_000),
			})
		case "review/rework":
			cycles := hotspot.PostReviewEditCalls + hotspot.FollowUpEdits
			if cycles < 2 {
				continue
			}
			findings = append(findings, sessionFinding{
				Category: "delivery-quality",
				Control:  "repository",
				Title:    "frequently edited target requires repeated rework",
				Evidence: fmt.Sprintf(
					"%s completed tasks edited this target %s times, with %s review-driven edits, %s downstream follow-up edits, %s failures, and %s reverts",
					formatCodexCount(int64(hotspot.CompletedTasks)),
					formatCodexCount(int64(hotspot.EditCalls)),
					formatCodexCount(int64(hotspot.PostReviewEditCalls)),
					formatCodexCount(int64(hotspot.FollowUpEdits)),
					formatCodexCount(int64(hotspot.DownstreamFailures)),
					formatCodexCount(int64(hotspot.Reverts)),
				),
				Action:     "Inspect the repeated corrections at this exact target, strengthen the owning invariant and focused pre-delivery check, then compare its rework rate in the next matched period.",
				Count:      cycles,
				Target:     hotspot.Target,
				LastSeen:   hotspot.LastSeen,
				Lever:      reworkTargetLever(hotspot.Target),
				Confidence: hotspotFindingConfidence(cycles),
				Why:        "Repeated corrections at the same owner indicate that implementation cost is surviving into review or downstream repair.",
				score:      650 + cycles*30,
			})
		case "downstream-risk":
			failures := hotspot.DownstreamFailures + hotspot.Reverts
			if failures < 2 {
				continue
			}
			findings = append(findings, sessionFinding{
				Category: "delivery-quality",
				Control:  "repository",
				Title:    "frequently edited target is associated with downstream failures",
				Evidence: fmt.Sprintf(
					"%s completed tasks edited this target %s times, with %s attributed downstream failures, %s follow-up edits, and %s reverts",
					formatCodexCount(int64(hotspot.CompletedTasks)),
					formatCodexCount(int64(hotspot.EditCalls)),
					formatCodexCount(int64(hotspot.DownstreamFailures)),
					formatCodexCount(int64(hotspot.FollowUpEdits)),
					formatCodexCount(int64(hotspot.Reverts)),
				),
				Action:     "Reproduce the attributed downstream failure at this exact target, add or strengthen the focused pre-delivery check, and compare the target's failure rate in the next matched period.",
				Count:      failures,
				Target:     hotspot.Target,
				LastSeen:   hotspot.LastSeen,
				Lever:      reworkTargetLever(hotspot.Target),
				Confidence: hotspotFindingConfidence(failures),
				Why:        "Failures attributed after delivery expose quality risk at an owner agents change repeatedly.",
				score:      680 + failures*35,
			})
		}
	}
	return findings
}

func ownerRediscoveryPolicy(target string, config repositoryConfig) (title, action string) {
	base := strings.ToLower(filepath.Base(target))
	switch {
	case base == "agents.md":
		return "injected repository instructions are repeatedly reopened",
			"AGENTS.md is normally injected into the session; clarify the relevant rule or tooling entry point, then avoid rereading the file."
	case repositoryManifestTarget(target):
		return "repository manifest is repeatedly reopened",
			"Add or use a bounded repository command for dependency and script discovery instead of repeatedly rereading the full manifest."
	case buildEntrypointTarget(target):
		return "build entry point is repeatedly reopened",
			"Expose canonical targets through a concise help target or injected workflow map, then invoke the target directly instead of reopening the build file."
	case instructionTarget(target):
		return "documentation entry point is repeatedly reopened",
			"Route agents to the exact heading through a concise documentation index or heading-aware inspection surface instead of reopening the whole document."
	default:
		return "an authoritative owner is repeatedly rediscovered", config.Actions.SourceContext
	}
}

func buildEntrypointTarget(target string) bool {
	switch strings.ToLower(filepath.Base(target)) {
	case "makefile", "justfile", "taskfile", "taskfile.yml", "taskfile.yaml":
		return true
	default:
		return false
	}
}

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
