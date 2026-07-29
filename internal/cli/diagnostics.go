package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
)

type normalizedDiagnosticFailure struct {
	Source           string    `json:"source"`
	Classification   string    `json:"classification"`
	FailureSource    string    `json:"failureSource"`
	FailureClass     string    `json:"failureClass"`
	Fingerprint      string    `json:"fingerprint"`
	FixturePhase     string    `json:"fixturePhase"`
	DiagnosticStatus string    `json:"diagnosticStatus"`
	ProbeCount       int       `json:"probeCount"`
	FailedAt         time.Time `json:"failedAt"`
	Lever            string    `json:"lever"`
}

type normalizedDiagnosticObservation struct {
	Status       string                       `json:"status"`
	Source       string                       `json:"source"`
	Target       string                       `json:"target,omitempty"`
	TargetLabels []string                     `json:"targetLabels,omitempty"`
	Finished     time.Time                    `json:"finishedAt"`
	Failure      *normalizedDiagnosticFailure `json:"failure,omitempty"`
}

type diagnosticFailureEpisode struct {
	normalizedDiagnosticFailure
	Target          string
	TargetLabels    []string
	Model           string
	ReasoningEffort string
	AgentKind       string
	EndedAt         time.Time
	Tokens          normalizedTokenUsage
	ToolCalls       int
	FailedCalls     int
	OutputBytes     int64
}

type diagnosticFailureAnalysis struct {
	Available bool                         `json:"available"`
	Failures  []diagnosticFailureAggregate `json:"failures,omitempty"`
	Passes    []diagnosticPassAggregate    `json:"passes,omitempty"`
}

type diagnosticFailureAggregate struct {
	Source            string                  `json:"source"`
	Classification    string                  `json:"classification"`
	FailureSource     string                  `json:"failureSource"`
	FailureClass      string                  `json:"failureClass"`
	Fingerprint       string                  `json:"fingerprint"`
	Target            string                  `json:"target,omitempty"`
	TargetLabels      []string                `json:"targetLabels,omitempty"`
	FixturePhase      string                  `json:"fixturePhase"`
	DiagnosticStatus  string                  `json:"diagnosticStatus"`
	Lever             string                  `json:"lever"`
	Occurrences       int                     `json:"occurrences"`
	Sessions          int                     `json:"sessions"`
	ProbeCount        int                     `json:"probeCount"`
	PostFailureTokens normalizedTokenUsage    `json:"postFailureTokens"`
	PostFailureCalls  int                     `json:"postFailureToolCalls"`
	PostFailureFailed int                     `json:"postFailureFailedCalls"`
	PostFailureOutput int64                   `json:"postFailureOutputBytes"`
	PostFailureSecs   int64                   `json:"postFailureDurationSeconds"`
	Profiles          []diagnosticCostProfile `json:"profiles,omitempty"`
	sessionKeys       map[int]struct{}
	profiles          map[string]*diagnosticCostProfile
	targetLabels      map[string]struct{}
}

type diagnosticPassAggregate struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Runs     int    `json:"runs"`
	Sessions int    `json:"sessions"`
	LastSeen string `json:"lastSeen"`
}

type diagnosticCostProfile struct {
	AgentKind       string               `json:"agentKind"`
	Model           string               `json:"model"`
	ReasoningEffort string               `json:"reasoningEffort"`
	Occurrences     int                  `json:"occurrences"`
	Tokens          normalizedTokenUsage `json:"tokens"`
	ToolCalls       int                  `json:"toolCalls"`
	DurationSeconds int64                `json:"durationSeconds"`
}

type diagnosticEffectivenessAnalysis struct {
	Available    bool                                 `json:"available"`
	Fingerprints []diagnosticEffectivenessFingerprint `json:"fingerprints,omitempty"`
}

