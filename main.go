package main

// Muninn analyzes local Codex rollout metadata without exposing
// prompts, raw tool inputs, raw tool output, paths, or session identifiers.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const codexSessionInsightsSchemaVersion = 28

var nonZeroExitCodePattern = regexp.MustCompile(`(?i)"exit_code"\s*:\s*[1-9][0-9]*`)
var nonZeroDisplayExitCodePattern = regexp.MustCompile(`(?im)^exit code:\s*[1-9][0-9]*`)
var nonZeroProcessExitCodePattern = regexp.MustCompile(`(?i)process exited with code\s+[1-9][0-9]*`)
var searchMissExitCodePattern = regexp.MustCompile(`(?im)(?:"exit_code"\s*:\s*1(?:[^0-9]|$)|^exit code:\s*1(?:[^0-9]|$)|process exited with code\s+1(?:[^0-9]|$))`)
var codexNestedCommandStartPattern = regexp.MustCompile(`(?:^|[,{]\s*)(?:"cmd"|'cmd'|cmd)\s*:\s*`)
var codexNestedContinuationPattern = regexp.MustCompile(`(?s)tools\.(?:write_stdin|wait)\s*\(\s*\{[^}]*?"?(session_id|cell_id)"?\s*:\s*(?:"([^"]+)"|'([^']+)'|([0-9]+))`)
var codexExplicitSessionMarkerPattern = regexp.MustCompile(`^SESSION_ID=([0-9]+)$`)
var codexContinuationStatusPattern = regexp.MustCompile(`(?im)^(?:script|process) running with (session|cell) id\s+([^\s]+)\s*$`)
var suppressedSignalPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,199}$`)

type codexTokenUsage struct {
	InputTokens         int64 `json:"inputTokens"`
	CachedInputTokens   int64 `json:"cachedInputTokens"`
	UncachedInputTokens int64 `json:"uncachedInputTokens"`
	OutputTokens        int64 `json:"outputTokens"`
	ReasoningTokens     int64 `json:"reasoningTokens"`
	TotalTokens         int64 `json:"totalTokens"`
}

type codexTaskInsights struct {
	Task                         string                                       `json:"task"`
	Sessions                     int                                          `json:"sessions"`
	CompletedSessions            int                                          `json:"completedSessions"`
	IncompleteSessions           int                                          `json:"incompleteSessions"`
	DurationSeconds              int64                                        `json:"durationSeconds"`
	Compactions                  int                                          `json:"compactions"`
	SessionsWithCompactions      int                                          `json:"sessionsWithCompactions"`
	Tokens                       codexTokenUsage                              `json:"tokens"`
	FreshTokens                  int64                                        `json:"freshTokens"`
	ToolCalls                    int                                          `json:"toolCalls"`
	FailedToolCalls              int                                          `json:"failedToolCalls"`
	TruncatedToolCalls           int                                          `json:"truncatedToolCalls"`
	ToolOutputBytes              int64                                        `json:"toolOutputBytes"`
	ToolOutputTokens             int64                                        `json:"toolOutputTokens"`
	ShellCommandsByFamily        map[string]codexToolMetrics                  `json:"shellCommandsByFamily"`
	MixedShellShapes             map[string]codexToolMetrics                  `json:"mixedShellShapes"`
	CrossCallTransitions         map[string]codexTransitionMetrics            `json:"crossCallTransitions"`
	OwnedTooling                 map[string]codexToolMetrics                  `json:"ownedTooling"`
	OwnedOperations              map[string]codexOwnedOperationMetrics        `json:"ownedOperations"`
	OwnedOperationFailureReasons map[string]map[string]codexOccurrenceMetrics `json:"ownedOperationFailureReasons"`
	ReadTargets                  map[string]codexTargetMetrics                `json:"readTargets"`
	InlineOrchestrationCalls     int                                          `json:"inlineOrchestrationCalls"`
	InlineOrchestrationBytes     int64                                        `json:"inlineOrchestrationBytes"`
	InlineOrchestrationMaxBytes  int64                                        `json:"inlineOrchestrationMaxBytes"`
	InlineOrchestrationSessions  int                                          `json:"inlineOrchestrationSessions"`
	InlineOrchestrationByTool    map[string]codexInlineMetrics                `json:"inlineOrchestrationByTool"`
	FailureReasons               map[string]int                               `json:"failureReasons"`
	FailureContexts              map[string]map[string]codexOccurrenceMetrics `json:"failureContexts"`
	ProgressStalls               map[string]codexWaitMetrics                  `json:"progressStalls"`
	ExpectedWaits                map[string]codexWaitMetrics                  `json:"expectedWaits"`
	OversizedOutputs             map[string]codexOversizedOutputMetrics       `json:"oversizedOutputs"`
	DeliveryRework               deliveryReworkMetrics                        `json:"deliveryRework"`
	DownstreamQuality            downstreamQualityMetrics                     `json:"downstreamQuality"`
	Activity                     map[string]time.Time                         `json:"-"`
}

type codexToolMetrics struct {
	Calls                 int   `json:"calls"`
	FailedCalls           int   `json:"failedCalls"`
	TruncatedCalls        int   `json:"truncatedCalls"`
	OutputBytes           int64 `json:"outputBytes"`
	EstimatedOutputTokens int64 `json:"estimatedOutputTokens"`
}

type codexOwnedOperationMetrics struct {
	Calls                          int   `json:"calls"`
	AmbiguousCalls                 int   `json:"ambiguousCalls"`
	Sessions                       int   `json:"sessions"`
	FailedCalls                    int   `json:"failedCalls"`
	AmbiguousFailedCalls           int   `json:"ambiguousFailedCalls"`
	TruncatedCalls                 int   `json:"truncatedCalls"`
	AmbiguousTruncatedCalls        int   `json:"ambiguousTruncatedCalls"`
	OutputBytes                    int64 `json:"outputBytes"`
	AmbiguousOutputBytes           int64 `json:"ambiguousOutputBytes"`
	EstimatedOutputTokens          int64 `json:"estimatedOutputTokens"`
	EstimatedAmbiguousOutputTokens int64 `json:"estimatedAmbiguousOutputTokens"`
}

type codexTransitionMetrics struct {
	Count    int `json:"count"`
	Sessions int `json:"sessions"`
}

type codexOccurrenceMetrics struct {
	Count    int `json:"count"`
	Sessions int `json:"sessions"`
}

type codexWaitMetrics struct {
	Calls    int   `json:"calls"`
	Seconds  int64 `json:"seconds"`
	Sessions int   `json:"sessions"`
}

type codexOversizedOutputMetrics struct {
	Calls          int   `json:"calls"`
	OutputBytes    int64 `json:"outputBytes"`
	MaxOutputBytes int64 `json:"maxOutputBytes"`
	Sessions       int   `json:"sessions"`
}

type codexInlineMetrics struct {
	Calls    int   `json:"calls"`
	Sessions int   `json:"sessions"`
	Bytes    int64 `json:"bytes"`
	MaxBytes int64 `json:"maxBytes"`
}

type codexTargetMetrics struct {
	Reads           int `json:"reads"`
	SearchReadLoops int `json:"searchReadLoops"`
	Sessions        int `json:"sessions"`
}

type codexSessionInsightsSummary struct {
	FilesScanned                 int                                          `json:"filesScanned"`
	FilesUnreadable              int                                          `json:"filesUnreadable"`
	Sessions                     int                                          `json:"sessions"`
	CompletedSessions            int                                          `json:"completedSessions"`
	IncompleteSessions           int                                          `json:"incompleteSessions"`
	DurationSeconds              int64                                        `json:"durationSeconds"`
	Compactions                  int                                          `json:"compactions"`
	SessionsWithCompactions      int                                          `json:"sessionsWithCompactions"`
	Tokens                       codexTokenUsage                              `json:"tokens"`
	FreshTokens                  int64                                        `json:"freshTokens"`
	ToolCalls                    int                                          `json:"toolCalls"`
	FailedToolCalls              int                                          `json:"failedToolCalls"`
	TruncatedToolCalls           int                                          `json:"truncatedToolCalls"`
	ToolOutputBytes              int64                                        `json:"toolOutputBytes"`
	ToolOutputTokens             int64                                        `json:"toolOutputTokens"`
	ToolCallsByName              map[string]int                               `json:"toolCallsByName"`
	ToolMetricsByName            map[string]codexToolMetrics                  `json:"toolMetricsByName"`
	ShellCommandsByFamily        map[string]codexToolMetrics                  `json:"shellCommandsByFamily"`
	MixedShellShapes             map[string]codexToolMetrics                  `json:"mixedShellShapes"`
	CrossCallTransitions         map[string]codexTransitionMetrics            `json:"crossCallTransitions"`
	OwnedTooling                 map[string]codexToolMetrics                  `json:"ownedTooling"`
	OwnedOperations              map[string]codexOwnedOperationMetrics        `json:"ownedOperations"`
	OwnedOperationFailureReasons map[string]map[string]codexOccurrenceMetrics `json:"ownedOperationFailureReasons"`
	ReadTargets                  map[string]codexTargetMetrics                `json:"readTargets"`
	InlineOrchestrationCalls     int                                          `json:"inlineOrchestrationCalls"`
	InlineOrchestrationBytes     int64                                        `json:"inlineOrchestrationBytes"`
	InlineOrchestrationMaxBytes  int64                                        `json:"inlineOrchestrationMaxBytes"`
	InlineOrchestrationSessions  int                                          `json:"inlineOrchestrationSessions"`
	InlineOrchestrationByTool    map[string]codexInlineMetrics                `json:"inlineOrchestrationByTool"`
	FailureReasons               map[string]int                               `json:"failureReasons"`
	FailureContexts              map[string]map[string]codexOccurrenceMetrics `json:"failureContexts"`
	ProgressStalls               map[string]codexWaitMetrics                  `json:"progressStalls"`
	ExpectedWaits                map[string]codexWaitMetrics                  `json:"expectedWaits"`
	OversizedOutputs             map[string]codexOversizedOutputMetrics       `json:"oversizedOutputs"`
	DeliveryRework               deliveryReworkMetrics                        `json:"deliveryRework"`
	DownstreamQuality            downstreamQualityMetrics                     `json:"downstreamQuality"`
	Activity                     map[string]time.Time                         `json:"-"`
}

type codexSessionInsightsReport struct {
	SchemaVersion  int                            `json:"schemaVersion"`
	Provider       string                         `json:"provider"`
	GeneratedAt    string                         `json:"generatedAt"`
	Since          string                         `json:"since"`
	WorkspaceRoot  string                         `json:"-"`
	SessionDirs    []string                       `json:"-"`
	Instructions   repositoryInstructionFootprint `json:"instructions"`
	Summary        codexSessionInsightsSummary    `json:"summary"`
	Tasks          []codexTaskInsights            `json:"tasks"`
	Feedback       []agentFeedbackAggregate       `json:"feedback,omitempty"`
	Findings       []sessionFinding               `json:"findings"`
	Outcomes       completionEpisodeAnalysis      `json:"outcomes"`
	Profiles       modelEffortAnalysis            `json:"profiles"`
	Delegation     delegationAnalysis             `json:"delegation"`
	taskEpisodes   []codexTaskEpisode
	sessionRecords []codexSessionRecord
}

type codexSessionRecord struct {
	SourcePath                   string
	CWD                          string
	Task                         string
	Model                        string
	ReasoningEffort              string
	AgentKind                    string
	LineageKey                   string
	ParentLineageKey             string
	SpawnStatus                  string
	StartedAt                    time.Time
	EndedAt                      time.Time
	Completed                    bool
	Compactions                  int
	Tokens                       codexTokenUsage
	ToolCalls                    int
	FailedToolCalls              int
	TruncatedToolCalls           int
	ToolOutputBytes              int64
	ToolCallsByName              map[string]int
	ToolMetricsByName            map[string]codexToolMetrics
	ShellCommandsByFamily        map[string]codexToolMetrics
	MixedShellShapes             map[string]codexToolMetrics
	CrossCallTransitions         map[string]int
	OwnedTooling                 map[string]codexToolMetrics
	OwnedOperations              map[string]codexToolMetrics
	OwnedOperationAmbiguous      map[string]codexToolMetrics
	OwnedOperationFailureReasons map[string]map[string]int
	ReadTargets                  map[string]codexTargetMetrics
	EditTargets                  map[string]int
	InlineOrchestrationCalls     int
	InlineOrchestrationBytes     int64
	InlineOrchestrationMaxBytes  int64
	InlineOrchestrationByTool    map[string]codexInlineMetrics
	FailureReasons               map[string]int
	FailureContexts              map[string]map[string]int
	ProgressStalls               map[string]codexWaitMetrics
	ExpectedWaits                map[string]codexWaitMetrics
	OversizedOutputs             map[string]codexOversizedOutputMetrics
	DeliveryRework               deliveryReworkMetrics
	DownstreamQuality            downstreamQualityMetrics
	Activity                     map[string]time.Time
	TaskEpisodes                 []codexTaskEpisode
}

type codexToolCallDescriptor struct {
	Name   string
	Family string
	Shape  string
	Active bool
	First  string
	Last   string
}

type codexRolloutEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexRolloutPayload struct {
	Type      string `json:"type"`
	CWD       string `json:"cwd"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Input     string `json:"input"`
	Info      struct {
		TotalTokenUsage struct {
			InputTokens       int64 `json:"input_tokens"`
			CachedInputTokens int64 `json:"cached_input_tokens"`
			OutputTokens      int64 `json:"output_tokens"`
			ReasoningTokens   int64 `json:"reasoning_output_tokens"`
			TotalTokens       int64 `json:"total_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
	Output json.RawMessage `json:"output"`
}

type repositoryConfig struct {
	SchemaVersion   int      `json:"schemaVersion"`
	SuppressSignals []string `json:"suppressSignals,omitempty"`
	Actions         struct {
		SourceContext       string `json:"sourceContext"`
		RecurringFailure    string `json:"recurringFailure"`
		AgentInterface      string `json:"agentInterface"`
		CodeStructure       string `json:"codeStructure"`
		SessionLoop         string `json:"sessionLoop"`
		InlineOrchestration string `json:"inlineOrchestration"`
	} `json:"actions"`
	OwnedTools []ownedToolConfig `json:"ownedTools"`
}

func defaultRepositoryConfig() repositoryConfig {
	config := repositoryConfig{SchemaVersion: 1}
	config.Actions.SourceContext = "Use or add one bounded repository source-context command that combines ranked search pointers with small excerpts."
	config.Actions.RecurringFailure = "Reproduce the shared failure once, then fix the owned tool/default or add concise repository guidance instead of repeating per-session workarounds."
	config.Actions.AgentInterface = "Consider one compact agent-facing command that owns bootstrap, state, bounded output, and recovery for this repeated workflow."
	config.Actions.CodeStructure = "Inspect whether this owner mixes responsibilities; split or add a stable routed entry point when the repeated reads reflect real ownership boundaries."
	config.Actions.SessionLoop = "Checkpoint progress, start a focused continuation, and remove repeated rediscovery or validation loops from the repository workflow."
	config.Actions.InlineOrchestration = "Extract the repeated orchestration into a tested repository helper or agent-facing CLI command."
	return config
}

func loadRepositoryConfig(repoRoot, explicit string) (repositoryConfig, error) {
	config := defaultRepositoryConfig()
	path := strings.TrimSpace(explicit)
	required := path != ""
	if path == "" {
		path = filepath.Join(repoRoot, ".muninn.json")
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return config, nil
		}
		return repositoryConfig{}, fmt.Errorf("read Muninn config %s: %w", path, err)
	}
	var decoded repositoryConfig
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return repositoryConfig{}, fmt.Errorf("parse Muninn config %s: %w", path, err)
	}
	if decoded.SchemaVersion != 1 {
		return repositoryConfig{}, fmt.Errorf("unsupported Muninn config schemaVersion %d in %s", decoded.SchemaVersion, path)
	}
	if strings.TrimSpace(decoded.Actions.SourceContext) != "" {
		config.Actions.SourceContext = strings.TrimSpace(decoded.Actions.SourceContext)
	}
	if strings.TrimSpace(decoded.Actions.RecurringFailure) != "" {
		config.Actions.RecurringFailure = strings.TrimSpace(decoded.Actions.RecurringFailure)
	}
	if strings.TrimSpace(decoded.Actions.AgentInterface) != "" {
		config.Actions.AgentInterface = strings.TrimSpace(decoded.Actions.AgentInterface)
	}
	if strings.TrimSpace(decoded.Actions.CodeStructure) != "" {
		config.Actions.CodeStructure = strings.TrimSpace(decoded.Actions.CodeStructure)
	}
	if strings.TrimSpace(decoded.Actions.SessionLoop) != "" {
		config.Actions.SessionLoop = strings.TrimSpace(decoded.Actions.SessionLoop)
	}
	if strings.TrimSpace(decoded.Actions.InlineOrchestration) != "" {
		config.Actions.InlineOrchestration = strings.TrimSpace(decoded.Actions.InlineOrchestration)
	}
	if err := validateOwnedToolConfig(decoded.OwnedTools); err != nil {
		return repositoryConfig{}, fmt.Errorf("parse Muninn config %s: %w", path, err)
	}
	suppressSignals, err := normalizeSuppressedSignals(decoded.SuppressSignals)
	if err != nil {
		return repositoryConfig{}, fmt.Errorf("parse Muninn config %s: %w", path, err)
	}
	config.SuppressSignals = suppressSignals
	config.OwnedTools = decoded.OwnedTools
	return config, nil
}

func normalizeSuppressedSignals(signals []string) ([]string, error) {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(signals))
	for index, signal := range signals {
		signal = strings.TrimSpace(signal)
		if signal == "" {
			return nil, fmt.Errorf("suppressSignals[%d] must not be empty", index)
		}
		if !suppressedSignalPattern.MatchString(signal) || strings.Contains(signal, "..") || strings.Contains(signal, "//") {
			return nil, fmt.Errorf("suppressSignals[%d] must be an exact printed signal ID", index)
		}
		if _, exists := seen[signal]; exists {
			continue
		}
		seen[signal] = struct{}{}
		result = append(result, signal)
	}
	sort.Strings(result)
	return result, nil
}

type sessionSource interface {
	Name() string
	SessionDirs(explicit string, includeArchived bool) ([]string, error)
	Analyze(sessionDirs []string, repoRoot string, since, generatedAt time.Time, taskFilter string, ownership ownershipCatalog, metadata map[string]normalizedSessionMetadata) (codexSessionInsightsReport, error)
}

type codexSessionSource struct{}

func (codexSessionSource) Name() string {
	return "codex"
}

func (codexSessionSource) SessionDirs(explicit string, includeArchived bool) ([]string, error) {
	resolved, err := resolveCodexSessionsDir(explicit)
	if err != nil {
		return nil, err
	}
	dirs := []string{resolved}
	if includeArchived {
		archivedDir := filepath.Join(filepath.Dir(resolved), "archived_sessions")
		if dirExists(archivedDir) {
			dirs = append(dirs, archivedDir)
		}
	}
	return dirs, nil
}

func (codexSessionSource) Analyze(sessionDirs []string, repoRoot string, since, generatedAt time.Time, taskFilter string, ownership ownershipCatalog, metadata map[string]normalizedSessionMetadata) (codexSessionInsightsReport, error) {
	return analyzeCodexSessionsFilteredWithMetadata(sessionDirs, repoRoot, since, generatedAt, taskFilter, ownership, metadata)
}

func resolveSessionSource(name string) (sessionSource, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "codex":
		return codexSessionSource{}, nil
	default:
		return nil, fmt.Errorf("unsupported session provider %q (available: codex)", name)
	}
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"sessions"}
	}
	if strings.EqualFold(args[0], "analyze") {
		args[0] = "sessions"
	}
	if err := cmdCodex(root, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func isHelpToken(arg string) bool {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func setFlagSetUsage(fs *flag.FlagSet, usageLine, summary string, examples []string) {
	fs.Usage = func() {
		if strings.TrimSpace(summary) != "" {
			fmt.Println(summary)
			fmt.Println()
		}
		fmt.Println("Usage:")
		fmt.Printf("  %s\n", usageLine)
		fmt.Println()
		fmt.Println("Flags:")
		fs.PrintDefaults()
		if len(examples) == 0 {
			return
		}
		fmt.Println()
		fmt.Println("Examples:")
		for _, example := range examples {
			if strings.TrimSpace(example) != "" {
				fmt.Printf("  %s\n", strings.TrimSpace(example))
			}
		}
	}
}

func cmdCodex(root string, args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printCodexHelp()
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "sessions":
		return cmdCodexSessions(root, args[1:])
	case "checkpoint":
		return cmdCheckpoint(root, args[1:])
	case "failures":
		return cmdFailures(root, args[1:])
	case "feedback":
		return cmdFeedback(root, args[1:])
	default:
		return fmt.Errorf("unknown Muninn command: %s", args[0])
	}
}

func printCodexHelp() {
	fmt.Print(`Muninn agent-session friction analysis

Usage:
  muninn analyze [flags]
  muninn sessions [flags]
  muninn checkpoint <name> [analysis flags]
  muninn failures --operation <tool/operation> [flags]
  muninn feedback <add|resolve|list> [flags]

Available Commands:
  analyze   Analyze agent-session cost and friction for a repository
  sessions  Compatibility alias for analyze
  checkpoint Save a quiet named trend checkpoint
  failures  Inspect bounded failure events for one owned operation
  feedback  Record or inspect normalized agent-reported friction

Codex is the first session provider. The provider boundary is explicit so
Claude Code and OpenCode adapters can be added later without changing the
analysis/reporting core.

Muninn skips prompts and messages. It scans tool calls locally for fixed,
privacy-safe labels and output volume/status markers. It never prints prompts,
tool inputs, tool output, command text, absolute paths, or session identifiers.

Examples:
  muninn
  muninn analyze --repo .
  muninn analyze --repo /path/to/repository --since 24h
  muninn analyze --repo . --since-commit HEAD~3
  muninn analyze --repo . --task my-worktree
  muninn analyze --repo . --since 14d --include-archived
  muninn analyze --repo . --checkpoint before-tooling-change
  muninn checkpoint before-tooling-change --repo .
  muninn analyze --repo . --compare before-tooling-change
  muninn analyze --repo . --overview
  muninn analyze --repo . --since 24h --operations bwb
  muninn analyze --repo . --focus structure
  muninn analyze --repo . --details
  muninn analyze --repo . --json
  muninn failures --repo . --operation bwb/comments-wait --since 14d
  muninn feedback add --category roundtrip --target bwb/pr --signal existing-pr-create-failed
`)
}

func cmdCheckpoint(root string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: muninn checkpoint <name> [analysis flags]")
	}
	if isHelpToken(args[0]) {
		fmt.Print(`Save a quiet named trend checkpoint

Usage:
  muninn checkpoint <name> [analysis flags]

Examples:
  muninn checkpoint before-tooling-change
  muninn checkpoint after-tooling-change --repo . --since 14d
  muninn checkpoint reclassified --repo . --refresh
`)
		return nil
	}
	analyzeArgs, err := checkpointAnalyzeArgs(args)
	if err != nil {
		return err
	}
	return cmdCodexSessions(root, analyzeArgs)
}

func checkpointAnalyzeArgs(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, errors.New("usage: muninn checkpoint <name> [analysis flags]")
	}
	name := strings.TrimSpace(args[0])
	if name == "" || strings.HasPrefix(name, "-") {
		return nil, errors.New("checkpoint name must be the first argument")
	}
	for _, arg := range args[1:] {
		if arg == "--checkpoint" || strings.HasPrefix(arg, "--checkpoint=") ||
			arg == "--quiet" || strings.HasPrefix(arg, "--quiet=") {
			return nil, errors.New("muninn checkpoint manages --checkpoint and --quiet automatically")
		}
	}
	analyzeArgs := append([]string(nil), args[1:]...)
	analyzeArgs = append(analyzeArgs, "--checkpoint", name, "--quiet")
	return analyzeArgs, nil
}

func cmdCodexSessions(root string, args []string) error {
	fs := flag.NewFlagSet("muninn analyze", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	sinceRaw := fs.String("since", "7d", "lookback duration (for example 24h, 7d, or 2w)")
	sinceCommit := fs.String("since-commit", "", "analyze activity after a Git commit timestamp")
	providerName := fs.String("provider", "codex", "session provider (available: codex)")
	sessionsDir := fs.String("sessions-dir", "", "provider session directory (Codex default: $CODEX_HOME/sessions or ~/.codex/sessions)")
	repoRoot := root
	fs.StringVar(&repoRoot, "repo", root, "only include sessions whose cwd is inside this repository")
	fs.StringVar(&repoRoot, "workspace-root", root, "compatibility alias for --repo")
	taskFilter := fs.String("task", "", "only include sessions attributed to this exact worktree/task ID")
	configPath := fs.String("config", "", "repository config path (default: <repo>/.muninn.json when present)")
	includeArchived := fs.Bool("include-archived", false, "also scan the sibling archived_sessions directory")
	defaultStorePath, err := defaultSessionStorePath()
	if err != nil {
		return err
	}
	storePath := fs.String("db", defaultStorePath, "local privacy-safe SQLite index path")
	noCache := fs.Bool("no-cache", false, "scan provider files directly without the SQLite index")
	forceRefresh := fs.Bool("refresh", false, "re-index all discovered session files")
	checkpointName := fs.String("checkpoint", "", "save this analysis as a named trend checkpoint")
	compareName := fs.String("compare", "", "compare this analysis with a named checkpoint")
	quietOutput := fs.Bool("quiet", false, "suppress report output after saving a checkpoint")
	overviewOutput := fs.Bool("overview", false, "show aggregate family totals only")
	findingsOutput := fs.Bool("findings", false, "show actionable findings (default)")
	detailsOutput := fs.Bool("details", false, "show full rankings, transitions, failures, and signals; with --operations, show all operation rows")
	focus := fs.String("focus", "", "filter findings: friction (broad), tooling, instructions, interface, structure, discovery, failures, loops, output, or quality")
	operationsTool := fs.String("operations", "", "show only configured operations for one locally owned tool ID")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON")
	limit := fs.Int("limit", 10, "maximum task rows in human output (0 shows all)")
	setFlagSetUsage(
		fs,
		"muninn analyze [--provider codex] [--repo <path>] [--since <duration>] [--sessions-dir <path>] [--task <task-id>] [--operations <owned-tool>] [--include-archived] [--json] [--limit <n>]",
		"Summarize token usage and tool-output attribution without exposing session content or command text.",
		[]string{
			"muninn analyze --repo .",
			"muninn analyze --repo . --since 24h",
			"muninn analyze --repo . --since 24h --operations bwb",
			"muninn analyze --repo . --since 24h --operations bwb --details",
			"muninn analyze --repo . --focus structure --details",
			"muninn analyze --repo . --task my-worktree",
			"muninn analyze --repo . --since 14d --include-archived --limit 20",
			"muninn analyze --repo . --json",
		},
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: muninn analyze [flags]")
	}
	if *limit < 0 {
		return errors.New("--limit must be 0 or greater")
	}
	sinceWasSet := false
	limitWasSet := false
	fs.Visit(func(visited *flag.Flag) {
		switch visited.Name {
		case "since":
			sinceWasSet = true
		case "limit":
			limitWasSet = true
		}
	})
	if strings.TrimSpace(*sinceCommit) != "" && sinceWasSet {
		return errors.New("--since and --since-commit are mutually exclusive")
	}
	if *noCache && (*forceRefresh || strings.TrimSpace(*checkpointName) != "" || strings.TrimSpace(*compareName) != "") {
		return errors.New("--no-cache cannot be combined with --refresh, --checkpoint, or --compare")
	}
	if *jsonOutput && strings.TrimSpace(*compareName) != "" {
		return errors.New("--compare currently requires human output")
	}
	if *quietOutput && (*jsonOutput ||
		strings.TrimSpace(*compareName) != "" ||
		*overviewOutput ||
		*findingsOutput ||
		*detailsOutput ||
		strings.TrimSpace(*focus) != "" ||
		strings.TrimSpace(*operationsTool) != "") {
		return errors.New("--quiet cannot be combined with report, comparison, focus, or operations output")
	}
	if *quietOutput && strings.TrimSpace(*checkpointName) == "" {
		return errors.New("--quiet requires --checkpoint")
	}
	outputSelection, err := resolveAnalyzeOutputSelection(
		*overviewOutput,
		*findingsOutput,
		*detailsOutput,
		*focus,
		*operationsTool,
		*limit,
		limitWasSet,
	)
	if err != nil {
		return err
	}
	source, err := resolveSessionSource(*providerName)
	if err != nil {
		return err
	}
	sessionDirs, err := source.SessionDirs(*sessionsDir, *includeArchived)
	if err != nil {
		return err
	}
	sessionMetadata := loadProviderSessionMetadata(source.Name(), sessionDirs)
	resolvedRepoRoot, err := filepath.Abs(strings.TrimSpace(repoRoot))
	if err != nil {
		return fmt.Errorf("resolve --repo: %w", err)
	}
	now := time.Now().UTC()
	since := time.Time{}
	if reference := strings.TrimSpace(*sinceCommit); reference != "" {
		since, err = resolveSinceCommit(resolvedRepoRoot, reference)
		if err != nil {
			return err
		}
	} else {
		lookback, err := parseCodexLookback(*sinceRaw)
		if err != nil {
			return fmt.Errorf("invalid --since value %q: %w", *sinceRaw, err)
		}
		since = now.Add(-lookback)
	}
	config, err := loadRepositoryConfig(resolvedRepoRoot, *configPath)
	if err != nil {
		return err
	}
	ownership := newOwnershipCatalog(config.OwnedTools)
	var report codexSessionInsightsReport
	var store *sessionStore
	if *noCache {
		report, err = source.Analyze(sessionDirs, resolvedRepoRoot, since, now, strings.TrimSpace(*taskFilter), ownership, sessionMetadata)
		if err != nil {
			return err
		}
	} else {
		normalizer, ok := source.(sessionNormalizer)
		if !ok {
			return fmt.Errorf("session provider %q does not support indexed analysis; use --no-cache", source.Name())
		}
		store, err = openSessionStore(*storePath)
		if err != nil {
			return fmt.Errorf("%w (use --no-cache to bypass the index)", err)
		}
		defer store.Close()
		if *forceRefresh && !*jsonOutput {
			fmt.Fprintf(os.Stderr, "Refreshing Muninn index for %s...\n", filepath.Base(resolvedRepoRoot))
		}
		stats, err := store.refresh(context.Background(), source.Name(), sessionDirs, resolvedRepoRoot, normalizer, ownership, *forceRefresh)
		if err != nil {
			return err
		}
		if *forceRefresh && !*jsonOutput {
			fmt.Fprintln(os.Stderr, formatRefreshCompletion(stats))
		}
		report, err = store.analyze(
			context.Background(),
			source.Name(),
			sessionDirs,
			resolvedRepoRoot,
			since,
			now,
			strings.TrimSpace(*taskFilter),
			ownership,
			stats,
			sessionMetadata,
		)
		if err != nil {
			return err
		}
	}
	report.Provider = source.Name()
	report.Instructions = inspectRepositoryInstructions(resolvedRepoRoot, source.Name())
	repositoryKey := ownershipSelectorDigest("repo", resolvedRepoRoot)
	if store != nil {
		report.Feedback, err = store.listFeedback(context.Background(), repositoryKey, since, false)
		if err != nil {
			return err
		}
	}
	report.Findings = buildSessionFindings(report, config)
	report.Findings, err = filterSessionFindings(report.Findings, *focus)
	if err != nil {
		return err
	}
	var baseline *codexSessionInsightsReport
	if name := strings.TrimSpace(*compareName); name != "" {
		loaded, err := store.loadCheckpoint(context.Background(), name, source.Name(), repositoryKey)
		if err != nil {
			return err
		}
		baseline = &loaded
	}
	if name := strings.TrimSpace(*checkpointName); name != "" {
		if err := store.saveCheckpoint(context.Background(), name, source.Name(), repositoryKey, report); err != nil {
			return err
		}
		if *jsonOutput {
			fmt.Fprintf(os.Stderr, "saved Muninn checkpoint %q\n", name)
		}
	}
	if *quietOutput {
		fmt.Printf("Saved checkpoint %q.\n", strings.TrimSpace(*checkpointName))
		return nil
	}
	if toolID := strings.TrimSpace(*operationsTool); toolID != "" {
		drilldown, err := buildOwnedOperationsDrilldown(report, config, toolID, outputSelection.OperationLimit)
		if err != nil {
			return err
		}
		if *jsonOutput {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(drilldown)
		}
		printOwnedOperationsDrilldown(drilldown)
		if name := strings.TrimSpace(*checkpointName); name != "" {
			fmt.Printf("\nSaved checkpoint %q.\n", name)
		}
		return nil
	}
	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	printCodexSessionInsights(report, config, *limit, outputSelection.View)
	if baseline != nil {
		printSessionTrend(*baseline, report, strings.TrimSpace(*compareName))
	}
	if name := strings.TrimSpace(*checkpointName); name != "" {
		fmt.Printf("\nSaved checkpoint %q.\n", name)
	}
	return nil
}

type analyzeOutputSelection struct {
	View           string
	OperationLimit int
}

func resolveAnalyzeOutputSelection(
	overview,
	findings,
	details bool,
	focus,
	operations string,
	limit int,
	limitWasSet bool,
) (analyzeOutputSelection, error) {
	viewCount := 0
	for _, selected := range []bool{overview, findings, details} {
		if selected {
			viewCount++
		}
	}
	if viewCount > 1 {
		return analyzeOutputSelection{}, errors.New("--overview, --findings, and --details are mutually exclusive")
	}
	focus = strings.TrimSpace(focus)
	operations = strings.TrimSpace(operations)
	if focus != "" && overview {
		return analyzeOutputSelection{}, errors.New("--focus applies to findings output and cannot be combined with --overview")
	}
	if operations != "" && (overview || findings || focus != "") {
		return analyzeOutputSelection{}, errors.New("--operations cannot be combined with --overview, --findings, or --focus")
	}

	selection := analyzeOutputSelection{
		View:           "findings",
		OperationLimit: limit,
	}
	switch {
	case operations != "":
		if details && !limitWasSet {
			selection.OperationLimit = 0
		}
	case focus != "":
		// Focused findings already include their full evidence. Accepting
		// --details here avoids a help/retry roundtrip without expanding output.
		selection.View = "focused"
	case overview:
		selection.View = "overview"
	case details:
		selection.View = "details"
	}
	return selection, nil
}

func formatRefreshCompletion(stats sessionRefreshStats) string {
	return fmt.Sprintf(
		"Refresh complete: %s scanned, %s indexed, %s reused, %s unreadable.",
		formatCodexCount(int64(stats.FilesScanned)),
		formatCodexCount(int64(stats.FilesIndexed)),
		formatCodexCount(int64(stats.FilesReused)),
		formatCodexCount(int64(stats.FilesUnreadable)),
	)
}

func resolveCodexSessionsDir(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		path, err := filepath.Abs(strings.TrimSpace(explicit))
		if err != nil {
			return "", err
		}
		if !dirExists(path) {
			return "", fmt.Errorf("Codex sessions directory does not exist: %s", path)
		}
		return path, nil
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	path := filepath.Join(codexHome, "sessions")
	if !dirExists(path) {
		return "", fmt.Errorf("Codex sessions directory does not exist: %s (use --sessions-dir)", path)
	}
	return path, nil
}

func parseCodexLookback(raw string) (time.Duration, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return 0, errors.New("duration cannot be empty")
	}
	multiplier := time.Duration(1)
	switch {
	case strings.HasSuffix(value, "d"):
		multiplier = 24 * time.Hour
		value = strings.TrimSuffix(value, "d")
	case strings.HasSuffix(value, "w"):
		multiplier = 7 * 24 * time.Hour
		value = strings.TrimSuffix(value, "w")
	default:
		duration, err := time.ParseDuration(value)
		if err != nil {
			return 0, err
		}
		if duration <= 0 {
			return 0, errors.New("duration must be positive")
		}
		return duration, nil
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number <= 0 {
		return 0, errors.New("duration must be positive")
	}
	return time.Duration(number * float64(multiplier)), nil
}

func analyzeCodexSessions(sessionDirs []string, workspaceRoot string, since, generatedAt time.Time) (codexSessionInsightsReport, error) {
	return analyzeCodexSessionsFiltered(sessionDirs, workspaceRoot, since, generatedAt, "")
}

func analyzeCodexSessionsFiltered(sessionDirs []string, workspaceRoot string, since, generatedAt time.Time, taskFilter string) (codexSessionInsightsReport, error) {
	return analyzeCodexSessionsFilteredWithOwnership(sessionDirs, workspaceRoot, since, generatedAt, taskFilter, ownershipCatalog{})
}

func analyzeCodexSessionsFilteredWithOwnership(sessionDirs []string, workspaceRoot string, since, generatedAt time.Time, taskFilter string, ownership ownershipCatalog) (codexSessionInsightsReport, error) {
	return analyzeCodexSessionsFilteredWithMetadata(sessionDirs, workspaceRoot, since, generatedAt, taskFilter, ownership, nil)
}

func analyzeCodexSessionsFilteredWithMetadata(sessionDirs []string, workspaceRoot string, since, generatedAt time.Time, taskFilter string, ownership ownershipCatalog, metadata map[string]normalizedSessionMetadata) (codexSessionInsightsReport, error) {
	report := newSessionInsightsReport("codex", sessionDirs, workspaceRoot, since, generatedAt)
	taskMap := map[string]*codexTaskInsights{}
	for _, sessionDir := range sessionDirs {
		err := filepath.WalkDir(sessionDir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				report.Summary.FilesUnreadable++
				return nil
			}
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
				return nil
			}
			report.Summary.FilesScanned++
			session, err := parseCodexNormalizedSession(path)
			if err != nil {
				report.Summary.FilesUnreadable++
				return nil
			}
			enrichNormalizedSession(&session, metadata)
			record, err := sessionRecordFromNormalized(session, workspaceRoot, since, generatedAt, ownership)
			if err != nil {
				report.Summary.FilesUnreadable++
				return nil
			}
			if record.CWD == "" || record.StartedAt.IsZero() {
				return nil
			}
			if taskFilter != "" && record.Task != taskFilter {
				return nil
			}
			addCodexSessionToReport(&report, taskMap, record)
			return nil
		})
		if err != nil {
			return report, fmt.Errorf("scan Codex sessions in %s: %w", sessionDir, err)
		}
	}
	finishSessionInsightsReport(&report, taskMap)
	return report, nil
}

func newSessionInsightsReport(provider string, sessionDirs []string, workspaceRoot string, since, generatedAt time.Time) codexSessionInsightsReport {
	return codexSessionInsightsReport{
		SchemaVersion: codexSessionInsightsSchemaVersion,
		Provider:      provider,
		GeneratedAt:   generatedAt.Format(time.RFC3339),
		Since:         since.Format(time.RFC3339),
		WorkspaceRoot: workspaceRoot,
		SessionDirs:   append([]string(nil), sessionDirs...),
		Summary: codexSessionInsightsSummary{
			ToolCallsByName:              map[string]int{},
			ToolMetricsByName:            map[string]codexToolMetrics{},
			ShellCommandsByFamily:        map[string]codexToolMetrics{},
			MixedShellShapes:             map[string]codexToolMetrics{},
			CrossCallTransitions:         map[string]codexTransitionMetrics{},
			OwnedTooling:                 map[string]codexToolMetrics{},
			OwnedOperations:              map[string]codexOwnedOperationMetrics{},
			OwnedOperationFailureReasons: map[string]map[string]codexOccurrenceMetrics{},
			ReadTargets:                  map[string]codexTargetMetrics{},
			InlineOrchestrationByTool:    map[string]codexInlineMetrics{},
			FailureReasons:               map[string]int{},
			FailureContexts:              map[string]map[string]codexOccurrenceMetrics{},
			ProgressStalls:               map[string]codexWaitMetrics{},
			ExpectedWaits:                map[string]codexWaitMetrics{},
			OversizedOutputs:             map[string]codexOversizedOutputMetrics{},
			Activity:                     map[string]time.Time{},
		},
	}
}

func finishSessionInsightsReport(report *codexSessionInsightsReport, taskMap map[string]*codexTaskInsights) {
	report.Tasks = make([]codexTaskInsights, 0, len(taskMap))
	for _, task := range taskMap {
		report.Tasks = append(report.Tasks, *task)
	}
	sort.Slice(report.Tasks, func(i, j int) bool {
		if report.Tasks[i].FreshTokens != report.Tasks[j].FreshTokens {
			return report.Tasks[i].FreshTokens > report.Tasks[j].FreshTokens
		}
		if report.Tasks[i].Sessions != report.Tasks[j].Sessions {
			return report.Tasks[i].Sessions > report.Tasks[j].Sessions
		}
		return report.Tasks[i].Task < report.Tasks[j].Task
	})
	report.Outcomes = analyzeCompletionEpisodes(report.taskEpisodes)
	report.Profiles = analyzeModelEffortProfiles(report.sessionRecords)
	report.Delegation = analyzeDelegation(report.sessionRecords)
}

func parseCodexSession(path, workspaceRoot string, since, generatedAt time.Time) (codexSessionRecord, error) {
	return parseCodexSessionWithOwnership(path, workspaceRoot, since, generatedAt, ownershipCatalog{})
}

func parseCodexSessionWithOwnership(path, workspaceRoot string, since, generatedAt time.Time, ownership ownershipCatalog) (codexSessionRecord, error) {
	session, err := parseCodexNormalizedSession(path)
	if err != nil {
		return codexSessionRecord{}, err
	}
	return sessionRecordFromNormalized(session, workspaceRoot, since, generatedAt, ownership)
}

func codexRolloutLineNeeded(line []byte) bool {
	return bytes.Contains(line, []byte(`"type":"session_meta"`)) ||
		bytes.Contains(line, []byte(`"type":"compacted"`)) ||
		bytes.Contains(line, []byte(`"type":"context_compacted"`)) ||
		bytes.Contains(line, []byte(`"type":"token_count"`)) ||
		bytes.Contains(line, []byte(`"type":"task_complete"`)) ||
		bytes.Contains(line, []byte(`"type":"task_completed"`)) ||
		bytes.Contains(line, []byte(`"type":"function_call"`)) ||
		bytes.Contains(line, []byte(`"type":"function_call_output"`)) ||
		bytes.Contains(line, []byte(`"type":"custom_tool_call"`)) ||
		bytes.Contains(line, []byte(`"type":"custom_tool_call_output"`))
}

func codexShellCommandFamily(toolName, arguments, input string) string {
	family, _ := codexShellCommandAnalysis(toolName, arguments, input)
	return family
}

func codexShellCommandAnalysis(toolName, arguments, input string) (string, string) {
	family, shape, _, _ := codexShellCommandDetails(toolName, arguments, input)
	return family, shape
}

func codexShellCommandDetails(toolName, arguments, input string) (string, string, string, string) {
	commands := codexShellCommands(toolName, arguments, input)
	if len(commands) == 0 {
		return "", "", "", ""
	}
	var sequence []string
	families := make(map[string]struct{})
	for _, command := range commands {
		for _, segment := range codexShellCommandSegments(command) {
			for _, family := range codexShellSegmentFamilySequence(segment) {
				families[family] = struct{}{}
				if len(sequence) == 0 || sequence[len(sequence)-1] != family {
					sequence = append(sequence, family)
				}
			}
		}
	}
	family := codexCombinedShellFamily(families)
	first := ""
	last := ""
	if len(sequence) > 0 {
		first = sequence[0]
		last = sequence[len(sequence)-1]
	}
	if family != "mixed shell" {
		return family, "", first, last
	}
	const maxShapeFamilies = 5
	if len(sequence) > maxShapeFamilies {
		sequence = append(append([]string(nil), sequence[:maxShapeFamilies]...), "additional families")
	}
	return family, strings.Join(sequence, " -> "), first, last
}

func codexShellCommands(toolName, arguments, input string) []string {
	normalizedName := strings.ToLower(strings.TrimSpace(toolName))
	if normalizedName != "exec" && normalizedName != "exec_command" {
		return nil
	}
	var commands []string
	if normalizedName == "exec_command" {
		var decoded struct {
			Command string `json:"cmd"`
		}
		if json.Unmarshal([]byte(arguments), &decoded) == nil && decoded.Command != "" {
			commands = append(commands, decoded.Command)
		} else {
			commands = append(commands, arguments)
		}
	} else {
		commands = codexNestedCommands(input)
		if len(commands) == 0 {
			if strings.Contains(input, "tools.") {
				return nil
			}
			commands = append(commands, input)
		}
	}
	return commands
}

func codexNestedCommands(input string) []string {
	var commands []string
	for _, location := range codexNestedCommandStartPattern.FindAllStringIndex(input, -1) {
		if command, _, ok := codexJavaScriptString(input, location[1]); ok {
			commands = append(commands, command)
		}
	}
	return commands
}

func codexJavaScriptString(input string, start int) (string, int, bool) {
	if start >= len(input) || (input[start] != '"' && input[start] != '\'' && input[start] != '`') {
		return "", start, false
	}
	quote := input[start]
	var decoded strings.Builder
	for index := start + 1; index < len(input); index++ {
		current := input[index]
		if current == quote {
			return decoded.String(), index + 1, true
		}
		if current != '\\' {
			decoded.WriteByte(current)
			continue
		}
		index++
		if index >= len(input) {
			return "", start, false
		}
		escaped := input[index]
		switch escaped {
		case '\n':
			continue
		case 'n':
			decoded.WriteByte('\n')
		case 'r':
			decoded.WriteByte('\r')
		case 't':
			decoded.WriteByte('\t')
		case 'b':
			decoded.WriteByte('\b')
		case 'f':
			decoded.WriteByte('\f')
		case 'v':
			decoded.WriteByte('\v')
		case 'x', 'u':
			digits := 2
			if escaped == 'u' {
				digits = 4
			}
			if index+digits >= len(input) {
				return "", start, false
			}
			value, err := strconv.ParseUint(input[index+1:index+1+digits], 16, 32)
			if err != nil {
				return "", start, false
			}
			decoded.WriteRune(rune(value))
			index += digits
		default:
			decoded.WriteByte(escaped)
		}
	}
	return "", start, false
}

