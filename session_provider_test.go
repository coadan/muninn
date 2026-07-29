package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type fakeSessionProvider struct {
	discovery sessionDiscovery
	sessions  map[string]normalizedSession
}

func TestSessionProviderRegistryOwnsPublicProviderList(t *testing.T) {
	original := sessionProviders
	sessionProviders = map[string]func() sessionProvider{
		"zeta":  func() sessionProvider { return fakeSessionProvider{} },
		"alpha": func() sessionProvider { return fakeSessionProvider{} },
	}
	t.Cleanup(func() { sessionProviders = original })

	if got := availableSessionProviders(); !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("available providers=%v", got)
	}
	if got := sessionProviderFlagHelp(); got != "session provider (available: alpha, zeta)" {
		t.Fatalf("provider help=%q", got)
	}
}

func (provider fakeSessionProvider) Name() string {
	return "fake-harness"
}

func (provider fakeSessionProvider) Discover(string, bool) (sessionDiscovery, error) {
	return provider.discovery, nil
}

func (provider fakeSessionProvider) Metadata(sessionDiscovery) map[string]normalizedSessionMetadata {
	return nil
}

func (provider fakeSessionProvider) NormalizeSession(path string) (normalizedSession, error) {
	return provider.sessions[path], nil
}

func (provider fakeSessionProvider) SessionCWD(path string) (string, error) {
	return provider.sessions[path].CWD, nil
}

func TestSessionProviderContractSupportsAnotherFileFormat(t *testing.T) {
	repositoryRoot := t.TempDir()
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "session.fake-harness")
	if err := os.WriteFile(sessionPath, []byte("provider-owned format"), 0o600); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC)
	provider := fakeSessionProvider{
		discovery: sessionDiscovery{
			Dirs:  []string{sessionDir},
			Files: []string{sessionPath},
		},
		sessions: map[string]normalizedSession{
			sessionPath: {
				CWD:       repositoryRoot,
				Model:     "fake-model",
				AgentKind: "root",
				Events: []normalizedSessionEvent{
					{Sequence: 1, OccurredAt: startedAt, Kind: sessionEventToolCall, ToolName: "shell", Family: "shell", ToolRound: 1},
					{Sequence: 2, OccurredAt: startedAt.Add(time.Second), Kind: sessionEventToolOutput, ToolName: "shell", Family: "shell", ToolRound: 1, OutputBytes: 12},
					{Sequence: 3, OccurredAt: startedAt.Add(2 * time.Second), Kind: sessionEventComplete},
				},
			},
		},
	}

	report, err := analyzeProviderSessions(
		provider,
		provider.discovery,
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
	if report.Provider != provider.Name() || report.Summary.FilesScanned != 1 ||
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
		provider.Name(),
		provider.discovery,
		repositoryRoot,
		provider,
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
