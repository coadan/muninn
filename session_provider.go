package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const defaultSessionProvider = "codex"

type sessionDiscovery struct {
	Dirs            []string
	Files           []string
	FilesUnreadable int
}

type sessionNormalizer interface {
	NormalizeSession(path string) (normalizedSession, error)
	SessionCWD(path string) (string, error)
}

// sessionProvider is the complete provider-specific boundary. Analysis and
// storage consume only sessionDiscovery and normalizedSession values.
type sessionProvider interface {
	sessionNormalizer
	Name() string
	Discover(explicit string, includeArchived bool) (sessionDiscovery, error)
	Metadata(sessionDiscovery) map[string]normalizedSessionMetadata
}

var sessionProviders = map[string]func() sessionProvider{
	"codex": func() sessionProvider { return codexSessionSource{} },
}

func resolveSessionSource(name string) (sessionProvider, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = defaultSessionProvider
	}
	factory, ok := sessionProviders[name]
	if ok {
		return factory(), nil
	}
	return nil, fmt.Errorf("unsupported session provider %q (available: %s)", name, strings.Join(availableSessionProviders(), ", "))
}

func sessionProviderFlagHelp() string {
	return fmt.Sprintf("session provider (available: %s)", strings.Join(availableSessionProviders(), ", "))
}

func availableSessionProviders() []string {
	names := make([]string, 0, len(sessionProviders))
	for name := range sessionProviders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func analyzeProviderSessions(provider sessionProvider, discovery sessionDiscovery, workspaceRoot string, since, generatedAt time.Time, taskFilter string, ownership ownershipCatalog, metadata map[string]normalizedSessionMetadata) (codexSessionInsightsReport, error) {
	report := newSessionInsightsReport(provider.Name(), discovery.Dirs, workspaceRoot, since, generatedAt)
	report.Summary.FilesScanned = len(discovery.Files)
	report.Summary.FilesUnreadable = discovery.FilesUnreadable
	taskMap := map[string]*codexTaskInsights{}
	for _, path := range discovery.Files {
		session, err := provider.NormalizeSession(path)
		if err != nil {
			report.Summary.FilesUnreadable++
			continue
		}
		session.Provider = provider.Name()
		session.SourcePath = path
		enrichNormalizedSession(&session, metadata)
		record, err := sessionRecordFromNormalized(session, workspaceRoot, since, generatedAt, ownership)
		if err != nil {
			report.Summary.FilesUnreadable++
			continue
		}
		if record.CWD == "" || record.StartedAt.IsZero() {
			continue
		}
		if taskFilter != "" && record.Task != taskFilter {
			continue
		}
		addCodexSessionToReport(&report, taskMap, record)
	}
	finishSessionInsightsReport(&report, taskMap)
	return report, nil
}
