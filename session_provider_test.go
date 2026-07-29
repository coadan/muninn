package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSessionProviderRegistryOwnsPublicProviderList(t *testing.T) {
	original := sessionProviders
	sessionProviders = map[string]sessionProviderAdapter{
		"zeta": {
			name: "zeta", discover: emptySessionDiscovery,
			sessionCWD: emptySessionCWD, normalize: emptyNormalizedSession,
		},
		"alpha": {
			name: "alpha", discover: emptySessionDiscovery,
			sessionCWD: emptySessionCWD, normalize: emptyNormalizedSession,
		},
	}
	t.Cleanup(func() { sessionProviders = original })

	if got := availableSessionProviders(); !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("available providers=%v", got)
	}
	if got := sessionProviderFlagHelp(); got != "session provider (available: alpha, zeta)" {
		t.Fatalf("provider help=%q", got)
	}
}

func emptySessionDiscovery(string, bool) (sessionDiscovery, error) {
	return sessionDiscovery{}, nil
}

func emptySessionCWD(string) (string, error) {
	return "", nil
}

func emptyNormalizedSession(string) (normalizedSession, error) {
	return normalizedSession{}, nil
}

func TestSessionProviderRegistryRejectsIncompleteOrMismatchedAdapters(t *testing.T) {
	original := sessionProviders
	t.Cleanup(func() { sessionProviders = original })

	sessionProviders = map[string]sessionProviderAdapter{
		"other": {name: "different"},
	}
	if _, err := resolveSessionSource("other"); err == nil {
		t.Fatal("mismatched provider registration unexpectedly accepted")
	}
	sessionProviders = map[string]sessionProviderAdapter{
		"other": {name: "other"},
	}
	if _, err := resolveSessionSource("other"); err == nil {
		t.Fatal("incomplete provider registration unexpectedly accepted")
	}
}

func TestSessionProviderContractSupportsAnotherFileFormat(t *testing.T) {
	repositoryRoot := t.TempDir()
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session.fake-harness")
	if err := os.WriteFile(sessionPath, []byte("provider-owned format"), 0o600); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	discovery := sessionDiscovery{
		Dirs:  []string{sessionDir},
		Files: []string{sessionPath},
	}
	session := normalizedSession{
		CWD:       repositoryRoot,
		Model:     "fake-model",
		AgentKind: "root",
		Events: []normalizedSessionEvent{
			{Sequence: 1, OccurredAt: startedAt, Kind: sessionEventToolCall, ToolName: "shell", Family: "shell", ToolRound: 1},
			{Sequence: 2, OccurredAt: startedAt.Add(time.Second), Kind: sessionEventToolOutput, ToolName: "shell", Family: "shell", ToolRound: 1, OutputBytes: 12},
			{Sequence: 3, OccurredAt: startedAt.Add(2 * time.Second), Kind: sessionEventComplete},
		},
	}
	provider := sessionProviderAdapter{
		name: "fake-harness",
		discover: func(string, bool) (sessionDiscovery, error) {
			return discovery, nil
		},
		sessionCWD: func(path string) (string, error) {
			if path != sessionPath {
				t.Fatalf("unexpected session path %q", path)
			}
			return repositoryRoot, nil
		},
		normalize: func(path string) (normalizedSession, error) {
			if path != sessionPath {
				t.Fatalf("unexpected session path %q", path)
			}
			return session, nil
		},
	}
	original := sessionProviders
	sessionProviders = map[string]sessionProviderAdapter{
		"fake-harness": provider,
	}
	t.Cleanup(func() { sessionProviders = original })
	source, err := resolveSessionSource("fake-harness")
	if err != nil {
		t.Fatal(err)
	}

	report, err := analyzeProviderSessions(
		source,
		discovery,
		repositoryRoot,
		startedAt.Add(-time.Hour),
		startedAt.Add(time.Hour),
		"",
		ownershipCatalog{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Provider != source.Name() || report.Summary.FilesScanned != 1 ||
		report.Summary.FilesUnreadable != 0 || report.Summary.Sessions != 1 {
		t.Fatalf("provider-neutral direct analysis mismatch: %#v", report.Summary)
	}

	store, err := openSessionStore(filepath.Join(t.TempDir(), "sessions.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stats, err := store.refresh(
		t.Context(),
		source.Name(),
		discovery,
		repositoryRoot,
		source,
		ownershipCatalog{},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesIndexed != 1 || stats.FilesScanned != 1 {
		t.Fatalf("provider-neutral indexed ingestion mismatch: %#v", stats)
	}
}
