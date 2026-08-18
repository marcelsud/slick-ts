package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOperationalAnalyzesReachablePackageImplementations(t *testing.T) {
	root, config := packageFixture(t)
	result := analyzePackageFixture(t, config)

	pure := summaryNamed(t, result.Summaries, "src/main.ts::usePure")
	if len(pure.Errors) != 0 || len(pure.Effects) != 0 || len(pure.Unresolved) != 0 {
		t.Fatalf("pure package export was not complete: %+v", pure)
	}

	request := summaryNamed(t, result.Summaries, "src/main.ts::useRequest")
	assertFactNames(t, request.Effects, "network")
	if len(request.Unresolved) != 0 {
		t.Fatalf("transitive source package remained unresolved: %+v", request.Unresolved)
	}
	client := summaryNamed(t, result.Summaries, "src/main.ts::useClient")
	assertFactNames(t, client.Effects, "network")
	if len(client.Unresolved) != 0 {
		t.Fatalf("package class method remained unresolved: %+v", client.Unresolved)
	}
	fieldClient := summaryNamed(t, result.Summaries, "src/main.ts::useFieldClient")
	if len(fieldClient.Unresolved) == 0 {
		t.Fatalf("field initializer construction was declared pure: %+v", fieldClient)
	}

	caught := summaryNamed(t, result.Summaries, "src/main.ts::useCaught")
	if len(caught.Errors) != 0 {
		t.Fatalf("package error identity did not match authored catch: %+v", caught.Errors)
	}

	failure := summaryNamed(t, result.Summaries, "src/main.ts::useFailure")
	if len(failure.Errors) != 1 || failure.Errors[0].Name != "PackageError" {
		t.Fatalf("package error did not propagate: %+v", failure.Errors)
	}

	declaration := summaryNamed(t, result.Summaries, "src/main.ts::useDeclaration")
	if len(declaration.Unresolved) != 1 {
		t.Fatalf("declaration-only export: %+v", declaration.Unresolved)
	}
	leaf := declaration.Unresolved[0]
	if leaf.Reason != "declaration_only" || leaf.Package == nil || leaf.Package.Name != "demo" || leaf.Package.Version != "1.2.3" || leaf.Package.Export != "." {
		t.Fatalf("imprecise declaration-only leaf: %+v", leaf)
	}
	if leaf.Package.Declaration != "node_modules/demo/index.d.ts" || leaf.Package.Implementation != "node_modules/demo/index.js" {
		t.Fatalf("missing package sources: %+v", leaf.Package)
	}

	dynamic := summaryNamed(t, result.Summaries, "src/main.ts::useDynamic")
	if len(dynamic.Unresolved) != 1 || dynamic.Unresolved[0].Reason != "dynamic_code" ||
		dynamic.Unresolved[0].Package == nil || dynamic.Unresolved[0].Package.Name != "demo" {
		t.Fatalf("dynamic package leaf: %+v", dynamic.Unresolved)
	}

	native := summaryNamed(t, result.Summaries, "src/main.ts::useNative")
	if len(native.Unresolved) != 1 || native.Unresolved[0].Reason != "native_addon" || native.Unresolved[0].Package.Name != "native-demo" {
		t.Fatalf("native package leaf: %+v", native.Unresolved)
	}

	dependency := summaryNamed(t, result.Summaries, "node_modules/demo/index.js::pure")
	if dependency.Package == nil || dependency.Package.Name != "demo" || dependency.Package.Version != "1.2.3" || !strings.HasPrefix(dependency.Package.Integrity, "sha256-") {
		t.Fatalf("missing package identity: %+v", dependency.Package)
	}
	if !reflect.DeepEqual(dependency.Package.Conditions, []string{"import", "node"}) {
		t.Fatalf("export conditions: %v", dependency.Package.Conditions)
	}
	for _, summary := range result.Summaries {
		if strings.HasPrefix(summary.Symbol, "node_modules/type-only-demo/") {
			t.Fatalf("type-only package was analyzed: %s", summary.Symbol)
		}
	}

	if _, err := os.Stat(filepath.Join(root, "node_modules", ".cache", "slick")); err != nil {
		t.Fatalf("package cache was not created: %v", err)
	}
}

