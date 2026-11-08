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

	languagePattern = regexp.MustCompile(`english|ingilizce|turkish|türkçe|german|deutsch|almanca|french|français|fransızca|spanish|español|ispanyolca`)
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
	for code, words := range map[string][]string{"en": {"english", "ingilizce"}, "tr": {"turkish", "türkçe"}, "de": {"german", "deutsch", "almanca"}, "fr": {"french", "français", "fransızca"}, "es": {"spanish", "español", "ispanyolca"}} {
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
	name := request.Language
	if name == "" {
		name = "English"
		if mockTurkish.MatchString(request.Brief) {
			name = "Turkish"
		}
	}
	copy, known := mockCopies[name]
	if !known {
		copy = mockCopies["English"]
	}
	avoid := string(request.Current)
	if len(request.OtherVariant) > 0 {
		avoid = string(request.OtherVariant)
	}
	second := mockMentions(avoid, 0)
	// Another language's version says what this one says, in the same wording.
	if len(request.Reference) > 0 {
		second = mockMentions(string(request.Reference), 1)
	}
	draft := mockDraft(copy, mockDiscount.FindString(source), second)
	draft = mockTone(draft, request.Tone, copy)
	// A code is never typed out: the brief asks for the variable, and the backend fills it.
	// The email shows the code in a box of its own, so only the SMS names it.
	if strings.Contains(request.Brief, "{{"+content.CodeVariable+"}}") {
		draft.SMS += fmt.Sprintf(copy.code, "{{"+content.CodeVariable+"}}")
	}
	// The SMS carries the link as {{link}}, which becomes a short link when it is sent.
	if strings.Contains(request.Brief, "{{"+content.LinkVariable+"}}") {
		draft.SMS += " {{" + content.LinkVariable + "}}"
	}
	if link := mockLink.FindString(request.Brief + " " + request.Instruction); link != "" {
		draft.EmailCTAURL, draft.EmailCTALabel = link, copy.ctaLabel
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

func mockDraft(copy mockCopy, offer string, second bool) content.Content {
	offerSMS, offerEmail := "", ""
	if offer != "" {
		offerSMS, offerEmail = fmt.Sprintf(copy.offerSMS, offer), fmt.Sprintf(copy.offerEmail, offer)
	}
	wording := copy.wordings[0]
	if second {
		wording = copy.wordings[1]
	}
	wording.SMS += offerSMS
	wording.EmailBody = strings.Replace(wording.EmailBody, mockOfferSlot, offerEmail, 1)
	return wording
}

// mockTone makes the chosen tone visible, as a real model's wording would.
func mockTone(draft content.Content, tone string, copy mockCopy) content.Content {
	switch tone {
	case "formal":
		draft.SMS, draft.EmailBody = copy.formal.Replace(draft.SMS), copy.formal.Replace(draft.EmailBody)
	case "urgent":
		draft.SMS += copy.hurry
		draft.EmailBody += copy.hurry
	}
	return draft
}

func mustJSON(text string) string {
	encoded, _ := json.Marshal(text)
	return string(encoded)
}

// mockOfferSlot marks where an email's body names the discount.
const mockOfferSlot = "{offer}"

// mockCopy is what the mock writes in one language: two wordings, so regenerating and
// a test's second version read differently.
type mockCopy struct {
	wordings             [2]content.Content
	markers              [2]string
	offerSMS, offerEmail string
	// code names the discount code, with %s where it goes.
	code            string
	formal          *strings.Replacer
	hurry, ctaLabel string
}

var mockCopies = map[string]mockCopy{
	"Turkish": {
		wordings: [2]content.Content{{
			SMS:            "Merhaba {{first_name}}, seni unutmadık.",
			EmailSubject:   "{{first_name}}, sana bir haberimiz var",
			EmailPreheader: "Seçtiklerin seni bekliyor.",
			EmailHeadline:  "Seni tekrar görmek güzel",
			EmailBody:      "Merhaba {{first_name}},\n\nSeçtiklerin hâlâ burada.{offer}\n\nTekrar görüşmek üzere.",
		}, {
			SMS:            "{{first_name}}, kaldığın yerden devam et.",
			EmailSubject:   "Kaldığın yerden devam et, {{first_name}}",
			EmailPreheader: "Kısa bir hatırlatma.",
			EmailHeadline:  "Bıraktığın yerde duruyor",
			EmailBody:      "Selam {{first_name}},\n\nKısa bir hatırlatma yapmak istedik.{offer}\n\nGörüşmek üzere.",
		}},
		markers:  [2]string{"seni unutmadık", "kaldığın yerden"},
		offerSMS: " %s indirimin hazır.", offerEmail: " Sana özel %s indirimin seni bekliyor.",
		code:   " Kod: %s",
		formal: strings.NewReplacer("Merhaba {{", "Sayın {{", "Selam {{", "Sayın {{"),
		hurry:  " Acele et!", ctaLabel: "Teklifi gör",
	},
	"English": {
		wordings: [2]content.Content{{
			SMS:            "Hi {{first_name}}, we saved your spot.",
			EmailSubject:   "{{first_name}}, we have news for you",
			EmailPreheader: "What you picked is waiting for you.",
			EmailHeadline:  "Good to see you again",
			EmailBody:      "Hi {{first_name}},\n\nWhat you picked is still here.{offer}\n\nSee you soon.",
		}, {
			SMS:            "{{first_name}}, pick up where you left off.",
			EmailSubject:   "Pick up where you left off, {{first_name}}",
			EmailPreheader: "A quick reminder from us.",
			EmailHeadline:  "Still where you left it",
			EmailBody:      "Hello {{first_name}},\n\nA quick reminder from us.{offer}\n\nThanks for reading.",
		}},
		markers:  [2]string{"we saved your spot", "pick up where"},
		offerSMS: " Your %s discount is ready.", offerEmail: " Your %s discount is waiting for you.",
		code:   " Code: %s",
		formal: strings.NewReplacer("Hi {{", "Dear {{", "Hello {{", "Dear {{"),
		hurry:  " Hurry!", ctaLabel: "See the offer",
	},
	"German": {
		wordings: [2]content.Content{{
			SMS:            "Hallo {{first_name}}, wir haben an dich gedacht.",
			EmailSubject:   "{{first_name}}, wir haben Neuigkeiten für dich",
			EmailPreheader: "Deine Auswahl wartet auf dich.",
			EmailHeadline:  "Schön, dich wiederzusehen",
			EmailBody:      "Hallo {{first_name}},\n\ndeine Auswahl ist noch da.{offer}\n\nBis bald.",
		}, {
			SMS:            "{{first_name}}, mach dort weiter, wo du aufgehört hast.",
			EmailSubject:   "Mach dort weiter, wo du aufgehört hast, {{first_name}}",
			EmailPreheader: "Eine kurze Erinnerung von uns.",
			EmailHeadline:  "Noch genau dort, wo du es gelassen hast",
			EmailBody:      "Hallo {{first_name}},\n\neine kurze Erinnerung von uns.{offer}\n\nDanke fürs Lesen.",
		}},
		markers:  [2]string{"an dich gedacht", "aufgehört hast"},
		offerSMS: " Dein Rabatt von %s ist bereit.", offerEmail: " Dein Rabatt von %s wartet auf dich.",
		code:   " Code: %s",
		formal: strings.NewReplacer("Hallo {{", "Guten Tag {{"),
		hurry:  " Schnell!", ctaLabel: "Angebot ansehen",
	},
	"French": {
		wordings: [2]content.Content{{
			SMS:            "Bonjour {{first_name}}, nous pensons à vous.",
			EmailSubject:   "{{first_name}}, nous avons du nouveau pour vous",
			EmailPreheader: "Votre sélection vous attend.",
			EmailHeadline:  "Heureux de vous revoir",
			EmailBody:      "Bonjour {{first_name}},\n\nVotre sélection est toujours là.{offer}\n\nÀ bientôt.",
		}, {
			SMS:            "{{first_name}}, reprenez là où vous en étiez.",
			EmailSubject:   "Reprenez là où vous en étiez, {{first_name}}",
			EmailPreheader: "Un petit rappel de notre part.",
			EmailHeadline:  "Toujours là où vous l'avez laissé",
			EmailBody:      "Bonjour {{first_name}},\n\nUn petit rappel de notre part.{offer}\n\nMerci de votre lecture.",
		}},
		markers:  [2]string{"nous pensons à vous", "reprenez là"},
		offerSMS: " Votre réduction de %s est prête.", offerEmail: " Votre réduction de %s vous attend.",
		code:   " Code : %s",
		formal: strings.NewReplacer("Bonjour {{", "Madame, Monsieur {{"),
		hurry:  " Vite !", ctaLabel: "Voir l'offre",
	},
	"Spanish": {
		wordings: [2]content.Content{{
			SMS:            "Hola {{first_name}}, pensamos en ti.",
			EmailSubject:   "{{first_name}}, tenemos novedades para ti",
			EmailPreheader: "Lo que elegiste te está esperando.",
			EmailHeadline:  "Qué bueno verte de nuevo",
			EmailBody:      "Hola {{first_name}},\n\nLo que elegiste sigue aquí.{offer}\n\nHasta pronto.",
		}, {
			SMS:            "{{first_name}}, sigue donde lo dejaste.",
			EmailSubject:   "Sigue donde lo dejaste, {{first_name}}",
			EmailPreheader: "Un breve recordatorio.",
			EmailHeadline:  "Sigue donde lo dejaste",
			EmailBody:      "Hola {{first_name}},\n\nUn breve recordatorio de nuestra parte.{offer}\n\nGracias por leer.",
		}},
		markers:  [2]string{"pensamos en ti", "donde lo dejaste"},
		offerSMS: " Tu descuento del %s está listo.", offerEmail: " Tu descuento del %s te espera.",
		code:   " Código: %s",
		formal: strings.NewReplacer("Hola {{", "Estimado {{"),
		hurry:  " ¡Date prisa!", ctaLabel: "Ver la oferta",
	},
}

// mockMentions says whether text uses one of a language's wordings, in any language.
func mockMentions(text string, wording int) bool {
	lowered := strings.ToLower(text)
	for _, copy := range mockCopies {
		if strings.Contains(lowered, copy.markers[wording]) {
			return true
		}
	}
	return false
}
