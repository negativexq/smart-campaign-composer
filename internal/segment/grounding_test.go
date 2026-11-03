package segment_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

var groundingCities = []string{"İstanbul", "Ankara", "İzmir", "Bursa", "Antalya", "Adana"}

func text(value string) *string { return &value }
func number(value int) *int     { return &value }
func yes() *bool                { value := true; return &value }

func proposalOf(spec segment.Spec, unsupported ...string) segment.Proposal {
	return segment.Proposal{Segment: spec, UnsupportedConditions: unsupported}
}

func cartAssumed(spec segment.Spec) segment.Proposal {
	proposal := proposalOf(spec)
	proposal.Assumptions = []segment.Assumption{{Field: "cart_added_within_days", Note: "No window given."}}
	return proposal
}

// These must pass: a false block costs a marketer a working campaign.
func TestARuleThatMatchesTheBriefPasses(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		brief    string
		proposal segment.Proposal
	}{
		"city with a suffix":      {"Ankara'daki kullanıcılara haber ver", proposalOf(segment.Spec{City: text("Ankara")})},
		"city without apostrophe": {"Ankaradaki müşterilere", proposalOf(segment.Spec{City: text("Ankara")})},
		"people of a city":        {"İzmirlilere duyuru", proposalOf(segment.Spec{City: text("İzmir")})},
		"city in capitals":        {"ISTANBUL users", proposalOf(segment.Spec{City: text("İstanbul")})},
		"city without diacritics": {"izmir kullanicilari", proposalOf(segment.Spec{City: text("İzmir")})},
		"days in turkish": {"Son 30 günde sepetine ürün ekleyip almayan İstanbul kullanıcıları",
			proposalOf(segment.Spec{City: text("İstanbul"), CartAddedWithinDays: number(30), NoPurchaseSinceCart: yes()})},
		"days with a suffix":      {"son 14 gündür alışveriş yapmamış Bursa'lılar", proposalOf(segment.Spec{City: text("Bursa"), NotPurchasedWithinDays: number(14)})},
		"weeks":                   {"2 haftadır alışveriş yapmayanlar", proposalOf(segment.Spec{NotPurchasedWithinDays: number(14)})},
		"a month in words":        {"son bir ayda alışveriş yapmış kişiler", proposalOf(segment.Spec{PurchasedWithinDays: number(30)})},
		"last week":               {"People who bought last week", proposalOf(segment.Spec{PurchasedWithinDays: number(7)})},
		"months in english":       {"Customers who purchased in the last 3 months", proposalOf(segment.Spec{PurchasedWithinDays: number(90)})},
		"english negative":        {"users who did not buy in the last 60 days", proposalOf(segment.Spec{NotPurchasedWithinDays: number(60)})},
		"haven't":                 {"users who haven't purchased for 10 days", proposalOf(segment.Spec{NotPurchasedWithinDays: number(10)})},
		"abandoned cart":          {"Ankara users who abandoned their cart", cartAssumed(segment.Spec{City: text("Ankara"), CartAddedWithinDays: number(30), NoPurchaseSinceCart: yes()})},
		"left in the cart":        {"sepette ürün bırakanlar", cartAssumed(segment.Spec{CartAddedWithinDays: number(30), NoPurchaseSinceCart: yes()})},
		"weekend is no window":    {"Ankara'da son 60 günde alışveriş yapmış kişilere hafta sonu bedava gönderim", proposalOf(segment.Spec{City: text("Ankara"), PurchasedWithinDays: number(60)})},
		"message wording":         {"Ankara müşterilerine yeni sezonu duyur. Keyifli alışverişler de", proposalOf(segment.Spec{City: text("Ankara")})},
		"a discount is no window": {"İzmir'e %30 indirim gönder", proposalOf(segment.Spec{City: text("İzmir")})},
		"two windows": {"Son 60 günde alışveriş yapmış, 7 gündür mesaj almayan Adana kullanıcıları",
			proposalOf(segment.Spec{City: text("Adana"), PurchasedWithinDays: number(60), NotMessagedWithinDays: number(7)})},
		"language asked":        {"İngilizce konuşan Antalya kullanıcıları", proposalOf(segment.Spec{City: text("Antalya"), Language: text("en")})},
		"two cities kept apart": {"Ankara ve İzmir kullanıcıları", proposalOf(segment.Spec{}, "Ankara ve İzmir")},
		"unsupported reported":  {"Ankara'daki 25 yaş üstü kadınlar", proposalOf(segment.Spec{City: text("Ankara")}, "25 yaş üstü kadınlar")},
		"similar words":         {"Ankara'da güncelleme duyurusu, ayna ve yaşam ürünleri", proposalOf(segment.Spec{City: text("Ankara")})},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			proposal := tt.proposal
			if problems := segment.CheckAgainstBrief(&proposal, tt.brief, groundingCities, nil); problems != nil {
				t.Fatalf("blocked: %v", problems)
			}
			if len(proposal.Unmatched) != 0 {
				t.Fatalf("unmatched: %v", proposal.Unmatched)
			}
		})
	}
}

