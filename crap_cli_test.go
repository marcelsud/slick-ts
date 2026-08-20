package slick_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type crapDocument struct {
	Version     int          `json:"version"`
	Command     string       `json:"command"`
	Success     bool         `json:"success"`
	Project     string       `json:"project"`
	Diagnostics []diagnostic `json:"diagnostics"`
	Threshold   float64      `json:"threshold"`
	Coverage    string       `json:"coverage"`
	Functions   []crapResult `json:"functions"`
	Error       *failure     `json:"error"`
}

type crapResult struct {
	Symbol     string      `json:"symbol"`
	Path       string      `json:"path"`
	Range      sourceRange `json:"range"`
	Complexity int         `json:"complexity"`
	Coverage   float64     `json:"coverage"`
	Score      float64     `json:"score"`
}

func TestCRAPChecksComplexityCoverageAndThreshold(t *testing.T) {
	root := project(t, `{
		"compilerOptions":{"strict":true,"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext"},
		"include":["src/**/*.ts"]
	}`, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/main.ts": `export function risky(value: number): number {
  if (value > 10) return 1;
  if (value > 5) return 2;
  for (let index = 0; index < value; index++) value--;
  return value;
}
export function simple(): number { return 1; }
`,
	})
	coveragePath := writeCRAPCoverage(t, root, map[string]int{"0": 0, "1": 0, "2": 0, "3": 0, "4": 1})

	first, firstErr, firstCode := runSlick(t, root, nil, "crap", "--json", "--coverage", coveragePath, "--threshold", "15")
	second, secondErr, secondCode := runSlick(t, root, nil, "crap", "--json", "--coverage", coveragePath, "--threshold", "15")
	if firstCode != 1 || secondCode != 1 || firstErr != "" || secondErr != "" || first != second {
		t.Fatalf("codes %d/%d, stderr %q/%q, output:\n%s\n%s", firstCode, secondCode, firstErr, secondErr, first, second)
	}
	document := decodeCRAP(t, first)
	if document.Success || document.Threshold != 15 || len(document.Functions) != 2 {
		t.Fatalf("unexpected document: %+v", document)
	}
	risky := document.Functions[0]
	if risky.Symbol != "src/main.ts::risky" || risky.Complexity != 4 || risky.Coverage != 0 || risky.Score != 20 {
		t.Fatalf("risky score: %+v", risky)
	}
	simple := document.Functions[1]
	if simple.Symbol != "src/main.ts::simple" || simple.Complexity != 1 || simple.Coverage != 1 || simple.Score != 1 {
		t.Fatalf("simple score: %+v", simple)
	}

	highOutput, highErr, highCode := runSlick(t, root, nil, "crap", "--json", "--coverage", coveragePath, "--threshold", "20")
	if highCode != 0 || highErr != "" || !decodeCRAP(t, highOutput).Success {
		t.Fatalf("high threshold exit %d, stderr %q, output %s", highCode, highErr, highOutput)
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "crap", "--coverage", coveragePath, "--threshold", "15")
	if humanCode != 1 || humanErr != "" || !strings.Contains(human, "fail CRAP 20.00") || !strings.Contains(human, "complexity 4") || !strings.Contains(human, "coverage 0.0%") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func TestCRAPCoverageLowersRiskAndMissingCoverageIsStructured(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function branch(value: boolean): number {
  if (value) return 1;
  return 0;
}`,
	})
	coveragePath := writeCRAPCoverage(t, root, map[string]int{"0": 1, "1": 1, "2": 1, "3": 1, "4": 1})
	output, stderr, code := runSlick(t, root, nil, "crap", "--json", "--coverage", coveragePath, "--threshold", "2")
	document := decodeCRAP(t, output)
	if code != 0 || stderr != "" || !document.Success || len(document.Functions) != 1 || document.Functions[0].Score != 2 || document.Functions[0].Coverage != 1 {
		t.Fatalf("covered exit %d, stderr %q, output %+v", code, stderr, document)
	}

	missing, missingErr, missingCode := runSlick(t, root, nil, "crap", "--json", "--coverage", "missing.json")
	missingDocument := decodeCRAP(t, missing)
	if missingCode != 1 || missingErr != "" || missingDocument.Error == nil || missingDocument.Error.Kind != "coverage_failure" {
		t.Fatalf("missing exit %d, stderr %q, output %+v", missingCode, missingErr, missingDocument)
	}
	source := filepath.Join(root, "src", "main.ts")
	malformedCoverage, _ := json.Marshal(map[string]any{
		source: map[string]any{
			"path":         source,
			"statementMap": map[string]any{"0": nil},
			"s":            map[string]int{"0": 0},
			"fnMap":        map[string]any{},
			"f":            map[string]int{},
		},
	})
	malformedPath := filepath.Join(root, "coverage", "malformed.json")
	if err := os.WriteFile(malformedPath, malformedCoverage, 0o644); err != nil {
		t.Fatal(err)
	}
	malformed, malformedErr, malformedCode := runSlick(t, root, nil, "crap", "--json", "--coverage", malformedPath)
	malformedDocument := decodeCRAP(t, malformed)
	if malformedCode != 1 || malformedErr != "" || malformedDocument.Error == nil || malformedDocument.Error.Kind != "coverage_failure" {
		t.Fatalf("malformed exit %d, stderr %q, output %+v", malformedCode, malformedErr, malformedDocument)
	}

}

