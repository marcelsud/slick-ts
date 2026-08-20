package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

type MutationCandidate struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Range       Range  `json:"range"`
	Replacement string `json:"replacement"`
	Operator    string `json:"operator"`
	Original    string `json:"original"`
	Symbol      string `json:"symbol,omitempty"`
}

type MutationResult struct {
	MutationCandidate
	Status string `json:"status"`
}

type MutationReport struct {
	TestCommand []string         `json:"testCommand"`
	Total       int              `json:"total"`
	Killed      int              `json:"killed"`
	Survived    int              `json:"survived"`
	TimedOut    int              `json:"timedOut"`
	Invalid     int              `json:"invalid"`
	NotCovered  int              `json:"notCovered"`
	Score       float64          `json:"score"`
	Results     []MutationResult `json:"results"`
}

func copyMutationProject(source, destination string) error {
	return filepath.WalkDir(source, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(destination, 0o755)
		}
		first := relative
		if separator := filepath.Separator; len(relative) > 0 {
			if index := strings.IndexByte(relative, byte(separator)); index >= 0 {
				first = relative[:index]
			}
		}
		if first == ".git" || first == "node_modules" || first == "dist" || first == "coverage" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(name)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, info.Mode().Perm())
	})
}

func runTestCommand(ctx context.Context, root string, timeout time.Duration, argv []string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, argv[0], argv[1:]...)
	command.Dir = root
	command.Env = os.Environ()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err := command.Run()
	if commandCtx.Err() != nil {
		if command.Process != nil {
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		}
		return "timed_out", commandCtx.Err()
	}
	if err != nil {
		return "killed", err
	}
	return "survived", nil
}

func applyMutation(root string, candidate MutationCandidate) ([]byte, error) {
	name := filepath.Join(root, filepath.FromSlash(candidate.Path))
	content, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	start, startOK := utf16ByteIndex(string(content), candidate.Range.Start.Offset)
	end, endOK := utf16ByteIndex(string(content), candidate.Range.End.Offset)
	if !startOK || !endOK || end < start {
		return nil, fmt.Errorf("invalid mutation range for %s", candidate.Path)
	}
	mutated := make([]byte, 0, len(content)+len(candidate.Replacement)-(end-start))
	mutated = append(mutated, content[:start]...)
	mutated = append(mutated, candidate.Replacement...)
	mutated = append(mutated, content[end:]...)
	if err := os.WriteFile(name, mutated, 0o644); err != nil {
		return nil, err
	}
	return content, nil
}

func utf16ByteIndex(value string, target int) (int, bool) {
	units := 0
	for index, current := range value {
		if units == target {
			return index, true
		}
		units += utf16.RuneLen(current)
		if units > target {
			return 0, false
		}
	}
	return len(value), units == target
}

func finishMutationReport(report *MutationReport) {
	for _, result := range report.Results {
		switch result.Status {
		case "killed":
			report.Killed++
		case "survived":
			report.Survived++
		case "timed_out":
			report.TimedOut++
		case "invalid":
			report.Invalid++
		case "not_covered":
			report.NotCovered++
		}
	}
	report.Total = len(report.Results)
	denominator := report.Killed + report.Survived
	if denominator > 0 {
		report.Score = float64(report.Killed) * 100 / float64(denominator)
	}
	sort.Slice(report.Results, func(i, j int) bool { return report.Results[i].ID < report.Results[j].ID })
}

func mutationTimedOut(err error) bool { return errors.Is(err, context.DeadlineExceeded) }