type diagnosticEffectivenessFingerprint struct {
	Fingerprint         string  `json:"fingerprint"`
	Classification      string  `json:"classification"`
	Lever               string  `json:"lever"`
	State               string  `json:"state"`
	BaselineOccurrences int     `json:"baselineOccurrences"`
	CurrentOccurrences  int     `json:"currentOccurrences"`
	ExplicitPasses      int     `json:"explicitPasses"`
	BaselineFresh       float64 `json:"baselineFreshTokensPerOccurrence"`
	CurrentFresh        float64 `json:"currentFreshTokensPerOccurrence"`
	FreshDelta          float64 `json:"freshTokenDeltaPerOccurrence"`
	BaselineCalls       float64 `json:"baselineCallsPerOccurrence"`
	CurrentCalls        float64 `json:"currentCallsPerOccurrence"`
	CallDelta           float64 `json:"callDeltaPerOccurrence"`
	BaselineSeconds     float64 `json:"baselineSecondsPerOccurrence"`
	CurrentSeconds      float64 `json:"currentSecondsPerOccurrence"`
	SecondsDelta        float64 `json:"secondsDeltaPerOccurrence"`
}

type heimdalReport struct {
	Status         string `json:"status"`
	FinishedAt     string `json:"finished_at"`
	PrimaryFailure *struct {
		Class               string `json:"class"`
		Fingerprint         string `json:"fingerprint"`
		SemanticFingerprint string `json:"semantic_fingerprint"`
		Test                string `json:"test"`
	} `json:"primary_failure"`
	TraceDiagnosis *struct {
		FailureSource    string `json:"failure_source"`
		CaughtProbeCount int    `json:"caught_probe_count"`
	} `json:"trace_diagnosis"`
	Tests *struct {
		Executed  int `json:"executed"`
		Skipped   int `json:"skipped"`
		DidNotRun int `json:"did_not_run"`
	} `json:"tests"`
	Metadata   map[string]json.RawMessage `json:"metadata"`
	Invocation struct {
		TestFiles []string `json:"test_files"`
		Grep      string   `json:"grep"`
	} `json:"invocation"`
}

func isHeimdalReportInvocation(invocations []ownedCommandInvocation) bool {
	for _, invocation := range invocations {
		if strings.EqualFold(invocation.Executable, "heimdal") &&
			len(invocation.Args) > 0 &&
			invocation.Args[0] == "report" {
			return true
		}
	}
	return false
}

func parseHeimdalDiagnosticObservation(text string) *normalizedDiagnosticObservation {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return nil
	}
	var report heimdalReport
	if json.Unmarshal([]byte(text[start:]), &report) != nil {
		return nil
	}
	finishedAt, _ := time.Parse(time.RFC3339Nano, report.FinishedAt)
	target := diagnosticTarget("heimdal", report.Invocation.TestFiles, report.Invocation.Grep)
	targetLabels := diagnosticTestFiles(report.Invocation.TestFiles)
	if report.Status == "passed" {
		if target == "" {
			return nil
		}
		return &normalizedDiagnosticObservation{
			Status:       "passed",
			Source:       "heimdal",
			Target:       target,
			TargetLabels: targetLabels,
			Finished:     finishedAt,
		}
	}
	if report.Status != "failed" || report.PrimaryFailure == nil {
		return nil
	}
	rawFingerprint := report.PrimaryFailure.SemanticFingerprint
	if rawFingerprint == "" {
		rawFingerprint = report.PrimaryFailure.Fingerprint
	}
	if rawFingerprint == "" {
		return nil
	}
	failureSource := "runner"
	probeCount := 0
	if report.TraceDiagnosis != nil {
		failureSource = safeDiagnosticLabel(report.TraceDiagnosis.FailureSource, "runner")
		probeCount = max(0, report.TraceDiagnosis.CaughtProbeCount)
	}
	phase := "test-execution"
	classification := "product"
	lever := "source code"
	if report.Tests != nil && report.Tests.Executed == 0 &&
		(report.Tests.Skipped > 0 || report.Tests.DidNotRun > 0) {
		phase = "test-selection"
		classification = "coverage"
		lever = "tests/instructions"
	} else if report.Tests == nil || report.Tests.Executed == 0 {
		phase = "fixture-startup"
		classification = "infrastructure"
		lever = "tooling"
	}
	diagnosticStatus := "absent"
	if raw, ok := report.Metadata["void.diagnostics"]; ok {
		var metadata struct {
			Snapshot struct {
				Status string `json:"status"`
			} `json:"snapshot"`
		}
		if json.Unmarshal(raw, &metadata) == nil {
			diagnosticStatus = safeDiagnosticLabel(metadata.Snapshot.Status, "present")
		} else {
			diagnosticStatus = "present"
		}
	}
	return &normalizedDiagnosticObservation{
		Status:       "failed",
		Source:       "heimdal",
		Target:       target,
		TargetLabels: targetLabels,
		Finished:     finishedAt,
		Failure: &normalizedDiagnosticFailure{
			Source:           "heimdal",
			Classification:   classification,
			FailureSource:    failureSource,
			FailureClass:     safeDiagnosticLabel(report.PrimaryFailure.Class, "error"),
			Fingerprint:      diagnosticFingerprint("heimdal", rawFingerprint),
			FixturePhase:     phase,
			DiagnosticStatus: diagnosticStatus,
			ProbeCount:       probeCount,
			FailedAt:         finishedAt,
			Lever:            lever,
		},
	}
}

