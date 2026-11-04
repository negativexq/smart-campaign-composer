package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/segment"
)

const (
	defaultCartWindowDays = 30
	cartWindowField       = "cart_added_within_days"
)

// Mock is a deterministic keyword matcher. It lets the whole product run and be
// tested without an API key; it does not attempt to be clever.
type Mock struct {
	// StreamDelay spaces out stream chunks.
	StreamDelay time.Duration
}

// NewMock builds the mock provider.
func NewMock() *Mock { return &Mock{} }

// Name implements Provider.
func (*Mock) Name() string { return "mock" }

// Model implements Provider.
func (*Mock) Model() string { return "keyword-matcher" }

var (
	mockFolder    = strings.NewReplacer("İ", "i", "I", "i", "ı", "i")
	daysPattern   = regexp.MustCompile(`(\d+)\s*(?:gün|gun|days?)`)
	agePattern    = regexp.MustCompile(`(^|[^a-z])age([^a-z]|$)|yaş|years old`)
	genderPattern = regexp.MustCompile(`gender|cinsiyet`)
	tierPattern   = regexp.MustCompile(`(^|[^a-z])vip([^a-z]|$)|loyal|sadik`)
	cartPattern   = regexp.MustCompile(`sepet|cart|abandon`)
	noBuyPattern  = regexp.MustCompile(`aliş ?veriş yapma|satin alma|almayan|did not buy|didn't buy|did not purchase|didn't purchase|not purchased|not bought|no purchase|haven't purchased|without purchas|without buy|abandon`)
	boughtPattern = regexp.MustCompile(`purchased|bought|satin alan|aliş ?veriş yapan`)
	quietPattern  = regexp.MustCompile(`(?:not messaged|no message)[^0-9]*(\d+)\s*days?|(\d+)\s*g[uü]n\S*\s*mesaj almayan`)
	clearPattern  = regexp.MustCompile(`remove|drop|ignore|kaldir|çikar`)

	languagePattern = regexp.MustCompile(`english|ingilizce|turkish|türkçe`)
)

const mockQuoteWords = 8

func mockFold(text string) string {
	return strings.ToLower(mockFolder.Replace(text))
}

// ProposeSegment implements Provider.
func (*Mock) ProposeSegment(_ context.Context, request SegmentRequest) (Response, error) {
	proposal := segment.Proposal{Assumptions: []segment.Assumption{}, Evidence: []segment.Evidence{}, UnsupportedConditions: []string{}}
	text := request.Brief
	if len(request.Current) > 0 {
		if err := json.Unmarshal(request.Current, &proposal); err != nil {
			return Response{}, fmt.Errorf("%w: current proposal: %w", ErrInvalidOutput, err)
		}
		text = request.Refinement
	}
	folded := mockFold(text)

	if len(request.Current) > 0 && clearPattern.MatchString(folded) {
		proposal.UnsupportedConditions = []string{}
	} else {
		addUnsupported(&proposal, folded)
	}
	before := proposal.Segment
	applyFilters(&proposal, folded, request.Cities)
	addMockEvidence(&proposal, before, text)
	proposal.CampaignName = mockName(proposal.Segment)
	proposal.BriefParts = nil
	if request.Refinement == "" {
		proposal.BriefParts = mockParts(request.Brief, proposal.Evidence)
	}
	proposal.UpdatedBrief = ""
	if request.Refinement != "" {
		proposal.UpdatedBrief = mockUpdatedBrief(request.Brief, request.Refinement, before.City, proposal.Segment.City)
	}

	output, err := json.Marshal(proposal)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrInvalidOutput, err)
	}
	return Response{Output: output}, nil
}

// mockParts marks what the keywords used and calls the rest other. A condition the
// mock can name is reported in unsupported_conditions, as it cannot quote one.
func mockParts(brief string, evidence []segment.Evidence) []segment.BriefPart {
	parts := []segment.BriefPart{}
	rest := brief
	for _, quote := range evidence {
		before, after, found := strings.Cut(rest, quote.Quote)
		if !found {
			continue
		}
		if strings.TrimSpace(before) != "" {
			parts = append(parts, segment.BriefPart{Text: before, Kind: segment.PartOther})
		}
		parts = append(parts, segment.BriefPart{Text: quote.Quote, Kind: segment.PartAudience, UsedAs: []string{quote.Field}})
		rest = after
	}
	if strings.TrimSpace(rest) != "" {
		parts = append(parts, segment.BriefPart{Text: rest, Kind: segment.PartOther})
	}
	return parts
}

