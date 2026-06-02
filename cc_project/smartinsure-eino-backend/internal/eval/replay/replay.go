package replay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const PromptVersion = "v1.0"

var ErrCaseNotFound = errors.New("replay case not found")

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Case struct {
	CaseID         string    `json:"case_id"`
	Stage          string    `json:"stage"`
	InputMessage   string    `json:"input_message"`
	PromptMessages []Message `json:"prompt_messages"`
	RawOutput      string    `json:"raw_output"`
	Expected       string    `json:"expected,omitempty"`
	Error          string    `json:"error,omitempty"`
	Tags           []string  `json:"tags"`
	RecordedAt     string    `json:"recorded_at"`
	PromptVersion  string    `json:"prompt_version"`

	File string `json:"-"`
	Line int    `json:"-"`
}

type Recorder struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
}

func NewRecorder(path string) (*Recorder, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("replay path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create replay directory: %w", err)
	}
	return &Recorder{path: path, now: time.Now}, nil
}

func DefaultPath() string {
	return filepath.Join("data", "replay_cases", "cases.jsonl")
}

func NewDefaultRecorder() (*Recorder, error) {
	return NewRecorder(DefaultPath())
}

func (r *Recorder) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

func (r *Recorder) Record(c Case) (Case, error) {
	if r == nil {
		return Case{}, fmt.Errorf("replay recorder is nil")
	}
	if strings.TrimSpace(c.CaseID) == "" {
		return Case{}, fmt.Errorf("case_id is required")
	}
	if strings.TrimSpace(c.Stage) == "" {
		return Case{}, fmt.Errorf("stage is required")
	}

	c.CaseID = strings.TrimSpace(c.CaseID)
	c.Stage = strings.TrimSpace(c.Stage)
	if c.PromptMessages == nil {
		c.PromptMessages = []Message{}
	}
	if c.Tags == nil {
		c.Tags = []string{}
	}
	if c.RecordedAt == "" {
		c.RecordedAt = r.now().Format(time.RFC3339)
	}
	if c.PromptVersion == "" {
		c.PromptVersion = PromptVersion
	}
	c.File = r.path

	line, err := json.Marshal(c)
	if err != nil {
		return Case{}, fmt.Errorf("marshal replay case: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	fp, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return Case{}, fmt.Errorf("open replay file: %w", err)
	}
	defer fp.Close()

	if _, err := fp.Write(append(line, '\n')); err != nil {
		return Case{}, fmt.Errorf("write replay case: %w", err)
	}
	return c, nil
}

func (r *Recorder) List(stage string) ([]Case, error) {
	if r == nil {
		return nil, fmt.Errorf("replay recorder is nil")
	}
	stage = strings.TrimSpace(stage)
	return r.readCases(func(c Case) bool {
		return stage == "" || c.Stage == stage
	})
}

func (r *Recorder) Load(caseID string) (Case, error) {
	if r == nil {
		return Case{}, fmt.Errorf("replay recorder is nil")
	}
	caseID = strings.TrimSpace(caseID)
	if caseID == "" {
		return Case{}, fmt.Errorf("case_id is required")
	}

	cases, err := r.readCases(func(c Case) bool {
		return c.CaseID == caseID
	})
	if err != nil {
		return Case{}, err
	}
	if len(cases) == 0 {
		return Case{}, ErrCaseNotFound
	}
	return cases[0], nil
}

func (r *Recorder) readCases(include func(Case) bool) ([]Case, error) {
	fp, err := os.Open(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Case{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open replay file: %w", err)
	}
	defer fp.Close()

	cases := make([]Case, 0)
	scanner := bufio.NewScanner(fp)
	scanner.Buffer(make([]byte, 1024), 10*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var c Case
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, fmt.Errorf("decode replay case line %d: %w", lineNo, err)
		}
		c.File = r.path
		c.Line = lineNo
		if c.Tags == nil {
			c.Tags = []string{}
		}
		if c.PromptMessages == nil {
			c.PromptMessages = []Message{}
		}
		if include == nil || include(c) {
			cases = append(cases, c)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan replay file: %w", err)
	}
	return cases, nil
}
