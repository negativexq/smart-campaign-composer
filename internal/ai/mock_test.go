package ai_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

func proposeWithMock(t *testing.T, request ai.SegmentRequest) segment.Proposal {
	t.Helper()
	request.Cities = seed.Cities
	response, err := ai.NewMock().ProposeSegment(context.Background(), request)
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	proposal, err := segment.ParseProposal(response.Output, seed.Cities)
	if err != nil {
		t.Fatalf("mock output must pass real validation: %v\n%s", err, response.Output)
	}
	return proposal
}

func TestMockUnderstandsTheExampleBriefInBothLanguages(t *testing.T) {
	t.Parallel()
	for _, brief := range []string{
		"İstanbul'da son 30 günde sepete ürün ekleyip alışveriş yapmayan kullanıcılara %20 indirim hatırlatması gönder",
		"Remind users in Istanbul who added to cart in the last 30 days and did not buy about a 20% discount",
		"Remind users in Istanbul who added a product to their cart in the last 30 days and did not purchase",
	} {
		spec := proposeWithMock(t, ai.SegmentRequest{Brief: brief}).Segment
		if spec.City == nil || *spec.City != seed.CityIstanbul {
			t.Errorf("%q: city %v", brief, spec.City)
		}
		if spec.CartAddedWithinDays == nil || *spec.CartAddedWithinDays != 30 {
			t.Errorf("%q: window %v", brief, spec.CartAddedWithinDays)
		}
		if spec.NoPurchaseSinceCart == nil || !*spec.NoPurchaseSinceCart {
			t.Errorf("%q: no purchase filter missing", brief)
		}
	}
}

func TestMockRecordsTheWindowItAssumed(t *testing.T) {
	t.Parallel()
	proposal := proposeWithMock(t, ai.SegmentRequest{Brief: "Ankara users who abandoned their cart"})
	if *proposal.Segment.CartAddedWithinDays != 30 {
		t.Fatalf("window: %v", *proposal.Segment.CartAddedWithinDays)
	}
	if len(proposal.Assumptions) != 1 || proposal.Assumptions[0].Field != "cart_added_within_days" {
		t.Fatalf("assumption missing: %+v", proposal.Assumptions)
	}
}

func TestMockReportsWhatItCannotExpress(t *testing.T) {
	t.Parallel()
	proposal := proposeWithMock(t, ai.SegmentRequest{Brief: "Ankara users over 25 years of age"})
	if len(proposal.UnsupportedConditions) != 1 {
		t.Fatalf("unsupported condition missing: %+v", proposal)
	}
}

func TestMockAppliesRefinementToTheCurrentProposal(t *testing.T) {
	t.Parallel()
	first := proposeWithMock(t, ai.SegmentRequest{Brief: "Ankara users who abandoned their cart"})
	current, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}

	refined := proposeWithMock(t, ai.SegmentRequest{
		Brief:      "Ankara users who abandoned their cart",
		Current:    current,
		Refinement: "make it 14 days",
	})

	if *refined.Segment.CartAddedWithinDays != 14 {
		t.Fatalf("window: %v", *refined.Segment.CartAddedWithinDays)
	}
	if *refined.Segment.City != "Ankara" {
		t.Fatalf("refinement lost the city: %v", *refined.Segment.City)
	}
	if len(refined.Assumptions) != 0 {
		t.Fatalf("a stated window is no longer an assumption: %+v", refined.Assumptions)
	}
}

func TestMockSuggestsANameFromTheAudience(t *testing.T) {
	t.Parallel()
	proposal := proposeWithMock(t, ai.SegmentRequest{Brief: "Ankara users who abandoned their cart"})
	if proposal.CampaignName != "Cart Recovery — Ankara" {
		t.Fatalf("name: %q", proposal.CampaignName)
	}
}

func TestMockIsDeterministic(t *testing.T) {
	t.Parallel()
	request := ai.SegmentRequest{Brief: "İzmir sepet", Cities: seed.Cities}
	first, _ := ai.NewMock().ProposeSegment(context.Background(), request)
	second, _ := ai.NewMock().ProposeSegment(context.Background(), request)
	if string(first.Output) != string(second.Output) {
		t.Fatalf("%s != %s", first.Output, second.Output)
	}
}

func TestFactoryRefusesMissingKeyInsteadOfFallingBackToMock(t *testing.T) {
	t.Parallel()
	if _, err := ai.NewFromEnv(func(string) string { return "" }); err != nil {
		t.Fatalf("default should be mock: %v", err)
	}
	for _, name := range []string{"anthropic", "openai"} {
		_, err := ai.NewFromEnv(func(key string) string {
			if key == "LLM_PROVIDER" {
				return name
			}
			return ""
		})
		if err == nil {
			t.Errorf("%s without a key must fail at startup", name)
		}
	}
	if _, err := ai.NewFromEnv(func(key string) string {
		if key == "LLM_PROVIDER" {
			return "gemini"
		}
		return ""
	}); err == nil {
		t.Error("unknown provider must fail at startup")
	}
}
