package slick_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type coverageDocument struct {
	Version         int              `json:"version"`
	Command         string           `json:"command"`
	Success         bool             `json:"success"`
	Project         string           `json:"project"`
	CoverageSummary *coverageSummary `json:"coverageSummary"`
	Files           []coverageFile   `json:"files"`
	Functions       []coverageFn     `json:"functions"`
	Error           *failure         `json:"error"`
}

type coverageSummary struct {
	BranchPercent      float64 `json:"branchPercent"`
	ChangedCovered     int     `json:"changedCovered"`
	ChangedTotal       int     `json:"changedTotal"`
	ChangedLinePercent float64 `json:"changedLinePercent"`
}

type coverageFile struct {
	Path          string `json:"path"`
	State         string `json:"state"`
	BranchCovered int    `json:"branchCovered"`
	BranchTotal   int    `json:"branchTotal"`
}

type coverageFn struct {
	Symbol             string `json:"symbol"`
	Complexity         int    `json:"complexity"`
	UncoveredDecisions int    `json:"uncoveredDecisions"`
}

func TestCoverageEnforcesBranchAndUncoveredComplexityThresholds(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function branch(flag: boolean): number {
  if (flag) return 1;
  return 0;
}`,
	})
	coveragePath := writeCoverageQuality(t, root, "src/main.ts", []int{1, 0})
	output, stderr, code := runSlick(t, root, nil, "coverage", "--json", "--coverage", coveragePath,
		"--branch-threshold", "50", "--changed-line-threshold", "100", "--uncovered-complexity-threshold", "1")
	document := decodeCoverage(t, output)
	if code != 0 || stderr != "" || !document.Success || document.CoverageSummary == nil ||
		document.CoverageSummary.BranchPercent != 50 || len(document.Functions) != 1 ||
		document.Functions[0].Complexity != 2 || document.Functions[0].UncoveredDecisions != 1 {
		t.Fatalf("boundary exit %d, stderr %q, output %+v", code, stderr, document)
	}

	failedOutput, failedErr, failedCode := runSlick(t, root, nil, "coverage", "--json", "--coverage", coveragePath,
		"--branch-threshold", "51", "--uncovered-complexity-threshold", "0")
	failed := decodeCoverage(t, failedOutput)
	if failedCode != 1 || failedErr != "" || failed.Success {
		t.Fatalf("failure exit %d, stderr %q, output %+v", failedCode, failedErr, failed)
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "coverage", "--coverage", coveragePath,
		"--branch-threshold", "51", "--uncovered-complexity-threshold", "0")
	if humanCode != 1 || humanErr != "" || !strings.Contains(human, "branch coverage: 50.0%") || !strings.Contains(human, "fail uncovered decisions 1") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func TestCoverageScoresChangedExecutableLinesAndInvalidGitBase(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function value(): number {
  return 1;
}`,
	})
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "slick@example.test")
	runGit(t, root, "config", "user.name", "Slick Test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "base")
	base := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(root, "src", "main.ts"), `export function value(): number {
  const changed = 2;
  return changed;
}`)
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "change")
	coveragePath := writeCoverageQuality(t, root, "src/main.ts", []int{0, 1})

	output, stderr, code := runSlick(t, root, nil, "coverage", "--json", "--coverage", coveragePath,
		"--base", base, "--branch-threshold", "0", "--changed-line-threshold", "100")
	document := decodeCoverage(t, output)
	if code != 1 || stderr != "" || document.CoverageSummary == nil || document.CoverageSummary.ChangedTotal == 0 || document.CoverageSummary.ChangedLinePercent == 100 {
		t.Fatalf("changed lines exit %d, stderr %q, output %+v", code, stderr, document)
	}

	badOutput, badErr, badCode := runSlick(t, root, nil, "coverage", "--json", "--coverage", coveragePath, "--base", "missing-ref")
	bad := decodeCoverage(t, badOutput)
	if badCode != 1 || badErr != "" || bad.Error == nil || bad.Error.Kind != "git_failure" {
		t.Fatalf("invalid base exit %d, stderr %q, output %+v", badCode, badErr, bad)
	}
}

func TestCoverageIgnoresChangedTypeOnlyLines(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true,"module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/main.ts":  `export const value = 1;`,
		"src/types.ts": `export interface Value { value: number }`,
	})
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "slick@example.test")
	runGit(t, root, "config", "user.name", "Slick Test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "base")
	base := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(root, "src", "main.ts"), `import type { Value } from "./types.js";
export const value: Value = { value: 1 };`)
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "type-only")
	coveragePath := writeCoverageQuality(t, root, "src/main.ts", []int{1, 1})
	output, stderr, code := runSlick(t, root, nil, "coverage", "--json", "--coverage", coveragePath,
		"--base", base, "--branch-threshold", "0", "--changed-line-threshold", "100")
	document := decodeCoverage(t, output)
	if code != 0 || stderr != "" || !document.Success || document.CoverageSummary == nil || document.CoverageSummary.ChangedTotal != 1 || document.CoverageSummary.ChangedLinePercent != 100 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
}
func TestCoverageDistinguishesMissingFilesFromMeasuredZero(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function measured(flag: boolean): number {
  if (flag) return 1;
  return 0;
}`,
		"src/missing.ts": `export function missing(flag: boolean): number {
  if (flag) return 1;
  return 0;
}`,
	})
	coveragePath := writeCoverageQuality(t, root, "src/main.ts", []int{0, 0})
	output, stderr, code := runSlick(t, root, nil, "coverage", "--json", "--coverage", coveragePath,
		"--branch-threshold", "0", "--changed-line-threshold", "0", "--uncovered-complexity-threshold", "10")
	document := decodeCoverage(t, output)
	if code != 1 || stderr != "" {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	states := map[string]string{}
	for _, file := range document.Files {
		states[file.Path] = file.State
	}
	if states["src/main.ts"] != "measured" || states["src/missing.ts"] != "missing" || document.CoverageSummary == nil || document.CoverageSummary.BranchPercent >= 100 {
		t.Fatalf("coverage states: %+v summary=%+v", states, document.CoverageSummary)
	}
}

func writeCoverageQuality(t *testing.T, root, relative string, counts []int) string {

	t.Helper()
	source := filepath.Join(root, filepath.FromSlash(relative))
	statements := map[string]any{}
	statementCounts := map[string]int{}
	for index, count := range counts {
		line := index + 2
		identifier := string(rune('0' + index))
		statements[identifier] = map[string]any{
			"start": map[string]int{"line": line, "column": 2},
			"end":   map[string]int{"line": line, "column": 3},
		}
		statementCounts[identifier] = count
	}
	coverage := map[string]any{
		source: map[string]any{
			"path":         source,
			"statementMap": statements,
			"s":            statementCounts,
			"fnMap":        map[string]any{},
			"f":            map[string]int{},
			"branchMap": map[string]any{
				"0": map[string]any{
					"type": "if",
					"loc":  map[string]any{"start": map[string]int{"line": 2, "column": 2}, "end": map[string]int{"line": 3, "column": 10}},
					"locations": []any{
						map[string]any{"start": map[string]int{"line": 2, "column": 2}, "end": map[string]int{"line": 2, "column": 21}},
						map[string]any{"start": map[string]int{"line": 3, "column": 2}, "end": map[string]int{"line": 3, "column": 11}},
					},
				},
			},
			"b": map[string]any{"0": counts[:2]},
		},
	}
	content, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "coverage", "coverage-final.json")
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func decodeCoverage(t *testing.T, output string) coverageDocument {
	t.Helper()
	var document coverageDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "coverage" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
