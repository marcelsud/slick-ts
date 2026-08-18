package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

type recordingAnalyzer struct {
	config string
	result Analysis
}

func (a *recordingAnalyzer) Analyze(_ context.Context, config string) Analysis {
	a.config = config
	return a.result
}

func TestRunFindsParentConfiguration(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "tsconfig.json"), `{}`)
	nested := filepath.Join(root, "src", "feature")
	writeTestFile(t, filepath.Join(nested, "main.ts"), "export {}")
	analyzer := &recordingAnalyzer{result: Analysis{Diagnostics: []Diagnostic{}}}
	var stdout, stderr bytes.Buffer

	code := Run(context.Background(), []string{"check", "--json", nested}, &stdout, &stderr, analyzer)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	if analyzer.config != filepath.Join(root, "tsconfig.json") {
		t.Fatalf("analyzed %q", analyzer.config)
	}
	var document Document
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if !document.Success || document.Version != 1 || document.Project != "tsconfig.json" {
		t.Fatalf("unexpected document: %+v", document)
	}
}

func TestRunReportsMissingConfiguration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"check", "--json", t.TempDir()}, &stdout, &stderr, &recordingAnalyzer{})
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	var document Document
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Error == nil || document.Error.Kind != "missing_configuration" {
		t.Fatalf("unexpected document: %+v", document)
	}
}
