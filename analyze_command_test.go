package main

import (
	"testing"
	"time"
)

func TestResolveAnalyzeOutputSelectionComposesDetailsWithOperations(t *testing.T) {
	selection, err := resolveAnalyzeOutputSelection(true, "", "bwb", 10, false)
	if err != nil {
		t.Fatalf("resolve operations details output: %v", err)
	}
	if selection.OperationLimit != 0 {
		t.Fatalf("operations details limit=%d, want all rows", selection.OperationLimit)
	}

	selection, err = resolveAnalyzeOutputSelection(true, "", "bwb", 4, true)
	if err != nil {
		t.Fatalf("resolve explicitly bounded operations details output: %v", err)
	}
	if selection.OperationLimit != 4 {
		t.Fatalf("explicit operations details limit=%d, want 4", selection.OperationLimit)
	}
}

func TestResolveAnalyzeOutputSelectionComposesDetailsWithFocus(t *testing.T) {
	selection, err := resolveAnalyzeOutputSelection(true, "structure", "", 10, false)
	if err != nil {
		t.Fatalf("resolve focused details output: %v", err)
	}
	if selection.View != "focused" {
		t.Fatalf("focused details view=%q, want focused", selection.View)
	}
}

func TestResolveAnalyzeOutputSelectionRejectsOperationWithFocus(t *testing.T) {
	if _, err := resolveAnalyzeOutputSelection(false, "structure", "bwb", 10, false); err == nil {
		t.Fatal("expected --operation with --focus to fail")
	}
}

func TestFormatRefreshCompletionIsBoundedAndActionable(t *testing.T) {
	got := formatRefreshCompletion(sessionRefreshStats{
		FilesScanned:    600,
		FilesIndexed:    24,
		FilesReused:     576,
		FilesUnreadable: 1,
	})
	want := "Refresh complete: 600 scanned, 24 indexed, 576 reused, 1 unreadable."
	if got != want {
		t.Fatalf("unexpected refresh completion: %q", got)
	}
}

func TestParseCodexLookback(t *testing.T) {
	tests := map[string]time.Duration{
		"24h":  24 * time.Hour,
		"7d":   7 * 24 * time.Hour,
		"2w":   14 * 24 * time.Hour,
		"0.5d": 12 * time.Hour,
	}
	for input, want := range tests {
		got, err := parseCodexLookback(input)
		if err != nil {
			t.Fatalf("parseCodexLookback(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("parseCodexLookback(%q)=%s want %s", input, got, want)
		}
	}
	for _, input := range []string{"", "0d", "-1h", "nope"} {
		if _, err := parseCodexLookback(input); err == nil {
			t.Fatalf("parseCodexLookback(%q) unexpectedly succeeded", input)
		}
	}
}