// mockUpdatedBrief swaps a day count or city the refinement changes and otherwise
// adds the refinement to the end, which is as far as keywords can rewrite a sentence.
func mockUpdatedBrief(brief, refinement string, oldCity, newCity *string) string {
	wanted := daysPattern.FindStringSubmatch(refinement)
	current := daysPattern.FindStringSubmatchIndex(brief)
	if wanted != nil && current != nil && !quietPattern.MatchString(mockFold(refinement)) {
		return brief[:current[2]] + wanted[1] + brief[current[3]:]
	}
	if oldCity != nil && newCity != nil && *oldCity != *newCity && strings.Contains(brief, *oldCity) {
		return strings.ReplaceAll(brief, *oldCity, *newCity)
	}
	if clearPattern.MatchString(mockFold(refinement)) {
		return refinement
	}
	return brief + " " + refinement
}

func addUnsupported(proposal *segment.Proposal, folded string) {
	for _, unsupported := range []struct {
		pattern *regexp.Regexp
		label   string
	}{
		{agePattern, "Age"},
		{genderPattern, "Gender"},
		{tierPattern, "Loyalty tier"},
	} {
		if unsupported.pattern.MatchString(folded) && !slices.Contains(proposal.UnsupportedConditions, unsupported.label) {
			proposal.UnsupportedConditions = append(proposal.UnsupportedConditions, unsupported.label)
		}
	}
}

func applyFilters(proposal *segment.Proposal, folded string, cities []string) {
	spec := &proposal.Segment
	if match := quietPattern.FindStringSubmatch(folded); match != nil {
		if days, err := strconv.Atoi(match[1] + match[2]); err == nil {
			spec.NotMessagedWithinDays = &days
		}
		// The rest of the text has its own day count.
		folded = strings.Replace(folded, match[0], "", 1)
	}
	for _, city := range cities {
		if strings.Contains(folded, mockFold(city)) {
			spec.City = &city
			break
		}
	}
	for code, words := range map[string][]string{"en": {"english", "ingilizce"}, "tr": {"turkish", "türkçe"}} {
		for _, word := range words {
			if strings.Contains(folded, word) {
				spec.Language = &code
			}
		}
	}

	days, hasDays := parseDays(folded)
	mentionsCart := cartPattern.MatchString(folded)
	noBuy := noBuyPattern.MatchString(folded)

	switch {
	case mentionsCart || spec.CartAddedWithinDays != nil && hasDays:
		if hasDays {
			spec.CartAddedWithinDays = &days
			removeAssumption(proposal, cartWindowField)
		} else if spec.CartAddedWithinDays == nil {
			window := defaultCartWindowDays
			spec.CartAddedWithinDays = &window
			proposal.Assumptions = append(proposal.Assumptions, segment.Assumption{
				Field: cartWindowField,
				Note:  fmt.Sprintf("The brief gives no time window, so %d days is assumed.", window),
			})
		}
		if noBuy {
			yes := true
			spec.NoPurchaseSinceCart = &yes
		}
	case noBuy && hasDays:
		spec.NotPurchasedWithinDays = &days
	case boughtPattern.MatchString(folded) && hasDays:
		spec.PurchasedWithinDays = &days
	}
}

