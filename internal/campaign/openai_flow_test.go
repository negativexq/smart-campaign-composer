package campaign_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/openai/openai-go/v3/option"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

// strictProposal is a proposal as OpenAI's strict mode returns it: every property
// present, and the ones that do not apply set to null.
const strictProposal = `{"campaign_name":"Ankara","updated_brief":"","segment":{"city":"Ankara","language":null,"cart_added_within_days":null,` +
	`"no_purchase_since_cart":null,"purchased_within_days":30,"not_purchased_within_days":null,"not_messaged_within_days":null},` +
	`"assumptions":[],"evidence":[{"field":"city","quote":"Ankara"}],"unsupported_conditions":[]}`

const strictContent = `{"sms":"Hi {{first_name}}","email_subject":"Hello {{first_name}}","email_body":"Your cart is waiting.",` +
	`"email_preheader":null,"email_headline":null,"email_cta_label":null,"email_cta_url":null}`

// openAIFlow answers each OpenAI call with the next output, through the real
// adapter, so these tests cover what reaches the validators from that provider.
type openAIFlow struct {
	mu      sync.Mutex
	outputs []string
	calls   int
}

func (f *openAIFlow) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var request struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(raw, &request)

	f.mu.Lock()
	output := f.outputs[min(f.calls, len(f.outputs)-1)]
	f.calls++
	f.mu.Unlock()

	response := map[string]any{
		"id": "resp", "object": "response", "created_at": 1, "model": "gpt-test", "status": "completed",
		"output": []map[string]any{{"id": "msg", "type": "message", "role": "assistant", "status": "completed",
			"content": []map[string]any{{"type": "output_text", "text": output, "annotations": []any{}}}}},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	}
	if !request.Stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	delta, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "sequence_number": 1, "delta": output})
	done, _ := json.Marshal(map[string]any{"type": "response.completed", "sequence_number": 2, "response": response})
	_, _ = fmt.Fprintf(w, "event: response.output_text.delta\ndata: %s\n\nevent: response.completed\ndata: %s\n\n", delta, done)
}

func newOpenAIHarness(t *testing.T, outputs ...string) (*harness, *openAIFlow, string) {
	t.Helper()
	h := newHarness(t)
	flow := &openAIFlow{outputs: outputs}
	server := httptest.NewServer(flow)
	t.Cleanup(server.Close)
	h.service = h.serviceWith(ai.NewOpenAI("test-key", "gpt-test", option.WithBaseURL(server.URL), option.WithMaxRetries(0)))
	return h, flow, h.create(t)
}

func TestOpenAINullsMeanNoFilter(t *testing.T) {
	t.Parallel()
	h, _, id := newOpenAIHarness(t, strictProposal)
	view, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	spec := view.Proposal.Segment
	if *spec.City != "Ankara" || *spec.PurchasedWithinDays != 30 || spec.Language != nil || spec.CartAddedWithinDays != nil {
		t.Fatalf("spec: %+v", spec)
	}
	if len(view.Conditions) != 2 {
		t.Fatalf("conditions: %+v", view.Conditions)
	}
}

