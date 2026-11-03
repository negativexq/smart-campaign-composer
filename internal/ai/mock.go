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
	noBuyPattern  = regexp.MustCompile(`aliş ?veriş yapma|satin alma|almayan|did not buy|didn't buy|not purchased|not bought|no purchase|haven't purchased|without purchas|without buy|abandon`)
	boughtPattern = regexp.MustCompile(`purchased|bought|satin alan|aliş ?veriş yapan`)
	clearPattern  = regexp.MustCompile(`remove|drop|ignore|kaldir|çikar`)
)

func mockFold(text string) string {
	return strings.ToLower(mockFolder.Replace(text))
}

// ProposeSegment implements Provider.
func (*Mock) ProposeSegment(_ context.Context, request SegmentRequest) (Response, error) {
	proposal := segment.Proposal{Assumptions: []segment.Assumption{}, UnsupportedConditions: []string{}}
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
	applyFilters(&proposal, folded, request.Cities)
	proposal.CampaignName = mockName(proposal.Segment)

	output, err := json.Marshal(proposal)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %w", ErrInvalidOutput, err)
	}
	return Response{Output: output}, nil
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
	mockTurkish  = regexp.MustCompile(`[çğıİöşüÇĞÖŞÜ]|indirim|gönder|sepet|kargo`)
)

// StreamContent implements Provider. It writes one of two wordings per language,
// switching when regenerating, and carries the discount the text asks for.
func (m *Mock) StreamContent(ctx context.Context, request ContentRequest, handler StreamHandler) (Response, error) {
	source := content.OfferSource(request.Brief, request.Instruction)
	draft := mockDraft(mockTurkish.MatchString(request.Brief), mockDiscount.FindString(source), string(request.Current))
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

func mockDraft(turkish bool, offer, current string) content.Content {
	offerSMS, offerEmail := "", ""
	if offer != "" {
		if turkish {
			offerSMS, offerEmail = " "+offer+" indirimin hazır.", " Sana özel "+offer+" indirimin seni bekliyor."
		} else {
			offerSMS, offerEmail = " Your "+offer+" discount is ready.", " Your "+offer+" discount is waiting for you."
		}
	}
	first := mockWording(turkish, 0, offerSMS, offerEmail)
	if current != "" && strings.Contains(current, mustJSON(first.SMS)) {
		return mockWording(turkish, 1, offerSMS, offerEmail)
	}
	return first
}

func mustJSON(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded)
}

func mockWording(turkish bool, variant int, offerSMS, offerEmail string) content.Content {
	if turkish {
		if variant == 0 {
			return content.Content{
				SMS:          "Merhaba {{first_name}}, seni unutmadık." + offerSMS,
				EmailSubject: "{{first_name}}, sana bir haberimiz var",
				EmailBody:    "Merhaba {{first_name}},\n\nSeçtiklerin hâlâ burada." + offerEmail + "\n\nTekrar görüşmek üzere.",
			}
		}
		return content.Content{
			SMS:          "{{first_name}}, kaldığın yerden devam et." + offerSMS,
			EmailSubject: "Kaldığın yerden devam et, {{first_name}}",
			EmailBody:    "Selam {{first_name}},\n\nKısa bir hatırlatma yapmak istedik." + offerEmail + "\n\nGörüşmek üzere.",
		}
	}
	if variant == 0 {
		return content.Content{
			SMS:          "Hi {{first_name}}, we saved your spot." + offerSMS,
			EmailSubject: "{{first_name}}, we have news for you",
			EmailBody:    "Hi {{first_name}},\n\nWhat you picked is still here." + offerEmail + "\n\nSee you soon.",
		}
	}
	return content.Content{
		SMS:          "{{first_name}}, pick up where you left off." + offerSMS,
		EmailSubject: "Pick up where you left off, {{first_name}}",
		EmailBody:    "Hello {{first_name}},\n\nA quick reminder from us." + offerEmail + "\n\nThanks for reading.",
	}
}
