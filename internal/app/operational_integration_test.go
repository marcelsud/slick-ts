package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestOperationalDirectAndTransitiveFacts(t *testing.T) {
	result := analyzeOperationalFixture(t, map[string]string{
		"main.ts": `class RequestError extends Error {}
const leaf = (): void => {
  fetch("https://example.com");
  throw new RequestError("failed");
};
class Caller {
  run(): void { leaf(); }
}
function root(caller: Caller): void { caller.run(); }
`,
	})

	for _, symbol := range []string{"main.ts::leaf", "main.ts::Caller.run", "main.ts::root"} {
		summary := summaryNamed(t, result.Summaries, symbol)
		assertFactNames(t, summary.Errors, "RequestError")
		assertFactNames(t, summary.Effects, "network")
	}
}

func TestOperationalCaughtAndRethrownErrors(t *testing.T) {
	result := analyzeOperationalFixture(t, map[string]string{
		"main.ts": `class HandledError extends Error {}
class RemainingError extends Error {}
class ReplacementError extends Error {}
function risky(kind: "handled" | "remaining"): void {
  if (kind === "handled") throw new HandledError("handled");
  throw new RemainingError("remaining");
}
function handlesOne(): void {
  try { risky("handled"); }
  catch (error) {
    if (error instanceof HandledError) return;
    throw error;
  }
}
function replacesAll(): void {
  try { risky("remaining"); }
  catch (_error) { throw new ReplacementError("replacement"); }
}
function rethrowsOne(): void {
  try { risky("handled"); }
  catch (error) {
    if (error instanceof HandledError) throw error;
  }
}
`,
	})

	assertFactNames(t, summaryNamed(t, result.Summaries, "main.ts::handlesOne").Errors, "RemainingError")
	assertFactNames(t, summaryNamed(t, result.Summaries, "main.ts::replacesAll").Errors, "ReplacementError")
	assertFactNames(t, summaryNamed(t, result.Summaries, "main.ts::rethrowsOne").Errors, "HandledError")
}

func TestOperationalRecursiveGraphConverges(t *testing.T) {
	result := analyzeOperationalFixture(t, map[string]string{
		"main.ts": `class RecursiveError extends Error {}
function left(depth: number): void {
  if (depth === 0) throw new RecursiveError("done");
  right(depth - 1);
}
function right(depth: number): void {
  if (depth === 0) return;
  left(depth - 1);
}
`,
	})

	assertFactNames(t, summaryNamed(t, result.Summaries, "main.ts::left").Errors, "RecursiveError")
	assertFactNames(t, summaryNamed(t, result.Summaries, "main.ts::right").Errors, "RecursiveError")
}

func TestOperationalAsyncPropagationAndClassification(t *testing.T) {
	result := analyzeOperationalFixture(t, map[string]string{
		"main.ts": `class AsyncError extends Error {}
async function leaf(): Promise<void> {
  await fetch("https://example.com");
  throw new AsyncError("failed");
}
async function awaited(): Promise<void> { await leaf(); }
function returned(): Promise<void> { return awaited(); }
function synchronous(): number { return 1; }
`,
	})

	for _, symbol := range []string{"main.ts::leaf", "main.ts::awaited", "main.ts::returned"} {
		summary := summaryNamed(t, result.Summaries, symbol)
		if summary.Execution != ExecutionAsynchronous {
			t.Fatalf("%s classified %q", symbol, summary.Execution)
		}
		assertFactNames(t, summary.Errors, "AsyncError")
		assertFactNames(t, summary.Effects, "network")
	}
	if summaryNamed(t, result.Summaries, "main.ts::synchronous").Execution != ExecutionSynchronous {
		t.Fatal("synchronous function classified asynchronous")
	}
}

func TestOperationalPureAndUnresolvedSummaries(t *testing.T) {
	result := analyzeOperationalFixture(t, map[string]string{
		"main.ts": `declare function opaqueOperation(): void;
function pure(value: number): number { return value * 2; }
function incomplete(): void { opaqueOperation(); }
`,
	})

	pure := summaryNamed(t, result.Summaries, "main.ts::pure")
	if len(pure.Errors) != 0 || len(pure.Effects) != 0 || len(pure.Unresolved) != 0 {
		t.Fatalf("pure function has facts: %+v", pure)
	}
	incomplete := summaryNamed(t, result.Summaries, "main.ts::incomplete")
	if len(incomplete.Unresolved) != 1 || incomplete.Unresolved[0].Symbol != "opaqueOperation" || incomplete.Unresolved[0].Reason != "declaration_only" {
		t.Fatalf("unexpected unresolved leaf: %+v", incomplete.Unresolved)
	}
	if len(incomplete.Unresolved[0].Provenance) != 1 || incomplete.Unresolved[0].Provenance[0].Symbol != "main.ts::incomplete" {
		t.Fatalf("missing unresolved provenance: %+v", incomplete.Unresolved)
	}
}

