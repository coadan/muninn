package cli

import (
	"sort"
	"strings"
)

type discoveryFocusEvidence struct {
	ReadTargets      []discoveryReadTargetEvidence `json:"readTargets,omitempty"`
	SearchReadShapes []discoveryShapeEvidence      `json:"searchReadShapes,omitempty"`
}

type discoveryReadTargetEvidence struct {
	Target              string `json:"target"`
	Repository          string `json:"repository,omitempty"`
	Reads               int    `json:"reads"`
	SearchReadLoops     int    `json:"searchReadLoops"`
	Sessions            int    `json:"sessions"`
	RediscoverySessions int    `json:"rediscoverySessions"`
}

type discoveryShapeEvidence struct {
	Shape                 string `json:"shape"`
	Calls                 int    `json:"calls"`
	Sessions              int    `json:"sessions"`
	OutputBytes           int64  `json:"outputBytes"`
	EstimatedOutputTokens int64  `json:"estimatedOutputTokens"`
}

func buildDiscoveryFocusEvidence(summary codexSessionInsightsSummary, repositoryRoot string, limit int) discoveryFocusEvidence {
	evidence := discoveryFocusEvidence{
		ReadTargets: make([]discoveryReadTargetEvidence, 0, len(summary.ReadTargets)),
	}
	for target, metrics := range summary.ReadTargets {
		if _, _, exists := repositoryTargetSize(repositoryRoot, target); !exists {
			continue
		}
		repository, evidenceTarget, _ := splitManagedRepositoryTarget(target)
		evidence.ReadTargets = append(evidence.ReadTargets, discoveryReadTargetEvidence{
			Target:              evidenceTarget,
			Repository:          repository,
			Reads:               metrics.Reads,
			SearchReadLoops:     metrics.SearchReadLoops,
			Sessions:            metrics.Sessions,
			RediscoverySessions: metrics.RediscoverySessions,
		})
	}
	sort.Slice(evidence.ReadTargets, func(i, j int) bool {
		if evidence.ReadTargets[i].SearchReadLoops != evidence.ReadTargets[j].SearchReadLoops {
			return evidence.ReadTargets[i].SearchReadLoops > evidence.ReadTargets[j].SearchReadLoops
		}
		if evidence.ReadTargets[i].Reads != evidence.ReadTargets[j].Reads {
			return evidence.ReadTargets[i].Reads > evidence.ReadTargets[j].Reads
		}
		if evidence.ReadTargets[i].RediscoverySessions != evidence.ReadTargets[j].RediscoverySessions {
			return evidence.ReadTargets[i].RediscoverySessions > evidence.ReadTargets[j].RediscoverySessions
		}
		left := evidence.ReadTargets[i].Repository + "/" + evidence.ReadTargets[i].Target
		right := evidence.ReadTargets[j].Repository + "/" + evidence.ReadTargets[j].Target
		return left < right
	})

	for shape, metrics := range summary.MixedShellShapes {
		if !strings.Contains(shape, "search") || !strings.Contains(shape, "file reads") {
			continue
		}
		evidence.SearchReadShapes = append(evidence.SearchReadShapes, discoveryShapeEvidence{
			Shape:                 shape,
			Calls:                 metrics.Calls,
			Sessions:              metrics.Sessions,
			OutputBytes:           metrics.OutputBytes,
			EstimatedOutputTokens: estimatedTokens(metrics.OutputBytes),
		})
	}
	sort.Slice(evidence.SearchReadShapes, func(i, j int) bool {
		if evidence.SearchReadShapes[i].OutputBytes != evidence.SearchReadShapes[j].OutputBytes {
			return evidence.SearchReadShapes[i].OutputBytes > evidence.SearchReadShapes[j].OutputBytes
		}
		if evidence.SearchReadShapes[i].Calls != evidence.SearchReadShapes[j].Calls {
			return evidence.SearchReadShapes[i].Calls > evidence.SearchReadShapes[j].Calls
		}
		return evidence.SearchReadShapes[i].Shape < evidence.SearchReadShapes[j].Shape
	})

	if limit > 0 && len(evidence.ReadTargets) > limit {
		evidence.ReadTargets = evidence.ReadTargets[:limit]
	}
	if limit > 0 && len(evidence.SearchReadShapes) > limit {
		evidence.SearchReadShapes = evidence.SearchReadShapes[:limit]
	}
	return evidence
}
