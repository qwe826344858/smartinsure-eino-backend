package replay

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordWritesJSONLCase(t *testing.T) {
	rec := newTestRecorder(t)
	got, err := rec.Record(sampleCase("LLM-001", "intent"))
	if err != nil {
		t.Fatalf("Record() returned error: %v", err)
	}

	if got.File != rec.Path() {
		t.Fatalf("File = %q, want %q", got.File, rec.Path())
	}
	if got.RecordedAt != "2026-05-20T18:30:00Z" {
		t.Fatalf("RecordedAt = %q", got.RecordedAt)
	}
	if got.PromptVersion != PromptVersion {
		t.Fatalf("PromptVersion = %q, want %q", got.PromptVersion, PromptVersion)
	}

	cases, err := rec.List("")
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("len(cases) = %d, want 1", len(cases))
	}
	if cases[0].CaseID != "LLM-001" || cases[0].Stage != "intent" {
		t.Fatalf("case = %#v", cases[0])
	}
	if cases[0].Line != 1 || cases[0].File != rec.Path() {
		t.Fatalf("case location = line %d file %q", cases[0].Line, cases[0].File)
	}
}

func TestListFiltersByStage(t *testing.T) {
	rec := newTestRecorder(t)
	for _, c := range []Case{
		sampleCase("LLM-001", "intent"),
		sampleCase("LLM-002", "query"),
		sampleCase("LLM-003", "intent"),
	} {
		if _, err := rec.Record(c); err != nil {
			t.Fatalf("Record() returned error: %v", err)
		}
	}

	cases, err := rec.List("intent")
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}
	if len(cases) != 2 {
		t.Fatalf("len(cases) = %d, want 2", len(cases))
	}
	for _, c := range cases {
		if c.Stage != "intent" {
			t.Fatalf("stage = %q, want intent", c.Stage)
		}
	}
}

func TestLoadCaseByID(t *testing.T) {
	rec := newTestRecorder(t)
	if _, err := rec.Record(sampleCase("LLM-010", "answer")); err != nil {
		t.Fatalf("Record() returned error: %v", err)
	}

	got, err := rec.Load("LLM-010")
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if got.CaseID != "LLM-010" || got.Stage != "answer" {
		t.Fatalf("Load() = %#v", got)
	}
	if got.Line != 1 {
		t.Fatalf("Line = %d, want 1", got.Line)
	}
}

func TestLoadMissingCaseReturnsNotFound(t *testing.T) {
	rec := newTestRecorder(t)
	_, err := rec.Load("missing")
	if !errors.Is(err, ErrCaseNotFound) {
		t.Fatalf("Load() error = %v, want ErrCaseNotFound", err)
	}
}

func TestRecordRequiresCaseIDAndStage(t *testing.T) {
	rec := newTestRecorder(t)
	if _, err := rec.Record(sampleCase("", "intent")); err == nil {
		t.Fatal("Record() with empty case_id returned nil error")
	}
	if _, err := rec.Record(sampleCase("LLM-001", "")); err == nil {
		t.Fatal("Record() with empty stage returned nil error")
	}
}

func newTestRecorder(t *testing.T) *Recorder {
	t.Helper()
	rec, err := NewRecorder(filepath.Join(t.TempDir(), "replay_cases", "cases.jsonl"))
	if err != nil {
		t.Fatalf("NewRecorder() returned error: %v", err)
	}
	rec.now = func() time.Time {
		return time.Date(2026, 5, 20, 18, 30, 0, 0, time.UTC)
	}
	return rec
}

func sampleCase(caseID, stage string) Case {
	return Case{
		CaseID:       caseID,
		Stage:        stage,
		InputMessage: "推荐个保险",
		PromptMessages: []Message{
			{Role: "user", Content: "推荐个保险"},
		},
		RawOutput: `{"intent":"knowledge_explain"}`,
		Expected:  "product_recommendation with followup",
		Error:     "intent misclassified as knowledge_explain",
		Tags:      []string{"误判", "推荐类"},
	}
}