// addMockEvidence quotes the words behind each filter this text changed. A filter
// it did not touch keeps its earlier quote.
func addMockEvidence(proposal *segment.Proposal, before segment.Spec, text string) {
	spec := proposal.Segment
	for _, filter := range []struct {
		field   string
		changed bool
		pattern *regexp.Regexp
	}{
		{"city", spec.City != nil && !samePointer(before.City, spec.City), cityWords(spec.City)},
		{"language", spec.Language != nil && !samePointer(before.Language, spec.Language), languagePattern},
		{cartWindowField, spec.CartAddedWithinDays != nil && !samePointer(before.CartAddedWithinDays, spec.CartAddedWithinDays), cartPattern},
		{cartWindowField, spec.CartAddedWithinDays != nil && !samePointer(before.CartAddedWithinDays, spec.CartAddedWithinDays), daysPattern},
		{"no_purchase_since_cart", spec.NoPurchaseSinceCart != nil && !samePointer(before.NoPurchaseSinceCart, spec.NoPurchaseSinceCart), noBuyPattern},
		{"purchased_within_days", spec.PurchasedWithinDays != nil && !samePointer(before.PurchasedWithinDays, spec.PurchasedWithinDays), boughtPattern},
		{"not_purchased_within_days", spec.NotPurchasedWithinDays != nil && !samePointer(before.NotPurchasedWithinDays, spec.NotPurchasedWithinDays), noBuyPattern},
		{"not_messaged_within_days", spec.NotMessagedWithinDays != nil && !samePointer(before.NotMessagedWithinDays, spec.NotMessagedWithinDays), quietPattern},
	} {
		if !filter.changed || filter.pattern == nil {
			continue
		}
		if quote := mockQuote(text, filter.pattern); quote != "" {
			proposal.Evidence = append(proposal.Evidence, segment.Evidence{Field: filter.field, Quote: quote})
		}
	}
}

func samePointer[T comparable](a, b *T) bool {
	return a != nil && b != nil && *a == *b
}

func cityWords(city *string) *regexp.Regexp {
	if city == nil {
		return nil
	}
	return regexp.MustCompile(regexp.QuoteMeta(mockFold(*city)))
}

// mockQuote finds the fewest consecutive words of text that match pattern.
func mockQuote(text string, pattern *regexp.Regexp) string {
	words := strings.Fields(text)
	for size := 1; size <= mockQuoteWords; size++ {
		for start := 0; start+size <= len(words); start++ {
			window := strings.Join(words[start:start+size], " ")
			if pattern.MatchString(mockFold(window)) {
				return strings.TrimRight(window, ".,;:!?")
			}
		}
	}
	return ""
}

func mockName(spec segment.Spec) string {
	name := "Audience"
	switch {
	case spec.NoPurchaseSinceCart != nil && *spec.NoPurchaseSinceCart:
		name = "Cart Recovery"
	case spec.CartAddedWithinDays != nil:
		name = "Cart Activity"
	case spec.NotPurchasedWithinDays != nil:
		name = "Win-back"
	case spec.PurchasedWithinDays != nil:
		name = "Recent Buyers"
	}
	if spec.City != nil {
		name += " — " + *spec.City
	}
	return name
}

func parseDays(folded string) (int, bool) {
	match := daysPattern.FindStringSubmatch(folded)
	if match == nil {
		return 0, false
	}
	days, err := strconv.Atoi(match[1])
	return days, err == nil
}

func removeAssumption(proposal *segment.Proposal, field string) {
	proposal.Assumptions = slices.DeleteFunc(proposal.Assumptions, func(assumption segment.Assumption) bool {
		return assumption.Field == field
	})
}

const mockChunkSize = 12

var (
	mockDiscount = regexp.MustCompile(`%\s*\d+|\d+\s*%`)
	mockLink     = regexp.MustCompile(`https?://[^\s<>"]+[^\s<>".,;:!?)]`)
	mockTurkish  = regexp.MustCompile(`[çğıİöşüÇĞÖŞÜ]|indirim|gönder|sepet|kargo`)
)

// StreamContent implements Provider. It writes one of two wordings per language,
// switching when regenerating, and carries the discount the text asks for.
func (m *Mock) StreamContent(ctx context.Context, request ContentRequest, handler StreamHandler) (Response, error) {
	source := content.OfferSource(request.Brief, request.Instruction)
	// The language tag holds a language's name, and older callers leave it empty.
	turkish := request.Language == "Turkish" || (request.Language == "" && mockTurkish.MatchString(request.Brief))
	avoid := string(request.Current)
	if len(request.OtherVariant) > 0 {
		avoid = string(request.OtherVariant)
	}
	second := strings.Contains(avoid, "seni unutmadık") || strings.Contains(avoid, "we saved your spot")
	// Another language's version says what this one says, in the same wording.
	if len(request.Reference) > 0 {
		second = strings.Contains(string(request.Reference), "kaldığın yerden") || strings.Contains(string(request.Reference), "pick up where you left off")
	}
	draft := mockDraft(turkish, mockDiscount.FindString(source), second)
	draft = mockTone(draft, request.Tone, turkish)
	if link := mockLink.FindString(request.Brief + " " + request.Instruction); link != "" {
		draft.EmailCTAURL, draft.EmailCTALabel = link, "See the offer"
		if turkish {
			draft.EmailCTALabel = "Teklifi gör"
		}
	}
	draft = request.Limits.Keep(draft)
	output, err := json.Marshal(draft)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrInvalidOutput, err)
	}

	runes := []rune(string(output))
	for start := 0; start < len(runes); start += mockChunkSize {
		select {
		case <-ctx.Done():
			return Response{}, ctx.Err()
		case <-time.After(m.StreamDelay):
		}
		handler.OnText(string(runes[start:min(start+mockChunkSize, len(runes))]))
	}
	return Response{Output: output}, nil
}

