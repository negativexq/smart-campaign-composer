package segment_test

import (
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

// An assumption is shown as a value to check, so one that repeats the brief is dropped.
func TestAnAssumptionThatRepeatsTheBriefIsDropped(t *testing.T) {
	t.Parallel()
	brief := "Remind customers in İzmir who added to their cart in the last 14 days and did not buy."
	proposal := proposalOf(segment.Spec{City: text("İzmir"), CartAddedWithinDays: number(14), NoPurchaseSinceCart: yes()})
	proposal.Assumptions = []segment.Assumption{
		{Field: "city", Note: "Brief says İzmir; used as city filter."},
		{Field: "cart_added_within_days", Note: "Brief says last 14 days; converted to 14."},
		{Field: "no_purchase_since_cart", Note: "Brief says did not buy; set to true."},
	}
	if err := segment.CheckAgainstBrief(&proposal, brief, groundingCities, nil); err != nil {
		t.Fatalf("a rule that matches the brief was refused: %v", err)
	}
	if len(proposal.Assumptions) != 0 {
		t.Fatalf("assumptions that repeat the brief were kept: %+v", proposal.Assumptions)
	}
}

func TestAnAssumptionTheBriefDoesNotStateStays(t *testing.T) {
	t.Parallel()
	brief := "Remind customers in Bursa who abandoned their cart."
	proposal := cartAssumed(segment.Spec{City: text("Bursa"), CartAddedWithinDays: number(30), NoPurchaseSinceCart: yes()})
	if err := segment.CheckAgainstBrief(&proposal, brief, groundingCities, nil); err != nil {
		t.Fatalf("an assumed window was refused: %v", err)
	}
	if len(proposal.Assumptions) != 1 || proposal.Assumptions[0].Field != "cart_added_within_days" {
		t.Fatalf("the assumed window was dropped: %+v", proposal.Assumptions)
	}
}

func TestAQuietPeriodTheMarketerChoseIsNotAnAssumption(t *testing.T) {
	t.Parallel()
	brief := "Remind customers in Bursa who added to their cart in the last 7 days and did not buy."
	proposal := proposalOf(segment.Spec{City: text("Bursa"), CartAddedWithinDays: number(7), NoPurchaseSinceCart: yes(), NotMessagedWithinDays: number(3)})
	proposal.Assumptions = []segment.Assumption{{Field: "not_messaged_within_days", Note: "Leaves out people messaged in the last 3 days."}}
	if err := segment.CheckAgainstBrief(&proposal, brief, groundingCities, number(3)); err != nil {
		t.Fatalf("a chosen quiet period was refused: %v", err)
	}
	if len(proposal.Assumptions) != 0 {
		t.Fatalf("the marketer's own choice was kept as an assumption: %+v", proposal.Assumptions)
	}
}
