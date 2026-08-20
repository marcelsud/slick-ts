package slick_test

import (
	"encoding/json"
	"strings"
	"testing"
)

type deadCodeDocument struct {
	Version  int             `json:"version"`
	Command  string          `json:"command"`
	Success  bool            `json:"success"`
	DeadCode *deadCodeReport `json:"deadCode"`
	Error    *failure        `json:"error"`
}

type deadCodeReport struct {
	Entries     []string          `json:"entries"`
	Unreachable []deadCodeItem    `json:"unreachable"`
	Unknown     []deadCodeUnknown `json:"unknown"`
}

type deadCodeItem struct {
	Symbol string `json:"symbol"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
}

type deadCodeUnknown struct {
	Reason string `json:"reason"`
}

func TestDeadCodeFollowsExportsAliasesAndLocalCalls(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true,"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/index.ts": `export { publicValue } from "./lib.js";`,
		"src/lib.ts": `function helper(): number { return 1; }
export function publicValue(): number { return helper(); }
function deadFunction(): number { return 2; }
const deadArrow = (): number => 3;`,
	})
	output, stderr, code := runSlick(t, root, nil, "dead-code", "--json")
	document := decodeDeadCode(t, output)
	if code != 1 || stderr != "" || document.DeadCode == nil || len(document.DeadCode.Unknown) != 0 || len(document.DeadCode.Unreachable) != 2 {
		t.Fatalf("exit %d, stderr %q, output %s", code, stderr, output)
	}
	symbols := []string{document.DeadCode.Unreachable[0].Symbol, document.DeadCode.Unreachable[1].Symbol}
	joined := strings.Join(symbols, "\n")
	if !strings.Contains(joined, "deadFunction") || !strings.Contains(joined, "deadArrow") || strings.Contains(joined, "helper") || strings.Contains(joined, "publicValue") {
		t.Fatalf("unreachable symbols: %v", symbols)
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "dead-code")
	if humanCode != 1 || humanErr != "" || !strings.Contains(human, "unreachable function") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func TestDeadCodeKeepsUnknownDynamicTargetsExplicit(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true,"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/index.ts": `export function load(name: string): Promise<unknown> { return import(name); }
function maybeUsed(): number { return 1; }`,
	})
	output, stderr, code := runSlick(t, root, nil, "dead-code", "--json", "--entry", "src/index.ts")
	document := decodeDeadCode(t, output)
	if code != 1 || stderr != "" || document.DeadCode == nil || len(document.DeadCode.Unknown) != 1 || document.DeadCode.Unknown[0].Reason != "dynamic_import_target" || len(document.DeadCode.Unreachable) != 0 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
}

func TestDeadCodeRequiresAnEntryWhenNoIndexExists(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/feature.ts": `export function feature(): number { return 1; }`,
	})
	output, stderr, code := runSlick(t, root, nil, "dead-code", "--json")
	document := decodeDeadCode(t, output)
	if code != 1 || stderr != "" || document.Error == nil || document.Error.Kind != "entry_configuration" {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
}
func TestDeadCodeUsesPackageExportsAndSideEffectModules(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true,"module":"NodeNext","moduleResolution":"NodeNext","rootDir":"src","outDir":"dist"},"include":["src"]}`, map[string]string{
		"package.json":  `{"type":"module","exports":"./dist/public.js"}`,
		"src/public.ts": `import "./side.js"; export function publicValue(): number { return 1; }`,
		"src/side.ts": `function initialize(): void { console.log("ready"); }
initialize();
function unused(): void { console.log("unused"); }`,
	})
	output, stderr, code := runSlick(t, root, nil, "dead-code", "--json")
	document := decodeDeadCode(t, output)
	if code != 1 || stderr != "" || document.DeadCode == nil || len(document.DeadCode.Unreachable) != 1 || !strings.Contains(document.DeadCode.Unreachable[0].Symbol, "unused") {
		t.Fatalf("exit %d, stderr %q, output %s", code, stderr, output)
	}
}

func decodeDeadCode(t *testing.T, output string) deadCodeDocument {
	t.Helper()
	var document deadCodeDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "dead-code" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