func codexShellSegmentsFamily(segments [][]string) string {
	families := make(map[string]struct{})
	for _, segment := range segments {
		if family := codexShellSegmentFamily(segment); family != "" {
			families[family] = struct{}{}
		}
	}
	return codexCombinedShellFamily(families)
}

func codexShellSegmentFamilySequence(tokens []string) []string {
	for len(tokens) > 0 && codexShellAssignment(tokens[0]) {
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return nil
	}
	executable := strings.ToLower(filepath.Base(tokens[0]))
	if executable == "env" || executable == "command" || executable == "time" || executable == "sudo" {
		return codexShellSegmentFamilySequence(tokens[1:])
	}
	if executable == "bash" || executable == "zsh" || executable == "sh" {
		for index := 1; index < len(tokens); index++ {
			token := tokens[index]
			if token == "--" || token == "-" || !strings.HasPrefix(token, "-") {
				break
			}
			if !strings.HasPrefix(token, "--") && strings.Contains(strings.TrimPrefix(token, "-"), "c") && index+1 < len(tokens) {
				var sequence []string
				for _, segment := range codexShellCommandSegments(tokens[index+1]) {
					sequence = append(sequence, codexShellSegmentFamilySequence(segment)...)
				}
				return sequence
			}
		}
		return []string{"other shell"}
	}
	if family := codexShellSegmentFamily(tokens); family != "" {
		return []string{family}
	}
	return nil
}

