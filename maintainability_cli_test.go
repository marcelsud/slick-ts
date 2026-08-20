package slick_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

type maintainabilityDocument struct {
	Version   int                     `json:"version"`
	Command   string                  `json:"command"`
	Success   bool                    `json:"success"`
	Threshold float64                 `json:"threshold"`
	Functions []maintainabilityResult `json:"functions"`
	Error     *failure                `json:"error"`
}

type maintainabilityResult struct {
	Symbol            string  `json:"symbol"`
	Complexity        int     `json:"complexity"`
	DistinctOperators int     `json:"distinctOperators"`
	DistinctOperands  int     `json:"distinctOperands"`
	OperatorCount     int     `json:"operatorCount"`
	OperandCount      int     `json:"operandCount"`
	Vocabulary        int     `json:"vocabulary"`
	Length            int     `json:"length"`
	Volume            float64 `json:"volume"`
	LOC               int     `json:"loc"`
	Index             float64 `json:"index"`
}

func TestMaintainabilityReportsTransparentInputsAndThresholdBoundary(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function calculate(value: number): number {
  let result = value + 1;
  if (result > 10) result *= 2;
  if (result > 20) result -= 3;
  return result;
}`,
	})
	output, stderr, code := runSlick(t, root, nil, "maintainability", "--json")
	document := decodeMaintainability(t, output)
	if code != 0 || stderr != "" || !document.Success || len(document.Functions) != 1 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	result := document.Functions[0]
	if result.Complexity != 3 || result.Volume <= 0 || result.Vocabulary == 0 || result.Length == 0 || result.LOC == 0 || result.Index < 0 || result.Index > 100 {
		t.Fatalf("metrics: %+v", result)
	}
	equal := strconv.FormatFloat(result.Index, 'g', -1, 64)
	boundaryOutput, boundaryErr, boundaryCode := runSlick(t, root, nil, "maintainability", "--json", "--threshold", equal)
	if boundaryCode != 0 || boundaryErr != "" || !decodeMaintainability(t, boundaryOutput).Success {
		t.Fatalf("boundary exit %d, stderr %q, output %s", boundaryCode, boundaryErr, boundaryOutput)
	}
	failedOutput, failedErr, failedCode := runSlick(t, root, nil, "maintainability", "--json", "--threshold", strconv.FormatFloat(result.Index+0.01, 'f', 4, 64))
	if failedCode != 1 || failedErr != "" || decodeMaintainability(t, failedOutput).Success {
		t.Fatalf("failed threshold exit %d, stderr %q, output %s", failedCode, failedErr, failedOutput)
	}
}

func TestMaintainabilityIgnoresFormattingAndCommentsAndHandlesEmptyFunctions(t *testing.T) {
	compact := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function same(value: number): number { const next = value + 1; return next; }
export function empty(): void {}`,
	})
	formatted := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function same(value: number): number {
  // formatting and comments must not change the metric
  const next = value + 1;

  return next;
}
export function empty(): void {
}`,
	})
	compactDoc := decodeMaintainability(t, runMaintainability(t, compact))
	formattedDoc := decodeMaintainability(t, runMaintainability(t, formatted))
	if len(compactDoc.Functions) != 2 || len(formattedDoc.Functions) != 2 {
		t.Fatalf("function counts: %+v %+v", compactDoc, formattedDoc)
	}
	first := compactDoc.Functions[0]
	second := formattedDoc.Functions[0]
	if first.Volume != second.Volume || first.LOC != second.LOC || first.Index != second.Index {
		t.Fatalf("format changed metric: %+v %+v", first, second)
	}
	for _, result := range compactDoc.Functions {
		if result.Index < 0 || result.Index > 100 {
			t.Fatalf("invalid index: %+v", result)
		}
	}

	human, humanErr, humanCode := runSlick(t, compact, nil, "maintainability")
	if humanCode != 0 || humanErr != "" || !strings.Contains(human, "volume") || !strings.Contains(human, "LOC") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func runMaintainability(t *testing.T, root string) string {
	t.Helper()
	output, stderr, code := runSlick(t, root, nil, "maintainability", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q, output %s", code, stderr, output)
	}
	return output
}

func decodeMaintainability(t *testing.T, output string) maintainabilityDocument {
	t.Helper()
	var document maintainabilityDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "maintainability" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
