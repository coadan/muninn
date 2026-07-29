package cli

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

var codexNestedContinuationPattern = regexp.MustCompile(`(?s)tools\.(?:write_stdin|wait)\s*\(\s*\{[^}]*?"?(session_id|cell_id)"?\s*:\s*(?:"([^"]+)"|'([^']+)'|([0-9]+))`)
var codexExplicitSessionMarkerPattern = regexp.MustCompile(`^SESSION_ID=([0-9]+)$`)
var codexContinuationStatusPattern = regexp.MustCompile(`(?im)^(?:script|process) running with (session|cell) id\s+([^\s]+)\s*$`)

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
