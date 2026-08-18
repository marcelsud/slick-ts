package slick_test

import (
	"strings"
	"testing"
)

const strictConfig = `{
	"compilerOptions": {
		"strict": true,
		"target": "ES2022",
		"module": "NodeNext",
		"moduleResolution": "NodeNext",
		"lib": ["ES2022", "DOM"],
		"skipLibCheck": true
	},
	"include": ["src/**/*.ts"]
}`

func TestCheckRejectsInitialStrictRulesAndRepairs(t *testing.T) {
	tests := []struct {
		name   string
		code   int
		bad    string
		repair string
	}{
		{
			name: "unsafe any",
			code: 1001,
			bad:  `export function unsafe(value: any) { return value; }`,
			repair: `export function safe(value: unknown) {
				return typeof value === "string" ? value : "";
			}`,
		},
		{
			name:   "unchecked assertion",
			code:   1002,
			bad:    `export function unsafe(value: unknown) { return value as string; }`,
			repair: `export function safe(value: unknown) { return typeof value === "string" ? value : ""; }`,
		},
		{
			name:   "implicit truthiness",
			code:   1003,
			bad:    `export function unsafe(value: string) { if (value) return 1; return 0; }`,
			repair: `export function safe(value: string) { if (value.length > 0) return 1; return 0; }`,
		},
		{
			name:   "unconsumed Promise",
			code:   1004,
			bad:    `export function unsafe() { fetch("https://example.test"); }`,
			repair: `export async function safe() { await fetch("https://example.test"); }`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := project(t, strictConfig, map[string]string{"src/main.ts": test.bad})
			output, stderr, code := runSlick(t, root, nil, "check", "--json")
			document := decodeOutput(t, output)
			if code != 1 || stderr != "" || document.Success || len(document.Diagnostics) != 1 {
				t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
			}
			assertSlickDiagnostic(t, document.Diagnostics[0], test.code)

			human, humanErr, humanCode := runSlick(t, root, nil, "check")
			if humanCode != 1 || humanErr != "" || !strings.Contains(human, "SLICK") || !strings.Contains(human, "Fact:") || !strings.Contains(human, "Repair:") {
				t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
			}

			repaired := project(t, strictConfig, map[string]string{"src/main.ts": test.repair})
			repairedOutput, repairedErr, repairedCode := runSlick(t, repaired, nil, "check", "--json")
			if repairedCode != 0 || repairedErr != "" || !decodeOutput(t, repairedOutput).Success {
				t.Fatalf("repair exit %d, stderr %q, output %s", repairedCode, repairedErr, repairedOutput)
			}
		})
	}
}

func TestCheckStrictRuleBoundaries(t *testing.T) {
	root := project(t, strictConfig, map[string]string{
		"src/main.ts": `
import { safe } from "legacy";
function isNonEmpty(value: string): boolean { return value.length > 0; }
export async function accepted(flag: boolean, text: string) {
	const parsed: unknown = JSON.parse("{}");
	if (flag && isNonEmpty(text)) {
		const pending = fetch("https://example.test/one");
		await pending;
	}
	await Promise.all([fetch("https://example.test/two"), Promise.resolve(parsed)]);
	safe();
	return fetch("https://example.test/three");
}
`,
		"node_modules/legacy/package.json": `{"name":"legacy","version":"1.0.0","types":"index.d.ts"}`,
		"node_modules/legacy/index.d.ts": `
export declare function safe(): void;
export declare const unusedDeclarationInternal: any;
`,
	})
	output, stderr, code := runSlick(t, root, nil, "check", "--json")
	if code != 0 || stderr != "" || !decodeOutput(t, output).Success {
		t.Fatalf("exit %d, stderr %q, output %s", code, stderr, output)
	}
}

func TestCheckReportsInferredAnyOnlyWhenUsedByAuthoredFlow(t *testing.T) {
	root := project(t, strictConfig, map[string]string{
		"src/main.ts":                      `import { unsafe } from "legacy"; export const value = unsafe();`,
		"node_modules/legacy/package.json": `{"name":"legacy","version":"1.0.0","types":"index.d.ts"}`,
		"node_modules/legacy/index.d.ts":   `export declare function unsafe(): any; export declare const unused: any;`,
	})
	output, _, code := runSlick(t, root, nil, "check", "--json")
	document := decodeOutput(t, output)
	if code != 1 || len(document.Diagnostics) != 1 {
		t.Fatalf("exit %d, output %+v", code, document)
	}
	assertSlickDiagnostic(t, document.Diagnostics[0], 1001)
}