func codexCombinedShellFamily(families map[string]struct{}) string {
	if len(families) == 0 {
		return "other shell"
	}
	if len(families) > 1 {
		return "mixed shell"
	}
	for family := range families {
		return family
	}
	return "other shell"
}

func codexShellCommandSegments(command string) [][]string {
	var segments [][]string
	var tokens []string
	var token strings.Builder
	var quote rune
	escaped := false
	flushToken := func() {
		if token.Len() > 0 {
			tokens = append(tokens, token.String())
			token.Reset()
		}
	}
	flushSegment := func() {
		flushToken()
		if len(tokens) > 0 {
			segments = append(segments, tokens)
			tokens = nil
		}
	}
	for _, current := range command {
		if escaped {
			token.WriteRune(current)
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			} else {
				token.WriteRune(current)
			}
			continue
		}
		switch current {
		case '\'', '"', '`':
			quote = current
		case ' ', '\t', '\r':
			flushToken()
		case '\n', ';', '|', '&':
			flushSegment()
		default:
			token.WriteRune(current)
		}
	}
	if escaped {
		token.WriteByte('\\')
	}
	flushSegment()
	return segments
}

func codexShellSegmentFamily(tokens []string) string {
	for len(tokens) > 0 && codexShellAssignment(tokens[0]) {
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return ""
	}
	executable := strings.ToLower(filepath.Base(tokens[0]))
	if executable == "env" || executable == "command" || executable == "time" || executable == "sudo" {
		return codexShellSegmentFamily(tokens[1:])
	}
	if executable == "bash" || executable == "zsh" || executable == "sh" {
		for index := 1; index < len(tokens); index++ {
			token := tokens[index]
			if token == "--" || token == "-" || !strings.HasPrefix(token, "-") {
				break
			}
			if !strings.HasPrefix(token, "--") && strings.Contains(strings.TrimPrefix(token, "-"), "c") && index+1 < len(tokens) {
				return codexShellSegmentsFamily(codexShellCommandSegments(tokens[index+1]))
			}
		}
		return "other shell"
	}
	lowerTokens := make([]string, len(tokens))
	for index, token := range tokens {
		lowerTokens[index] = strings.ToLower(token)
	}
	switch executable {
	case "rg", "grep", "egrep", "fgrep":
		if executable == "rg" && len(lowerTokens) > 1 && lowerTokens[1] == "--files" {
			return "file reads"
		}
		return "search"
	case "sed", "head", "tail", "cat", "find", "tree", "wc", "ls":
		return "file reads"
	case "git":
		subcommand := codexGitSubcommand(lowerTokens[1:])
		switch subcommand {
		case "push":
			return "delivery"
		case "revert":
			return "revert"
		case "diff", "status", "log", "show", "branch", "rev-parse", "rev-list", "merge-base", "ls-files", "grep":
			return "git inspect"
		}
	case "go":
		if len(lowerTokens) > 1 {
			if lowerTokens[1] == "test" {
				return "tests"
			}
			if lowerTokens[1] == "vet" || lowerTokens[1] == "build" {
				return "build, lint, or install"
			}
		}
	case "clj", "clojure", "lein", "bb":
		for _, token := range lowerTokens[1:] {
			if strings.Contains(token, "test") {
				return "tests"
			}
		}
	case "pytest":
		return "tests"
	case "cargo":
		if len(lowerTokens) > 1 && lowerTokens[1] == "test" {
			return "tests"
		}
		if len(lowerTokens) > 1 && (lowerTokens[1] == "build" || lowerTokens[1] == "clippy") {
			return "build, lint, or install"
		}
	case "npm":
		if len(lowerTokens) > 1 && lowerTokens[1] == "test" {
			return "tests"
		}
		if len(lowerTokens) > 2 && lowerTokens[1] == "run" && (lowerTokens[2] == "build" || lowerTokens[2] == "lint") {
			return "build, lint, or install"
		}
	case "make":
		if len(lowerTokens) > 1 && (lowerTokens[1] == "install" || lowerTokens[1] == "build") {
			return "build, lint, or install"
		}
	case "codex":
		if len(lowerTokens) > 1 && lowerTokens[1] == "review" {
			return "review"
		}
	case "clj-kondo", "golangci-lint":
		return "build, lint, or install"
	case "heimdal", "playwright", "playwright-cli":
		if len(lowerTokens) > 1 && executable == "heimdal" && lowerTokens[1] == "run" {
			return "tests"
		}
		return "browser QA"
	case "bwb":
		for index, token := range lowerTokens {
			if token == "inspect" && index > 0 {
				return "bounded task inspect"
			}
			if token == "test" || token == "integration" {
				return "tests"
			}
		}
		return "other bwb task"
	}
	return "other shell"
}

