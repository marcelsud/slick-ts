package slick_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type buildDocument struct {
	Version     int          `json:"version"`
	Command     string       `json:"command"`
	Success     bool         `json:"success"`
	Project     string       `json:"project"`
	Diagnostics []diagnostic `json:"diagnostics"`
	Outputs     []string     `json:"outputs"`
	Error       *failure     `json:"error"`
}

const buildConfig = `{
	"compilerOptions": {
		"strict": true,
		"target": "ES2022",
		"module": "NodeNext",
		"moduleResolution": "NodeNext",
		"rootDir": "src",
		"outDir": "dist",
		"sourceMap": true,
		"declaration": true,
		"skipLibCheck": true
	},
	"include": ["src/**/*.ts"]
}`

func TestBuildEmitsTypeScriptEquivalentRunnableJavaScript(t *testing.T) {
	root := project(t, buildConfig, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/main.ts": `
export function greeting(name: string): string { return "hello " + name; }
console.log(greeting("slick"));
`,
	})
	output, stderr, code := runSlick(t, root, nil, "build", "--json")
	document := decodeBuild(t, output)
	expectedOutputs := []string{"dist/main.d.ts", "dist/main.js", "dist/main.js.map"}
	if code != 0 || stderr != "" || !document.Success || !reflect.DeepEqual(document.Outputs, expectedOutputs) {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}

	command := exec.Command("node", filepath.Join(root, "dist", "main.js"))
	runtimeOutput, err := command.CombinedOutput()
	if err != nil || string(runtimeOutput) != "hello slick\n" {
		t.Fatalf("run emitted JavaScript: err=%v output=%q", err, runtimeOutput)
	}

	reference := filepath.Join(root, "reference")
	tsc := filepath.Join(filepath.Dir(compilerPath), "tsc.js")
	command = exec.Command("node", tsc, "--project", root, "--outDir", reference)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run TypeScript emit: %v\n%s", err, output)
	}
	for _, name := range []string{"main.js", "main.js.map", "main.d.ts"} {
		slick, err := os.ReadFile(filepath.Join(root, "dist", name))
		if err != nil {
			t.Fatal(err)
		}
		typeScript, err := os.ReadFile(filepath.Join(reference, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(slick, typeScript) {
			t.Fatalf("%s differs from TypeScript emit:\n%s\n%s", name, slick, typeScript)
		}
		if strings.Contains(strings.ToLower(string(slick)), "effect") {
			t.Fatalf("%s contains an injected Effect runtime", name)
		}
	}
}

func TestBuildErrorsLeaveExistingOutputUnchanged(t *testing.T) {
	tests := []struct {
		name   string
		source string
		code   int
	}{
		{"Slick error", `fetch("https://example.test");`, 1004},
		{"TypeScript error", `const value: string = 42;`, 2322},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := project(t, buildConfig, map[string]string{
				"package.json":      `{"type":"module"}`,
				"src/main.ts":       test.source,
				"dist/sentinel.txt": "keep",
			})
			output, stderr, code := runSlick(t, root, nil, "build", "--json")
			document := decodeBuild(t, output)
			if code != 1 || stderr != "" || document.Success || len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != test.code || len(document.Outputs) != 0 {
				t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
			}
			if content, err := os.ReadFile(filepath.Join(root, "dist", "sentinel.txt")); err != nil || string(content) != "keep" {
				t.Fatalf("sentinel changed: content=%q err=%v", content, err)
			}
			if _, err := os.Stat(filepath.Join(root, "dist", "main.js")); !os.IsNotExist(err) {
				t.Fatalf("build installed output after error: %v", err)
			}
		})
	}
}

func TestBuildInstallFailurePreservesExistingPath(t *testing.T) {
	root := project(t, buildConfig, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/main.ts":  `export const answer: number = 42;`,
		"dist":         "keep",
	})
	output, stderr, code := runSlick(t, root, nil, "build", "--json")
	document := decodeBuild(t, output)
	if code != 1 || stderr != "" || document.Error == nil || document.Error.Kind != "emit_failure" || len(document.Outputs) != 0 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	if content, err := os.ReadFile(filepath.Join(root, "dist")); err != nil || string(content) != "keep" {
		t.Fatalf("pre-existing path changed: content=%q err=%v", content, err)
	}
}

func TestBuildSourceMapsRuntimeFailureToTypeScript(t *testing.T) {
	root := project(t, buildConfig, map[string]string{
		"package.json": `{"type":"module"}`,
		"src/main.ts": `export function crash(): never {
	throw new Error("boom");
}
crash();
`,
	})
	output, stderr, code := runSlick(t, root, nil, "build", "--json")
	if code != 0 || stderr != "" || !decodeBuild(t, output).Success {
		t.Fatalf("exit %d, stderr %q, output %s", code, stderr, output)
	}
	command := exec.Command("node", "--enable-source-maps", filepath.Join(root, "dist", "main.js"))
	runtimeOutput, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(runtimeOutput), filepath.ToSlash(filepath.Join("src", "main.ts"))+":2") {
		t.Fatalf("source-mapped failure: err=%v output=%s", err, runtimeOutput)
	}
}

func decodeBuild(t *testing.T, output string) buildDocument {
	t.Helper()
	var document buildDocument
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if document.Version != 1 || document.Command != "build" || document.Project != "tsconfig.json" {
		t.Fatalf("unexpected build document: %+v", document)
	}
	return document
}
