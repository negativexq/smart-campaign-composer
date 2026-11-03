// Package ai hides LLM vendors behind one interface. Providers return raw model
// output; deciding whether it can be trusted is the caller's job.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/content"
)

// Provider failures every adapter maps to, so callers never see vendor types.
var (
	ErrRefused       = errors.New("model refused the request")
	ErrTruncated     = errors.New("model output was cut off")
	ErrRateLimited   = errors.New("model provider is rate limiting")
	ErrUnavailable   = errors.New("model provider is unavailable")
	ErrInvalidOutput = errors.New("model returned no usable output")
)

// SegmentRequest asks for an audience proposal. PreviousOutput and
// ValidationError are set when repairing output that failed validation.
type SegmentRequest struct {
	Brief           string
	Cities          []string
	Schema          map[string]any
	Current         json.RawMessage
	Refinement      string
	PreviousOutput  string
	ValidationError string
}

// Usage is what a call cost, for telemetry.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

// Response is raw, unvalidated model output.
type Response struct {
	Output []byte
	Usage  Usage
}

// ContentRequest asks for messages. Current and Instruction are set when
// regenerating; PreviousOutput and ValidationError when repairing.
type ContentRequest struct {
	Brief           string
	Conditions      []string
	Schema          map[string]any
	Limits          content.Limits
	Current         json.RawMessage
	Instruction     string
	PreviousOutput  string
	ValidationError string
}

// StreamHandler receives output as it is produced. OnRestart means the text so
// far is abandoned, because another model took over mid-response.
type StreamHandler struct {
	OnText    func(text string)
	OnRestart func()
}

// Provider is an LLM vendor.
type Provider interface {
	Name() string
	Model() string
	ProposeSegment(ctx context.Context, request SegmentRequest) (Response, error)
	StreamContent(ctx context.Context, request ContentRequest, handler StreamHandler) (Response, error)
}

const (
	defaultAnthropicModel = "claude-opus-5-5"
	defaultOpenAIModel    = "gpt-6-astra"
	// mockStreamDelay makes the mock's stream visible in a demo; tests skip it.
	mockStreamDelay = 25 * time.Millisecond
)

// NewFromEnv picks the provider from LLM_PROVIDER, defaulting to the mock. A
// chosen provider without its key is a startup error, never a silent mock.
func NewFromEnv(getenv func(string) string) (Provider, error) {
	switch name := getenv("LLM_PROVIDER"); name {
	case "", "mock":
		mock := NewMock()
		mock.StreamDelay = mockStreamDelay
		return mock, nil
	case "anthropic":
		key := getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, errors.New("LLM_PROVIDER=anthropic requires ANTHROPIC_API_KEY")
		}
		model := getenv("ANTHROPIC_MODEL")
		if model == "" {
			model = defaultAnthropicModel
		}
		return NewAnthropic(key, model), nil
	case "openai":
		key := getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, errors.New("LLM_PROVIDER=openai requires OPENAI_API_KEY")
		}
		model := getenv("OPENAI_MODEL")
		if model == "" {
			model = defaultOpenAIModel
		}
		return NewOpenAI(key, model), nil
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER %q; use mock, anthropic or openai", name)
	}
}
