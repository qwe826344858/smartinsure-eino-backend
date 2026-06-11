package replay

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunCaseRunnerSuccess(t *testing.T) {
	c := sampleCase("LLM-020", "intent")
	runner := RunnerFunc(func(ctx context.Context, promptMessages []Message) (string, error) {
		if len(promptMessages) != 1 {
			t.Fatalf("len(promptMessages) = %d, want 1", len(promptMessages))
		}
		if promptMessages[0].Content != c.PromptMessages[0].Content {
			t.Fatalf("prompt content = %q", promptMessages[0].Content)
		}
		return c.Expected, nil
	})

	got := RunCase(context.Background(), runner, c)
	if got.Error != "" {
		t.Fatalf("RunCase() error = %q", got.Error)
	}
	if got.NewOutput != c.Expected {
		t.Fatalf("NewOutput = %q, want %q", got.NewOutput, c.Expected)
	}
	if !got.Diff.ExactMatch || !got.Diff.ContainsExpected || !got.Passed {
		t.Fatalf("diff = %#v passed = %v", got.Diff, got.Passed)
	}
}

func TestRunCaseRunnerFailure(t *testing.T) {
	wantErr := errors.New("llm unavailable")
	runner := RunnerFunc(func(ctx context.Context, promptMessages []Message) (string, error) {
		return "", wantErr
	})

	got := RunCase(context.Background(), runner, sampleCase("LLM-021", "answer"))
	if !strings.Contains(got.Error, wantErr.Error()) {
		t.Fatalf("Error = %q, want %q", got.Error, wantErr.Error())
	}
	if got.NewOutput != "" {
		t.Fatalf("NewOutput = %q, want empty", got.NewOutput)
	}
	if got.Passed {
		t.Fatal("Passed = true, want false")
	}
}

func TestCompareOutputs(t *testing.T) {
	diff := Compare("expected answer", "old answer", "prefix EXPECTED ANSWER suffix")
	if diff.ExactMatch {
		t.Fatal("ExactMatch = true, want false")
	}
	if !diff.ContainsExpected {
		t.Fatal("ContainsExpected = false, want true")
	}
	if !diff.Changed {
		t.Fatal("Changed = false, want true")
	}
	if diff.Summary != "new output contains expected" {
		t.Fatalf("Summary = %q", diff.Summary)
	}

	exact := Compare("expected answer", "old answer", " expected answer ")
	if !exact.ExactMatch || !exact.ContainsExpected {
		t.Fatalf("exact diff = %#v", exact)
	}
}

func TestSuiteRunAllFiltersStage(t *testing.T) {
	cases := []Case{
		sampleCase("LLM-030", "intent"),
		sampleCase("LLM-031", "query"),
		sampleCase("LLM-032", "intent"),
	}
	runs := 0
	suite := NewSuite(cases, RunnerFunc(func(ctx context.Context, promptMessages []Message) (string, error) {
		runs++
		return "product_recommendation with followup", nil
	}))

	got, err := suite.RunAll(context.Background(), "intent")
	if err != nil {
		t.Fatalf("RunAll() returned error: %v", err)
	}
	if runs != 2 {
		t.Fatalf("runs = %d, want 2", runs)
	}
	if len(got.Results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(got.Results))
	}
	if got.Summary.Total != 2 || got.Summary.Passed != 2 || got.Summary.Failed != 0 {
		t.Fatalf("Summary = %#v", got.Summary)
	}
	for _, result := range got.Results {
		if result.Stage != "intent" {
			t.Fatalf("Stage = %q, want intent", result.Stage)
		}
	}
}

func TestSuiteSummaryCounts(t *testing.T) {
	cases := []Case{
		{
			CaseID:         "LLM-040",
			Stage:          "intent",
			PromptMessages: []Message{{Role: "user", Content: "exact"}},
			RawOutput:      "old",
			Expected:       "expected",
		},
		{
			CaseID:         "LLM-041",
			Stage:          "answer",
			PromptMessages: []Message{{Role: "user", Content: "changed"}},
			RawOutput:      "old",
		},
		{
			CaseID:         "LLM-042",
			Stage:          "answer",
			PromptMessages: []Message{{Role: "user", Content: "fail"}},
			RawOutput:      "old",
			Expected:       "expected",
		},
	}
	suite := NewSuite(cases, RunnerFunc(func(ctx context.Context, promptMessages []Message) (string, error) {
		switch promptMessages[0].Content {
		case "exact":
			return "expected", nil
		case "changed":
			return "new", nil
		default:
			return "", errors.New("runner failed")
		}
	}))

	got, err := suite.RunAll(context.Background(), "")
	if err != nil {
		t.Fatalf("RunAll() returned error: %v", err)
	}
	want := Summary{
		Total:            3,
		Passed:           2,
		Failed:           1,
		Errored:          1,
		ExactMatches:     1,
		ContainsExpected: 1,
		Changed:          2,
	}
	if got.Summary != want {
		t.Fatalf("Summary = %#v, want %#v", got.Summary, want)
	}
}
