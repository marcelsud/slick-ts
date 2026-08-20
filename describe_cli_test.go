package slick_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type describeOutput struct {
	Version     int                `json:"version"`
	Command     string             `json:"command"`
	Success     bool               `json:"success"`
	Project     string             `json:"project"`
	Diagnostics []diagnostic       `json:"diagnostics"`
	Contract    *describedContract `json:"contract"`
	Error       *failure           `json:"error"`
}

type describedContract struct {
	CanonicalName  string                   `json:"canonicalName"`
	Name           string                   `json:"name"`
	Kind           string                   `json:"kind"`
	Visibility     string                   `json:"visibility"`
	Documentation  string                   `json:"documentation"`
	Aliases        []string                 `json:"aliases"`
	Location       sourceLocation           `json:"location"`
	TypeParameters []describedTypeParameter `json:"typeParameters"`
	Parameters     []describedParameter     `json:"parameters"`
	Return         *describedType           `json:"return"`
	Signatures     []describedSignature     `json:"signatures"`
	Members        []string                 `json:"members"`
	Package        *describedPackage        `json:"package"`
	Execution      string                   `json:"execution"`
	Errors         []describedFact          `json:"errors"`
	Effects        []describedFact          `json:"effects"`
	Completeness   string                   `json:"completeness"`
	Unresolved     []describedUnresolved    `json:"unresolved"`
	Bounds         *boundResult             `json:"bounds"`
}

type sourceLocation struct {
	Path  string      `json:"path"`
	Range sourceRange `json:"range"`
}

type describedType struct {
	Kind       string               `json:"kind"`
	Name       string               `json:"name"`
	Value      string               `json:"value"`
	Members    []describedType      `json:"members"`
	Element    *describedType       `json:"element"`
	Elements   []describedType      `json:"elements"`
	Arguments  []describedType      `json:"arguments"`
	Properties []describedProperty  `json:"properties"`
	Parameters []describedParameter `json:"parameters"`
	Return     *describedType       `json:"return"`
}

type describedProperty struct {
	Name     string        `json:"name"`
	Optional bool          `json:"optional"`
	Readonly bool          `json:"readonly"`
	Type     describedType `json:"type"`
}

type describedParameter struct {
	Name     string        `json:"name"`
	Optional bool          `json:"optional"`
	Rest     bool          `json:"rest"`
	Type     describedType `json:"type"`
}

type describedTypeParameter struct {
	Name       string         `json:"name"`
	Constraint *describedType `json:"constraint"`
	Default    *describedType `json:"default"`
}
type describedSignature struct {
	TypeParameters []describedTypeParameter `json:"typeParameters"`
	Parameters     []describedParameter     `json:"parameters"`
	Return         describedType            `json:"return"`
}

type describedPackage struct {
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Export         string   `json:"export"`
	Conditions     []string `json:"conditions"`
	Declaration    string   `json:"declaration"`
	Implementation string   `json:"implementation"`
}

type describedFact struct {
	Name string `json:"name"`
}
type describedProvenance struct {
	Symbol string      `json:"symbol"`
	Path   string      `json:"path"`
	Range  sourceRange `json:"range"`
}

type describedUnresolved struct {
	Symbol     string                `json:"symbol"`
	Reason     string                `json:"reason"`
	Package    *describedPackage     `json:"package"`
	Provenance []describedProvenance `json:"provenance"`
}

