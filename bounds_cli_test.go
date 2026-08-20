package slick_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type boundsDocument struct {
	Version int           `json:"version"`
	Command string        `json:"command"`
	Success bool          `json:"success"`
	Bounds  *boundsReport `json:"bounds"`
	Error   *failure      `json:"error"`
}

type boundsReport struct {
	Results    []boundResult    `json:"results"`
	Violations []boundViolation `json:"violations"`
}

type boundResult struct {
	Symbol  string         `json:"symbol"`
	Bounds  map[string]int `json:"bounds"`
	Limits  map[string]int `json:"limits"`
	Unknown []boundUnknown `json:"unknown"`
}

type boundUnknown struct {
	Reason string `json:"reason"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
}

type boundViolation struct {
	Symbol    string `json:"symbol"`
	Dimension string `json:"dimension"`
	Actual    int    `json:"actual"`
	Limit     int    `json:"limit"`
}

func TestBoundsPropagateSequentialBranchAndLoopLimits(t *testing.T) {
	root := boundsProject(t)
	writeBounds(t, root, map[string]any{
		"dep.work":                map[string]int{"timeoutMs": 100, "maxAttempts": 1, "maxItems": 2, "maxConcurrency": 1},
		"dep.asyncWork":           map[string]int{"timeoutMs": 100, "maxAttempts": 1, "maxItems": 2, "maxConcurrency": 1},
		"src/main.ts::sequential": map[string]int{"timeoutMs": 150},
		"src/main.ts::branch":     map[string]int{"timeoutMs": 100},
		"src/main.ts::bounded":    map[string]int{"maxItems": 5},
		"src/main.ts::unknown":    map[string]int{"maxItems": 100},
		"src/main.ts::concurrent": map[string]int{"maxConcurrency": 1},
		"src/main.ts::recursive":  map[string]int{"maxAttempts": 1},
	})
	output, stderr, code := runSlick(t, root, nil, "bounds", "--json")
	document := decodeBounds(t, output)
	if code != 1 || stderr != "" || document.Bounds == nil || len(document.Bounds.Violations) != 3 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	results := map[string]boundResult{}
	for _, result := range document.Bounds.Results {
		results[result.Symbol] = result
	}
	if results["src/main.ts::sequential"].Bounds["timeoutMs"] != 200 || results["src/main.ts::branch"].Bounds["timeoutMs"] != 100 || results["src/main.ts::bounded"].Bounds["maxItems"] != 6 {
		t.Fatalf("propagated bounds: %+v", results)
	}
	if len(results["src/main.ts::unknown"].Unknown) == 0 || results["src/main.ts::unknown"].Unknown[0].Reason != "unbounded_loop" {
		t.Fatalf("unknown bound: %+v", results["src/main.ts::unknown"])
	}
	if results["src/main.ts::concurrent"].Bounds["maxConcurrency"] != 2 || len(results["src/main.ts::recursive"].Unknown) == 0 || results["src/main.ts::recursive"].Unknown[0].Reason != "recursive_cycle" {
		t.Fatalf("concurrency or recursion: %+v", results)
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "bounds")
	if humanCode != 1 || humanErr != "" || !strings.Contains(human, "fail src/main.ts::sequential timeoutMs") || !strings.Contains(human, "unknown unbounded_loop") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func TestBoundsThresholdEqualityPassesAndDescribeUsesSameFacts(t *testing.T) {
	root := boundsProject(t)
	writeBounds(t, root, map[string]any{
		"dep.work":                map[string]int{"timeoutMs": 100, "maxAttempts": 1, "maxItems": 2, "maxConcurrency": 1},
		"dep.asyncWork":           map[string]int{"timeoutMs": 100, "maxAttempts": 1, "maxItems": 2, "maxConcurrency": 1},
		"src/main.ts::branch":     map[string]int{"timeoutMs": 100, "maxItems": 2},
		"src/main.ts::concurrent": map[string]int{"timeoutMs": 100, "maxItems": 4, "maxConcurrency": 2},
	})
	output, stderr, code := runSlick(t, root, nil, "bounds", "--json")
	document := decodeBounds(t, output)
	if code != 0 || stderr != "" || !document.Success || document.Bounds == nil || len(document.Bounds.Violations) != 0 {
		t.Fatalf("exit %d, stderr %q, output %s", code, stderr, output)
	}
	described := runDescribe(t, root, "src/main.ts::branch")
	if described.Contract.Bounds == nil || described.Contract.Bounds.Bounds["timeoutMs"] != 100 || described.Contract.Bounds.Bounds["maxItems"] != 2 {
		t.Fatalf("describe bounds: %+v", described.Contract)
	}
}

func TestBoundsRejectInvalidContracts(t *testing.T) {
	root := boundsProject(t)
	writeFile(t, filepath.Join(root, "slick.contracts.json"), `{"symbols":{"src/main.ts::branch":{"timeoutMs":-1}}}`)
	output, stderr, code := runSlick(t, root, nil, "bounds", "--json")
	document := decodeBounds(t, output)
	if code != 1 || stderr != "" || document.Error == nil || document.Error.Kind != "bounds_configuration" {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
}

func boundsProject(t *testing.T) string {
	t.Helper()
	return project(t, `{"compilerOptions":{"strict":true,"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","skipLibCheck":true},"include":["src"]}`, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/main.ts": `import { asyncWork, work } from "dep";
export function sequential(): void { work(); work(); }
export function branch(flag: boolean): void { if (flag) work(); else work(); }
export function bounded(): void { for (let index = 0; index < 3; index++) work(); }
export function unknown(items: number[]): void { for (const item of items) { void item; work(); } }
export async function concurrent(): Promise<void> { await Promise.all([asyncWork(), asyncWork()]); }
export function recursive(): void { recursive(); }`,
		"node_modules/dep/package.json": `{"name":"dep","version":"1.0.0","type":"module","exports":{".":{"types":"./index.d.ts","import":"./index.js"}}}`,
		"node_modules/dep/index.d.ts":   `export declare function work(): void; export declare function asyncWork(): Promise<void>;`,
		"node_modules/dep/index.js":     `export function work() {} export async function asyncWork() {}`,
	})
}

func writeBounds(t *testing.T, root string, symbols map[string]any) {
	t.Helper()
	content, err := json.Marshal(map[string]any{"symbols": symbols})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "slick.contracts.json"), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeBounds(t *testing.T, output string) boundsDocument {
	t.Helper()
	var document boundsDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "bounds" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
