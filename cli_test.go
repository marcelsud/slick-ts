package slick_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var (
	slickPath    string
	compilerPath string
)

type checkOutput struct {
	Version     int          `json:"version"`
	Command     string       `json:"command"`
	Success     bool         `json:"success"`
	Project     string       `json:"project"`
	Diagnostics []diagnostic `json:"diagnostics"`
	Error       *failure     `json:"error"`
}

type diagnostic struct {
	Source      string       `json:"source"`
	Code        int          `json:"code"`
	Category    string       `json:"category"`
	Title       string       `json:"title"`
	Message     string       `json:"message"`
	Explanation string       `json:"explanation"`
	Fact        string       `json:"fact"`
	Repairs     []string     `json:"repairs"`
	Path        string       `json:"path"`
	Range       *sourceRange `json:"range"`
}

type sourceRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
	Offset int `json:"offset"`
}

type failure struct {
	Kind         string   `json:"kind"`
	Message      string   `json:"message"`
	Alternatives []string `json:"alternatives"`
}

func TestMain(m *testing.M) {
	temp, err := os.MkdirTemp("", "slick-cli-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(temp)
	slickPath = filepath.Join(temp, "slick")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", slickPath, "./cmd/slick")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build slick: %v\n%s", err, output)
		os.Exit(1)
	}
	compilerPath, err = filepath.Abs(filepath.Join("node_modules", "typescript", "lib", "typescript.js"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestCheckValidProject(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `export const answer: number = 42;`,
	})

	humanOut, humanErr, code := runSlick(t, root, nil, "check")
	if code != 0 || humanOut != "" || humanErr != "" {
		t.Fatalf("human exit %d, stdout %q, stderr %q", code, humanOut, humanErr)
	}
	jsonOut, jsonErr, code := runSlick(t, root, nil, "check", "--json")
	if code != 0 || jsonErr != "" {
		t.Fatalf("json exit %d, stderr %q", code, jsonErr)
	}
	document := decodeOutput(t, jsonOut)
	if document.Version != 1 || document.Command != "check" || !document.Success || document.Project != "tsconfig.json" || len(document.Diagnostics) != 0 || document.Error != nil {
		t.Fatalf("unexpected output: %+v", document)
	}
}

func TestCheckSyntaxError(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `const broken = ;`,
	})
	output, _, code := runSlick(t, root, nil, "check", "--json")
	document := decodeOutput(t, output)
	if code != 1 || document.Success || len(document.Diagnostics) != 1 {
		t.Fatalf("exit %d, output %+v", code, document)
	}
	assertDiagnostic(t, document.Diagnostics[0], 1109, "Expression expected.", "src/main.ts", 1, 16)
}

func TestCheckTypeError(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/main.ts": `const value: string = 42;`,
	})
	output, stderr, code := runSlick(t, root, nil, "check", "--json")
	document := decodeOutput(t, output)
	if code != 1 || stderr != "" || len(document.Diagnostics) != 1 {
		t.Fatalf("exit %d, stderr %q, output %+v", code, stderr, document)
	}
	assertDiagnostic(t, document.Diagnostics[0], 2322, "Type 'number' is not assignable to type 'string'.", "src/main.ts", 1, 7)

	human, _, humanCode := runSlick(t, root, nil, "check")
	expected := "src/main.ts:1:7 - error TS2322: Type 'number' is not assignable to type 'string'.\n"
	if humanCode != 1 || human != expected {
		t.Fatalf("human exit %d, output %q", humanCode, human)
	}
}

func TestCheckModuleResolutionError(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true,"module":"NodeNext","moduleResolution":"NodeNext"},"include":["src"]}`, map[string]string{
		"src/main.ts": `import value from "missing-package"; export { value };`,
	})
	output, _, code := runSlick(t, root, nil, "check", "--json")
	document := decodeOutput(t, output)
	if code != 1 || len(document.Diagnostics) != 1 {
		t.Fatalf("exit %d, output %+v", code, document)
	}
	assertDiagnostic(t, document.Diagnostics[0], 2307, "Cannot find module 'missing-package' or its corresponding type declarations.", "src/main.ts", 1, 19)
}

func TestCheckInvalidConfiguration(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":"yes"},"include":["src"]}`, map[string]string{
		"src/main.ts": `export {};`,
	})
	output, _, code := runSlick(t, root, nil, "check", "--json")
	document := decodeOutput(t, output)
	if code != 1 || document.Error == nil || document.Error.Kind != "invalid_configuration" || len(document.Diagnostics) != 1 {
		t.Fatalf("exit %d, output %+v", code, document)
	}
	assertDiagnostic(t, document.Diagnostics[0], 5024, "Compiler option 'strict' requires a value of type boolean.", "tsconfig.json", 1, 30)
}

func TestCheckTransitiveProjectReferenceFailure(t *testing.T) {
	root := project(t, `{"files":["index.ts"],"references":[{"path":"./packages/middle"}]}`, map[string]string{
		"index.ts":                      `export {};`,
		"packages/middle/index.ts":      `export {};`,
		"packages/middle/tsconfig.json": `{"compilerOptions":{"composite":true},"files":["index.ts"],"references":[{"path":"../missing"}]}`,
	})
	output, _, code := runSlick(t, root, nil, "check", "--json")
	document := decodeOutput(t, output)
	if code != 1 || document.Error == nil || document.Error.Kind != "project_reference" || len(document.Diagnostics) == 0 {
		t.Fatalf("exit %d, output %+v", code, document)
	}
}