func TestDescribeLocalFunctionMethodAndNamespace(t *testing.T) {
	root := describeProject(t)
	function := runDescribe(t, root, "load")
	if function.Contract.CanonicalName != "src/main.ts::load" || function.Contract.Kind != "function" || function.Contract.Visibility != "exported" || function.Contract.Documentation != "Load one value." {
		t.Fatalf("local function: %+v", function.Contract)
	}
	if function.Contract.Execution != "asynchronous" || function.Contract.Completeness != "complete" || len(function.Contract.TypeParameters) != 1 || len(function.Contract.Parameters) != 2 {
		t.Fatalf("function contract: %+v", function.Contract)
	}
	if function.Contract.TypeParameters[0].Constraint == nil || function.Contract.TypeParameters[0].Constraint.Name != "string" || function.Contract.Return == nil || function.Contract.Return.Kind != "reference" || function.Contract.Return.Name != "Promise" {
		t.Fatalf("structured signature: %+v", function.Contract)
	}

	method := runDescribe(t, root, "Client.request")
	if method.Contract.Kind != "method" || method.Contract.Visibility != "public" || method.Contract.Execution != "asynchronous" || !reflect.DeepEqual(factNames(method.Contract.Effects), []string{"network"}) {
		t.Fatalf("class method: %+v", method.Contract)
	}

	namespace := runDescribe(t, root, "Tools")
	if namespace.Contract.Kind != "namespace" || !reflect.DeepEqual(namespace.Contract.Members, []string{"format", "parse"}) {
		t.Fatalf("namespace: %+v", namespace.Contract)
	}
	helper := runDescribe(t, root, "helper")
	if helper.Contract.Visibility != "local" {
		t.Fatalf("local visibility: %+v", helper.Contract)
	}
	arrow := runDescribe(t, root, "exportedArrow")
	if arrow.Contract.Visibility != "exported" {
		t.Fatalf("arrow visibility: %+v", arrow.Contract)
	}
	localAlias := runDescribe(t, root, "publicAlias")
	if localAlias.Contract.CanonicalName != "src/main.ts::localImplementation" {
		t.Fatalf("local export alias: %+v", localAlias.Contract)
	}
	localClassAlias := runDescribe(t, root, "PublicClient.request")
	if !strings.HasSuffix(localClassAlias.Contract.CanonicalName, "::LocalInternalClient.request") {
		t.Fatalf("local class alias: %+v", localClassAlias.Contract)
	}
	barrelAlias := runDescribe(t, root, "barrelAlias")
	if barrelAlias.Contract.CanonicalName != "src/internal.ts::barrelTarget" {
		t.Fatalf("barrel alias: %+v", barrelAlias.Contract)
	}
	overload := runDescribe(t, root, "overload")
	if len(overload.Contract.Signatures) != 2 {
		t.Fatalf("overloads: %+v", overload.Contract.Signatures)
	}
	box := runDescribe(t, root, "Box")
	if len(box.Contract.TypeParameters) != 1 || box.Contract.TypeParameters[0].Constraint == nil || box.Contract.TypeParameters[0].Constraint.Name != "string" {
		t.Fatalf("generic class: %+v", box.Contract)
	}
	constructor := runDescribe(t, root, "Overloaded.constructor")
	if len(constructor.Contract.Signatures) != 2 {
		t.Fatalf("constructor overloads: %+v", constructor.Contract.Signatures)
	}
}

func TestDescribeDependencyContractsMatchOperationalFacts(t *testing.T) {
	root := describeProject(t)
	remote := runDescribe(t, root, "demo.remote")
	if remote.Contract.Package == nil || remote.Contract.Package.Name != "demo" || remote.Contract.Package.Version != "1.2.3" || remote.Contract.Package.Declaration != "node_modules/demo/index.d.ts" || remote.Contract.Package.Implementation != "node_modules/demo/index.js" {
		t.Fatalf("dependency identity: %+v", remote.Contract.Package)
	}
	if !reflect.DeepEqual(factNames(remote.Contract.Effects), []string{"network"}) || remote.Contract.Completeness != "complete" {
		t.Fatalf("dependency effect: %+v", remote.Contract)
	}

	failure := runDescribe(t, root, "demo.fail")
	if !reflect.DeepEqual(factNames(failure.Contract.Errors), []string{"PackageError"}) {
		t.Fatalf("dependency error: %+v", failure.Contract.Errors)
	}

	partial := runDescribe(t, root, "demo.partial")
	if partial.Contract.Completeness != "partial" || len(partial.Contract.Unresolved) != 1 || partial.Contract.Unresolved[0].Reason != "dynamic_code" || partial.Contract.Unresolved[0].Package == nil || partial.Contract.Unresolved[0].Package.Name != "demo" {
		t.Fatalf("partial dependency: %+v", partial.Contract)
	}
	convert := runDescribe(t, root, "demo.convert")
	if len(convert.Contract.Signatures) != 2 {
		t.Fatalf("dependency overloads: %+v", convert.Contract.Signatures)
	}
	packageAlias := runDescribe(t, root, "demo.aliasedConvert")
	if !strings.HasSuffix(packageAlias.Contract.CanonicalName, "::internalAlias") {
		t.Fatalf("package export alias: %+v", packageAlias.Contract)
	}

	declarationOnly := runDescribe(t, root, "decl-only.only")
	if declarationOnly.Contract.Completeness != "partial" || len(declarationOnly.Contract.Unresolved) != 1 ||
		declarationOnly.Contract.Unresolved[0].Reason != "declaration_only" ||
		declarationOnly.Contract.Package == nil || declarationOnly.Contract.Package.Implementation != "" {
		t.Fatalf("declaration-only dependency: %+v", declarationOnly.Contract)
	}
	parent := runDescribe(t, root, "decl-only.NativeClient")
	if !reflect.DeepEqual(parent.Contract.Members, []string{"request"}) {
		t.Fatalf("declaration-only parent members: %+v", parent.Contract)
	}
	member := runDescribe(t, root, "decl-only.NativeClient.request")
	if member.Contract.Kind != "method" || member.Contract.Execution != "asynchronous" ||
		member.Contract.Completeness != "partial" || len(member.Contract.Unresolved) != 1 ||
		member.Contract.Unresolved[0].Reason != "declaration_only" {
		t.Fatalf("declaration-only member: %+v", member.Contract)
	}
	packageClassAlias := runDescribe(t, root, "demo.PublicService.request")
	if !strings.HasSuffix(packageClassAlias.Contract.CanonicalName, "::InternalService.request") {
		t.Fatalf("package class alias: %+v", packageClassAlias.Contract)
	}
}