func codexShellAssignment(token string) bool {
	name, _, found := strings.Cut(token, "=")
	if !found || name == "" {
		return false
	}
	for index, current := range name {
		if !(current == '_' || current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' || index > 0 && current >= '0' && current <= '9') {
			return false
		}
	}
	return true
}

func codexGitSubcommand(tokens []string) string {
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if token == "-c" || token == "-C" {
			index++
			continue
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		return token
	}
	return ""
}

func codexContinuationID(toolName, arguments string) (string, string) {
	normalizedName := strings.ToLower(strings.TrimSpace(toolName))
	if normalizedName != "write_stdin" && normalizedName != "wait" {
		return "", ""
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(arguments), &decoded) != nil {
		return "", ""
	}
	if value, ok := decoded["session_id"]; ok {
		return "session", codexJSONScalarString(value)
	}
	if value, ok := decoded["cell_id"]; ok {
		return "cell", strings.ToLower(codexJSONScalarString(value))
	}
	return "", ""
}

func codexNestedContinuationReference(toolName, input string) (codexContinuationReference, bool) {
	if !strings.EqualFold(strings.TrimSpace(toolName), "exec") {
		return codexContinuationReference{}, false
	}
	match := codexNestedContinuationPattern.FindStringSubmatch(input)
	if len(match) == 0 {
		return codexContinuationReference{}, false
	}
	id := ""
	for _, candidate := range match[2:] {
		if candidate != "" {
			id = candidate
			break
		}
	}
	if id == "" {
		return codexContinuationReference{}, false
	}
	referenceType := "cell"
	if strings.EqualFold(match[1], "session_id") {
		referenceType = "session"
	}
	return codexContinuationReference{Type: referenceType, ID: id}, true
}

