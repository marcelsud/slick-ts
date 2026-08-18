package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallOutputsRollsBackOnFailure(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	writeTestFile(t, filepath.Join(stage, "files", "0"), "new first")
	writeTestFile(t, filepath.Join(stage, "files", "1"), "new second")
	first := filepath.Join(root, "out", "a.js")
	writeTestFile(t, first, "old first")
	blocker := filepath.Join(root, "out", "z")
	writeTestFile(t, blocker, "keep blocker")

	err := installOutputs(context.Background(), stage, []BuildOutput{
		{Path: first, Staged: "files/0"},
		{Path: filepath.Join(blocker, "main.js"), Staged: "files/1"},
	})
	if err == nil {
		t.Fatal("install unexpectedly succeeded")
	}
	if content, readErr := os.ReadFile(first); readErr != nil || string(content) != "old first" {
		t.Fatalf("first output was not restored: content=%q err=%v", content, readErr)
	}
	if content, readErr := os.ReadFile(blocker); readErr != nil || string(content) != "keep blocker" {
		t.Fatalf("blocker changed: content=%q err=%v", content, readErr)
	}
}

func TestInstallOutputsDoesNothingAfterInterrupt(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	writeTestFile(t, filepath.Join(stage, "files", "0"), "new")
	final := filepath.Join(root, "dist", "main.js")
	writeTestFile(t, final, "old")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := installOutputs(ctx, stage, []BuildOutput{{Path: final, Staged: "files/0"}}); err == nil {
		t.Fatal("interrupted install unexpectedly succeeded")
	}
	if content, err := os.ReadFile(final); err != nil || string(content) != "old" {
		t.Fatalf("interrupted install changed output: content=%q err=%v", content, err)
	}
}
func TestInstallOutputsPreservesModes(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	existingStage := filepath.Join(stage, "files", "0")
	newStage := filepath.Join(stage, "files", "1")
	writeTestFile(t, existingStage, "replace")
	writeTestFile(t, newStage, "create")
	if err := os.Chmod(existingStage, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(newStage, 0o600); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(root, "dist", "a.js")
	created := filepath.Join(root, "dist", "b.js")
	writeTestFile(t, existing, "old")
	if err := os.Chmod(existing, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := installOutputs(context.Background(), stage, []BuildOutput{
		{Path: existing, Staged: "files/0"},
		{Path: created, Staged: "files/1"},
	}); err != nil {
		t.Fatal(err)
	}
	existingInfo, _ := os.Stat(existing)
	createdInfo, _ := os.Stat(created)
	if existingInfo.Mode().Perm() != 0o750 || createdInfo.Mode().Perm() != 0o600 {
		t.Fatalf("modes existing=%o created=%o", existingInfo.Mode().Perm(), createdInfo.Mode().Perm())
	}
}

func TestInstallOutputsRestoresDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	writeTestFile(t, filepath.Join(stage, "files", "0"), "new first")
	writeTestFile(t, filepath.Join(stage, "files", "1"), "new second")
	link := filepath.Join(root, "out", "a.js")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", link); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, "out", "z")
	writeTestFile(t, blocker, "keep")

	err := installOutputs(context.Background(), stage, []BuildOutput{
		{Path: link, Staged: "files/0"},
		{Path: filepath.Join(blocker, "main.js"), Staged: "files/1"},
	})
	if err == nil {
		t.Fatal("install unexpectedly succeeded")
	}
	info, statErr := os.Lstat(link)
	target, readErr := os.Readlink(link)
	if statErr != nil || readErr != nil || info.Mode()&os.ModeSymlink == 0 || target != "missing-target" {
		t.Fatalf("dangling symlink not restored: info=%v target=%q statErr=%v readErr=%v", info, target, statErr, readErr)
	}
}
