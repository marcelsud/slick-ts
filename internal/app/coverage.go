package app

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type CoverageLine struct {
	Line    int  `json:"line"`
	Covered bool `json:"covered"`
}

type CoverageFile struct {
	Path          string         `json:"path"`
	BranchCovered int            `json:"branchCovered"`
	BranchTotal   int            `json:"branchTotal"`
	Lines         []CoverageLine `json:"lines"`
}

type CoverageFunction struct {
	Symbol             string `json:"symbol"`
	Path               string `json:"path"`
	Range              Range  `json:"range"`
	Complexity         int    `json:"complexity"`
	UncoveredDecisions int    `json:"uncoveredDecisions"`
}

type CoverageReport struct {
	BranchCovered int                `json:"branchCovered"`
	BranchTotal   int                `json:"branchTotal"`
	Files         []CoverageFile     `json:"files"`
	Functions     []CoverageFunction `json:"functions"`
}

type CoverageSummary struct {
	BranchPercent      float64 `json:"branchPercent"`
	ChangedCovered     int     `json:"changedCovered"`
	ChangedTotal       int     `json:"changedTotal"`
	ChangedLinePercent float64 `json:"changedLinePercent"`
}

var hunkPattern = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

func gitChangedLines(ctx context.Context, root, base string) (map[string]map[int]struct{}, error) {
	if base == "" {
		return map[string]map[int]struct{}{}, nil
	}
	command := exec.CommandContext(ctx, "git", "diff", "--unified=0", "--find-renames", base+"...HEAD", "--")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %s", base, strings.TrimSpace(string(output)))
	}
	result := map[string]map[int]struct{}{}
	current := ""
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "+++ ") {
			name := strings.TrimPrefix(line, "+++ ")
			if name == "/dev/null" {
				current = ""
			} else {
				current = filepath.ToSlash(strings.TrimPrefix(name, "b/"))
			}
			continue
		}
		match := hunkPattern.FindStringSubmatch(line)
		if current == "" || match == nil {
			continue
		}
		start, _ := strconv.Atoi(match[1])
		count := 1
		if match[2] != "" {
			count, _ = strconv.Atoi(match[2])
		}
		if _, ok := result[current]; !ok {
			result[current] = map[int]struct{}{}
		}
		for number := start; number < start+count; number++ {
			result[current][number] = struct{}{}
		}
	}
	return result, nil
}

func summarizeCoverage(report CoverageReport, changed map[string]map[int]struct{}) CoverageSummary {
	summary := CoverageSummary{BranchPercent: percent(report.BranchCovered, report.BranchTotal)}
	for _, file := range report.Files {
		changedFile := changed[file.Path]
		for _, line := range file.Lines {
			if _, ok := changedFile[line.Line]; !ok {
				continue
			}
			summary.ChangedTotal++
			if line.Covered {
				summary.ChangedCovered++
			}
		}
	}
	summary.ChangedLinePercent = percent(summary.ChangedCovered, summary.ChangedTotal)
	return summary
}

func percent(covered, total int) float64 {
	if total == 0 {
		return 100
	}
	return float64(covered) * 100 / float64(total)
}

func sortCoverageFunctions(values []CoverageFunction) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Path != values[j].Path {
			return values[i].Path < values[j].Path
		}
		return values[i].Range.Start.Offset < values[j].Range.Start.Offset
	})
}