func codexJSONScalarString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return ""
	}
}

type codexContinuationReference struct {
	Type string
	ID   string
}

func codexToolContinuationReferences(raw json.RawMessage) []codexContinuationReference {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	var references []codexContinuationReference
	switch typed := value.(type) {
	case string:
		references = append(references, codexContinuationReferencesFromStructuredText(typed)...)
		references = append(references, codexContinuationReferencesFromWrapperStatus(typed)...)
	case []any:
		for index, item := range typed {
			text := codexOutputItemText(item)
			if text == "" {
				continue
			}
			references = append(references, codexContinuationReferencesFromStructuredText(text)...)
			if index == 0 {
				references = append(references, codexContinuationReferencesFromWrapperStatus(text)...)
			}
		}
	case map[string]any:
		references = append(references, codexContinuationReferencesFromMap(typed, false)...)
	}
	return references
}

func codexEmitsExplicitSessionMarker(toolName, input string) bool {
	if !strings.EqualFold(strings.TrimSpace(toolName), "exec") {
		return false
	}
	return strings.Contains(input, "tools.exec_command(") &&
		strings.Contains(input, ".session_id") &&
		strings.Contains(input, "SESSION_ID=${")
}

func codexExplicitSessionMarkerReferences(raw json.RawMessage) []codexContinuationReference {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	var texts []string
	switch typed := value.(type) {
	case string:
		texts = append(texts, typed)
	case []any:
		for _, item := range typed {
			if text := codexOutputItemText(item); text != "" {
				texts = append(texts, text)
			}
		}
	}
	var references []codexContinuationReference
	for _, text := range texts {
		match := codexExplicitSessionMarkerPattern.FindStringSubmatch(strings.TrimSpace(text))
		if len(match) == 2 {
			references = append(references, codexContinuationReference{Type: "session", ID: match[1]})
		}
	}
	return references
}

func codexOutputItemText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
	}
	return ""
}

func codexContinuationReferencesFromStructuredText(text string) []codexContinuationReference {
	var value map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(text)), &value) != nil {
		return nil
	}
	return codexContinuationReferencesFromMap(value, true)
}

func codexContinuationReferencesFromMap(value map[string]any, requireWrapperShape bool) []codexContinuationReference {
	if requireWrapperShape && !codexStructuredToolResultShape(value) {
		return nil
	}
	var references []codexContinuationReference
	for _, field := range []struct {
		Key  string
		Type string
	}{
		{Key: "session_id", Type: "session"},
		{Key: "cell_id", Type: "cell"},
	} {
		if id := codexJSONScalarString(value[field.Key]); id != "" {
			references = append(references, codexContinuationReference{Type: field.Type, ID: id})
		}
	}
	return references
}

func codexStructuredToolResultShape(value map[string]any) bool {
	if _, ok := value["output"]; !ok {
		return false
	}
	for _, key := range []string{"chunk_id", "wall_time_seconds", "status", "exit_code", "original_token_count"} {
		if _, ok := value[key]; ok {
			return true
		}
	}
	return false
}

func codexContinuationReferencesFromWrapperStatus(text string) []codexContinuationReference {
	status := text
	if index := strings.Index(status, "\nOutput:\n"); index >= 0 {
		status = status[:index]
	}
	if len(status) > 4096 {
		status = status[:4096]
	}
	var references []codexContinuationReference
	for _, match := range codexContinuationStatusPattern.FindAllStringSubmatch(status, -1) {
		references = append(references, codexContinuationReference{
			Type: strings.ToLower(match[1]),
			ID:   strings.TrimSpace(match[2]),
		})
	}
	return references
}

func addCodexToolMetrics(metrics map[string]codexToolMetrics, key string, calls int, failed, truncated bool, outputBytes int64) {
	if key == "" {
		return
	}
	value := metrics[key]
	value.Calls += calls
	if failed {
		value.FailedCalls++
	}
	if truncated {
		value.TruncatedCalls++
	}
	value.OutputBytes += outputBytes
	value.EstimatedOutputTokens = estimatedTokens(value.OutputBytes)
	metrics[key] = value
}

func sessionActivityKey(kind, target string) string {
	return kind + "\x00" + target
}

func touchSessionActivity(activity map[string]time.Time, kind, target string, occurredAt time.Time) {
	if occurredAt.IsZero() {
		return
	}
	key := sessionActivityKey(kind, target)
	if current := activity[key]; current.IsZero() || occurredAt.After(current) {
		activity[key] = occurredAt
	}
}

func mergeSessionActivity(target, additions map[string]time.Time) {
	for key, occurredAt := range additions {
		if current := target[key]; current.IsZero() || occurredAt.After(current) {
			target[key] = occurredAt
		}
	}
}

func pathInsideRoot(root, path string) (bool, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(root, absolutePath)
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))), nil
}

func codexTaskName(workspaceRoot, cwd string) string {
	relative, err := filepath.Rel(workspaceRoot, cwd)
	if err != nil || relative == "." {
		return "(root)"
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	if len(parts) >= 2 && parts[0] == ".worktrees" {
		return parts[1]
	}
	if len(parts) >= 3 && parts[0] == ".workbench" && parts[1] == "worktrees" {
		return parts[2]
	}
	if len(parts) >= 3 && parts[0] == ".workbench" && parts[1] == "repos" {
		return "(cached)/" + parts[2]
	}
	return "(root)"
}

func codexToolOutputText(raw json.RawMessage) (string, string, int64) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", 0
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", "", int64(len(raw))
	}
	var texts []string
	var collect func(any)
	collect = func(item any) {
		switch typed := item.(type) {
		case string:
			texts = append(texts, typed)
		case []any:
			for _, child := range typed {
				collect(child)
			}
		case map[string]any:
			if text, ok := typed["text"].(string); ok {
				texts = append(texts, text)
				return
			}
			if output, ok := typed["output"]; ok {
				collect(output)
			}
		}
	}
	collect(value)
	text := strings.Join(texts, "\n")
	statusText := ""
	if len(texts) > 0 {
		statusText = texts[0]
	}
	return text, statusText, int64(len([]byte(text)))
}

func codexToolOutputTruncated(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "warning: truncated output") ||
		strings.Contains(lower, "output truncated") ||
		strings.Contains(lower, "…truncated")
}

func codexToolOutputFailed(statusText, toolName string) bool {
	preview := statusText
	if len(preview) > 8192 {
		preview = preview[:8192]
	}
	lower := strings.ToLower(preview)
	if strings.HasPrefix(strings.TrimSpace(lower), "error:") ||
		strings.HasPrefix(strings.TrimSpace(lower), "failed:") ||
		nonZeroDisplayExitCodePattern.MatchString(preview) {
		return true
	}
	execLike := strings.Contains(strings.ToLower(toolName), "exec") ||
		strings.EqualFold(toolName, "write_stdin") ||
		strings.EqualFold(toolName, "wait")
	if !execLike {
		return false
	}
	if strings.Contains(lower, "script failed") ||
		nonZeroProcessExitCodePattern.MatchString(preview) ||
		strings.Contains(lower, "command timed out") ||
		strings.Contains(lower, "timed out after") {
		return true
	}
	if nonZeroExitCodePattern.MatchString(preview) {
		return true
	}
	return false
}

func codexToolFailureReason(statusText string) string {
	preview := strings.ToLower(statusText)
	if len(preview) > 32768 {
		preview = preview[:32768]
	}
	switch {
	case strings.Contains(preview, "--api override is disabled"):
		return "local CLI targeting"
	case strings.Contains(preview, "missing test result sentinel") ||
		strings.Contains(preview, "failed before reporting test results"):
		return "test harness protocol"
	case strings.Contains(preview, "head sha can't be blank") ||
		strings.Contains(preview, "base sha can't be blank") ||
		strings.Contains(preview, "no commits between"):
		return "PR branch state"
	case strings.Contains(preview, "unknown nrepl target") ||
		strings.Contains(preview, "unsupported nrepl target"):
		return "unsupported command target"
	case strings.Contains(preview, "unknown option") ||
		strings.Contains(preview, "unknown flag") ||
		strings.Contains(preview, "unrecognized option"):
		return "unsupported command option"
	case strings.Contains(preview, "address already in use") ||
		strings.Contains(preview, "port already in use") ||
		strings.Contains(preview, "bind: address in use"):
		return "port collision"
	case strings.Contains(preview, "connection refused") ||
		strings.Contains(preview, "emulator unavailable"):
		return "local service unavailable"
	case strings.Contains(preview, "http 502") ||
		strings.Contains(preview, "http 503") ||
		strings.Contains(preview, "http 504") ||
		strings.Contains(preview, "bad gateway") ||
		strings.Contains(preview, "gateway timeout") ||
		strings.Contains(preview, "service unavailable") ||
		strings.Contains(preview, "couldn't respond to github"):
		return "transient service failure"
	case strings.Contains(preview, "command not found") ||
		strings.Contains(preview, "executable file not found"):
		return "missing executable"
	case strings.Contains(preview, "no such file or directory") ||
		strings.Contains(preview, "file not found") ||
		strings.Contains(preview, "missing fixture"):
		return "missing path or fixture"
	case strings.Contains(preview, "command timed out") ||
		strings.Contains(preview, "timed out after"):
		return "timeout"
	case strings.Contains(preview, "process exited with code 130") ||
		strings.Contains(preview, "process exited with code 137") ||
		strings.Contains(preview, "terminated by signal"):
		return "interrupted process"
	case strings.Contains(preview, "lint failed") ||
		strings.Contains(preview, "linting took") ||
		strings.Contains(preview, "clj-kondo found"):
		return "lint failure"
	case strings.Contains(preview, "fail in (") ||
		strings.Contains(preview, "tests failed") ||
		strings.Contains(preview, "test failed"):
		return "test failure"
	default:
		return "other non-zero exit"
	}
}