func TestDescribeJSONIsDeterministicAndVersioned(t *testing.T) {
	root := describeProject(t)
	first, firstErr, firstCode := runSlick(t, root, nil, "describe", "--json", "load")
	second, secondErr, secondCode := runSlick(t, root, nil, "describe", "--json", "load")
	if firstCode != 0 || secondCode != 0 || firstErr != "" || secondErr != "" || first != second {
		t.Fatalf("codes %d/%d, stderr %q/%q, output:\n%s\n%s", firstCode, secondCode, firstErr, secondErr, first, second)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(first), &raw); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sortedKeys(raw), []string{"command", "contract", "diagnostics", "project", "success", "version"}) {
		t.Fatalf("version 1 document shape changed: %v", sortedKeys(raw))
	}
	var contract map[string]json.RawMessage
	if err := json.Unmarshal(raw["contract"], &contract); err != nil {
		t.Fatal(err)
	}
	expected := []string{"aliases", "canonicalName", "completeness", "documentation", "effects", "errors", "execution", "kind", "location", "members", "name", "parameters", "return", "signatures", "typeParameters", "unresolved", "visibility"}
	if !reflect.DeepEqual(sortedKeys(contract), expected) {
		t.Fatalf("version 1 contract shape changed without a version bump: %v", sortedKeys(contract))
	}
}

