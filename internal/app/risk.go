package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type RiskInput struct {
	Symbol     string   `json:"symbol"`
	Path       string   `json:"path"`
	Range      Range    `json:"range"`
	Complexity int      `json:"complexity"`
	Coverage   *float64 `json:"coverage"`
	FanIn      int      `json:"fanIn"`
}

type RiskComponents struct {
	ChangedLines float64  `json:"changedLines"`
	Churn        float64  `json:"churn"`
	Authors      float64  `json:"authors"`
	Complexity   float64  `json:"complexity"`
	Uncovered    *float64 `json:"uncovered,omitempty"`
	FanIn        float64  `json:"fanIn"`
}

type RiskResult struct {
	RiskInput
	ChangedLineCount int            `json:"changedLineCount"`
	CommitCount      int            `json:"commitCount"`
	ChurnLines       int            `json:"churnLines"`
	AuthorCount      int            `json:"authorCount"`
	Components       RiskComponents `json:"components"`
	Missing          []string       `json:"missing"`
	Score            float64        `json:"score"`
}

type RiskReport struct {
	Base      string             `json:"base"`
	History   string             `json:"history"`
	Threshold float64            `json:"threshold"`
	Weights   map[string]float64 `json:"weights"`
	Results   []RiskResult       `json:"results"`
}

type riskConfig struct {
	Threshold float64            `json:"threshold"`
	Weights   map[string]float64 `json:"weights"`
}

type historyStats struct {
	commits, churn int
	authors        map[string]struct{}
}

func loadRiskConfig(name string) (riskConfig, error) {
	config := riskConfig{Weights: map[string]float64{"changedLines": .25, "churn": .20, "authors": .10, "complexity": .20, "uncovered": .15, "fanIn": .10}}
	content, err := os.ReadFile(name)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return config, err
	}
	if err := json.Unmarshal(content, &config); err != nil {
		return config, err
	}
	allowed := map[string]bool{"changedLines": true, "churn": true, "authors": true, "complexity": true, "uncovered": true, "fanIn": true}
	if len(config.Weights) == 0 {
		return config, fmt.Errorf("weights must not be empty")
	}
	for name, value := range config.Weights {
		if !allowed[name] || value < 0 {
			return config, fmt.Errorf("invalid risk weight %q", name)
		}
	}
	if config.Threshold < 0 || config.Threshold > 100 {
		return config, fmt.Errorf("threshold must be between 0 and 100")
	}
	return config, nil
}

func gitHistory(ctx context.Context, root, path, since string) (historyStats, error) {
	command := exec.CommandContext(ctx, "git", "log", "--since="+since, "--format=commit:%H:%ae", "--numstat", "--", path)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		return historyStats{}, fmt.Errorf("git history %s: %s", path, strings.TrimSpace(string(output)))
	}
	stats := historyStats{authors: map[string]struct{}{}}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "commit:") {
			stats.commits++
			parts := strings.Split(line, ":")
			if len(parts) >= 3 {
				stats.authors[parts[len(parts)-1]] = struct{}{}
			}
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) >= 2 {
			added, addErr := strconv.Atoi(fields[0])
			deleted, delErr := strconv.Atoi(fields[1])
			if addErr == nil {
				stats.churn += added
			}
			if delErr == nil {
				stats.churn += deleted
			}
		}
	}
	return stats, nil
}

func scoreRisk(input RiskInput, changedCount int, history historyStats, weights map[string]float64, shallow bool) RiskResult {
	components := RiskComponents{
		ChangedLines: clamp(float64(changedCount) / 50),
		Churn:        clamp(float64(history.churn) / 500),
		Authors:      clamp(float64(len(history.authors)) / 5),
		Complexity:   clamp(float64(input.Complexity) / 20),
		FanIn:        clamp(float64(input.FanIn) / 20),
	}
	missing := []string{}
	values := map[string]*float64{
		"changedLines": &components.ChangedLines, "churn": &components.Churn, "authors": &components.Authors,
		"complexity": &components.Complexity, "fanIn": &components.FanIn,
	}
	if input.Coverage == nil {
		missing = append(missing, "coverage")
	} else {
		uncovered := 1 - *input.Coverage
		components.Uncovered = &uncovered
		values["uncovered"] = components.Uncovered
	}
	if shallow {
		missing = append(missing, "shallow_history")
	}
	weighted, totalWeight := 0.0, 0.0
	for name, weight := range weights {
		value := values[name]
		if value == nil {
			continue
		}
		weighted += *value * weight
		totalWeight += weight
	}
	score := 0.0
	if totalWeight > 0 {
		score = weighted / totalWeight * 100
	}
	sort.Strings(missing)
	return RiskResult{RiskInput: input, ChangedLineCount: changedCount, CommitCount: history.commits, ChurnLines: history.churn, AuthorCount: len(history.authors), Components: components, Missing: missing, Score: score}
}

func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
