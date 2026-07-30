package cli

import (
	"encoding/json"
	"io"
	"strings"
)

const cliErrorSchemaVersion = 1

type cliErrorReport struct {
	SchemaVersion int            `json:"schemaVersion"`
	Status        string         `json:"status"`
	Error         cliErrorDetail `json:"error"`
}

type cliErrorDetail struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	NextAction string `json:"nextAction"`
}

func WriteError(output io.Writer, err error) error {
	code, action := classifyCLIError(err)
	return json.NewEncoder(output).Encode(cliErrorReport{
		SchemaVersion: cliErrorSchemaVersion,
		Status:        "error",
		Error: cliErrorDetail{
			Code:       code,
			Message:    strings.TrimSpace(err.Error()),
			NextAction: action,
		},
	})
}

func classifyCLIError(err error) (string, string) {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "flag provided but not defined"):
		return "invalid-option", "Run the selected command with --help and remove unsupported flags."
	case strings.HasPrefix(message, "unknown muninn command:"):
		return "unknown-command", "Run muninn --help and select a supported command."
	case strings.HasPrefix(message, "usage:"),
		strings.Contains(message, "mutually exclusive"),
		strings.Contains(message, "cannot be combined"),
		strings.Contains(message, " requires "),
		strings.Contains(message, " must "),
		strings.HasPrefix(message, "invalid --"),
		strings.HasPrefix(message, "unsupported --"):
		return "invalid-arguments", "Run the selected command with --help and correct the argument combination."
	default:
		return "operation-failed", "Use the fixed error code and message to repair the owning operation, then retry once."
	}
}
