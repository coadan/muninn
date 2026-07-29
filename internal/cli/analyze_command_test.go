package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAnalyzeUsesJSONByDefault(t *testing.T) {
	repository := t.TempDir()
	sessions := t.TempDir()
	output, err := captureStdout(t, func() error {
		return cmdAnalyze(repository, []string{"--sessions-dir", sessions, "--no-cache"})
	})
	if err != nil {
		t.Fatalf("default analysis: %v", err)
	}
	if !json.Valid([]byte(output)) {
		t.Fatalf("default analysis did not emit JSON: %q", output)
	}
}

func TestAnalyzeRejectsRemovedJSONFlag(t *testing.T) {
	err := cmdAnalyze(t.TempDir(), []string{"--json"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -json") {
		t.Fatalf("removed --json flag error=%v", err)
	}
}

func TestAnalyzeRejectsRemovedHumanAndLimitFlags(t *testing.T) {
	for _, flag := range []string{"--human", "--limit"} {
		err := cmdAnalyze(t.TempDir(), []string{flag})
		if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("removed %s flag error=%v", flag, err)
		}
	}
}

func TestResolveAnalyzeOutputSelectionComposesDetailsWithOperations(t *testing.T) {
	selection, err := resolveAnalyzeOutputSelection(true, "", "bwb")
	if err != nil {
		t.Fatalf("resolve operations details output: %v", err)
	}
	if selection.OperationLimit != 0 {
		t.Fatalf("operations details limit=%d, want all rows", selection.OperationLimit)
	}

	selection, err = resolveAnalyzeOutputSelection(false, "", "bwb")
	if err != nil {
		t.Fatalf("resolve bounded operations output: %v", err)
	}
	if selection.OperationLimit != compactInterventionLimit {
		t.Fatalf("operations limit=%d, want %d", selection.OperationLimit, compactInterventionLimit)
	}
}

func TestResolveAnalyzeOutputSelectionComposesDetailsWithFocus(t *testing.T) {
	if _, err := resolveAnalyzeOutputSelection(true, "structure", ""); err != nil {
		t.Fatalf("resolve focused details output: %v", err)
	}
}

func TestResolveAnalyzeOutputSelectionRejectsOperationWithFocus(t *testing.T) {
	if _, err := resolveAnalyzeOutputSelection(false, "structure", "bwb"); err == nil {
		t.Fatal("expected --operation with --focus to fail")
	}
}

func TestFormatRefreshCompletionIsBoundedAndActionable(t *testing.T) {
	got := formatRefreshCompletion(sessionRefreshStats{
		FilesScanned:    600,
		FilesIndexed:    24,
		FilesReused:     576,
		FilesPruned:     3,
		FilesUnreadable: 1,
	})
	want := "Refresh complete: 600 scanned, 24 indexed, 576 reused, 3 pruned, 1 unreadable."
	if got != want {
		t.Fatalf("unexpected refresh completion: %q", got)
	}
}

func TestAnalyzeHeartbeatWaitsAndStopsCleanly(t *testing.T) {
	var output bytes.Buffer
	stop := startAnalyzeHeartbeat(&output, 5*time.Millisecond, 5*time.Millisecond)
	time.Sleep(12 * time.Millisecond)
	stop()

	got := output.String()
	if !strings.Contains(got, "still analyzing sessions") {
		t.Fatalf("heartbeat output=%q, want progress", got)
	}
	before := output.Len()
	time.Sleep(10 * time.Millisecond)
	if output.Len() != before {
		t.Fatalf("heartbeat continued after stop: before=%d after=%d", before, output.Len())
	}
	stop()
}

func TestAnalyzeHeartbeatStaysSilentForFastRuns(t *testing.T) {
	var output bytes.Buffer
	stop := startAnalyzeHeartbeat(&output, time.Hour, time.Hour)
	stop()
	if output.Len() != 0 {
		t.Fatalf("fast analysis emitted progress: %q", output.String())
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