func parseHeimdalDiagnosticFailure(text string) *normalizedDiagnosticFailure {
	observation := parseHeimdalDiagnosticObservation(text)
	if observation == nil {
		return nil
	}
	return observation.Failure
}

func safeDiagnosticLabel(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 64 || !providerLabelPattern.MatchString(value) {
		return fallback
	}
	return value
}

func diagnosticFingerprint(source, raw string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + raw))
	return hex.EncodeToString(sum[:8])
}

func diagnosticTarget(source string, testFiles []string, grep string) string {
	parts := diagnosticTestFiles(testFiles)
	if grep = strings.TrimSpace(grep); grep != "" {
		parts = append(parts, grep)
	}
	if len(parts) == 0 {
		return ""
	}
	return diagnosticFingerprint(source+"-target", strings.Join(parts, "\x00"))
}

func diagnosticTestFiles(testFiles []string) []string {
	unique := map[string]struct{}{}
	for _, file := range testFiles {
		file = strings.TrimSpace(strings.ReplaceAll(file, "\\", "/"))
		if file == "" || len(file) > 240 || strings.HasPrefix(file, "/") ||
			(len(file) >= 2 && file[1] == ':') {
			continue
		}
		unsafe := false
		for _, char := range file {
			if unicode.IsControl(char) {
				unsafe = true
				break
			}
		}
		for _, segment := range strings.Split(file, "/") {
			if segment == ".." {
				unsafe = true
				break
			}
		}
		clean := path.Clean(file)
		if unsafe || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			continue
		}
		unique[clean] = struct{}{}
	}
	files := make([]string, 0, len(unique))
	for file := range unique {
		files = append(files, file)
	}
	sort.Strings(files)
	return files
}

func formatDiagnosticTargetLabels(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	sort.Strings(labels)
	const shown = 3
	if len(labels) <= shown {
		return strings.Join(labels, ", ")
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(labels[:shown], ", "), len(labels)-shown)
}