func codexToolFailureReasonForDescriptor(statusText string, descriptor codexToolCallDescriptor) string {
	reason := codexToolFailureReason(statusText)
	if reason != "other non-zero exit" || !searchMissExitCodePattern.MatchString(statusText) {
		return reason
	}
	if descriptor.Family == "search" || strings.Contains(descriptor.Shape, "search") {
		return "search no match"
	}
	return reason
}

func codexFailureContextLabel(descriptor codexToolCallDescriptor) string {
	if descriptor.Shape != "" {
		return descriptor.Shape
	}
	if descriptor.Family != "" {
		return descriptor.Family
	}
	if descriptor.Name != "" {
		return "tool " + descriptor.Name
	}
	return "(unknown)"
}

func addCodexFailureContext(contexts map[string]map[string]int, reason, context string) {
	if reason == "" {
		return
	}
	if context == "" {
		context = "(unknown)"
	}
	if contexts[reason] == nil {
		contexts[reason] = map[string]int{}
	}
	contexts[reason][context]++
}

func addCodexSessionToReport(report *codexSessionInsightsReport, taskMap map[string]*codexTaskInsights, record codexSessionRecord) {
	report.sessionRecords = append(report.sessionRecords, record)
	summary := &report.Summary
	summary.Sessions++
	if record.Completed {
		summary.CompletedSessions++
	} else {
		summary.IncompleteSessions++
	}
	duration := codexSessionDuration(record)
	summary.DurationSeconds += duration
	summary.Compactions += record.Compactions
	if record.Compactions > 0 {
		summary.SessionsWithCompactions++
	}
	addCodexTokenUsage(&summary.Tokens, record.Tokens)
	summary.FreshTokens += record.Tokens.UncachedInputTokens + record.Tokens.OutputTokens
	summary.ToolCalls += record.ToolCalls
	summary.FailedToolCalls += record.FailedToolCalls
	summary.TruncatedToolCalls += record.TruncatedToolCalls
	summary.ToolOutputBytes += record.ToolOutputBytes
	summary.ToolOutputTokens += estimatedTokens(record.ToolOutputBytes)
	for name, count := range record.ToolCallsByName {
		summary.ToolCallsByName[name] += count
	}
	for name, metrics := range record.ToolMetricsByName {
		addCodexToolMetricsValue(summary.ToolMetricsByName, name, metrics)
	}
	for family, metrics := range record.ShellCommandsByFamily {
		addCodexToolMetricsValue(summary.ShellCommandsByFamily, family, metrics)
	}
	for shape, metrics := range record.MixedShellShapes {
		addCodexToolMetricsValue(summary.MixedShellShapes, shape, metrics)
	}
	addCodexTransitionMetrics(summary.CrossCallTransitions, record.CrossCallTransitions)
	for id, metrics := range record.OwnedTooling {
		addCodexToolMetricsValue(summary.OwnedTooling, id, metrics)
	}
	addCodexOwnedOperationMetrics(summary.OwnedOperations, record.OwnedOperations, record.OwnedOperationAmbiguous)
	addCodexFailureContexts(summary.OwnedOperationFailureReasons, record.OwnedOperationFailureReasons)
	addCodexTargetMetrics(summary.ReadTargets, record.ReadTargets)
	summary.InlineOrchestrationCalls += record.InlineOrchestrationCalls
	summary.InlineOrchestrationBytes += record.InlineOrchestrationBytes
	summary.InlineOrchestrationMaxBytes = max(summary.InlineOrchestrationMaxBytes, record.InlineOrchestrationMaxBytes)
	addCodexInlineMetrics(summary.InlineOrchestrationByTool, record.InlineOrchestrationByTool)
	if record.InlineOrchestrationCalls > 0 {
		summary.InlineOrchestrationSessions++
	}
	for reason, count := range record.FailureReasons {
		summary.FailureReasons[reason] += count
	}
	addCodexFailureContexts(summary.FailureContexts, record.FailureContexts)
	addCodexWaitMetrics(summary.ProgressStalls, record.ProgressStalls)
	addCodexWaitMetrics(summary.ExpectedWaits, record.ExpectedWaits)
	addCodexOversizedOutputMetrics(summary.OversizedOutputs, record.OversizedOutputs)
	addDeliveryReworkMetrics(&summary.DeliveryRework, record.DeliveryRework)
	addDownstreamQualityMetrics(&summary.DownstreamQuality, record.DownstreamQuality)
	mergeSessionActivity(summary.Activity, record.Activity)
	report.taskEpisodes = append(report.taskEpisodes, record.TaskEpisodes...)

	task := taskMap[record.Task]
	if task == nil {
		task = &codexTaskInsights{
			Task:                         record.Task,
			ShellCommandsByFamily:        map[string]codexToolMetrics{},
			MixedShellShapes:             map[string]codexToolMetrics{},
			CrossCallTransitions:         map[string]codexTransitionMetrics{},
			OwnedTooling:                 map[string]codexToolMetrics{},
			OwnedOperations:              map[string]codexOwnedOperationMetrics{},
			OwnedOperationFailureReasons: map[string]map[string]codexOccurrenceMetrics{},
			ReadTargets:                  map[string]codexTargetMetrics{},
			InlineOrchestrationByTool:    map[string]codexInlineMetrics{},
			FailureReasons:               map[string]int{},
			FailureContexts:              map[string]map[string]codexOccurrenceMetrics{},
			ProgressStalls:               map[string]codexWaitMetrics{},
			ExpectedWaits:                map[string]codexWaitMetrics{},
			OversizedOutputs:             map[string]codexOversizedOutputMetrics{},
			Activity:                     map[string]time.Time{},
		}
		taskMap[record.Task] = task
	}
	task.Sessions++
	if record.Completed {
		task.CompletedSessions++
	} else {
		task.IncompleteSessions++
	}
	task.DurationSeconds += duration
	task.Compactions += record.Compactions
	if record.Compactions > 0 {
		task.SessionsWithCompactions++
	}
	addCodexTokenUsage(&task.Tokens, record.Tokens)
	task.FreshTokens += record.Tokens.UncachedInputTokens + record.Tokens.OutputTokens
	task.ToolCalls += record.ToolCalls
	task.FailedToolCalls += record.FailedToolCalls
	task.TruncatedToolCalls += record.TruncatedToolCalls
	task.ToolOutputBytes += record.ToolOutputBytes
	task.ToolOutputTokens += estimatedTokens(record.ToolOutputBytes)
	for family, metrics := range record.ShellCommandsByFamily {
		addCodexToolMetricsValue(task.ShellCommandsByFamily, family, metrics)
	}
	for shape, metrics := range record.MixedShellShapes {
		addCodexToolMetricsValue(task.MixedShellShapes, shape, metrics)
	}
	addCodexTransitionMetrics(task.CrossCallTransitions, record.CrossCallTransitions)
	for id, metrics := range record.OwnedTooling {
		addCodexToolMetricsValue(task.OwnedTooling, id, metrics)
	}
	addCodexOwnedOperationMetrics(task.OwnedOperations, record.OwnedOperations, record.OwnedOperationAmbiguous)
	addCodexFailureContexts(task.OwnedOperationFailureReasons, record.OwnedOperationFailureReasons)
	addCodexTargetMetrics(task.ReadTargets, record.ReadTargets)
	task.InlineOrchestrationCalls += record.InlineOrchestrationCalls
	task.InlineOrchestrationBytes += record.InlineOrchestrationBytes
	task.InlineOrchestrationMaxBytes = max(task.InlineOrchestrationMaxBytes, record.InlineOrchestrationMaxBytes)
	addCodexInlineMetrics(task.InlineOrchestrationByTool, record.InlineOrchestrationByTool)
	if record.InlineOrchestrationCalls > 0 {
		task.InlineOrchestrationSessions++
	}
	for reason, count := range record.FailureReasons {
		task.FailureReasons[reason] += count
	}
	addCodexFailureContexts(task.FailureContexts, record.FailureContexts)
	addCodexWaitMetrics(task.ProgressStalls, record.ProgressStalls)
	addCodexWaitMetrics(task.ExpectedWaits, record.ExpectedWaits)
	addCodexOversizedOutputMetrics(task.OversizedOutputs, record.OversizedOutputs)
	addDeliveryReworkMetrics(&task.DeliveryRework, record.DeliveryRework)
	addDownstreamQualityMetrics(&task.DownstreamQuality, record.DownstreamQuality)
	mergeSessionActivity(task.Activity, record.Activity)
}

func addCodexWaitMetrics(target, addition map[string]codexWaitMetrics) {
	for context, value := range addition {
		metrics := target[context]
		metrics.Calls += value.Calls
		metrics.Seconds += value.Seconds
		if value.Calls > 0 {
			metrics.Sessions++
		}
		target[context] = metrics
	}
}

func addCodexOversizedOutputMetrics(target, addition map[string]codexOversizedOutputMetrics) {
	for context, value := range addition {
		metrics := target[context]
		metrics.Calls += value.Calls
		metrics.OutputBytes += value.OutputBytes
		metrics.MaxOutputBytes = max(metrics.MaxOutputBytes, value.MaxOutputBytes)
		if value.Calls > 0 {
			metrics.Sessions++
		}
		target[context] = metrics
	}
}

func addCodexTransitionMetrics(target map[string]codexTransitionMetrics, additions map[string]int) {
	for transition, count := range additions {
		value := target[transition]
		value.Count += count
		value.Sessions++
		target[transition] = value
	}
}

func addCodexTargetMetrics(target, additions map[string]codexTargetMetrics) {
	for path, addition := range additions {
		metrics := target[path]
		metrics.Reads += addition.Reads
		metrics.SearchReadLoops += addition.SearchReadLoops
		metrics.Sessions++
		target[path] = metrics
	}
}

func addCodexFailureContexts(target map[string]map[string]codexOccurrenceMetrics, additions map[string]map[string]int) {
	for reason, contexts := range additions {
		for context, count := range contexts {
			if target[reason] == nil {
				target[reason] = map[string]codexOccurrenceMetrics{}
			}
			metrics := target[reason][context]
			metrics.Count += count
			metrics.Sessions++
			target[reason][context] = metrics
		}
	}
}

func addCodexToolMetricsValue(target map[string]codexToolMetrics, key string, addition codexToolMetrics) {
	value := target[key]
	value.Calls += addition.Calls
	value.FailedCalls += addition.FailedCalls
	value.TruncatedCalls += addition.TruncatedCalls
	value.OutputBytes += addition.OutputBytes
	value.EstimatedOutputTokens = estimatedTokens(value.OutputBytes)
	target[key] = value
}

func addCodexInlineMetrics(target, additions map[string]codexInlineMetrics) {
	for tool, addition := range additions {
		metrics := target[tool]
		metrics.Calls += addition.Calls
		if addition.Calls > 0 {
			metrics.Sessions++
		}
		metrics.Bytes += addition.Bytes
		metrics.MaxBytes = max(metrics.MaxBytes, addition.MaxBytes)
		target[tool] = metrics
	}
}

func addCodexOwnedOperationMetrics(target map[string]codexOwnedOperationMetrics, additions, ambiguous map[string]codexToolMetrics) {
	operations := map[string]struct{}{}
	for operation := range additions {
		operations[operation] = struct{}{}
	}
	for operation := range ambiguous {
		operations[operation] = struct{}{}
	}
	for operation := range operations {
		addition := additions[operation]
		ambiguousAddition := ambiguous[operation]
		metrics := target[operation]
		metrics.Calls += addition.Calls + ambiguousAddition.Calls
		metrics.AmbiguousCalls += ambiguousAddition.Calls
		if addition.Calls > 0 || ambiguousAddition.Calls > 0 {
			metrics.Sessions++
		}
		metrics.FailedCalls += addition.FailedCalls
		metrics.AmbiguousFailedCalls += ambiguousAddition.FailedCalls
		metrics.TruncatedCalls += addition.TruncatedCalls
		metrics.AmbiguousTruncatedCalls += ambiguousAddition.TruncatedCalls
		metrics.OutputBytes += addition.OutputBytes
		metrics.AmbiguousOutputBytes += ambiguousAddition.OutputBytes
		metrics.EstimatedOutputTokens = estimatedTokens(metrics.OutputBytes)
		metrics.EstimatedAmbiguousOutputTokens = estimatedTokens(metrics.AmbiguousOutputBytes)
		target[operation] = metrics
	}
}

