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
}

// sessionProvider is the complete provider-specific boundary. Analysis and
// storage consume only sessionDiscovery and normalizedSession values.
type sessionProvider interface {
	sessionNormalizer
	Name() string
	Discover(explicit string, includeArchived bool) (sessionDiscovery, error)
	Metadata(sessionDiscovery) map[string]normalizedSessionMetadata
}

// sessionProviderAdapter keeps provider registration declarative. A new
// harness supplies discovery and normalization; metadata enrichment is
// optional.
type sessionProviderAdapter struct {
	name      string
	discover  func(string, bool) (sessionDiscovery, error)
	normalize func(string) (normalizedSession, error)
	metadata  func(sessionDiscovery) map[string]normalizedSessionMetadata
}

func (provider sessionProviderAdapter) Name() string {
	return provider.name
}

func (provider sessionProviderAdapter) Discover(explicit string, includeArchived bool) (sessionDiscovery, error) {
	if provider.discover == nil {
		return sessionDiscovery{}, fmt.Errorf("session provider %q has no discovery adapter", provider.name)
	}
	return provider.discover(explicit, includeArchived)
}

func (provider sessionProviderAdapter) NormalizeSession(path string) (normalizedSession, error) {
	if provider.normalize == nil {
		return normalizedSession{}, fmt.Errorf("session provider %q has no normalization adapter", provider.name)
	}
	return provider.normalize(path)
}

func (provider sessionProviderAdapter) Metadata(discovery sessionDiscovery) map[string]normalizedSessionMetadata {
	if provider.metadata == nil {
		return nil
	}
	return provider.metadata(discovery)
}

func (provider sessionProviderAdapter) validate(registryName string) error {
	if provider.name == "" {
		return fmt.Errorf("session provider %q has no stable name", registryName)
	}
	if provider.name != registryName {
		return fmt.Errorf("session provider registry key %q does not match adapter name %q", registryName, provider.name)
	}
	if provider.discover == nil || provider.normalize == nil {
		return fmt.Errorf("session provider %q must define discovery and normalization adapters", registryName)
	}
	return nil
}

var codexSessionProvider = sessionProviderAdapter{
	name:      "codex",
	discover:  discoverCodexSessionSource,
	normalize: parseCodexNormalizedSession,
	metadata:  codexSessionMetadata,
}

var sessionProviders = map[string]sessionProviderAdapter{
	"codex": codexSessionProvider,
}

func resolveSessionSource(name string) (sessionProvider, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = defaultSessionProvider
	}
	provider, ok := sessionProviders[name]
	if ok {
		if err := provider.validate(name); err != nil {
			return nil, err
		}
		return provider, nil
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