func analyzeDiagnosticFailures(records []codexSessionRecord) diagnosticFailureAnalysis {
	byFingerprint := map[string]*diagnosticFailureAggregate{}
	type passAccumulator struct {
		diagnosticPassAggregate
		sessionKeys map[int]struct{}
		lastSeen    time.Time
	}
	byPassTarget := map[string]*passAccumulator{}
	for sessionIndex, record := range records {
		for _, episode := range record.DiagnosticFailures {
			current := byFingerprint[episode.Fingerprint]
			if current == nil {
				current = &diagnosticFailureAggregate{
					Source:           episode.Source,
					Classification:   episode.Classification,
					FailureSource:    episode.FailureSource,
					FailureClass:     episode.FailureClass,
					Fingerprint:      episode.Fingerprint,
					Target:           episode.Target,
					FixturePhase:     episode.FixturePhase,
					DiagnosticStatus: episode.DiagnosticStatus,
					Lever:            episode.Lever,
					sessionKeys:      map[int]struct{}{},
					profiles:         map[string]*diagnosticCostProfile{},
					targetLabels:     map[string]struct{}{},
				}
				byFingerprint[episode.Fingerprint] = current
			}
			for _, label := range episode.TargetLabels {
				current.targetLabels[label] = struct{}{}
			}
			current.Occurrences++
			current.sessionKeys[sessionIndex] = struct{}{}
			current.ProbeCount += episode.ProbeCount
			addNormalizedTokenUsage(&current.PostFailureTokens, episode.Tokens)
			current.PostFailureCalls += episode.ToolCalls
			current.PostFailureFailed += episode.FailedCalls
			current.PostFailureOutput += episode.OutputBytes
			duration := max(int64(0), int64(episode.EndedAt.Sub(episode.FailedAt).Seconds()))
			current.PostFailureSecs += duration
			profileKey := strings.Join([]string{episode.AgentKind, episode.Model, episode.ReasoningEffort}, "\x00")
			profile := current.profiles[profileKey]
			if profile == nil {
				profile = &diagnosticCostProfile{
					AgentKind:       nonemptyProfileLabel(episode.AgentKind, "(unknown)"),
					Model:           nonemptyProfileLabel(episode.Model, "(unknown)"),
					ReasoningEffort: nonemptyProfileLabel(episode.ReasoningEffort, "(unknown)"),
				}
				current.profiles[profileKey] = profile
			}
			profile.Occurrences++
			addNormalizedTokenUsage(&profile.Tokens, episode.Tokens)
			profile.ToolCalls += episode.ToolCalls
			profile.DurationSeconds += duration
		}
		for _, pass := range record.DiagnosticPasses {
			key := pass.Source + "\x00" + pass.Target
			current := byPassTarget[key]
			if current == nil {
				current = &passAccumulator{
					diagnosticPassAggregate: diagnosticPassAggregate{
						Source: pass.Source,
						Target: pass.Target,
					},
					sessionKeys: map[int]struct{}{},
				}
				byPassTarget[key] = current
			}
			current.Runs++
			current.sessionKeys[sessionIndex] = struct{}{}
			if pass.Finished.After(current.lastSeen) {
				current.lastSeen = pass.Finished
			}
		}
	}
	analysis := diagnosticFailureAnalysis{Available: len(byFingerprint) > 0 || len(byPassTarget) > 0}
	for _, aggregate := range byFingerprint {
		aggregate.Sessions = len(aggregate.sessionKeys)
		labels := make([]string, 0, len(aggregate.targetLabels))
		for label := range aggregate.targetLabels {
			labels = append(labels, label)
		}
		sort.Strings(labels)
		aggregate.TargetLabels = labels
		for _, profile := range aggregate.profiles {
			aggregate.Profiles = append(aggregate.Profiles, *profile)
		}
		sort.Slice(aggregate.Profiles, func(i, j int) bool {
			return aggregate.Profiles[i].Tokens.TotalTokens > aggregate.Profiles[j].Tokens.TotalTokens
		})
		aggregate.sessionKeys = nil
		aggregate.profiles = nil
		aggregate.targetLabels = nil
		analysis.Failures = append(analysis.Failures, *aggregate)
	}
	for _, pass := range byPassTarget {
		pass.Sessions = len(pass.sessionKeys)
		if !pass.lastSeen.IsZero() {
			pass.LastSeen = pass.lastSeen.Format(time.RFC3339)
		}
		analysis.Passes = append(analysis.Passes, pass.diagnosticPassAggregate)
	}
	sort.Slice(analysis.Passes, func(i, j int) bool {
		return analysis.Passes[i].Target < analysis.Passes[j].Target
	})
	sort.Slice(analysis.Failures, func(i, j int) bool {
		leftFresh := diagnosticFreshTokens(analysis.Failures[i].PostFailureTokens)
		rightFresh := diagnosticFreshTokens(analysis.Failures[j].PostFailureTokens)
		if leftFresh != rightFresh {
			return leftFresh > rightFresh
		}
		return analysis.Failures[i].Fingerprint < analysis.Failures[j].Fingerprint
	})
	return analysis
}

func diagnosticFreshTokens(tokens normalizedTokenUsage) int64 {
	return tokens.UncachedInputTokens + tokens.OutputTokens
}

