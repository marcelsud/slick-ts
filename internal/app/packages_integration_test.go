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
	if len(dynamic.Unresolved) != 1 || dynamic.Unresolved[0].Reason != "dynamic_code" {
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
	if !reflect.DeepEqual(dependency.Package.Conditions, []string{"import"}) {
		t.Fatalf("export conditions: %v", dependency.Package.Conditions)
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
	if first.Cache.Misses != 2 || second.Cache.Hits != 2 || second.Cache.Misses != 0 {
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
	if invalidated.Cache.Misses != 2 || invalidated.Cache.Hits != 0 {
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
import { pure, request, fail, declarationOnly, dynamic } from "demo";
import { nativeCall } from "native-demo";
export function usePure(value: number) { return pure(value); }
export async function useRequest() { return await request(); }
export function useFailure() { return fail(); }
export function useDeclaration() { return declarationOnly(); }
export function useDynamic() { return dynamic(); }
export function useNative() { return nativeCall(); }
`,
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
export declare function fail(): never;
export declare function declarationOnly(): void;
export declare function dynamic(): unknown;
`,
		"node_modules/demo/index.js": `
import { transitive } from "nested-demo";
export function pure(value) { return value + 1; }
export function request() { return transitive(); }
export class PackageError extends Error {}
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