func codexSessionDuration(record codexSessionRecord) int64 {
	if record.StartedAt.IsZero() || record.EndedAt.IsZero() || record.EndedAt.Before(record.StartedAt) {
		return 0
	}
	return int64(record.EndedAt.Sub(record.StartedAt).Seconds())
}

func addCodexTokenUsage(total *codexTokenUsage, usage codexTokenUsage) {
	total.InputTokens += usage.InputTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.UncachedInputTokens += usage.UncachedInputTokens
	total.OutputTokens += usage.OutputTokens
	total.ReasoningTokens += usage.ReasoningTokens
	total.TotalTokens += usage.TotalTokens
}

func estimatedTokens(bytes int64) int64 {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}

func printCodexSessionInsights(report codexSessionInsightsReport, config repositoryConfig, limit int, view string) {
	summary := report.Summary
	fmt.Printf("Muninn session insights since %s\n", report.Since)
	fmt.Printf("Provider: %s\n", report.Provider)
	fmt.Printf("Repository: %s\n", filepath.Base(report.WorkspaceRoot))
	if view == "focused" {
		fmt.Printf(
			"Scope: %s sessions, %s tool calls, %s failed\n",
			formatCodexCount(int64(summary.Sessions)),
			formatCodexCount(int64(summary.ToolCalls)),
			formatCodexCount(int64(summary.FailedToolCalls)),
		)
		printSessionFindings(report.Findings, limit)
		return
	}
	fmt.Printf("Sessions: %s (%s complete, %s incomplete)\n",
		formatCodexCount(int64(summary.Sessions)),
		formatCodexCount(int64(summary.CompletedSessions)),
		formatCodexCount(int64(summary.IncompleteSessions)),
	)
	fmt.Printf("Model tokens: %s input (%s cached, %s uncached), %s output, %s total\n",
		formatCodexCount(summary.Tokens.InputTokens),
		formatCodexCount(summary.Tokens.CachedInputTokens),
		formatCodexCount(summary.Tokens.UncachedInputTokens),
		formatCodexCount(summary.Tokens.OutputTokens),
		formatCodexCount(summary.Tokens.TotalTokens),
	)
	fmt.Printf("Fresh-token proxy: %s (uncached input + output)\n", formatCodexCount(summary.FreshTokens))
	printRepositoryInstructionFootprint(report.Instructions)
	fmt.Printf("Tool calls: %s (%s failed, %s truncated); visible output: ~%s tokens\n",
		formatCodexCount(int64(summary.ToolCalls)),
		formatCodexCount(int64(summary.FailedToolCalls)),
		formatCodexCount(int64(summary.TruncatedToolCalls)),
		formatCodexCount(summary.ToolOutputTokens),
	)
	printCompletionEpisodeAnalysis(report.Outcomes)
	printModelEffortAnalysis(report.Profiles)
	printDelegationAnalysis(report.Delegation)
	printDeliveryReworkAnalysis(summary.DeliveryRework)
	printDownstreamQualityAnalysis(summary.DownstreamQuality)
	if summary.FilesUnreadable > 0 {
		fmt.Printf("Files: %s scanned, %s unreadable\n", formatCodexCount(int64(summary.FilesScanned)), formatCodexCount(int64(summary.FilesUnreadable)))
	}
	if view == "findings" && len(report.Findings) > 0 {
		printSessionFindings(report.Findings, limit)
		return
	}
	if len(report.Tasks) == 0 {
		fmt.Println("\nNo matching sessions.")
		return
	}
	if view == "findings" {
		printSessionFindings(report.Findings, limit)
		return
	}
	if view == "overview" {
		printCodexToolMetrics("\nShell output by command family:", "FAMILY", summary.ShellCommandsByFamily, limit, 32)
		printOwnedTooling(summary.OwnedTooling, config.OwnedTools)
		fmt.Printf("\nCurrent findings: %s. Use --findings for actions or --details for full evidence.\n", formatCodexCount(int64(len(report.Findings))))
		return
	}

	rows := report.Tasks
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nTop tasks by fresh-token proxy:")
	fmt.Printf("%-40s %8s %13s %13s %13s %13s %10s %8s\n", "TASK", "SESSIONS", "INPUT", "UNCACHED", "FRESH", "FRESH/SESS", "TOOL OUT", "FAILED")
	for _, task := range rows {
		fmt.Printf("%-40s %8s %13s %13s %13s %13s %10s %8s\n",
			truncateCodexLabel(task.Task, 40),
			formatCodexCount(int64(task.Sessions)),
			formatCodexCount(task.Tokens.InputTokens),
			formatCodexCount(task.Tokens.UncachedInputTokens),
			formatCodexCount(task.FreshTokens),
			formatCodexCount(perSessionTokens(task.FreshTokens, task.Sessions)),
			"~"+formatCodexCount(task.ToolOutputTokens),
			formatCodexCount(int64(task.FailedToolCalls)),
		)
	}
	if len(rows) < len(report.Tasks) {
		fmt.Printf("... %d more tasks; use --limit 0 to show all.\n", len(report.Tasks)-len(rows))
	}

	printCodexToolMetrics("\nTool output by name:", "TOOL", summary.ToolMetricsByName, 8, 32)
	printCodexToolMetrics("\nShell output by command family:", "FAMILY", summary.ShellCommandsByFamily, 12, 32)
	printCodexToolMetrics("\nMixed-shell output by family sequence:", "SEQUENCE", summary.MixedShellShapes, 12, 56)
	printCodexTransitions(summary.CrossCallTransitions, 12)
	printCodexReadTargets(summary.ReadTargets, 12)
	printOwnedTooling(summary.OwnedTooling, config.OwnedTools)
	printOwnedOperations(summary.OwnedOperations, 16)
	printCodexWaitMetrics("\nCandidate progress stalls (long, low-output waits):", summary.ProgressStalls, 12)
	printCodexWaitMetrics("\nExpected long waits excluded from stall findings:", summary.ExpectedWaits, 12)
	printCodexOversizedOutputMetrics(summary.OversizedOutputs, 12)
	printCodexFailureReasons(summary.FailureReasons)
	printCodexFailureContexts(summary.FailureContexts, 12)

	fmt.Println("\nSignals:")
	if summary.InlineOrchestrationCalls > 0 {
		fmt.Printf("- %s large inline orchestration calls carried %s input bytes across %s sessions (largest call: %s bytes). Extract repeated scripts into a tested helper or compact agent-facing command.\n",
			formatCodexCount(int64(summary.InlineOrchestrationCalls)),
			formatCodexCount(summary.InlineOrchestrationBytes),
			formatCodexCount(int64(summary.InlineOrchestrationSessions)),
			formatCodexCount(summary.InlineOrchestrationMaxBytes),
		)
		printCodexInlineTools(summary.InlineOrchestrationByTool)
	}
	if summary.Compactions > 0 {
		cachedRatio := float64(0)
		if summary.Tokens.InputTokens > 0 {
			cachedRatio = 100 * float64(summary.Tokens.CachedInputTokens) / float64(summary.Tokens.InputTokens)
		}
		fmt.Printf("- %s context compactions occurred with %.0f%% cached input. Repeated compactions plus recurring transitions indicate a session loop or stale-context problem; cache hits alone do not.\n",
			formatCodexCount(int64(summary.Compactions)),
			cachedRatio,
		)
	}
	stallCalls, stallSeconds := codexWaitTotals(summary.ProgressStalls)
	expectedWaitCalls, expectedWaitSeconds := codexWaitTotals(summary.ExpectedWaits)
	oversizedCalls, oversizedBytes := codexOversizedOutputTotals(summary.OversizedOutputs)
	if stallCalls > 0 {
		fmt.Printf("- %s candidate low-output waits consumed %s. Remove redundant polling or add bounded progress for non-essential waits.\n",
			formatCodexCount(int64(stallCalls)),
			formatDurationSeconds(stallSeconds),
		)
	}
	if expectedWaitCalls > 0 {
		fmt.Printf("- %s long waits consuming %s were classified as expected tests, builds, local reviews, or remote GitHub review and excluded from stall findings.\n",
			formatCodexCount(int64(expectedWaitCalls)),
			formatDurationSeconds(expectedWaitSeconds),
		)
	}
	if oversizedCalls > 0 {
		fmt.Printf("- %s oversized tool outputs returned ~%s visible tokens. Lower default output or provide a bounded follow-up surface.\n",
			formatCodexCount(int64(oversizedCalls)),
			formatCodexCount(estimatedTokens(oversizedBytes)),
		)
	}
	if summary.TruncatedToolCalls > 0 {
		fmt.Printf("- %s tool calls returned truncated output. Narrow file/diff reads or lower command output before retrying.\n", formatCodexCount(int64(summary.TruncatedToolCalls)))
	} else {
		fmt.Println("- No truncated tool output was detected.")
	}
	searchReadMetrics := codexMixedSearchReadMetrics(summary.MixedShellShapes)
	if searchReadMetrics.Calls > 0 {
		fmt.Printf("- Bundled search/read calls returned ~%s visible tokens across %s calls. %s\n",
			formatCodexCount(searchReadMetrics.EstimatedOutputTokens),
			formatCodexCount(int64(searchReadMetrics.Calls)),
			config.Actions.SourceContext,
		)
	}
	multiSessionTasks := 0
	for _, task := range report.Tasks {
		if task.Sessions > 1 {
			multiSessionTasks++
		}
	}
	if multiSessionTasks > 0 {
		fmt.Printf("- %s tasks span multiple sessions; preserve focused findings and validation state in task progress to avoid rediscovery.\n", formatCodexCount(int64(multiSessionTasks)))
	}
	searchMisses := summary.FailureReasons["search no match"]
	if searchMisses > 0 {
		fmt.Printf("- %s non-zero calls were search misses. Prefer a bounded source-context command that returns no matches cleanly.\n", formatCodexCount(int64(searchMisses)))
	}
	remainingFailures := summary.FailedToolCalls - searchMisses
	if remainingFailures > 0 {
		fmt.Printf("- %s remaining tool calls failed or timed out; inspect the reason/context rows before changing shared tooling.\n", formatCodexCount(int64(remainingFailures)))
	}
	fmt.Println("- Token counts are rollout totals, not billing amounts. Fresh-token proxy excludes cached input but does not apply model prices.")
}

func printRepositoryInstructionFootprint(footprint repositoryInstructionFootprint) {
	if footprint.RootFiles == 0 && footprint.ScopedFiles == 0 {
		return
	}
	fmt.Printf(
		"Repository instructions: %s root files, %s bytes (~%s tokens/session baseline); %s scoped files, %s bytes\n",
		formatCodexCount(int64(footprint.RootFiles)),
		formatCodexCount(footprint.RootBytes),
		formatCodexCount(footprint.RootEstimatedTokens),
		formatCodexCount(int64(footprint.ScopedFiles)),
		formatCodexCount(footprint.ScopedBytes),
	)
}

