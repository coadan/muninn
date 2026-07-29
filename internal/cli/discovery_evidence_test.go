package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildDiscoveryFocusEvidenceRanksFiltersAndBoundsRows(t *testing.T) {
	root := t.TempDir()
	for _, target := range []string{"most-loops.go", "most-reads.go", "bounded-out.go"} {
		if err := os.WriteFile(filepath.Join(root, target), []byte("current"), 0o644); err != nil {
			t.Fatalf("write %s: %v", target, err)
		}
	}
	summary := newSessionInsightsReport("codex", nil, root, zeroTime(), zeroTime()).Summary
	summary.ReadTargets["most-loops.go"] = codexTargetMetrics{
		Reads: 4, SearchReadLoops: 3, Sessions: 2, RediscoverySessions: 1,
	}
	summary.ReadTargets["most-reads.go"] = codexTargetMetrics{
		Reads: 20, SearchReadLoops: 1, Sessions: 1, RediscoverySessions: 1,
	}
	summary.ReadTargets["bounded-out.go"] = codexTargetMetrics{
		Reads: 10, SearchReadLoops: 0, Sessions: 2, RediscoverySessions: 2,
	}
	summary.ReadTargets["deleted.go"] = codexTargetMetrics{
		Reads: 100, SearchReadLoops: 50, Sessions: 10, RediscoverySessions: 10,
	}
	summary.MixedShellShapes["search -> file reads"] = codexToolMetrics{
		Calls: 2, Sessions: 2, OutputBytes: 80_000,
	}
	summary.MixedShellShapes["file reads -> search"] = codexToolMetrics{
		Calls: 5, Sessions: 1, OutputBytes: 40_000,
	}
	summary.MixedShellShapes["file reads"] = codexToolMetrics{
		Calls: 20, Sessions: 3, OutputBytes: 200_000,
	}

	evidence := buildDiscoveryFocusEvidence(summary, root, 2)
	if len(evidence.ReadTargets) != 2 ||
		evidence.ReadTargets[0].Target != "most-loops.go" ||
		evidence.ReadTargets[1].Target != "most-reads.go" {
		t.Fatalf("ranked read targets mismatch: %#v", evidence.ReadTargets)
	}
	if len(evidence.SearchReadShapes) != 2 ||
		evidence.SearchReadShapes[0].Shape != "search -> file reads" ||
		evidence.SearchReadShapes[0].EstimatedOutputTokens != 20_000 ||
		evidence.SearchReadShapes[1].Shape != "file reads -> search" {
		t.Fatalf("ranked search/read shapes mismatch: %#v", evidence.SearchReadShapes)
	}
}

func TestBuildDiscoveryFocusEvidenceExposesManagedRepository(t *testing.T) {
	root := t.TempDir()
	rawTarget := ".workbench/repos/breyta/src/runtime.clj"
	path := filepath.Join(root, filepath.FromSlash(rawTarget))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir managed source: %v", err)
	}
	if err := os.WriteFile(path, []byte("(ns runtime)\n"), 0o644); err != nil {
		t.Fatalf("write managed source: %v", err)
	}
	summary := newSessionInsightsReport("codex", nil, root, zeroTime(), zeroTime()).Summary
	summary.ReadTargets[rawTarget] = codexTargetMetrics{
		Reads: 8, SearchReadLoops: 2, Sessions: 3, RediscoverySessions: 2,
	}

	evidence := buildDiscoveryFocusEvidence(summary, root, 1)
	if len(evidence.ReadTargets) != 1 ||
		evidence.ReadTargets[0].Repository != "breyta" ||
		evidence.ReadTargets[0].Target != "src/runtime.clj" {
		t.Fatalf("managed discovery target leaked cache identity: %#v", evidence.ReadTargets)
	}
}
