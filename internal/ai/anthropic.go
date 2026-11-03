package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const anthropicMaxTokens = 16000

// Anthropic calls the Claude API. It uses the beta endpoint for server-side
// fallback, which lets a refused or overloaded request be retried on another
// model by the API itself.
type Anthropic struct {
	client anthropic.Client
	model  string
}

// NewAnthropic builds the adapter. Options exist for tests to point the client
// at a local server.
func NewAnthropic(apiKey, model string, options ...option.RequestOption) *Anthropic {
	options = append([]option.RequestOption{option.WithAPIKey(apiKey)}, options...)
	return &Anthropic{client: anthropic.NewClient(options...), model: model}
}

// Name implements Provider.
func (a *Anthropic) Name() string { return "anthropic" }

// Model implements Provider.
func (a *Anthropic) Model() string { return a.model }

func (a *Anthropic) params(system, user string, schema map[string]any) anthropic.BetaMessageNewParams {
	return anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: anthropicMaxTokens,
		System:    []anthropic.BetaTextBlockParam{{Text: system}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user)),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Format: anthropic.BetaJSONOutputFormatParam{Schema: schema},
		},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	}
}

// ProposeSegment implements Provider.
func (a *Anthropic) ProposeSegment(ctx context.Context, request SegmentRequest) (Response, error) {
	message, err := a.client.Beta.Messages.New(ctx, a.params(segmentSystem(request.Cities), segmentUserMessage(request), request.Schema))
	if err != nil {
		return Response{}, mapAnthropicError(err)
	}
	// Text before a fallback block belongs to a model that gave up.
	var text strings.Builder
	for _, block := range message.Content {
		switch block.Type {
		case "fallback":
			text.Reset()
		case "text":
			text.WriteString(block.Text)
		}
	}
	return finishMessage(message, text.String())
}

// StreamContent implements Provider.
func (a *Anthropic) StreamContent(ctx context.Context, request ContentRequest, handler StreamHandler) (Response, error) {
	stream := a.client.Beta.Messages.NewStreaming(ctx, a.params(contentSystem(request.Limits), contentUserMessage(request), request.Schema))
	defer stream.Close()

	var message anthropic.BetaMessage
	var text strings.Builder
	for stream.Next() {
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			return Response{}, fmt.Errorf("%w: read stream: %w", ErrUnavailable, err)
		}
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "fallback" {
				text.Reset()
				handler.OnRestart()
			}
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				text.WriteString(event.Delta.Text)
				handler.OnText(event.Delta.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return Response{}, mapAnthropicError(err)
	}
	if message.StopReason == "" {
		return Response{}, fmt.Errorf("%w: stream ended early", ErrUnavailable)
	}
	return finishMessage(&message, text.String())
}

// finishMessage checks why the model stopped before trusting its text.
func finishMessage(message *anthropic.BetaMessage, text string) (Response, error) {
	switch message.StopReason {
	case anthropic.BetaStopReasonEndTurn:
	case anthropic.BetaStopReasonRefusal:
		return Response{}, ErrRefused
	case anthropic.BetaStopReasonMaxTokens, anthropic.BetaStopReasonModelContextWindowExceeded:
		return Response{}, ErrTruncated
	default:
		return Response{}, fmt.Errorf("%w: unexpected stop reason %q", ErrInvalidOutput, message.StopReason)
	}
	if text == "" {
		return Response{}, fmt.Errorf("%w: response has no text", ErrInvalidOutput)
	}
	return Response{
		Output: []byte(text),
		Usage:  Usage{InputTokens: message.Usage.InputTokens, OutputTokens: message.Usage.OutputTokens},
	}, nil
}

// mapAnthropicError keeps only the status: SDK errors dump the whole exchange,
// which does not belong in logs or API responses.
func mapAnthropicError(err error) error {
	var apiError *anthropic.Error
	if errors.As(err, &apiError) {
		if apiError.StatusCode == http.StatusTooManyRequests {
			return ErrRateLimited
		}
		return fmt.Errorf("%w: anthropic returned status %d", ErrUnavailable, apiError.StatusCode)
	}
	for _, contextErr := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, contextErr) {
			return fmt.Errorf("%w: %w", ErrUnavailable, contextErr)
		}
	}
	return fmt.Errorf("%w: could not reach anthropic", ErrUnavailable)
}
