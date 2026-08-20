package slick_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type complexityDocument struct {
	Version     int                `json:"version"`
	Command     string             `json:"command"`
	Success     bool               `json:"success"`
	Project     string             `json:"project"`
	Diagnostics []diagnostic       `json:"diagnostics"`
	Threshold   float64            `json:"threshold"`
	Functions   []complexityResult `json:"functions"`
	Error       *failure           `json:"error"`
}

type complexityResult struct {
	Symbol     string      `json:"symbol"`
	Path       string      `json:"path"`
	Range      sourceRange `json:"range"`
	Complexity int         `json:"complexity"`
}

func TestComplexityCountsEveryDecisionAndKeepsNestedScoresIndependent(t *testing.T) {
	root := project(t, `{
		"compilerOptions":{"strict":true,"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext"},
		"include":["src/**/*.ts"]
	}`, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/main.ts": `export function decisions(value: number, flag: boolean, items: number[]): number {
  if (flag) return value;
  for (let index = 0; index < value; index += 1) value += 1;
  for (const key in items) value += Number(key);
  for (const item of items) value += item;
  while (value < 0) value += 1;
  do {
    value += 1;
  } while (value < 0);
  try {
    value += 1;
  } catch {
    return value;
  }
  const chosen = flag ? value : 0;
  switch (value) {
    case 1:
      return chosen;
    default:
      break;
  }
  const both = flag && flag;
  const either = flag || flag;
  const fallback = flag ?? flag;
  let assigned: boolean | undefined = flag;
  assigned &&= flag;
  assigned ||= flag;
  assigned ??= flag;
  return Number(both) + Number(either) + Number(fallback) + Number(assigned) + chosen;
}
export function parent(value: number): number {
  const child = (flag: boolean): number => (flag ? 1 : 0);
  function nested(flag: boolean): number {
    return flag ? 1 : 0;
  }
  if (value > 0) {
    return child(true);
  }
  return nested(false);
}
`,
	})

	output, stderr, code := runSlick(t, root, nil, "complexity", "--json", "--threshold", "20")
	document := decodeComplexity(t, output)
	if code != 0 || stderr != "" || !document.Success {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	bySymbol := complexityBySymbol(document.Functions)
	if got := bySymbol["src/main.ts::decisions"].Complexity; got != 16 {
		t.Fatalf("decisions complexity %d, functions %+v", got, document.Functions)
	}
	if got := bySymbol["src/main.ts::parent"].Complexity; got != 2 {
		t.Fatalf("parent complexity %d", got)
	}
	if got := bySymbol["src/main.ts::parent.child"].Complexity; got != 2 {
		t.Fatalf("child complexity %d", got)
	}
	if got := bySymbol["src/main.ts::parent.nested"].Complexity; got != 2 {
		t.Fatalf("nested complexity %d", got)
	}
}

func TestComplexityIdentifiesMethodsConstructorsAccessorsArrowsAndCallbacks(t *testing.T) {
	root := project(t, `{
		"compilerOptions":{"strict":true,"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext"},
		"include":["src/**/*.ts"]
	}`, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/main.ts": `export class Widget {
  constructor(readonly value: number) {}
  method(flag: boolean): number {
    return flag ? this.value : 0;
  }
  get size(): number {
    return this.value > 0 ? this.value : 0;
  }
  set size(next: number) {
    if (next < 0) {
      throw new Error("negative");
    }
  }
}
export const bound = (flag: boolean): number => (flag ? 1 : 0);
export function mapped(values: number[]): number[] {
  return values.map(function (value: number): number {
    return value > 0 ? value : 0;
  });
}
export function filtered(values: number[]): number[] {
  return values.filter((value: number): boolean => value > 0 || value < -1);
}
`,
	})

	output, stderr, code := runSlick(t, root, nil, "complexity", "--json", "--threshold", "10")
	document := decodeComplexity(t, output)
	if code != 0 || stderr != "" || !document.Success {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	bySymbol := complexityBySymbol(document.Functions)
	expected := map[string]int{
		"src/main.ts::Widget.constructor": 1,
		"src/main.ts::Widget.method":      2,
		"src/main.ts::Widget.get:size":    2,
		"src/main.ts::Widget.set:size":    2,
		"src/main.ts::bound":              2,
		"src/main.ts::mapped":             1,
		"src/main.ts::filtered":           1,
	}
	for symbol, complexity := range expected {
		got, ok := bySymbol[symbol]
		if !ok || got.Complexity != complexity || got.Range.Start.Line < 1 || got.Range.Start.Offset < 0 {
			t.Fatalf("identity %s: %+v", symbol, got)
		}
	}
	var anonymous []complexityResult
	for _, result := range document.Functions {
		if strings.Contains(result.Symbol, "::anonymous@") {
			anonymous = append(anonymous, result)
		}
	}
	if len(anonymous) != 2 || anonymous[0].Complexity != 2 || anonymous[1].Complexity != 2 {
		t.Fatalf("anonymous callbacks: %+v", anonymous)
	}
	if !strings.HasPrefix(anonymous[0].Symbol, "src/main.ts::anonymous@") ||
		!strings.HasPrefix(anonymous[1].Symbol, "src/main.ts::anonymous@") ||
		anonymous[0].Symbol == anonymous[1].Symbol {
		t.Fatalf("anonymous identities were not stable: %+v", anonymous)
	}
}

func TestComplexityThresholdBoundaryAndHumanJSONMatch(t *testing.T) {
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

	equalOutput, equalErr, equalCode := runSlick(t, root, nil, "complexity", "--json", "--threshold", "4")
	equal := decodeComplexity(t, equalOutput)
	if equalCode != 0 || equalErr != "" || !equal.Success || equal.Threshold != 4 {
		t.Fatalf("equal threshold exit %d, stderr %q, output %+v", equalCode, equalErr, equal)
	}

	aboveOutput, aboveErr, aboveCode := runSlick(t, root, nil, "complexity", "--json", "--threshold", "3")
	above := decodeComplexity(t, aboveOutput)
	if aboveCode != 1 || aboveErr != "" || above.Success || len(above.Functions) != 2 {
		t.Fatalf("above threshold exit %d, stderr %q, output %+v", aboveCode, aboveErr, above)
	}
	risky := above.Functions[0]
	if risky.Symbol != "src/main.ts::risky" || risky.Complexity != 4 || risky.Path != "src/main.ts" {
		t.Fatalf("risky: %+v", risky)
	}
	if above.Functions[1].Symbol != "src/main.ts::simple" || above.Functions[1].Complexity != 1 {
		t.Fatalf("simple: %+v", above.Functions[1])
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "complexity", "--threshold", "3")
	if humanCode != 1 || humanErr != "" {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
	for _, result := range above.Functions {
		status := "ok"
		if result.Complexity > 3 {
			status = "fail"
		}
		want := fmt.Sprintf("%s:%d:%d - %s complexity %d: %s",
			result.Path, result.Range.Start.Line, result.Range.Start.Column, status, result.Complexity, result.Symbol)
		if !strings.Contains(human, want) {
			t.Fatalf("human output missing %q\n%s", want, human)
		}
	}
}

func TestComplexityJSONIsDeterministicAndMatchesCRAP(t *testing.T) {
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

	first, firstErr, firstCode := runSlick(t, root, nil, "complexity", "--json", "--threshold", "10")
	second, secondErr, secondCode := runSlick(t, root, nil, "complexity", "--json", "--threshold", "10")
	if firstCode != 0 || secondCode != 0 || firstErr != "" || secondErr != "" || first != second {
		t.Fatalf("codes %d/%d, stderr %q/%q, output:\n%s\n%s", firstCode, secondCode, firstErr, secondErr, first, second)
	}
	complexity := decodeComplexity(t, first)
	if complexity.Version != 1 || complexity.Command != "complexity" || complexity.Threshold != 10 {
		t.Fatalf("document: %+v", complexity)
	}

	crapOutput, crapErr, crapCode := runSlick(t, root, nil, "crap", "--json", "--coverage", coveragePath, "--threshold", "100")
	crap := decodeCRAP(t, crapOutput)
	if crapCode != 0 || crapErr != "" || len(crap.Functions) != len(complexity.Functions) {
		t.Fatalf("crap exit %d, stderr %q, output %+v", crapCode, crapErr, crap)
	}
	for index, result := range complexity.Functions {
		other := crap.Functions[index]
		if result.Symbol != other.Symbol || result.Path != other.Path || result.Complexity != other.Complexity ||
			result.Range != other.Range {
			t.Fatalf("shared metric diverged at %d: %+v vs %+v", index, result, other)
		}
	}
}

func TestComplexityReportsStructuredFailures(t *testing.T) {
	missing, missingErr, missingCode := runSlick(t, t.TempDir(), nil, "complexity", "--json")
	missingDocument := decodeComplexityFailure(t, missing)
	if missingCode != 1 || missingErr != "" || missingDocument.Error == nil || missingDocument.Error.Kind != "missing_configuration" {
		t.Fatalf("missing exit %d, stderr %q, output %+v", missingCode, missingErr, missingDocument)
	}

	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function simple(): number { return 1; }`,
	})
	_, usageErr, usageCode := runSlick(t, root, nil, "complexity", "--threshold", "-1")
	if usageCode != 2 || !strings.Contains(usageErr, "usage: slick complexity") {
		t.Fatalf("negative threshold exit %d, stderr %q", usageCode, usageErr)
	}
	_, extraErr, extraCode := runSlick(t, root, nil, "complexity", "src", "extra")
	if extraCode != 2 || !strings.Contains(extraErr, "usage: slick complexity") {
		t.Fatalf("extra args exit %d, stderr %q", extraCode, extraErr)
	}
}

func decodeComplexity(t *testing.T, output string) complexityDocument {
	t.Helper()
	var document complexityDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "complexity" || document.Project != "tsconfig.json" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}

func decodeComplexityFailure(t *testing.T, output string) complexityDocument {
	t.Helper()
	var document complexityDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "complexity" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}

func complexityBySymbol(results []complexityResult) map[string]complexityResult {
	bySymbol := make(map[string]complexityResult, len(results))
	for _, result := range results {
		bySymbol[result.Symbol] = result
	}
	return bySymbol
}