func TestPackageSummaryCacheReusesAndInvalidatesEntries(t *testing.T) {
	_, config := packageFixture(t)
	cache := t.TempDir()
	t.Setenv("SLICK_CACHE_DIR", cache)

	first := analyzePackageFixture(t, config)
	second := analyzePackageFixture(t, config)
	if first.Cache.Misses != 3 || second.Cache.Hits != 3 || second.Cache.Misses != 0 {
		t.Fatalf("cache first=%+v second=%+v", first.Cache, second.Cache)
	}
	firstJSON, _ := json.Marshal(first.Summaries)
	secondJSON, _ := json.Marshal(second.Summaries)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("cached summaries changed:\n%s\n%s", firstJSON, secondJSON)
	}

	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) == 0 {
		t.Fatalf("read cache: entries=%d err=%v", len(entries), err)
	}
	for _, entry := range entries {
		name := filepath.Join(cache, entry.Name())
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(content, &value); err != nil {
			t.Fatal(err)
		}
		value["version"] = float64(0)
		content, _ = json.Marshal(value)
		if err := os.WriteFile(name, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	invalidated := analyzePackageFixture(t, config)
	if invalidated.Cache.Misses != 3 || invalidated.Cache.Hits != 0 {
		t.Fatalf("schema change did not invalidate cache: %+v", invalidated.Cache)
	}

	writeTestFile(t, filepath.Join(filepath.Dir(config), "node_modules", "demo", "index.js"), `
import { transitive } from "nested-demo";
export function pure(value) { return value + 2; }
export function request() { return transitive(); }
export class PackageError extends Error {}
export function fail() { throw new PackageError("failed"); }
export function dynamic() { return eval("1"); }
`)
	changed := analyzePackageFixture(t, config)
	if changed.Cache.Misses < 1 {
		t.Fatalf("integrity change reused stale cache: %+v", changed.Cache)
	}
}
func TestPackageResolutionUsesTypeScriptConditions(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"package.json": `{"type":"module"}`,
		"tsconfig.json": `{
			"compilerOptions": {
				"strict": true,
				"target": "ES2022",
				"module": "ESNext",
				"moduleResolution": "Bundler",
				"skipLibCheck": true
			},
			"include": ["src/**/*.ts"]
		}`,
		"src/main.ts": `import { selected } from "conditions-demo"; export function use() { return selected(); }`,
		"node_modules/conditions-demo/package.json": `{
			"name":"conditions-demo",
			"version":"1.0.0",
			"type":"module",
			"exports":{".":{"types":"./index.d.ts","node":"./node.js","default":"./default.js"}}
		}`,
		"node_modules/conditions-demo/index.d.ts": `export declare function selected(): number;`,
		"node_modules/conditions-demo/node.js":    `export function selected() { return fetch("https://wrong.test"); }`,
		"node_modules/conditions-demo/default.js": `export function selected() { return 1; }`,
	}
	for name, content := range files {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
	result := analyzePackageFixture(t, filepath.Join(root, "tsconfig.json"))
	summary := summaryNamed(t, result.Summaries, "src/main.ts::use")
	if len(summary.Effects) != 0 || len(summary.Unresolved) != 0 {
		t.Fatalf("Bundler resolution analyzed node condition: %+v", summary)
	}
	dependency := summaryNamed(t, result.Summaries, "node_modules/conditions-demo/default.js::selected")
	if !reflect.DeepEqual(dependency.Package.Conditions, []string{"import"}) {
		t.Fatalf("Bundler conditions: %v", dependency.Package.Conditions)
	}
}

func TestPackageCacheFailureFallsBackToAnalysis(t *testing.T) {
	root, config := packageFixture(t)
	notDirectory := filepath.Join(root, "cache-file")
	writeTestFile(t, notDirectory, "not a directory")
	t.Setenv("SLICK_CACHE_DIR", notDirectory)
	result := analyzePackageFixture(t, config)
	request := summaryNamed(t, result.Summaries, "src/main.ts::useRequest")
	assertFactNames(t, request.Effects, "network")
}

func packageFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"package.json": `{"type":"module"}`,
		"tsconfig.json": `{
			"compilerOptions": {
				"strict": true,
				"target": "ES2022",
				"module": "NodeNext",
				"moduleResolution": "NodeNext",
				"lib": ["ES2022", "DOM"],
				"skipLibCheck": true
			},
			"include": ["src/**/*.ts"]
		}`,
		"src/main.ts": `
import { Client, FieldClient, PackageError, pure, request, fail, declarationOnly, dynamic } from "demo";
import { nativeCall } from "native-demo";
import { type Erased } from "type-only-demo";
export type PublicErased = Erased;
export function usePure(value: number) { return pure(value); }
export async function useRequest() { return await request(); }
export async function useClient() { return await new Client().request(); }
export function useFieldClient() { return new FieldClient(); }
export function useFailure() { return fail(); }
export function useCaught() {
	try { fail(); }
	catch (error) {
		if (error instanceof PackageError) return;
		throw error;
	}
}
export function useDeclaration() { return declarationOnly(); }
export function useDynamic() { return dynamic(); }
export function useNative() { return nativeCall(); }
`,
		"src/reexport.ts": `export { type Erased } from "type-only-demo";`,
		"node_modules/demo/package.json": `{
			"name":"demo",
			"version":"1.2.3",
			"type":"module",
			"exports":{".":{"types":"./index.d.ts","import":"./index.js"}}
		}`,
		"node_modules/demo/index.d.ts": `
export declare function pure(value: number): number;
export declare function request(): Promise<Response>;
export declare class PackageError extends Error {}
export declare class Client { request(): Promise<Response>; }
export declare class FieldClient { field: Promise<Response>; }
export declare function fail(): never;
export declare function declarationOnly(): void;
export declare function dynamic(): unknown;
`,
		"node_modules/demo/index.js": `
import { transitive } from "nested-demo";
export function pure(value) { return value + 1; }
export function request() { return transitive(); }
export class PackageError extends Error {}
export class Client { request() { return fetch("https://example.test"); } }
export class FieldClient { field = fetch("https://example.test"); }
export function fail() { throw new PackageError("failed"); }
export function dynamic() { return eval("1"); }
`,
		"node_modules/nested-demo/package.json": `{
			"name":"nested-demo",
			"version":"2.0.0",
			"type":"module",
			"exports":{".":{"types":"./index.d.ts","import":"./index.js"}}
		}`,
		"node_modules/nested-demo/index.d.ts": `export declare function transitive(): Promise<Response>;`,
		"node_modules/nested-demo/index.js":   `export function transitive() { return fetch("https://example.test"); }`,
		"node_modules/native-demo/package.json": `{
			"name":"native-demo",
			"version":"3.0.0",
			"exports":{".":{"types":"./index.d.ts","import":"./binding.node"}}
		}`,
		"node_modules/native-demo/index.d.ts":   `export declare function nativeCall(): void;`,
		"node_modules/native-demo/binding.node": "native",
		"node_modules/type-only-demo/package.json": `{
			"name":"type-only-demo",
			"version":"1.0.0",
			"type":"module",
			"exports":{".":{"types":"./index.d.ts","import":"./index.js"}}
		}`,
		"node_modules/type-only-demo/index.d.ts": `export interface Erased { value: string }`,
		"node_modules/type-only-demo/index.js":   `export function runtime() { return fetch("https://erased.test"); }`,
	}
	for name, content := range files {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
	return root, filepath.Join(root, "tsconfig.json")
}

func analyzePackageFixture(t *testing.T, config string) Analysis {
	t.Helper()
	compiler, err := filepath.Abs(filepath.Join("..", "..", "node_modules", "typescript", "lib", "typescript.js"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLICK_TYPESCRIPT_PATH", compiler)
	result := (NodeAnalyzer{}).Analyze(context.Background(), config)
	if result.Failure != nil || len(result.Diagnostics) != 0 {
		t.Fatalf("analysis failed: failure=%+v diagnostics=%+v", result.Failure, result.Diagnostics)
	}
	return result
}