func TestCheckPromiseOwnershipAcrossAssignmentsBranchesAndCallbacks(t *testing.T) {
	root := project(t, strictConfig, map[string]string{
		"src/main.ts": `
export async function rejected(flag: boolean) {
	fetch("https://example.test/one");
	const pending = fetch("https://example.test/two");
	if (flag) fetch("https://example.test/three");
	[1].forEach(async () => { await fetch("https://example.test/four"); });
	[1].forEach(() => fetch("https://example.test/five"));
	let overwritten = fetch("https://example.test/six");
	overwritten = fetch("https://example.test/seven");
	await overwritten;
	const conditional = fetch("https://example.test/eight");
	if (flag) await conditional;
	return pending;
}
`,
	})
	output, _, code := runSlick(t, root, nil, "check", "--json")
	document := decodeOutput(t, output)
	if code != 1 {
		t.Fatalf("exit %d, output %+v", code, document)
	}
	codes := slickCodes(document.Diagnostics)
	if len(codes) != 6 {
		t.Fatalf("Promise diagnostics %v, output %+v", codes, document)
	}
	for _, got := range codes {
		if got != 1004 {
			t.Fatalf("Promise diagnostics %v", codes)
		}
	}
}

func TestCheckTruthinessAndAssertionBoundaries(t *testing.T) {
	root := project(t, strictConfig, map[string]string{
		"src/main.ts": `
function predicate(value: string): boolean { return value.length > 0; }
export function truth(flag: boolean, text: string, count: number, object: {}, maybe: string | undefined) {
	if (flag) {}
	if (predicate(text)) {}
	if (text) {}
	if (count) {}
	if (object) {}
	if (maybe) {}
}
export function assertions(value: string | undefined, unknownValue: unknown) {
	value!;
	unknownValue as string;
	<string>unknownValue;
}
`,
	})
	output, _, code := runSlick(t, root, nil, "check", "--json")
	document := decodeOutput(t, output)
	if code != 1 {
		t.Fatalf("exit %d, output %+v", code, document)
	}
	counts := map[int]int{}
	for _, diagnostic := range document.Diagnostics {
		if diagnostic.Source == "slick" {
			counts[diagnostic.Code]++
		}
	}
	if counts[1002] != 3 || counts[1003] != 4 {
		t.Fatalf("diagnostics %v, output %+v", counts, document)
	}
}

func TestCheckStrictJSONIsDeterministicAndDoesNotDuplicateTypeScript(t *testing.T) {
	root := project(t, strictConfig, map[string]string{
		"src/main.ts": `
export function implicit(value) { return value; }
export function strict(value: string) {
	if (value) fetch("https://example.test");
}
`,
	})
	first, _, firstCode := runSlick(t, root, nil, "check", "--json")
	second, _, secondCode := runSlick(t, root, nil, "check", "--json")
	if firstCode != 1 || secondCode != 1 || first != second {
		t.Fatalf("codes %d/%d or nondeterministic:\n%s\n%s", firstCode, secondCode, first, second)
	}
	document := decodeOutput(t, first)
	implicitCount := 0
	for _, diagnostic := range document.Diagnostics {
		if diagnostic.Range != nil && diagnostic.Range.Start.Line == 2 && diagnostic.Range.Start.Column == 26 {
			implicitCount++
			if diagnostic.Source != "typescript" || diagnostic.Code != 7006 {
				t.Fatalf("duplicate implicit-any diagnostic: %+v", diagnostic)
			}
		}
	}
	if implicitCount != 1 {
		t.Fatalf("implicit-any diagnostics=%d: %+v", implicitCount, document.Diagnostics)
	}
}

func assertSlickDiagnostic(t *testing.T, got diagnostic, code int) {
	t.Helper()
	if got.Source != "slick" || got.Code != code || got.Category != "error" || got.Title == "" || got.Explanation == "" || got.Fact == "" || len(got.Repairs) == 0 || got.Path != "src/main.ts" || got.Range == nil {
		t.Fatalf("unexpected Slick diagnostic: %+v", got)
	}
}

func slickCodes(diagnostics []diagnostic) []int {
	codes := []int{}
	for _, diagnostic := range diagnostics {
		if diagnostic.Source == "slick" {
			codes = append(codes, diagnostic.Code)
		}
	}
	return codes
}
