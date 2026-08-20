package slick_test

import (
	"encoding/json"
	"strings"
	"testing"
)

type duplicationDocument struct {
	Version     int                `json:"version"`
	Command     string             `json:"command"`
	Success     bool               `json:"success"`
	Duplication *duplicationReport `json:"duplication"`
	Error       *failure           `json:"error"`
}

type duplicationReport struct {
	MinNodes       int          `json:"minNodes"`
	MinOccurrences int          `json:"minOccurrences"`
	Clones         []cloneGroup `json:"clones"`
}

type cloneGroup struct {
	Fingerprint string            `json:"fingerprint"`
	Nodes       int               `json:"nodes"`
	Occurrences []cloneOccurrence `json:"occurrences"`
}

type cloneOccurrence struct {
	Path  string      `json:"path"`
	Range sourceRange `json:"range"`
}

func TestDuplicationFindsAlphaRenamedBlocksAndIgnoresChangedBehavior(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function first(input: number): number {
  const doubled = input * 2;
  if (doubled > 10) return doubled;
  return doubled + 1;
}
export function second(value: number): number {
  const result = value * 2;
  if (result > 10) return result;
  return result + 1;
}
export function different(value: number): number {
  const result = value * 3;
  if (result > 10) return result;
  return result + 1;
}`,
	})
	output, stderr, code := runSlick(t, root, nil, "duplication", "--json", "--min-nodes", "10")
	document := decodeDuplication(t, output)
	if code != 1 || stderr != "" || document.Duplication == nil || len(document.Duplication.Clones) != 1 || len(document.Duplication.Clones[0].Occurrences) != 2 || document.Duplication.Clones[0].Nodes < 10 {
		t.Fatalf("exit %d, stderr %q, output %s", code, stderr, output)
	}
	if document.Duplication.Clones[0].Occurrences[0].Range.Start.Offset >= document.Duplication.Clones[0].Occurrences[1].Range.Start.Offset {
		t.Fatal("clone occurrences are not deterministic")
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "duplication", "--min-nodes", "10")
	if humanCode != 1 || humanErr != "" || !strings.Contains(human, "clone ") || !strings.Contains(human, "2 occurrences") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func TestDuplicationThresholdsAndExcludedTests(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export function first(value: number): number { const next = value + 1; return next * 2; }
export function second(input: number): number { const result = input + 1; return result * 2; }`,
		"src/main.test.ts": `export function third(input: number): number { const result = input + 1; return result * 2; }`,
	})
	output, stderr, code := runSlick(t, root, nil, "duplication", "--json", "--min-nodes", "5", "--min-occurrences", "3")
	document := decodeDuplication(t, output)
	if code != 0 || stderr != "" || !document.Success || document.Duplication == nil || len(document.Duplication.Clones) != 0 {
		t.Fatalf("exit %d, stderr %q, output %s", code, stderr, output)
	}

	bad, badErr, badCode := runSlick(t, root, nil, "duplication", "--json", "--min-nodes", "0")
	if badCode != 2 || !strings.Contains(badErr, "usage:") || bad != "" {
		t.Fatalf("bad threshold exit %d, stderr %q, output %q", badCode, badErr, bad)
	}
}

func decodeDuplication(t *testing.T, output string) duplicationDocument {
	t.Helper()
	var document duplicationDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "duplication" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