func TestCheckDiscoversProjectFromNestedDirectory(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/nested/main.ts": `const value: string = 42;`,
	})
	fromRoot, _, rootCode := runSlick(t, root, nil, "check", "--json")
	fromNested, _, nestedCode := runSlick(t, filepath.Join(root, "src", "nested"), nil, "check", "--json")
	if rootCode != 1 || nestedCode != 1 || fromRoot != fromNested {
		t.Fatalf("root exit %d, nested exit %d\nroot: %s\nnested: %s", rootCode, nestedCode, fromRoot, fromNested)
	}
}

func TestCheckJSONIsDeterministic(t *testing.T) {
	root := project(t, `{"compilerOptions":{"strict":true},"include":["src"]}`, map[string]string{
		"src/z.ts": `const z: string = 1;`,
		"src/a.ts": `const a: string = 2;`,
	})
	first, _, firstCode := runSlick(t, root, nil, "check", "--json")
	second, _, secondCode := runSlick(t, root, nil, "check", "--json")
	if firstCode != 1 || secondCode != 1 || first != second {
		t.Fatalf("outputs differ:\n%s\n%s", first, second)
	}
	document := decodeOutput(t, first)
	if len(document.Diagnostics) != 2 || document.Diagnostics[0].Path != "src/a.ts" || document.Diagnostics[1].Path != "src/z.ts" {
		t.Fatalf("unstable ordering: %+v", document.Diagnostics)
	}
}

func TestCheckDistinguishesInfrastructureFailures(t *testing.T) {
	t.Run("missing configuration", func(t *testing.T) {
		output, _, code := runSlick(t, t.TempDir(), nil, "check", "--json")
		assertFailure(t, output, code, "missing_configuration")
	})

	t.Run("missing toolchain", func(t *testing.T) {
		root := project(t, `{}`, map[string]string{"index.ts": `export {};`})
		output, _, code := runSlick(t, root, []string{"PATH="}, "check", "--json")
		assertFailure(t, output, code, "missing_toolchain")
	})

	t.Run("unsupported toolchain", func(t *testing.T) {
		root := project(t, `{}`, map[string]string{"index.ts": `export {};`})
		fake := filepath.Join(t.TempDir(), "typescript.cjs")
		writeFile(t, fake, `module.exports = { version: "0.0.0" };`)
		output, _, code := runSlick(t, root, []string{"SLICK_TYPESCRIPT_PATH=" + fake}, "check", "--json")
		assertFailure(t, output, code, "unsupported_toolchain")
	})

	t.Run("analyzer failure", func(t *testing.T) {
		root := project(t, `{}`, map[string]string{"index.ts": `export {};`})
		fake := filepath.Join(t.TempDir(), "typescript.cjs")
		writeFile(t, fake, `module.exports = { version: "5.9.3" };`)
		output, _, code := runSlick(t, root, []string{"SLICK_TYPESCRIPT_PATH=" + fake}, "check", "--json")
		assertFailure(t, output, code, "analyzer_failure")
	})
}

func TestInterruptStopsAnalyzerProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process ownership is observed through procfs")
	}
	root := project(t, `{"include":["src"]}`, map[string]string{"src/main.ts": `export const value = 1;`})
	hold := filepath.Join(t.TempDir(), "hold.cjs")
	writeFile(t, hold, `setInterval(() => {}, 1000);`)
	command := exec.Command(slickPath, "check", "--json")
	command.Dir = root
	command.Env = append(os.Environ(), "SLICK_TYPESCRIPT_PATH="+compilerPath, "NODE_OPTIONS=--require="+hold)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	child := waitForChild(t, command.Process.Pid)
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case err := <-wait:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 130 {
			t.Fatalf("expected exit 130, got %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("slick did not stop after interrupt")
	}
	for deadline := time.Now().Add(2 * time.Second); processExists(child) && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if processExists(child) {
		t.Fatalf("analyzer process %d survived slick", child)
	}
}

func project(t *testing.T, config string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "tsconfig.json"), config)
	for name, content := range files {
		writeFile(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
	return root
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runSlick(t *testing.T, directory string, extraEnv []string, args ...string) (string, string, int) {
	t.Helper()
	command := exec.Command(slickPath, args...)
	command.Dir = directory
	command.Env = append(os.Environ(), "SLICK_TYPESCRIPT_PATH="+compilerPath)
	for _, variable := range extraEnv {
		name := strings.SplitN(variable, "=", 2)[0] + "="
		filtered := command.Env[:0]
		for _, existing := range command.Env {
			if !strings.HasPrefix(existing, name) {
				filtered = append(filtered, existing)
			}
		}
		command.Env = append(filtered, variable)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("run slick: %v", err)
	}
	return stdout.String(), stderr.String(), exit.ExitCode()
}

func decodeOutput(t *testing.T, output string) checkOutput {
	t.Helper()
	var document checkOutput
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	return document
}

func assertDiagnostic(t *testing.T, got diagnostic, code int, message, path string, line, column int) {
	t.Helper()
	if got.Source != "typescript" || got.Code != code || got.Category != "error" || got.Message != message || got.Path != path || got.Range == nil || got.Range.Start.Line != line || got.Range.Start.Column != column {
		t.Fatalf("unexpected diagnostic: %+v", got)
	}
}

func assertFailure(t *testing.T, output string, code int, kind string) {
	t.Helper()
	document := decodeOutput(t, output)
	if code != 1 || document.Success || document.Error == nil || document.Error.Kind != kind {
		t.Fatalf("exit %d, output %+v", code, document)
	}
}

func waitForChild(t *testing.T, parent int) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			pid, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			status, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "status"))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(status), "\n") {
				if strings.HasPrefix(line, "PPid:") && strings.TrimSpace(strings.TrimPrefix(line, "PPid:")) == strconv.Itoa(parent) {
					return pid
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("analyzer process did not start")
	return 0
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
