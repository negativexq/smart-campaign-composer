package segment_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

// A trait the brief says does not matter, and a cart named as a feature, are no
// conditions, so they must not block a rule that has everything the brief asks.
func TestWordsThatSayNoConditionDoNotBlock(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		brief       string
		city        string
		unsupported []string
	}{
		"age does not matter":    {"Tüm Ankara müşterilerine, yaş fark etmeksizin", "Ankara", []string{"yaş fark etmeksizin"}},
		"gender does not matter": {"Erkek kadın fark etmeksizin tüm Bursa müşterileri", "Bursa", nil},
		"written as one word":    {"Kadın erkek farketmez, İzmir kullanıcıları", "İzmir", nil},
		"regardless of":          {"All customers in Adana regardless of age or gender", "Adana", []string{"regardless of age or gender"}},
		"cart feature":           {"Alışveriş sepeti özelliğimizi tanıtan duyuru, Bursa müşterilerine", "Bursa", nil},
		"cart page":              {"Announce the new cart page to customers in Ankara", "Ankara", nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			proposal := proposalOf(segment.Spec{City: text(tt.city)})
			proposal.UnsupportedConditions = tt.unsupported
			if problems := segment.CheckAgainstBrief(&proposal, tt.brief, groundingCities, nil); problems != nil {
				t.Fatalf("rejected: %v", problems)
			}
			if len(proposal.Unmatched) > 0 || len(proposal.UnsupportedConditions) > 0 {
				t.Fatalf("unmatched %v, unsupported %v", proposal.Unmatched, proposal.UnsupportedConditions)
			}
		})
	}
}

// The same words next to a real condition leave that condition in place.
func TestARealConditionNextToThoseWordsStillCounts(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		brief string
		want  string
	}{
		"gender with age not mattering": {"Ankara'daki kadınlara, yaş fark etmeksizin", "gender"},
		"age with gender not mattering": {"Ankara'daki 25 yaş üstü kullanıcılar, cinsiyet fark etmeksizin", "age"},
		"a cart people used":            {"Ankara'da sepete ürün ekleyenler, sepet sayfası yenilendi", "cart"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			proposal := proposalOf(segment.Spec{City: text("Ankara")})
			if problems := segment.CheckAgainstBrief(&proposal, tt.brief, groundingCities, nil); problems != nil {
				t.Fatalf("rejected: %v", problems)
			}
			if !slices.ContainsFunc(proposal.Unmatched, func(note string) bool { return strings.Contains(note, tt.want) }) {
				t.Fatalf("unmatched: %v, want %s", proposal.Unmatched, tt.want)
			}
		})
	}
}

// A model that marks "yaş fark etmeksizin" as an audience part does not make it a condition.
func TestAPartThatSaysNoConditionIsNotAnAudiencePart(t *testing.T) {
	t.Parallel()
	brief := "Tüm Ankara müşterilerine, yaş fark etmeksizin"
	proposal := withParts(segment.Spec{City: text("Ankara")}, audiencePart("Tüm Ankara müşterilerine", "city"), audiencePart("yaş fark etmeksizin"))
	if problems := segment.CheckAgainstBrief(&proposal, brief, groundingCities, nil); problems != nil {
		t.Fatal(problems)
	}
	if len(proposal.UnsupportedConditions) > 0 || proposal.BriefParts[1].Kind != segment.PartOther {
		t.Fatalf("unsupported %v, parts %+v", proposal.UnsupportedConditions, proposal.BriefParts)
	}
}