func compareDiagnosticEffectiveness(
	baseline,
	current diagnosticFailureAnalysis,
) diagnosticEffectivenessAnalysis {
	baselineByFingerprint := diagnosticFailuresByFingerprint(baseline.Failures)
	currentByFingerprint := diagnosticFailuresByFingerprint(current.Failures)
	currentPasses := map[string]int{}
	for _, pass := range current.Passes {
		currentPasses[pass.Source+"\x00"+pass.Target] += pass.Runs
	}
	fingerprints := map[string]struct{}{}
	for fingerprint := range baselineByFingerprint {
		fingerprints[fingerprint] = struct{}{}
	}
	for fingerprint := range currentByFingerprint {
		fingerprints[fingerprint] = struct{}{}
	}
	analysis := diagnosticEffectivenessAnalysis{Available: len(fingerprints) > 0}
	for fingerprint := range fingerprints {
		base, hadBase := baselineByFingerprint[fingerprint]
		now, hasCurrent := currentByFingerprint[fingerprint]
		row := diagnosticEffectivenessFingerprint{Fingerprint: fingerprint}
		if hadBase {
			row.Classification = base.Classification
			row.Lever = base.Lever
			row.BaselineOccurrences = base.Occurrences
			row.BaselineFresh = diagnosticPerOccurrence(
				diagnosticFreshTokens(base.PostFailureTokens),
				base.Occurrences,
			)
			row.BaselineCalls = diagnosticPerOccurrence(int64(base.PostFailureCalls), base.Occurrences)
			row.BaselineSeconds = diagnosticPerOccurrence(base.PostFailureSecs, base.Occurrences)
		}
		if hasCurrent {
			row.Classification = now.Classification
			row.Lever = now.Lever
			row.CurrentOccurrences = now.Occurrences
			row.CurrentFresh = diagnosticPerOccurrence(
				diagnosticFreshTokens(now.PostFailureTokens),
				now.Occurrences,
			)
			row.CurrentCalls = diagnosticPerOccurrence(int64(now.PostFailureCalls), now.Occurrences)
			row.CurrentSeconds = diagnosticPerOccurrence(now.PostFailureSecs, now.Occurrences)
		}
		row.FreshDelta = row.CurrentFresh - row.BaselineFresh
		row.CallDelta = row.CurrentCalls - row.BaselineCalls
		row.SecondsDelta = row.CurrentSeconds - row.BaselineSeconds
		switch {
		case !hadBase:
			row.State = "new"
		case !hasCurrent:
			row.ExplicitPasses = currentPasses[base.Source+"\x00"+base.Target]
			if base.Target != "" && row.ExplicitPasses > 0 {
				row.State = "resolved"
			} else {
				row.State = "not-observed"
			}
		default:
			row.State = diagnosticCostDirection(row)
		}
		analysis.Fingerprints = append(analysis.Fingerprints, row)
	}
	sort.Slice(analysis.Fingerprints, func(i, j int) bool {
		left := diagnosticUnresolvedCost(analysis.Fingerprints[i])
		right := diagnosticUnresolvedCost(analysis.Fingerprints[j])
		if left != right {
			return left > right
		}
		return analysis.Fingerprints[i].Fingerprint < analysis.Fingerprints[j].Fingerprint
	})
	return analysis
}

func diagnosticFailuresByFingerprint(
	failures []diagnosticFailureAggregate,
) map[string]diagnosticFailureAggregate {
	result := make(map[string]diagnosticFailureAggregate, len(failures))
	for _, failure := range failures {
		result[failure.Fingerprint] = failure
	}
	return result
}

func diagnosticPerOccurrence(total int64, occurrences int) float64 {
	if occurrences <= 0 {
		return 0
	}
	return float64(total) / float64(occurrences)
}

func diagnosticCostDirection(row diagnosticEffectivenessFingerprint) string {
	improved := 0
	regressed := 0
	for _, values := range [][2]float64{
		{row.BaselineFresh, row.CurrentFresh},
		{row.BaselineCalls, row.CurrentCalls},
		{row.BaselineSeconds, row.CurrentSeconds},
	} {
		if values[0] <= 0 {
			continue
		}
		change := (values[1] - values[0]) / values[0]
		switch {
		case change <= -0.1:
			improved++
		case change >= 0.1:
			regressed++
		}
	}
	switch {
	case regressed > 0:
		return "regressed"
	case improved > 0:
		return "improving"
	default:
		return "unchanged"
	}
}

func diagnosticUnresolvedCost(row diagnosticEffectivenessFingerprint) float64 {
	if row.State == "resolved" {
		return -1
	}
	if row.CurrentOccurrences > 0 {
		return row.CurrentFresh
	}
	return row.BaselineFresh
}