func TestDescribeUnknownAndAmbiguousSymbols(t *testing.T) {
	root := describeProject(t)
	ambiguous, stderr, code := runSlick(t, root, nil, "describe", "--json", "same")
	var ambiguousDocument describeOutput
	if err := json.Unmarshal([]byte(ambiguous), &ambiguousDocument); err != nil {
		t.Fatal(err)
	}
	if code != 1 || stderr != "" || ambiguousDocument.Error == nil || ambiguousDocument.Error.Kind != "ambiguous_symbol" || len(ambiguousDocument.Error.Alternatives) != 2 {
		t.Fatalf("ambiguous exit %d, stderr %q, output %+v", code, stderr, ambiguousDocument)
	}

	unknown, _, unknownCode := runSlick(t, root, nil, "describe", "--json", "loa")
	var unknownDocument describeOutput
	if err := json.Unmarshal([]byte(unknown), &unknownDocument); err != nil {
		t.Fatal(err)
	}
	if unknownCode != 1 || unknownDocument.Error == nil || unknownDocument.Error.Kind != "unknown_symbol" {
		t.Fatalf("unknown exit %d, output %+v", unknownCode, unknownDocument)
	}
	foundLoad := false
	for _, alternative := range unknownDocument.Error.Alternatives {
		foundLoad = foundLoad || alternative == "src/main.ts::load"
	}
	if !foundLoad {
		t.Fatalf("missing load alternative: %+v", unknownDocument.Error.Alternatives)
	}

	human, humanErr, humanCode := runSlick(t, root, nil, "describe", "Client.request")
	if humanCode != 0 || humanErr != "" || !strings.Contains(human, "src/main.ts::Client.request") ||
		!strings.Contains(human, "name: request") || !strings.Contains(human, `"end"`) ||
		!strings.Contains(human, `"offset"`) || !strings.Contains(human, `effects: [{"name":"network"`) ||
		!strings.Contains(human, `"provenance"`) || !strings.Contains(human, "aliases:") ||
		!strings.Contains(human, "completeness: complete") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func describeProject(t *testing.T) string {
	t.Helper()
	return project(t, strictConfig, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/main.ts": `
import { PublicService, aliasedConvert, convert, fail, partial, remote } from "demo";
import { NativeClient, only } from "decl-only";
function helper(): number { return 0; }
export const exportedArrow = (): number => helper();
function localImplementation(): number { return 1; }
export { localImplementation as publicAlias };
class LocalInternalClient {
	request(url: string): Promise<Response> { return fetch(url); }
}
export { LocalInternalClient as PublicClient };
export function overload(value: string): string;
export function overload(value: number): number;
export function overload(value: string | number): string | number { return value; }
/** Load one value. */
export async function load<T extends string>(input: T, count = 1): Promise<T> { void count; return input; }
export class Client {
	/** Request a URL. */
	async request(url: string): Promise<Response> { return fetch(url); }
}
export class Box<T extends string> { constructor(readonly value: T) {} }
export class Overloaded {
	constructor(value: string);
	constructor(value: number);
	constructor(value: string | number) { void value; }
}
export namespace Tools {
	export function parse(value: unknown): string { return typeof value === "string" ? value : ""; }
}
export namespace Tools {
	export function format(value: number): string { return String(value); }
}
export async function useRemote() { return remote(); }
export function useFail() { return fail(); }
export function usePartial() { return partial(); }
export function usePublicService(service: PublicService) { return service.request("https://example.test"); }
export function useConvert() { return convert("value"); }
export function useAliasedConvert() { return aliasedConvert("value"); }
export function useOnly() { return only(); }
export function useNativeClient(client: NativeClient) { return client.request(); }
`,
		"src/a.ts":        `export function same(): number { return 1; }`,
		"src/b.ts":        `export function same(): number { return 2; }`,
		"src/internal.ts": `export function barrelTarget(): number { return 1; }`,
		"src/barrel.ts":   `export { barrelTarget as barrelAlias } from "./internal.js";`,
		"node_modules/demo/package.json": `{
			"name":"demo",
			"version":"1.2.3",
			"type":"module",
			"exports":{ ".": { "types":"./index.d.ts", "import":"./index.js" } }
		}`,
		"node_modules/demo/index.d.ts": `
export declare class PackageError extends Error {}
export declare function remote(): Promise<Response>;
export declare function fail(): never;
export declare function partial(): unknown;
export declare function convert(value: string): string;
export declare function convert(value: number): number;
declare class InternalService { request(url: string): Promise<Response>; }
export { InternalService as PublicService };
export { internalAlias as aliasedConvert } from "./internal.js";
`,
		"node_modules/demo/index.js": `
export class PackageError extends Error {}
export function remote() { return fetch("https://example.test"); }
export function fail() { throw new PackageError("failed"); }
export function partial() { return eval("1"); }
class InternalService { request(url) { return fetch(url); } }
export { InternalService as PublicService };
export function convert(value) { return value; }
export { internalAlias as aliasedConvert } from "./internal.js";
`,
		"node_modules/demo/internal.d.ts": `export declare function internalAlias(value: string): string;`,
		"node_modules/demo/internal.js":   `export function internalAlias(value) { return value; }`,
		"node_modules/decl-only/package.json": `{
			"name":"decl-only",
			"version":"4.0.0",
			"types":"index.d.ts"
		}`,
		"node_modules/decl-only/index.d.ts": `
export declare function only(): void;
export declare class NativeClient { request(): Promise<Response>; }
`,
	})
}

func runDescribe(t *testing.T, root, symbol string) describeOutput {
	t.Helper()
	output, stderr, code := runSlick(t, root, nil, "describe", "--json", symbol)
	var document describeOutput
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if code != 0 || stderr != "" || !document.Success || document.Version != 1 || document.Command != "describe" || document.Contract == nil {
		t.Fatalf("exit %d, stderr %q, output %s", code, stderr, output)
	}
	return document
}

func factNames(facts []describedFact) []string {
	names := make([]string, len(facts))
	for index, fact := range facts {
		names[index] = fact.Name
	}
	return names
}

func sortedKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
