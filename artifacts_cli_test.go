package slick_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type artifactsDocument struct {
	Version   int             `json:"version"`
	Command   string          `json:"command"`
	Success   bool            `json:"success"`
	Outputs   []string        `json:"outputs"`
	Artifacts *artifactReport `json:"artifacts"`
	Error     *failure        `json:"error"`
}

type artifactReport struct {
	TotalBytes int                 `json:"totalBytes"`
	Files      []artifactFile      `json:"files"`
	Violations []artifactViolation `json:"violations"`
}

type artifactFile struct {
	Path    string          `json:"path"`
	Bytes   int             `json:"bytes"`
	Imports []runtimeImport `json:"imports"`
}

type runtimeImport struct {
	Specifier string `json:"specifier"`
	Package   string `json:"package"`
	Builtin   bool   `json:"builtin"`
	Kind      string `json:"kind"`
}

type artifactViolation struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Package string `json:"package"`
	Actual  int    `json:"actual"`
	Limit   int    `json:"limit"`
}

func TestArtifactsEnforcesBudgetsAndRuntimeImportsBeforeInstall(t *testing.T) {
	root := artifactProject(t, "NodeNext", "NodeNext")
	deniedOutput, deniedErr, deniedCode := runSlick(t, root, nil, "artifacts", "--json", "--deny-runtime-import", "demo")
	denied := decodeArtifacts(t, deniedOutput)
	if deniedCode != 1 || deniedErr != "" || denied.Artifacts == nil || len(denied.Artifacts.Violations) != 1 || denied.Artifacts.Violations[0].Kind != "runtime_import" || denied.Artifacts.Violations[0].Package != "demo" {
		t.Fatalf("denied exit %d, stderr %q, output %+v", deniedCode, deniedErr, denied)
	}
	if _, err := os.Stat(filepath.Join(root, "dist", "main.js")); !os.IsNotExist(err) {
		t.Fatalf("denied artifacts installed output: %v", err)
	}

	output, stderr, code := runSlick(t, root, nil, "artifacts", "--json")
	document := decodeArtifacts(t, output)
	if code != 0 || stderr != "" || !document.Success || document.Artifacts == nil || document.Artifacts.TotalBytes == 0 || len(document.Outputs) == 0 {
		t.Fatalf("pass exit %d, stderr %q, output %+v", code, stderr, document)
	}
	maxFile := 0
	for _, file := range document.Artifacts.Files {
		if file.Bytes > maxFile {
			maxFile = file.Bytes
		}
	}
	exact, exactErr, exactCode := runSlick(t, root, nil, "artifacts", "--json",
		"--max-total-bytes", integerString(document.Artifacts.TotalBytes), "--max-file-bytes", integerString(maxFile))
	if exactCode != 0 || exactErr != "" || !decodeArtifacts(t, exact).Success {
		t.Fatalf("exact limits exit %d, stderr %q, output %s", exactCode, exactErr, exact)
	}

	mainPath := filepath.Join(root, "dist", "main.js")
	before, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	failedOutput, failedErr, failedCode := runSlick(t, root, nil, "artifacts", "--json", "--max-total-bytes", integerString(document.Artifacts.TotalBytes-1))
	failed := decodeArtifacts(t, failedOutput)
	if failedCode != 1 || failedErr != "" || len(failed.Artifacts.Violations) == 0 || failed.Artifacts.Violations[0].Kind != "total_bytes" {
		t.Fatalf("budget exit %d, stderr %q, output %+v", failedCode, failedErr, failed)
	}
	after, _ := os.ReadFile(mainPath)
	if string(before) != string(after) {
		t.Fatal("failed artifact gate changed installed output")
	}
}

func TestArtifactsFindsCommonJSRequireAndOmitsTypeOnlyImports(t *testing.T) {
	root := artifactProject(t, "CommonJS", "Node10")
	output, stderr, code := runSlick(t, root, nil, "artifacts", "--json")
	document := decodeArtifacts(t, output)
	if code != 0 || stderr != "" || document.Artifacts == nil {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	imports := []runtimeImport{}
	for _, file := range document.Artifacts.Files {
		imports = append(imports, file.Imports...)
	}
	if len(imports) != 2 {
		t.Fatalf("runtime imports: %+v", imports)
	}
	foundPackage, foundBuiltin := false, false
	for _, imported := range imports {
		foundPackage = foundPackage || imported.Package == "demo" && imported.Kind == "require" && !imported.Builtin
		foundBuiltin = foundBuiltin || imported.Specifier == "fs" && imported.Builtin && imported.Package == ""
	}
	if !foundPackage || !foundBuiltin {
		t.Fatalf("runtime imports: %+v", imports)
	}
	human, humanErr, humanCode := runSlick(t, root, nil, "artifacts")
	if humanCode != 0 || humanErr != "" || !strings.Contains(human, "total emitted bytes:") || !strings.Contains(human, "runtime import demo (require)") {
		t.Fatalf("human exit %d, stderr %q, output %q", humanCode, humanErr, human)
	}
}

func artifactProject(t *testing.T, module, resolution string) string {
	t.Helper()
	config := `{
		"compilerOptions":{"strict":true,"target":"ES2022","module":"` + module + `","moduleResolution":"` + resolution + `","outDir":"dist","declaration":true,"sourceMap":true,"skipLibCheck":true},
		"include":["src"]
	}`
	return project(t, config, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/main.ts": `import { readFileSync } from "fs";
import { value, type Thing } from "demo";
void readFileSync;
export const result: Thing = { value };
console.log(result.value);`,
		"src/globals.d.ts":               `declare module "fs" { export function readFileSync(name: string): Uint8Array; }`,
		"node_modules/demo/package.json": `{"name":"demo","version":"1.0.0","main":"index.js","types":"index.d.ts"}`,
		"node_modules/demo/index.d.ts":   `export interface Thing { value: number } export declare const value: number;`,
		"node_modules/demo/index.js":     `exports.value = 42;`,
	})
}

func integerString(value int) string {
	return strconv.Itoa(value)
}

func decodeArtifacts(t *testing.T, output string) artifactsDocument {
	t.Helper()
	var document artifactsDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "artifacts" {
		t.Fatalf("unexpected document: %+v", document)
	}
	return document
}
