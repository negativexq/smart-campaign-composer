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

// ProposeSegment implements Provider.
func (a *Anthropic) ProposeSegment(ctx context.Context, request SegmentRequest) (SegmentResponse, error) {
	message, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: anthropicMaxTokens,
		System:    []anthropic.BetaTextBlockParam{{Text: segmentSystem(request.Cities)}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(segmentUserMessage(request))),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Format: anthropic.BetaJSONOutputFormatParam{Schema: request.Schema},
		},
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	})
	if err != nil {
		return SegmentResponse{}, mapAnthropicError(err)
	}

	switch message.StopReason {
	case anthropic.BetaStopReasonEndTurn:
	case anthropic.BetaStopReasonRefusal:
		return SegmentResponse{}, ErrRefused
	case anthropic.BetaStopReasonMaxTokens, anthropic.BetaStopReasonModelContextWindowExceeded:
		return SegmentResponse{}, ErrTruncated
	default:
		return SegmentResponse{}, fmt.Errorf("%w: unexpected stop reason %q", ErrInvalidOutput, message.StopReason)
	}

	var text strings.Builder
	for _, block := range message.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	if text.Len() == 0 {
		return SegmentResponse{}, fmt.Errorf("%w: response has no text", ErrInvalidOutput)
	}
	return SegmentResponse{
		Output: []byte(text.String()),
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