func TestOpenAIOutputIsCheckedLikeAnyOther(t *testing.T) {
	t.Parallel()
	for name, rejected := range map[string]string{
		"markdown around json": "```json\n" + strictProposal + "\n```",
		"prose":                "Here is your audience: Ankara",
		"cut off":              strictProposal[:40],
		"unknown city":         strings.Replace(strictProposal, `"city":"Ankara"`, `"city":"Atlantis"`, 1),
		"sql as a city":        strings.Replace(strictProposal, `"city":"Ankara"`, `"city":"x' OR 1=1 --"`, 1),
		"extra filter":         strings.Replace(strictProposal, `"language":null`, `"language":null,"age_over":25`, 1),
		"extra top field":      strings.Replace(strictProposal, `"campaign_name"`, `"sql":"DROP TABLE users","campaign_name"`, 1),
		"negative window":      strings.Replace(strictProposal, `"purchased_within_days":30`, `"purchased_within_days":-1`, 1),
		"window as text":       strings.Replace(strictProposal, `"purchased_within_days":30`, `"purchased_within_days":"30"`, 1),
		"no segment":           strings.Replace(strictProposal, `"segment":{`, `"other":{`, 1),
		"null segment":         `{"campaign_name":"A","updated_brief":"","segment":null,"assumptions":[],"evidence":[],"unsupported_conditions":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, flow, id := newOpenAIHarness(t, rejected)
			_, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{})
			if !errors.Is(err, campaign.ErrProposalRejected) {
				t.Fatalf("got %v", err)
			}
			if flow.calls != campaign.MaxProposalAttempts {
				t.Fatalf("calls: %d", flow.calls)
			}
			view, err := h.service.Get(context.Background(), id)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if view.Proposal != nil {
				t.Fatalf("stored: %+v", view.Proposal)
			}
		})
	}
}

func TestOpenAIRepairsAfterABadAnswer(t *testing.T) {
	t.Parallel()
	h, flow, id := newOpenAIHarness(t, "```json\n"+strictProposal+"\n```", strictProposal)
	view, err := h.service.ProposeSegment(context.Background(), id, campaign.ProposeInput{})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if flow.calls != 2 || view.Proposal == nil {
		t.Fatalf("calls %d, proposal %+v", flow.calls, view.Proposal)
	}
}

func TestOpenAIRefinementFlow(t *testing.T) {
	t.Parallel()
	refined := strings.Replace(strings.Replace(strictProposal, `"updated_brief":""`, `"updated_brief":"Ankara, 14 days"`, 1), `"purchased_within_days":30`, `"purchased_within_days":14`, 1)
	h, _, id := newOpenAIHarness(t, strictProposal, refined)
	ctx := context.Background()
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	view, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{Refinement: "14 days"})
	if err != nil {
		t.Fatalf("refine: %v", err)
	}
	if view.Brief != "Ankara, 14 days" || *view.Proposal.Segment.PurchasedWithinDays != 14 {
		t.Fatalf("view: %q %+v", view.Brief, view.Proposal.Segment)
	}
}

func TestOpenAIContentFlow(t *testing.T) {
	t.Parallel()
	h, _, id := newOpenAIHarness(t, validProposal, strictContent)
	ctx := context.Background()
	if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	view, log, err := run(t, h, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if view.Status != "READY_FOR_REVIEW" || view.Content.SMS != "Hi {{first_name}}" || view.Content.EmailHeadline != "" || view.Content.EmailCTAURL != "" {
		t.Fatalf("view: %s %+v", view.Status, view.Content)
	}
	if len(log.events) == 0 {
		t.Fatal("no text was streamed")
	}
}

func TestOpenAIContentIsCheckedLikeAnyOther(t *testing.T) {
	t.Parallel()
	for name, rejected := range map[string]string{
		"unknown variable":  strings.Replace(strictContent, "Hi {{first_name}}", "Use {{coupon}}", 1),
		"too long sms":      strings.Replace(strictContent, "Hi {{first_name}}", strings.Repeat("a", 161), 1),
		"invented link":     strings.Replace(strings.Replace(strictContent, `"email_cta_label":null`, `"email_cta_label":"Go"`, 1), `"email_cta_url":null`, `"email_cta_url":"https://evil.example"`, 1),
		"invented discount": strings.Replace(strictContent, "Hi {{first_name}}", "50% off {{first_name}}", 1),
		"not json":          "Here are your messages",
		"null sms":          strings.Replace(strictContent, `"sms":"Hi {{first_name}}"`, `"sms":null`, 1),
		"extra field":       strings.Replace(strictContent, `"sms"`, `"email_template":"banner","html":"<b>","sms"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, _, id := newOpenAIHarness(t, validProposal, rejected)
			ctx := context.Background()
			if _, err := h.service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
				t.Fatalf("propose: %v", err)
			}
			_, _, err := run(t, h, id, campaign.GenerateInput{})
			if !errors.Is(err, campaign.ErrContentRejected) {
				t.Fatalf("got %v", err)
			}
			view, err := h.service.Get(ctx, id)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if view.Content != nil || view.Status == "READY_FOR_REVIEW" {
				t.Fatalf("stored: %s %+v", view.Status, view.Content)
			}
		})
	}
}
