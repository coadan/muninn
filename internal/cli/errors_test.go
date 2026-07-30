package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestWriteErrorEmitsCompactStructuredContract(t *testing.T) {
	var output bytes.Buffer
	if err := WriteError(&output, errors.New("flag provided but not defined: -human")); err != nil {
		t.Fatalf("write CLI error: %v", err)
	}
	var report cliErrorReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode CLI error %q: %v", output.String(), err)
	}
	if report.SchemaVersion != 1 ||
		report.Status != "error" ||
		report.Error.Code != "invalid-option" ||
		report.Error.Message != "flag provided but not defined: -human" ||
		report.Error.NextAction == "" {
		t.Fatalf("CLI error report=%#v", report)
	}
	if output.Len() > 512 {
		t.Fatalf("CLI error output is not compact: %d bytes", output.Len())
	}
}

func TestClassifyCLIErrorKeepsStableFallback(t *testing.T) {
	code, action := classifyCLIError(errors.New("database unavailable"))
	if code != "operation-failed" || action == "" {
		t.Fatalf("fallback CLI error=%q/%q", code, action)
	}
}