func TestOperationalModelsInitialAuthoritySet(t *testing.T) {
	result := analyzeOperationalFixture(t, map[string]string{
		"fs.d.ts": `export declare function readFile(path: string, callback: (error: Error | null, data: string) => void): void;`,
		"globals.d.ts": `declare const process: {
  env: Record<string, string | undefined>;
  exit(code?: number): never;
};`,
		"main.ts": `import { readFile } from "./fs.js";
function authorities(): void {
  indexedDB.open("slick");
  process.env.HOME;
  readFile("data.txt", () => {});
  console.log("message");
  fetch("https://example.com");
  process.exit(1);
  Math.random();
  localStorage.getItem("key");
  Date.now();
}
`,
	})

	summary := summaryNamed(t, result.Summaries, "main.ts::authorities")
	assertFactNames(
		t,
		summary.Effects,
		"database", "environment", "filesystem", "io", "network", "process", "random", "state", "time",
	)
}

func TestOperationalFactsRetainExactSourceProvenance(t *testing.T) {
	result := analyzeOperationalFixture(t, map[string]string{
		"main.ts": `class SourceError extends Error {}
function first(): void {
  fetch("https://example.com");
  throw new SourceError("first");
}
function second(): void {
  throw new SourceError("second");
}
function caller(): void { first(); second(); }
`,
	})

	caller := summaryNamed(t, result.Summaries, "main.ts::caller")
	errorSource := caller.Errors[0].Provenance
	if len(errorSource) != 2 ||
		errorSource[0].Symbol != "main.ts::first" ||
		errorSource[0].Path != "main.ts" ||
		errorSource[0].Range.Start.Line != 4 ||
		errorSource[0].Range.Start.Column != 3 ||
		errorSource[1].Symbol != "main.ts::second" ||
		errorSource[1].Range.Start.Line != 7 {
		t.Fatalf("wrong error provenance: %+v", errorSource)
	}
	effectSource := caller.Effects[0].Provenance
	if len(effectSource) != 1 || effectSource[0].Symbol != "main.ts::first" || effectSource[0].Path != "main.ts" || effectSource[0].Range.Start.Line != 3 || effectSource[0].Range.Start.Column != 3 {
		t.Fatalf("wrong effect provenance: %+v", effectSource)
	}
}

func TestOperationalSemanticsIgnoreDeclarationOrder(t *testing.T) {
	first := analyzeOperationalFixture(t, map[string]string{
		"main.ts": `class OrderedError extends Error {}
function source(): void { throw new OrderedError("failed"); }
function caller(): void { source(); }
function unrelated(): number { return 1; }
`,
	})
	second := analyzeOperationalFixture(t, map[string]string{
		"main.ts": `class OrderedError extends Error {}
function unrelated(): number { return 1; }
function caller(): void { source(); }
function source(): void { throw new OrderedError("failed"); }
`,
	})

	if !reflect.DeepEqual(semanticProjection(first.Summaries), semanticProjection(second.Summaries)) {
		firstJSON, _ := json.Marshal(semanticProjection(first.Summaries))
		secondJSON, _ := json.Marshal(semanticProjection(second.Summaries))
		t.Fatalf("declaration order changed semantics:\n%s\n%s", firstJSON, secondJSON)
	}
}

func analyzeOperationalFixture(t *testing.T, files map[string]string) Analysis {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "tsconfig.json"), `{
		"compilerOptions": {
			"strict": true,
			"target": "ES2022",
			"module": "NodeNext",
			"moduleResolution": "NodeNext",
			"lib": ["ES2022", "DOM"],
			"skipLibCheck": true
		},
		"include": ["**/*.ts"]
	}`)
	for name, content := range files {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
	compiler, err := filepath.Abs(filepath.Join("..", "..", "node_modules", "typescript", "lib", "typescript.js"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLICK_TYPESCRIPT_PATH", compiler)
	result := (NodeAnalyzer{}).Analyze(context.Background(), filepath.Join(root, "tsconfig.json"))
	if result.Failure != nil || len(result.Diagnostics) != 0 {
		t.Fatalf("analysis failed: failure=%+v diagnostics=%+v", result.Failure, result.Diagnostics)
	}
	return result
}

func assertFactNames(t *testing.T, facts []OperationalFact, expected ...string) {
	t.Helper()
	actual := make([]string, len(facts))
	for index, fact := range facts {
		actual[index] = fact.Name
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("facts %v, want %v", actual, expected)
	}
}

type projectedSummary struct {
	Symbol     string
	Execution  Execution
	Errors     []string
	Effects    []string
	Unresolved []string
	Sources    []string
}

func semanticProjection(summaries []OperationalSummary) []projectedSummary {
	result := make([]projectedSummary, 0, len(summaries))
	for _, summary := range summaries {
		projected := projectedSummary{Symbol: summary.Symbol, Execution: summary.Execution}
		for _, fact := range summary.Errors {
			projected.Errors = append(projected.Errors, fact.Name)
			for _, source := range fact.Provenance {
				projected.Sources = append(projected.Sources, "error:"+fact.Name+":"+source.Symbol)
			}
		}
		for _, fact := range summary.Effects {
			projected.Effects = append(projected.Effects, fact.Name)
			for _, source := range fact.Provenance {
				projected.Sources = append(projected.Sources, "effect:"+fact.Name+":"+source.Symbol)
			}
		}
		for _, leaf := range summary.Unresolved {
			projected.Unresolved = append(projected.Unresolved, leaf.Symbol+":"+leaf.Reason)
			for _, source := range leaf.Provenance {
				projected.Sources = append(projected.Sources, "unresolved:"+leaf.Symbol+":"+source.Symbol)
			}
		}
		sort.Strings(projected.Sources)
		result = append(result, projected)
	}
	return result
}