// These must fail: the rule says something the brief does not.
func TestARuleThatContradictsTheBriefIsRejected(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		brief    string
		proposal segment.Proposal
		reason   string
	}{
		"ankara became izmir":   {"Ankara'daki kullanıcılara haber ver", proposalOf(segment.Spec{City: text("İzmir")}), "city_not_in_brief"},
		"a city from nowhere":   {"Kullanıcılara haber ver", proposalOf(segment.Spec{City: text("İzmir")}), "city_not_in_brief"},
		"excluded city chosen":  {"Ankara hariç herkese", proposalOf(segment.Spec{City: text("Ankara")}), "city_excluded_in_brief"},
		"60 became 30":          {"Son 60 günde alışveriş yapmış kişiler", proposalOf(segment.Spec{PurchasedWithinDays: number(30)}), "window_not_in_brief"},
		"2 weeks became 2 days": {"2 haftadır alışveriş yapmayanlar", proposalOf(segment.Spec{NotPurchasedWithinDays: number(2)}), "window_not_in_brief"},
		"an assumption cannot override": {"Son 60 günde sepete ekleyenler", func() segment.Proposal {
			return cartAssumed(segment.Spec{CartAddedWithinDays: number(30)})
		}(), "window_not_in_brief"},
		"a window from nowhere": {"Sepete ekleyenler", proposalOf(segment.Spec{CartAddedWithinDays: number(30)}), "window_not_in_brief"},
		"windows swapped": {"Son 60 günde alışveriş yapmış, 7 gündür mesaj almayanlar",
			proposalOf(segment.Spec{PurchasedWithinDays: number(7), NotMessagedWithinDays: number(60)}), "window_swapped"},
		"bought became not bought": {"Son 60 günde alışveriş yapmış kişiler", proposalOf(segment.Spec{NotPurchasedWithinDays: number(60)}), "behavior_flipped"},
		"not bought became bought": {"Son 60 gündür alışveriş yapmamış kişiler", proposalOf(segment.Spec{PurchasedWithinDays: number(60)}), "behavior_flipped"},
		"english flipped":          {"users who did not buy in the last 60 days", proposalOf(segment.Spec{PurchasedWithinDays: number(60)}), "behavior_flipped"},
		"cart from nowhere":        {"Son 30 günde alışveriş yapanlar", proposalOf(segment.Spec{CartAddedWithinDays: number(30)}), "behavior_not_in_brief"},
		"purchase from nowhere":    {"Ankara'dakilere son 30 gün duyuru", proposalOf(segment.Spec{City: text("Ankara"), PurchasedWithinDays: number(30)}), "behavior_not_in_brief"},
		"language from nowhere":    {"Ankara kullanıcıları", proposalOf(segment.Spec{City: text("Ankara"), Language: text("tr")}), "language_not_in_brief"},
		"wrong language":           {"İngilizce konuşanlar", proposalOf(segment.Spec{Language: text("tr")}), "language_not_in_brief"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			proposal := tt.proposal
			problems := segment.CheckAgainstBrief(&proposal, tt.brief, groundingCities, nil)
			if problems == nil || !slices.Contains(problems.Reasons, tt.reason) {
				t.Fatalf("got %v, want %s", problems, tt.reason)
			}
		})
	}
}