func TestCRAPCountsLogicalAssignments(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function assignments(first: boolean | undefined, second: boolean): boolean {
  first &&= second;
  first ||= second;
  first ??= second;
  first &&= second;
  first ||= second;
  return Boolean(first);
}`,
	})
	coveragePath := writeCRAPCoverage(t, root, map[string]int{"0": 0, "1": 0, "2": 0, "3": 0, "4": 0})
	output, _, code := runSlick(t, root, nil, "crap", "--json", "--coverage", coveragePath)
	document := decodeCRAP(t, output)
	if code != 1 || len(document.Functions) != 1 || document.Functions[0].Complexity != 6 || document.Functions[0].Score != 42 {
		t.Fatalf("logical assignments exit %d, output %+v", code, document)
	}
}

func TestCRAPOrderingUsesCodeUnits(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/a-b.ts": `export function dashed(): number { return 1; }`,
		"src/a_b.ts": `export function underscored(): number { return 1; }`,
	})
	coverage := map[string]any{}
	for _, name := range []string{"a-b.ts", "a_b.ts"} {
		source := filepath.Join(root, "src", name)
		coverage[source] = map[string]any{
			"path":         source,
			"statementMap": map[string]any{},
			"s":            map[string]int{},
			"fnMap":        map[string]any{},
			"f":            map[string]int{},
		}
	}
	content, _ := json.Marshal(coverage)
	coveragePath := filepath.Join(root, "coverage-final.json")
	if err := os.WriteFile(coveragePath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	output, _, code := runSlick(t, root, nil, "crap", "--json", "--coverage", coveragePath, "--threshold", "100")
	document := decodeCRAP(t, output)
	if code != 0 || len(document.Functions) != 2 ||
		document.Functions[0].Path != "src/a-b.ts" || document.Functions[1].Path != "src/a_b.ts" {
		t.Fatalf("ordering exit %d, output %+v", code, document)
	}
}

func writeCRAPCoverage(t *testing.T, root string, counts map[string]int) string {
	t.Helper()
	source := filepath.Join(root, "src", "main.ts")
	coverage := map[string]any{
		source: map[string]any{
			"path": source,
			"statementMap": map[string]any{
				"0": location(2, 2),
				"1": location(3, 2),
				"2": location(4, 2),
				"3": location(5, 2),
				"4": location(7, 36),
			},
			"s":         counts,
			"fnMap":     map[string]any{},
			"f":         map[string]int{},
			"branchMap": map[string]any{},
			"b":         map[string]any{},
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

func location(line, column int) map[string]any {
	return map[string]any{
		"start": map[string]int{"line": line, "column": column},
		"end":   map[string]int{"line": line, "column": column + 1},
	}
}

func decodeCRAP(t *testing.T, output string) crapDocument {
	t.Helper()
	var document crapDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "crap" || document.Project != "tsconfig.json" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
