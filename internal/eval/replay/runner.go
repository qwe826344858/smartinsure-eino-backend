package replay

import (
	"context"
	"fmt"
	"strings"
)

// Runner executes a recorded prompt and returns the new raw model output.
type Runner interface {
	Run(ctx context.Context, promptMessages []Message) (string, error)
}

// RunnerFunc adapts a function into a Runner.
type RunnerFunc func(ctx context.Context, promptMessages []Message) (string, error)

func (f RunnerFunc) Run(ctx context.Context, promptMessages []Message) (string, error) {
	if f == nil {
		return "", fmt.Errorf("replay runner func is nil")
	}
	return f(ctx, promptMessages)
}

type Diff struct {
	Expected         string `json:"expected,omitempty"`
	RawOutput        string `json:"raw_output"`
	NewOutput        string `json:"new_output"`
	ExactMatch       bool   `json:"exact_match"`
	ContainsExpected bool   `json:"contains_expected"`
	Changed          bool   `json:"changed"`
	Summary          string `json:"summary"`
}

func Compare(expected, rawOutput, newOutput string) Diff {
	trimmedExpected := strings.TrimSpace(expected)
	trimmedRaw := strings.TrimSpace(rawOutput)
	trimmedNew := strings.TrimSpace(newOutput)

	exactMatch := trimmedExpected != "" && trimmedNew == trimmedExpected
	containsExpected := trimmedExpected != "" && strings.Contains(
		strings.ToLower(trimmedNew),
		strings.ToLower(trimmedExpected),
	)
	changed := trimmedRaw != trimmedNew

	summary := "output unchanged"
	switch {
	case exactMatch:
		summary = "new output exactly matches expected"
	case containsExpected:
		summary = "new output contains expected"
	case trimmedExpected != "" && changed:
		summary = "new output changed but does not contain expected"
	case trimmedExpected != "":
		summary = "new output unchanged and does not contain expected"
	case changed:
		summary = "new output changed"
	}

	return Diff{
		Expected:         expected,
		RawOutput:        rawOutput,
		NewOutput:        newOutput,
		ExactMatch:       exactMatch,
		ContainsExpected: containsExpected,
		Changed:          changed,
		Summary:          summary,
	}
}

func (d Diff) Passed() bool {
	if strings.TrimSpace(d.Expected) == "" {
		return d.Changed
	}
	return d.ExactMatch || d.ContainsExpected
}

type RunResult struct {
	CaseID    string `json:"case_id"`
	Stage     string `json:"stage"`
	RawOutput string `json:"raw_output"`
	NewOutput string `json:"new_output,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Error     string `json:"error,omitempty"`
	Diff      Diff   `json:"diff"`
	Passed    bool   `json:"passed"`
}

func RunCase(ctx context.Context, runner Runner, c Case) RunResult {
	if ctx == nil {
		ctx = context.Background()
	}

	result := RunResult{
		CaseID:    c.CaseID,
		Stage:     c.Stage,
		RawOutput: c.RawOutput,
		Expected:  c.Expected,
	}
	if runner == nil {
		result.Error = "replay runner is nil"
		result.Diff = Compare(c.Expected, c.RawOutput, "")
		return result
	}

	promptMessages := append([]Message(nil), c.PromptMessages...)
	newOutput, err := runner.Run(ctx, promptMessages)
	if err != nil {
		result.Error = err.Error()
		result.Diff = Compare(c.Expected, c.RawOutput, "")
		return result
	}

	result.NewOutput = newOutput
	result.Diff = Compare(c.Expected, c.RawOutput, newOutput)
	result.Passed = result.Diff.Passed()
	return result
}

type Suite struct {
	cases  []Case
	runner Runner
}

func NewSuite(cases []Case, runner Runner) *Suite {
	return &Suite{
		cases:  append([]Case(nil), cases...),
		runner: runner,
	}
}

type SuiteResult struct {
	Results []RunResult `json:"results"`
	Summary Summary     `json:"summary"`
}

type Summary struct {
	Total            int `json:"total"`
	Passed           int `json:"passed"`
	Failed           int `json:"failed"`
	Errored          int `json:"errored"`
	ExactMatches     int `json:"exact_matches"`
	ContainsExpected int `json:"contains_expected"`
	Changed          int `json:"changed"`
}

func (s *Suite) RunAll(ctx context.Context, stage string) (SuiteResult, error) {
	if s == nil {
		return SuiteResult{}, fmt.Errorf("replay suite is nil")
	}
	if s.runner == nil {
		return SuiteResult{}, fmt.Errorf("replay runner is nil")
	}

	stage = strings.TrimSpace(stage)
	results := make([]RunResult, 0, len(s.cases))
	for _, c := range s.cases {
		if stage != "" && c.Stage != stage {
			continue
		}
		results = append(results, RunCase(ctx, s.runner, c))
	}

	return SuiteResult{
		Results: results,
		Summary: Summarize(results),
	}, nil
}

func Summarize(results []RunResult) Summary {
	var summary Summary
	summary.Total = len(results)
	for _, result := range results {
		if result.Error != "" {
			summary.Errored++
			summary.Failed++
			continue
		}
		if result.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
		if result.Diff.ExactMatch {
			summary.ExactMatches++
		}
		if result.Diff.ContainsExpected {
			summary.ContainsExpected++
		}
		if result.Diff.Changed {
			summary.Changed++
		}
	}
	return summary
}