// These are for a person: the rule leaves out something the brief may mean, and
// only they can say whether it matters.
func TestWhatARuleLeavesOutIsPutToAPerson(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		brief    string
		proposal segment.Proposal
		want     string
	}{
		"a second city":       {"Ankara ve İzmir kullanıcıları", proposalOf(segment.Spec{City: text("Ankara")}), "İzmir"},
		"no city at all":      {"Ankara kullanıcılarına duyuru", proposalOf(segment.Spec{}), "Ankara"},
		"excluded city":       {"Ankara hariç son 7 günde mesaj almayan herkes", proposalOf(segment.Spec{NotMessagedWithinDays: number(7)}), "Ankara"},
		"age dropped":         {"Ankara'daki 25 yaş üstü kullanıcılar", proposalOf(segment.Spec{City: text("Ankara")}), "age"},
		"gender dropped":      {"Ankara'daki kadınlara", proposalOf(segment.Spec{City: text("Ankara")}), "gender"},
		"loyalty dropped":     {"Ankara VIP customers", proposalOf(segment.Spec{City: text("Ankara")}), "loyalty"},
		"cart dropped":        {"Ankara'da sepete ürün ekleyenler", proposalOf(segment.Spec{City: text("Ankara")}), "cart"},
		"purchase dropped":    {"Ankara'da son 60 günde alışveriş yapanlar", proposalOf(segment.Spec{City: text("Ankara")}), "purchase"},
		"no purchase dropped": {"Son 30 günde sepete ekleyip almayanlar", proposalOf(segment.Spec{CartAddedWithinDays: number(30)}), "purchase"},
		"quiet rule dropped":  {"Ankara'da 7 gündür mesaj almayanlar", proposalOf(segment.Spec{City: text("Ankara")}), "message"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			proposal := tt.proposal
			if problems := segment.CheckAgainstBrief(&proposal, tt.brief, groundingCities, nil); problems != nil {
				t.Fatalf("rejected: %v", problems)
			}
			if !slices.ContainsFunc(proposal.Unmatched, func(note string) bool { return strings.Contains(note, tt.want) }) {
				t.Fatalf("unmatched: %v, want %s", proposal.Unmatched, tt.want)
			}
		})
	}
}

func TestACampaignOptionNeedNotBeInTheBrief(t *testing.T) {
	t.Parallel()
	proposal := proposalOf(segment.Spec{City: text("Ankara"), NotMessagedWithinDays: number(7)})
	if problems := segment.CheckAgainstBrief(&proposal, "Ankara kullanıcıları", groundingCities, number(7)); problems != nil {
		t.Fatalf("blocked: %v", problems)
	}
	other := proposalOf(segment.Spec{City: text("Ankara"), NotMessagedWithinDays: number(30)})
	if problems := segment.CheckAgainstBrief(&other, "Ankara kullanıcıları", groundingCities, number(7)); problems == nil {
		t.Fatal("only the chosen option is exempt")
	}
}

func TestUnmatchedIsNeverTakenFromAModel(t *testing.T) {
	t.Parallel()
	_, err := segment.ParseProposal([]byte(`{"segment":{"city":"Ankara"},"assumptions":[],"unsupported_conditions":[],"unmatched":["x"]}`), groundingCities)
	if err == nil {
		t.Fatal("a model cannot write this field")
	}
}

// A place is known by how the sentence uses it, so no list of places can run out.
func TestAPlaceTheBriefUsesIsSeenWithoutAList(t *testing.T) {
	t.Parallel()
	for brief, want := range map[string]string{
		"Ankara müşterileri ve Paris'teki kullanıcılar": "Paris",
		"Ankara müşterileri, Van'daki kullanıcılar":     "Van",
		"Ankara müşterileri ve Ordu'da yaşayanlar":      "Ordu",
		"Ankara customers and people in Berlin":         "Berlin",
		"Ankara and Kars users":                         "Kars",
		"Ankara and Tokyo customers":                    "Tokyo",
		"Oslo customers in Ankara":                      "Oslo",
	} {
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			proposal := proposalOf(segment.Spec{City: text("Ankara")})
			if problems := segment.CheckAgainstBrief(&proposal, brief, groundingCities, nil); problems != nil {
				t.Fatalf("rejected: %v", problems)
			}
			if !slices.Contains(proposal.Unmatched, want) {
				t.Fatalf("unmatched: %v, want %s", proposal.Unmatched, want)
			}
		})
	}
}

