package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

const (
	openAIMaxOutputTokens = 16000
	openAIRateLimitCode   = "rate_limit_exceeded"
)

// OpenAI calls the Responses API. Briefs are not stored on OpenAI's side, since
// nothing here continues an earlier response.
type OpenAI struct {
	client openai.Client
	model  string
}

// NewOpenAI builds the adapter. Options exist for tests to point the client at
// a local server.
func NewOpenAI(apiKey, model string, options ...option.RequestOption) *OpenAI {
	options = append([]option.RequestOption{option.WithAPIKey(apiKey)}, options...)
	return &OpenAI{client: openai.NewClient(options...), model: model}
}

// Name implements Provider.
func (o *OpenAI) Name() string { return "openai" }

// Model implements Provider.
func (o *OpenAI) Model() string { return o.model }

func (o *OpenAI) params(system, user, schemaName string, schema map[string]any) responses.ResponseNewParams {
	format := responses.ResponseFormatTextConfigParamOfJSONSchema(schemaName, strictSchema(schema))
	format.OfJSONSchema.Strict = param.NewOpt(true)
	return responses.ResponseNewParams{
		Model:           o.model,
		Instructions:    param.NewOpt(system),
		Input:           responses.ResponseNewParamsInputUnion{OfString: param.NewOpt(user)},
		MaxOutputTokens: param.NewOpt[int64](openAIMaxOutputTokens),
		Store:           param.NewOpt(false),
		Text:            responses.ResponseTextConfigParam{Format: format},
	}
}

// ProposeSegment implements Provider.
func (o *OpenAI) ProposeSegment(ctx context.Context, request SegmentRequest) (Response, error) {
	response, err := o.client.Responses.New(ctx, o.params(segmentSystem(request.Cities), segmentUserMessage(request), "campaign_segment", request.Schema))
	if err != nil {
		return Response{}, mapOpenAIError(err)
	}
	return finishOpenAIResponse(response)
}

// StreamContent implements Provider.
func (o *OpenAI) StreamContent(ctx context.Context, request ContentRequest, handler StreamHandler) (Response, error) {
	stream := o.client.Responses.NewStreaming(ctx, o.params(contentSystem(request.Limits), contentUserMessage(request), "campaign_content", request.Schema))
	defer stream.Close()

	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			handler.OnText(event.Delta)
		case "response.completed", "response.incomplete", "response.failed":
			return finishOpenAIResponse(&event.Response)
		case "error":
			return Response{}, openAIFailure(event.Code)
		}
	}
	if err := stream.Err(); err != nil {
		return Response{}, mapOpenAIError(err)
	}
	return Response{}, fmt.Errorf("%w: stream ended early", ErrUnavailable)
}

// finishOpenAIResponse checks why the model stopped before trusting its text.
func finishOpenAIResponse(response *responses.Response) (Response, error) {
	switch response.Status {
	case responses.ResponseStatusCompleted:
	case responses.ResponseStatusIncomplete:
		switch reason := response.IncompleteDetails.Reason; reason {
		case "max_output_tokens":
			return Response{}, ErrTruncated
		case "content_filter":
			return Response{}, ErrRefused
		default:
			return Response{}, fmt.Errorf("%w: incomplete because of %q", ErrInvalidOutput, reason)
		}
	case responses.ResponseStatusFailed:
		return Response{}, openAIFailure(string(response.Error.Code))
	default:
		return Response{}, fmt.Errorf("%w: unexpected status %q", ErrInvalidOutput, response.Status)
	}
	// A refusal arrives as a completed response with a refusal part.
	for _, item := range response.Output {
		for _, part := range item.Content {
			if part.Type == "refusal" {
				return Response{}, ErrRefused
			}
		}
	}
	text := response.OutputText()
	if text == "" {
		return Response{}, fmt.Errorf("%w: response has no text", ErrInvalidOutput)
	}
	return Response{
		Output: []byte(text),
		Usage:  Usage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens},
	}, nil
}

func openAIFailure(code string) error {
	if code == openAIRateLimitCode {
		return ErrRateLimited
	}
	return fmt.Errorf("%w: openai reported failure %q", ErrUnavailable, code)
}

// mapOpenAIError keeps only the status: SDK errors dump the whole exchange,
// which does not belong in logs or API responses.
func mapOpenAIError(err error) error {
	var apiError *openai.Error
	if errors.As(err, &apiError) {
		if apiError.StatusCode == http.StatusTooManyRequests {
			return ErrRateLimited
		}
		return fmt.Errorf("%w: openai returned status %d", ErrUnavailable, apiError.StatusCode)
	}
	for _, contextErr := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, contextErr) {
			return fmt.Errorf("%w: %w", ErrUnavailable, contextErr)
		}
	}
	return fmt.Errorf("%w: could not reach openai", ErrUnavailable)
}

// strictSchema rewrites a schema for OpenAI's strict mode, which needs every
// property listed as required. An optional property becomes required but may be
// null, and the backend's validation reads null the same as a missing filter.
func strictSchema(schema map[string]any) map[string]any {
	strict := make(map[string]any, len(schema))
	for key, value := range schema {
		strict[key] = value
	}
	if items, ok := schema["items"].(map[string]any); ok {
		strict["items"] = strictSchema(items)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return strict
	}
	required, _ := schema["required"].([]string)
	strictProperties := make(map[string]any, len(properties))
	names := make([]string, 0, len(properties))
	for name, value := range properties {
		names = append(names, name)
		property := strictSchema(value.(map[string]any))
		if !slices.Contains(required, name) {
			property = nullable(property)
		}
		strictProperties[name] = property
	}
	slices.SortFunc(names, strings.Compare)
	strict["properties"] = strictProperties
	strict["required"] = names
	strict["additionalProperties"] = false
	return strict
}

func nullable(property map[string]any) map[string]any {
	property["type"] = []any{property["type"], "null"}
	if values, ok := property["enum"].([]string); ok {
		withNull := make([]any, 0, len(values)+1)
		for _, value := range values {
			withNull = append(withNull, value)
		}
		property["enum"] = append(withNull, nil)
	}
	return property
}