func mockDraft(turkish bool, offer string, second bool) content.Content {
	offerSMS, offerEmail := "", ""
	if offer != "" {
		if turkish {
			offerSMS, offerEmail = " "+offer+" indirimin hazır.", " Sana özel "+offer+" indirimin seni bekliyor."
		} else {
			offerSMS, offerEmail = " Your "+offer+" discount is ready.", " Your "+offer+" discount is waiting for you."
		}
	}
	if second {
		return mockWording(turkish, 1, offerSMS, offerEmail)
	}
	return mockWording(turkish, 0, offerSMS, offerEmail)
}

// mockTone makes the chosen tone visible, as a real model's wording would.
func mockTone(draft content.Content, tone string, turkish bool) content.Content {
	switch tone {
	case "formal":
		greeting := strings.NewReplacer("Hi {{", "Dear {{", "Hello {{", "Dear {{")
		if turkish {
			greeting = strings.NewReplacer("Merhaba {{", "Sayın {{", "Selam {{", "Sayın {{")
		}
		draft.SMS, draft.EmailBody = greeting.Replace(draft.SMS), greeting.Replace(draft.EmailBody)
	case "urgent":
		hurry := " Hurry!"
		if turkish {
			hurry = " Acele et!"
		}
		draft.SMS += hurry
		draft.EmailBody += hurry
	}
	return draft
}

func mustJSON(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded)
}

func mockWording(turkish bool, variant int, offerSMS, offerEmail string) content.Content {
	if turkish {
		if variant == 0 {
			return content.Content{
				SMS:            "Merhaba {{first_name}}, seni unutmadık." + offerSMS,
				EmailSubject:   "{{first_name}}, sana bir haberimiz var",
				EmailPreheader: "Seçtiklerin seni bekliyor.",
				EmailHeadline:  "Seni tekrar görmek güzel",
				EmailBody:      "Merhaba {{first_name}},\n\nSeçtiklerin hâlâ burada." + offerEmail + "\n\nTekrar görüşmek üzere.",
			}
		}
		return content.Content{
			SMS:            "{{first_name}}, kaldığın yerden devam et." + offerSMS,
			EmailSubject:   "Kaldığın yerden devam et, {{first_name}}",
			EmailPreheader: "Kısa bir hatırlatma.",
			EmailHeadline:  "Bıraktığın yerde duruyor",
			EmailBody:      "Selam {{first_name}},\n\nKısa bir hatırlatma yapmak istedik." + offerEmail + "\n\nGörüşmek üzere.",
		}
	}
	if variant == 0 {
		return content.Content{
			SMS:            "Hi {{first_name}}, we saved your spot." + offerSMS,
			EmailSubject:   "{{first_name}}, we have news for you",
			EmailPreheader: "What you picked is waiting for you.",
			EmailHeadline:  "Good to see you again",
			EmailBody:      "Hi {{first_name}},\n\nWhat you picked is still here." + offerEmail + "\n\nSee you soon.",
		}
	}
	return content.Content{
		SMS:            "{{first_name}}, pick up where you left off." + offerSMS,
		EmailSubject:   "Pick up where you left off, {{first_name}}",
		EmailPreheader: "A quick reminder from us.",
		EmailHeadline:  "Still where you left it",
		EmailBody:      "Hello {{first_name}},\n\nA quick reminder from us." + offerEmail + "\n\nThanks for reading.",
	}
}
