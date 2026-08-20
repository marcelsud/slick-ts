package slick_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mutationDocument struct {
	Version  int             `json:"version"`
	Command  string          `json:"command"`
	Success  bool            `json:"success"`
	Mutation *mutationReport `json:"mutation"`
	Error    *failure        `json:"error"`
}

type mutationReport struct {
	Total      int              `json:"total"`
	Killed     int              `json:"killed"`
	Survived   int              `json:"survived"`
	TimedOut   int              `json:"timedOut"`
	Invalid    int              `json:"invalid"`
	NotCovered int              `json:"notCovered"`
	Score      float64          `json:"score"`
	Results    []mutationResult `json:"results"`
}

type mutationResult struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Operator string `json:"operator"`
	Status   string `json:"status"`
}

func TestMutationClassifiesKilledAndSurvivedMutantsWithoutChangingSource(t *testing.T) {
	root := mutationProject(t, `const fs = require("node:fs");
const source = fs.readFileSync("src/main.ts", "utf8");
process.exit(source.includes("value - 1") ? 1 : 0);`)
	before, _ := os.ReadFile(filepath.Join(root, "src", "main.ts"))
	output, stderr, code := runSlick(t, root, nil, "mutate", "--json", "--max-mutants", "2", "--", "node", "test.cjs")
	document := decodeMutation(t, output)
	if code != 1 || stderr != "" || document.Mutation == nil || document.Mutation.Total != 2 || document.Mutation.Killed != 1 || document.Mutation.Survived != 1 || document.Mutation.Score != 50 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	after, _ := os.ReadFile(filepath.Join(root, "src", "main.ts"))
	if string(before) != string(after) {
		t.Fatal("mutation changed the working tree")
	}
	if document.Mutation.Results[0].ID > document.Mutation.Results[1].ID {
		t.Fatal("mutant IDs are not deterministic")
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "mutate", "--max-mutants", "2", "--", "node", "test.cjs")
	if humanCode != 1 || humanErr != "" || !strings.Contains(human, "mutation score") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func TestMutationTimeoutAndOriginalTestFailureAreStructured(t *testing.T) {
	timeoutRoot := mutationProject(t, `const fs = require("node:fs");
const source = fs.readFileSync("src/main.ts", "utf8");
if (source.includes("value - 1")) setTimeout(() => {}, 1000);`)
	output, stderr, code := runSlick(t, timeoutRoot, nil, "mutate", "--json", "--timeout", "100ms", "--max-mutants", "1", "--", "node", "test.cjs")
	document := decodeMutation(t, output)
	if code != 1 || stderr != "" || document.Mutation == nil || document.Mutation.TimedOut != 1 {
		t.Fatalf("timeout exit %d, stderr %q, output %+v", code, stderr, document)
	}

	failedRoot := mutationProject(t, `process.exit(1);`)
	failedOutput, failedErr, failedCode := runSlick(t, failedRoot, nil, "mutate", "--json", "--", "node", "test.cjs")
	failed := decodeMutation(t, failedOutput)
	if failedCode != 1 || failedErr != "" || failed.Error == nil || failed.Error.Kind != "test_command_failure" {
		t.Fatalf("original failure exit %d, stderr %q, output %+v", failedCode, failedErr, failed)
	}
}

func TestMutationMarksUncoveredMutantsWithoutRunningThem(t *testing.T) {
	root := mutationProject(t, `process.exit(0);`)
	coverage := writeCRAPCoverage(t, root, map[string]int{"0": 0, "1": 0, "2": 0, "3": 0, "4": 0})
	output, stderr, code := runSlick(t, root, nil, "mutate", "--json", "--coverage", coverage, "--max-mutants", "1", "--", "node", "test.cjs")
	document := decodeMutation(t, output)
	if code != 0 || stderr != "" || !document.Success || document.Mutation == nil || document.Mutation.NotCovered != 1 || document.Mutation.Killed != 0 || document.Mutation.Survived != 0 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
}

func mutationProject(t *testing.T, testScript string) string {
	t.Helper()
	return project(t, `{"compilerOptions":{"strict":true,"target":"ES2022"},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function increment(value: number): number {
  return value + 1;
}`,
		"test.cjs": testScript,
	})
}

func decodeMutation(t *testing.T, output string) mutationDocument {
	t.Helper()
	var document mutationDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "mutate" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
