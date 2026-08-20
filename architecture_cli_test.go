package slick_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type architectureDocument struct {
	Version      int                 `json:"version"`
	Command      string              `json:"command"`
	Success      bool                `json:"success"`
	Architecture *architectureReport `json:"architecture"`
	Error        *failure            `json:"error"`
}

type architectureReport struct {
	Modules    []architectureModule    `json:"modules"`
	Edges      []architectureEdge      `json:"edges"`
	Cycles     []architectureCycle     `json:"cycles"`
	Violations []architectureViolation `json:"violations"`
	Unresolved []architectureUnknown   `json:"unresolved"`
}

type architectureModule struct {
	Path   string `json:"path"`
	Layer  string `json:"layer"`
	FanIn  int    `json:"fanIn"`
	FanOut int    `json:"fanOut"`
}

type architectureEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	TypeOnly bool   `json:"typeOnly"`
}

type architectureCycle struct {
	Modules []string `json:"modules"`
}
type architectureViolation struct {
	Kind string `json:"kind"`
}
type architectureUnknown struct {
	Reason string `json:"reason"`
}

func TestArchitectureReportsLayersCyclesAndFanThresholds(t *testing.T) {
	root := architectureProject(t)
	writeArchitectureConfig(t, root, false, 1, 1)
	output, stderr, code := runSlick(t, root, nil, "architecture", "--json")
	document := decodeArchitecture(t, output)
	if code != 1 || stderr != "" || document.Architecture == nil || len(document.Architecture.Edges) < 2 || len(document.Architecture.Cycles) != 1 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	kinds := map[string]bool{}
	for _, violation := range document.Architecture.Violations {
		kinds[violation.Kind] = true
	}
	if !kinds["layer"] {
		t.Fatalf("missing layer violation: %+v", document.Architecture.Violations)
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "architecture")
	if humanCode != 1 || humanErr != "" || !strings.Contains(human, "fail cycle:") || !strings.Contains(human, "fan-in") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func TestArchitectureCanAllowTypeOnlyCyclesAndRejectsBadConfig(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true,"module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/a.ts":     `import type { B } from "./b.js"; export { type B } from "./b.js"; export interface A { b: B }`,
		"src/b.ts":     `import type { A } from "./a.js"; export interface B { a: A }`,
	})
	writeArchitectureConfig(t, root, true, 0, 0)
	output, stderr, code := runSlick(t, root, nil, "architecture", "--json")
	if code != 0 || stderr != "" || !decodeArchitecture(t, output).Success {
		t.Fatalf("allowed cycle exit %d, stderr %q, output %s", code, stderr, output)
	}

	writeFile(t, filepath.Join(root, "slick.architecture.json"), `{"layers":[{"name":"one","include":["src/**"],"mayImport":[]},{"name":"two","include":["src/a.ts"],"mayImport":[]}]}`)
	badOutput, badErr, badCode := runSlick(t, root, nil, "architecture", "--json")
	bad := decodeArchitecture(t, badOutput)
	if badCode != 1 || badErr != "" || bad.Error == nil || bad.Error.Kind != "architecture_configuration" {
		t.Fatalf("bad config exit %d, stderr %q, output %+v", badCode, badErr, bad)
	}
}

func TestArchitectureKeepsDynamicTargetsUnresolved(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true,"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/index.ts": `export function load(name: string): Promise<unknown> { return import(name); }`,
	})
	writeArchitectureConfig(t, root, false, 0, 0)
	output, stderr, code := runSlick(t, root, nil, "architecture", "--json")
	document := decodeArchitecture(t, output)
	if code != 1 || stderr != "" || document.Architecture == nil || len(document.Architecture.Unresolved) != 1 || document.Architecture.Unresolved[0].Reason != "dynamic_import_target" {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
}

func TestArchitectureReturnsAClosedCyclePath(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true,"module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/a.ts":     `import { c } from "./c.js"; export const a = c;`,
		"src/b.ts":     `import { a } from "./a.js"; export const b = a;`,
		"src/c.ts":     `import { b } from "./b.js"; export const c = b;`,
	})
	writeArchitectureConfig(t, root, false, 0, 0)
	output, _, code := runSlick(t, root, nil, "architecture", "--json")
	document := decodeArchitecture(t, output)
	if code != 1 || document.Architecture == nil || len(document.Architecture.Cycles) != 1 {
		t.Fatalf("exit %d, output %+v", code, document)
	}
	path := document.Architecture.Cycles[0].Modules
	expected := []string{"src/a.ts", "src/c.ts", "src/b.ts", "src/a.ts"}
	if len(path) != len(expected) {
		t.Fatalf("cycle path: %v", path)
	}
	for index := range expected {
		if path[index] != expected[index] {
			t.Fatalf("cycle path: %v", path)
		}
	}
}

func architectureProject(t *testing.T) string {
	t.Helper()
	return project(t, `{"compilerOptions":{"strict":true,"module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`, map[string]string{
		"package.json":        `{"type":"module"}`,
		"src/domain/value.ts": `import { use } from "../app/use.js"; export function value(): number { return use(); }`,
		"src/app/use.ts":      `import { value } from "../domain/value.js"; export function use(): number { return value(); }`,
	})
}

func writeArchitectureConfig(t *testing.T, root string, allowTypes bool, maxIn, maxOut int) {
	t.Helper()
	value := map[string]any{
		"layers": []any{
			map[string]any{"name": "domain", "include": []string{"src/domain/**", "src/a.ts", "src/b.ts", "src/index.ts"}, "mayImport": []string{}},
			map[string]any{"name": "application", "include": []string{"src/app/**"}, "mayImport": []string{"domain"}},
		},
		"maxFanIn": maxIn, "maxFanOut": maxOut, "allowTypeOnlyCycles": allowTypes,
	}
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "slick.architecture.json"), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeArchitecture(t *testing.T, output string) architectureDocument {
	t.Helper()
	var document architectureDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "architecture" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
