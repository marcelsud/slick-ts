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
