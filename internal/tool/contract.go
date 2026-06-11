package tool

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	BackendInProcess = "inprocess"
	BackendMCP       = "mcp"
)

const (
	ErrorCodeInvalidInput    = "invalid_input"
	ErrorCodeBackendFailure  = "backend_failure"
	ErrorCodeTimeout         = "timeout"
	ErrorCodeCanceled        = "canceled"
	ErrorCodePartialFailure  = "partial_failure"
	ErrorCodeAllBackendsFail = "all_backends_failed"
)

// ToolBackend is the stable boundary used by deterministic graph tool nodes
// and future MCP adapters.
type ToolBackend[I any, O any] interface {
	Name() string
	Invoke(ctx context.Context, input I) (O, error)
}

type ToolRuntimeConfig struct {
	Timeout    time.Duration
	MaxRetries int
	Backend    string
}

func (c ToolRuntimeConfig) WithDefaults() ToolRuntimeConfig {
	if c.Backend == "" {
		c.Backend = BackendInProcess
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	return c
}

type ToolFailure struct {
	ToolName  string `json:"tool_name"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type ToolError struct {
	ToolName  string
	Code      string
	Message   string
	Retryable bool
	Cause     error
}

func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return fmt.Sprintf("%s failed with %s", e.ToolName, e.Code)
	}
	return fmt.Sprintf("%s failed with %s: %s", e.ToolName, e.Code, e.Message)
}

func (e *ToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func FailureFromError(toolName string, err error, retryable bool) ToolFailure {
	if err == nil {
		return ToolFailure{}
	}
	var typed *ToolError
	if errors.As(err, &typed) {
		return ToolFailure{
			ToolName:  typed.ToolName,
			Code:      typed.Code,
			Message:   typed.Message,
			Retryable: typed.Retryable,
		}
	}
	code := ErrorCodeBackendFailure
	if errors.Is(err, context.DeadlineExceeded) {
		code = ErrorCodeTimeout
	}
	if errors.Is(err, context.Canceled) {
		code = ErrorCodeCanceled
	}
	return ToolFailure{
		ToolName:  toolName,
		Code:      code,
		Message:   err.Error(),
		Retryable: retryable,
	}
}

type ChatMessage struct {
	ID        string         `json:"id,omitempty"`
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
}