func printCodexWaitMetrics(title string, metrics map[string]codexWaitMetrics, limit int) {
	type row struct {
		Context string
		Metrics codexWaitMetrics
	}
	rows := make([]row, 0, len(metrics))
	for context, value := range metrics {
		rows = append(rows, row{Context: context, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.Seconds != rows[j].Metrics.Seconds {
			return rows[i].Metrics.Seconds > rows[j].Metrics.Seconds
		}
		if rows[i].Metrics.Calls != rows[j].Metrics.Calls {
			return rows[i].Metrics.Calls > rows[j].Metrics.Calls
		}
		return rows[i].Context < rows[j].Context
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println(title)
	fmt.Printf("%-40s %8s %10s %10s\n", "CONTEXT", "CALLS", "SESSIONS", "WAIT")
	for _, row := range rows {
		fmt.Printf("%-40s %8s %10s %10s\n",
			truncateCodexLabel(row.Context, 40),
			formatCodexCount(int64(row.Metrics.Calls)),
			formatCodexCount(int64(row.Metrics.Sessions)),
			formatDurationSeconds(row.Metrics.Seconds),
		)
	}
}

func codexWaitTotals(metrics map[string]codexWaitMetrics) (calls int, seconds int64) {
	for _, value := range metrics {
		calls += value.Calls
		seconds += value.Seconds
	}
	return calls, seconds
}

func printCodexOversizedOutputMetrics(metrics map[string]codexOversizedOutputMetrics, limit int) {
	type row struct {
		Context string
		Metrics codexOversizedOutputMetrics
	}
	rows := make([]row, 0, len(metrics))
	for context, value := range metrics {
		rows = append(rows, row{Context: context, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.OutputBytes != rows[j].Metrics.OutputBytes {
			return rows[i].Metrics.OutputBytes > rows[j].Metrics.OutputBytes
		}
		if rows[i].Metrics.Calls != rows[j].Metrics.Calls {
			return rows[i].Metrics.Calls > rows[j].Metrics.Calls
		}
		return rows[i].Context < rows[j].Context
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nOversized tool outputs (at least 30,000 bytes in one call):")
	fmt.Printf("%-40s %8s %10s %13s %13s\n", "CONTEXT", "CALLS", "SESSIONS", "OUTPUT", "MAX CALL")
	for _, row := range rows {
		fmt.Printf("%-40s %8s %10s %13s %13s\n",
			truncateCodexLabel(row.Context, 40),
			formatCodexCount(int64(row.Metrics.Calls)),
			formatCodexCount(int64(row.Metrics.Sessions)),
			formatCodexCount(row.Metrics.OutputBytes),
			formatCodexCount(row.Metrics.MaxOutputBytes),
		)
	}
}

func codexOversizedOutputTotals(metrics map[string]codexOversizedOutputMetrics) (calls int, bytes int64) {
	for _, value := range metrics {
		calls += value.Calls
		bytes += value.OutputBytes
	}
	return calls, bytes
}

func printCodexInlineTools(metrics map[string]codexInlineMetrics) {
	type row struct {
		Tool    string
		Metrics codexInlineMetrics
	}
	rows := make([]row, 0, len(metrics))
	for tool, value := range metrics {
		rows = append(rows, row{Tool: tool, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.Bytes != rows[j].Metrics.Bytes {
			return rows[i].Metrics.Bytes > rows[j].Metrics.Bytes
		}
		return rows[i].Tool < rows[j].Tool
	})
	for _, row := range rows {
		fmt.Printf("  - %s: %s calls, %s bytes, largest %s\n",
			row.Tool,
			formatCodexCount(int64(row.Metrics.Calls)),
			formatCodexCount(row.Metrics.Bytes),
			formatCodexCount(row.Metrics.MaxBytes),
		)
	}
}

func codexMixedSearchReadMetrics(shapes map[string]codexToolMetrics) codexToolMetrics {
	var result codexToolMetrics
	for shape, metrics := range shapes {
		if !strings.Contains(shape, "search") || !strings.Contains(shape, "file reads") {
			continue
		}
		result.Calls += metrics.Calls
		result.FailedCalls += metrics.FailedCalls
		result.TruncatedCalls += metrics.TruncatedCalls
		result.OutputBytes += metrics.OutputBytes
	}
	result.EstimatedOutputTokens = estimatedTokens(result.OutputBytes)
	return result
}

func printCodexFailureReasons(reasons map[string]int) {
	type row struct {
		Name  string
		Count int
	}
	rows := make([]row, 0, len(reasons))
	for name, count := range reasons {
		rows = append(rows, row{Name: name, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Name < rows[j].Name
	})
	if len(rows) == 0 {
		return
	}
	fmt.Println("\nFailed tool calls by fixed reason:")
	for _, row := range rows {
		fmt.Printf("- %-30s %s\n", row.Name, formatCodexCount(int64(row.Count)))
	}
}

func printCodexTransitions(transitions map[string]codexTransitionMetrics, limit int) {
	type row struct {
		Name    string
		Metrics codexTransitionMetrics
	}
	rows := make([]row, 0, len(transitions))
	for name, metrics := range transitions {
		rows = append(rows, row{Name: name, Metrics: metrics})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.Count != rows[j].Metrics.Count {
			return rows[i].Metrics.Count > rows[j].Metrics.Count
		}
		if rows[i].Metrics.Sessions != rows[j].Metrics.Sessions {
			return rows[i].Metrics.Sessions > rows[j].Metrics.Sessions
		}
		return rows[i].Name < rows[j].Name
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nCross-call command-family transitions:")
	fmt.Printf("%-52s %10s %10s\n", "TRANSITION", "COUNT", "SESSIONS")
	for _, row := range rows {
		fmt.Printf("%-52s %10s %10s\n",
			truncateCodexLabel(row.Name, 52),
			formatCodexCount(int64(row.Metrics.Count)),
			formatCodexCount(int64(row.Metrics.Sessions)),
		)
	}
}

func printOwnedTooling(metrics map[string]codexToolMetrics, configs []ownedToolConfig) {
	if len(metrics) == 0 {
		return
	}
	configByID := map[string]ownedToolConfig{}
	for _, config := range configs {
		configByID[config.ID] = config
	}
	type row struct {
		ID      string
		Metrics codexToolMetrics
		Config  ownedToolConfig
	}
	rows := make([]row, 0, len(metrics))
	for id, value := range metrics {
		value.EstimatedOutputTokens = estimatedTokens(value.OutputBytes)
		rows = append(rows, row{ID: id, Metrics: value, Config: configByID[id]})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.FailedCalls != rows[j].Metrics.FailedCalls {
			return rows[i].Metrics.FailedCalls > rows[j].Metrics.FailedCalls
		}
		if rows[i].Metrics.OutputBytes != rows[j].Metrics.OutputBytes {
			return rows[i].Metrics.OutputBytes > rows[j].Metrics.OutputBytes
		}
		return rows[i].ID < rows[j].ID
	})
	fmt.Println("\nLocally controlled tooling:")
	fmt.Printf("%-24s %-24s %9s %10s %10s\n", "TOOL", "REPOSITORY", "CALLS", "OUTPUT", "FAILED")
	for _, row := range rows {
		repository := row.Config.Repository
		if repository == "" {
			repository = "(configured locally)"
		}
		fmt.Printf("%-24s %-24s %9s %10s %10s\n",
			truncateCodexLabel(row.ID, 24),
			truncateCodexLabel(repository, 24),
			formatCodexCount(int64(row.Metrics.Calls)),
			"~"+formatCodexCount(row.Metrics.EstimatedOutputTokens),
			formatCodexCount(int64(row.Metrics.FailedCalls)),
		)
		if recommendation := strings.TrimSpace(row.Config.Recommendation); recommendation != "" {
			fmt.Printf("  %s\n", recommendation)
		}
	}
}

func printOwnedOperations(metrics map[string]codexOwnedOperationMetrics, limit int) {
	type row struct {
		Operation string
		Metrics   codexOwnedOperationMetrics
	}
	rows := make([]row, 0, len(metrics))
	for operation, value := range metrics {
		rows = append(rows, row{Operation: operation, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.FailedCalls != rows[j].Metrics.FailedCalls {
			return rows[i].Metrics.FailedCalls > rows[j].Metrics.FailedCalls
		}
		if rows[i].Metrics.OutputBytes != rows[j].Metrics.OutputBytes {
			return rows[i].Metrics.OutputBytes > rows[j].Metrics.OutputBytes
		}
		if rows[i].Metrics.Calls != rows[j].Metrics.Calls {
			return rows[i].Metrics.Calls > rows[j].Metrics.Calls
		}
		return rows[i].Operation < rows[j].Operation
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nLocally controlled operations:")
	fmt.Printf(
		"%-32s %8s %8s %10s %10s %7s %7s %7s %7s\n",
		"OPERATION",
		"CALLS",
		"SESSIONS",
		"OUTPUT",
		"AMBIG OUT",
		"FAILED",
		"TRUNC",
		"AMB F",
		"AMB T",
	)
	for _, row := range rows {
		fmt.Printf("%-32s %8s %8s %10s %10s %7s %7s %7s %7s\n",
			truncateCodexLabel(row.Operation, 32),
			formatCodexCount(int64(row.Metrics.Calls)),
			formatCodexCount(int64(row.Metrics.Sessions)),
			"~"+formatCodexCount(row.Metrics.EstimatedOutputTokens),
			"~"+formatCodexCount(row.Metrics.EstimatedAmbiguousOutputTokens),
			formatCodexCount(int64(row.Metrics.FailedCalls)),
			formatCodexCount(int64(row.Metrics.TruncatedCalls)),
			formatCodexCount(int64(row.Metrics.AmbiguousFailedCalls)),
			formatCodexCount(int64(row.Metrics.AmbiguousTruncatedCalls)),
		)
	}
}

func printCodexReadTargets(targets map[string]codexTargetMetrics, limit int) {
	type row struct {
		Path    string
		Metrics codexTargetMetrics
	}
	rows := make([]row, 0, len(targets))
	for path, metrics := range targets {
		rows = append(rows, row{Path: path, Metrics: metrics})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.SearchReadLoops != rows[j].Metrics.SearchReadLoops {
			return rows[i].Metrics.SearchReadLoops > rows[j].Metrics.SearchReadLoops
		}
		if rows[i].Metrics.Reads != rows[j].Metrics.Reads {
			return rows[i].Metrics.Reads > rows[j].Metrics.Reads
		}
		return rows[i].Path < rows[j].Path
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nRepository-relative read targets:")
	fmt.Printf("%-72s %8s %10s %9s\n", "TARGET", "READS", "LOOPS", "SESSIONS")
	for _, row := range rows {
		fmt.Printf("%-72s %8s %10s %9s\n",
			truncateCodexLabel(row.Path, 72),
			formatCodexCount(int64(row.Metrics.Reads)),
			formatCodexCount(int64(row.Metrics.SearchReadLoops)),
			formatCodexCount(int64(row.Metrics.Sessions)),
		)
	}
}

func printCodexFailureContexts(contexts map[string]map[string]codexOccurrenceMetrics, limit int) {
	type row struct {
		Reason  string
		Context string
		Metrics codexOccurrenceMetrics
	}
	var rows []row
	for reason, values := range contexts {
		for context, metrics := range values {
			rows = append(rows, row{Reason: reason, Context: context, Metrics: metrics})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.Sessions != rows[j].Metrics.Sessions {
			return rows[i].Metrics.Sessions > rows[j].Metrics.Sessions
		}
		if rows[i].Metrics.Count != rows[j].Metrics.Count {
			return rows[i].Metrics.Count > rows[j].Metrics.Count
		}
		if rows[i].Reason != rows[j].Reason {
			return rows[i].Reason < rows[j].Reason
		}
		return rows[i].Context < rows[j].Context
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println("\nFailed calls by reason and privacy-safe context:")
	fmt.Printf("%-28s %-52s %8s %9s\n", "REASON", "CONTEXT", "CALLS", "SESSIONS")
	for _, row := range rows {
		fmt.Printf("%-28s %-52s %8s %9s\n",
			truncateCodexLabel(row.Reason, 28),
			truncateCodexLabel(row.Context, 52),
			formatCodexCount(int64(row.Metrics.Count)),
			formatCodexCount(int64(row.Metrics.Sessions)),
		)
	}
}

func printCodexToolMetrics(title, label string, metrics map[string]codexToolMetrics, limit, labelWidth int) {
	type row struct {
		Name    string
		Metrics codexToolMetrics
	}
	rows := make([]row, 0, len(metrics))
	for name, value := range metrics {
		rows = append(rows, row{Name: name, Metrics: value})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Metrics.OutputBytes != rows[j].Metrics.OutputBytes {
			return rows[i].Metrics.OutputBytes > rows[j].Metrics.OutputBytes
		}
		if rows[i].Metrics.Calls != rows[j].Metrics.Calls {
			return rows[i].Metrics.Calls > rows[j].Metrics.Calls
		}
		return rows[i].Name < rows[j].Name
	})
	if len(rows) == 0 {
		return
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	fmt.Println(title)
	fmt.Printf("%-*s %10s %14s %10s %9s\n", labelWidth, label, "CALLS", "OUTPUT", "TRUNC", "FAILED")
	for _, row := range rows {
		fmt.Printf("%-*s %10s %14s %10s %9s\n",
			labelWidth,
			truncateCodexLabel(row.Name, labelWidth),
			formatCodexCount(int64(row.Metrics.Calls)),
			"~"+formatCodexCount(row.Metrics.EstimatedOutputTokens),
			formatCodexCount(int64(row.Metrics.TruncatedCalls)),
			formatCodexCount(int64(row.Metrics.FailedCalls)),
		)
	}
}

func formatCodexCount(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	raw := strconv.FormatInt(value, 10)
	for i := len(raw) - 3; i > 0; i -= 3 {
		raw = raw[:i] + "," + raw[i:]
	}
	if negative {
		return "-" + raw
	}
	return raw
}

func truncateCodexLabel(value string, max int) string {
	if len(value) <= max {
		return value
	}
	if max <= 1 {
		return value[:max]
	}
	return value[:max-1] + "…"
}