// The paired briefs: capitalised words and these same endings that are not places.
func TestWordsThatOnlyLookLikePlacesPass(t *testing.T) {
	t.Parallel()
	for _, brief := range []string{
		"Ankara'daki müşterilere Kasım'da büyük indirim",
		"Ankara customers, sale ends in December",
		"Ankara customers who write in English",
		"Ankara'daki kullanıcılara Cuma'ya kadar kargo bedava",
		"All Ankara users",
		"Tüm Ankara kullanıcıları",
		"Our Ankara customers",
		"Ramazan'da Ankara kullanıcılarına duyuru",
		"Ankara kullanıcılarına Ayşe'den selam",
		"Remind users in Ankara about the sale",
		"Notify customers in Ankara",
		"Bu kullanıcılara Ankara'da duyuru",
		"Send this to Ankara users. Customers will like it.",
	} {
		t.Run(brief, func(t *testing.T) {
			t.Parallel()
			proposal := proposalOf(segment.Spec{City: text("Ankara")})
			if problems := segment.CheckAgainstBrief(&proposal, brief, groundingCities, nil); problems != nil {
				t.Fatalf("rejected: %v", problems)
			}
			if len(proposal.Unmatched) != 0 {
				t.Fatalf("unmatched: %v", proposal.Unmatched)
			}
		})
	}
}

func TestAPlaceWrittenAsUnsupportedIsNotCountedTwice(t *testing.T) {
	t.Parallel()
	proposal := proposalOf(segment.Spec{City: text("Ankara")})
	proposal.UnsupportedConditions = []string{"Paris'teki kullanıcılar"}
	if problems := segment.CheckAgainstBrief(&proposal, "Ankara ve Paris'teki kullanıcılar", groundingCities, nil); problems != nil {
		t.Fatal(problems)
	}
	if len(proposal.Unmatched) != 0 {
		t.Fatalf("unmatched: %v", proposal.Unmatched)
	}
}

// A word used like a place may be something else. Saying so lifts the block and stays
// visible, so a wrong word never traps a person and a wrong claim can still be seen.
func TestAModelCanSayANameIsNotAboutTheAudience(t *testing.T) {
	t.Parallel()
	for brief, name := range map[string]string{
		"Instagram'da gördüğünüz ürünler için Ankara müşterilerine duyuru": "Instagram",
		"İzmir mağazamız açıldı, Ankara müşterilerine duyuru":              "İzmir",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			proposal := proposalOf(segment.Spec{City: text("Ankara")})
			proposal.NotAudience = []string{name}
			if problems := segment.CheckAgainstBrief(&proposal, brief, groundingCities, nil); problems != nil {
				t.Fatal(problems)
			}
			if len(proposal.Unmatched) != 0 {
				t.Fatalf("unmatched: %v", proposal.Unmatched)
			}
		})
	}
}

// When the brief itself says these are the people, no claim can wave them away.
func TestAModelCannotWaveAwayWhoTheBriefNames(t *testing.T) {
	t.Parallel()
	for brief, name := range map[string]string{
		"Ankara ve İzmir kullanıcıları":                 "İzmir",
		"Ankara müşterileri ve Paris'teki kullanıcılar": "Paris",
		"Ankara and Tokyo customers":                    "Tokyo",
		"Ankara müşterileri, Ordu'da yaşayanlar":        "Ordu",
		"Ankara customers and people in Berlin":         "Berlin",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			proposal := proposalOf(segment.Spec{City: text("Ankara")})
			proposal.NotAudience = []string{name}
			if problems := segment.CheckAgainstBrief(&proposal, brief, groundingCities, nil); problems != nil {
				t.Fatal(problems)
			}
			if !slices.Contains(proposal.Unmatched, name) {
				t.Fatalf("unmatched: %v, want %s", proposal.Unmatched, name)
			}
		})
	}
}

// "müşterilerine" names everyone the rule is about, so it cannot be a missing condition.
func TestAGenericWordForPeopleIsNotAnUnsupportedCondition(t *testing.T) {
	t.Parallel()
	proposal := proposalOf(segment.Spec{City: text("Ankara")})
	proposal.UnsupportedConditions = []string{"müşterilerine", "Customers", "25 yaş üstü müşteriler"}
	if problems := segment.CheckAgainstBrief(&proposal, "Ankara'daki 25 yaş üstü müşterilerine", groundingCities, nil); problems != nil {
		t.Fatal(problems)
	}
	if len(proposal.UnsupportedConditions) != 1 || proposal.UnsupportedConditions[0] != "25 yaş üstü müşteriler" {
		t.Fatalf("unsupported: %v", proposal.UnsupportedConditions)
	}
}
