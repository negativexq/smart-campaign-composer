package segment

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/negativexq/smart-campaign-composer/internal/matching"
)

const (
	subjectCart     = "cart"
	subjectPurchase = "purchase"
	subjectMessage  = "message"
	// A length of time further than this from a word says nothing about that word.
	maxBindingDistance = 6
)

var (
	cartWord = regexp.MustCompile(`^(sepet|carts?$|baskets?$|abandon|terk)`)
	// Words for leaving a cart behind, which say no purchase on their own.
	leftBehindWord = regexp.MustCompile(`^(abandon|terk|birak)`)
	purchaseWord   = regexp.MustCompile(`^(alisveris|satin|siparis|purchas|bought$|buy|buyer|order|alan$|alanlar|almis)`)
	// A Turkish verb made negative, whatever the verb: vermeyen, uğramayan, olmayan.
	turkishNot  = regexp.MustCompile(`^\pL+m[ae](y[ae]n|m[iu]s|d[iu]|y[iu]p|z$|dan$|den$)`)
	messageWord = regexp.MustCompile(`^(mesaj|messag|ileti)`)
	notWords    = []string{"not", "never", "no", "nothing", "none", "without", "didn", "haven", "hasn", "dont", "havent", "hasnt", "didnt", "inactive", "yok", "hic", "uzak"}

	ageWord     = regexp.MustCompile(`^(yas|yasinda|yasindan|yasinin|yasli|yaslilar|genc|gencler|age|aged)$`)
	genderWord  = regexp.MustCompile(`^(kadin|kadinlar|kadinlara|erkek|erkekler|erkeklere|gender|cinsiyet|female|females|male|males|women|men)$`)
	loyaltyWord = regexp.MustCompile(`^(vip|gold|premium|sadik|loyal|loyalty)$`)

	languageWords = map[string]*regexp.Regexp{
		"tr": regexp.MustCompile(`^(turkce|turkish)$`),
		"en": regexp.MustCompile(`^(ingilizce|english)$`),
	}
)

// CheckAgainstBrief compares a valid rule with the brief it should express, using
// only what can be read from the words. A rule that says something the brief does
// not is returned as a problem for the model to fix. What the rule leaves out goes
// to Unmatched for a person, because only they know whether those words were about
// the audience. An option the marketer chose need not be in the brief.
func CheckAgainstBrief(proposal *Proposal, brief string, cities []string, chosenQuietDays *int) *ValidationError {
	problems := &ValidationError{}
	reading := readBrief(brief)
	proposal.Unmatched, proposal.Unsure = nil, nil
	proposal.UnsupportedConditions = slices.DeleteFunc(proposal.UnsupportedConditions, matching.OnlyPeople)
	checkParts(proposal, brief)
	checkCity(proposal, problems, brief, cities)
	checkWindows(proposal, problems, reading, chosenQuietDays)
	checkBehavior(proposal, problems, reading)
	checkLanguage(proposal, problems, reading)
	checkLeftOut(proposal, reading)
	if len(problems.Problems) == 0 {
		return nil
	}
	return problems
}

func checkCity(proposal *Proposal, problems *ValidationError, brief string, cities []string) {
	mentions := matching.Cities(brief, cities)
	if chosen := proposal.Segment.City; chosen != nil {
		index := slices.IndexFunc(mentions, func(mention matching.CityMention) bool { return mention.City == *chosen })
		switch {
		case index < 0:
			problems.add("city_not_in_brief", fmt.Sprintf("city is %q but the brief does not name it (it names: %s)", *chosen, cityNames(mentions)))
		case mentions[index].Excluded:
			problems.add("city_excluded_in_brief", fmt.Sprintf("city is %q but the brief leaves that city out", *chosen))
		}
	}
	for _, mention := range mentions {
		if proposal.Segment.City != nil && *proposal.Segment.City == mention.City {
			continue
		}
		if !accountedFor(proposal, brief, mention.City) {
			proposal.Unmatched = append(proposal.Unmatched, mention.City)
		}
	}
	for _, place := range matching.Places(brief, cities) {
		if !accountedFor(proposal, brief, place) {
			proposal.Unmatched = append(proposal.Unmatched, place)
		}
	}
}

// accountedFor says whether a name the rule does not use has been dealt with. A model
// may say it is not about the audience, unless the brief uses it for the people.
func accountedFor(proposal *Proposal, brief, name string) bool {
	if mentionedIn(proposal.UnsupportedConditions, name) {
		return true
	}
	return mentionedIn(proposal.NotAudience, name) && !matching.NamesAudience(brief, name)
}

func cityNames(mentions []matching.CityMention) string {
	if len(mentions) == 0 {
		return "no city"
	}
	names := make([]string, 0, len(mentions))
	for _, mention := range mentions {
		names = append(names, mention.City)
	}
	return strings.Join(names, ", ")
}

func mentionedIn(conditions []string, word string) bool {
	return slices.ContainsFunc(conditions, func(condition string) bool {
		return strings.Contains(matching.Key(condition), matching.Key(word))
	})
}

type briefReading struct {
	durations    []matching.Duration
	bound        map[string][]matching.Duration
	hasCart      bool
	boughtWord   bool
	unsure       bool
	notBought    bool
	hasMessage   bool
	ageWords     []string
	genderWords  []string
	loyaltyWords []string
	cartWords    []string
	languages    map[string]bool
}

func readBrief(brief string) briefReading {
	tokens := matching.Tokens(brief)
	reading := briefReading{durations: matching.Durations(brief), bound: map[string][]matching.Duration{}, languages: map[string]bool{}}
	subjects := map[int]string{}
	for index, token := range tokens {
		switch {
		case cartWord.MatchString(token):
			reading.hasCart = true
			subjects[index] = subjectCart
			reading.notBought = reading.notBought || leftBehindWord.MatchString(token)
		case leftBehindWord.MatchString(token):
			reading.notBought = true
		case purchaseWord.MatchString(token):
			subjects[index] = subjectPurchase
			if negatedAt(tokens, index) {
				reading.notBought = true
			} else {
				reading.boughtWord = true
			}
		case turkishNot.MatchString(token):
			// A negative verb with no purchase word: it may mean no purchase, and only
			// a person or the model can say, so nothing is refused on this reading.
			// "mesaj almayan" is about messages and tells nothing about purchases.
			if index == 0 || !messageWord.MatchString(tokens[index-1]) {
				reading.unsure = true
			}
		case messageWord.MatchString(token):
			reading.hasMessage = true
			subjects[index] = subjectMessage
		}
		for _, list := range []struct {
			pattern *regexp.Regexp
			words   *[]string
		}{{ageWord, &reading.ageWords}, {genderWord, &reading.genderWords}, {loyaltyWord, &reading.loyaltyWords}, {cartWord, &reading.cartWords}} {
			if list.pattern.MatchString(token) {
				*list.words = append(*list.words, token)
			}
		}
		for code, word := range languageWords {
			if word.MatchString(token) && !aboutTheText(tokens, index) {
				reading.languages[code] = true
			}
		}
	}
	// "sepete ekleyip almayan": after a cart, a negative verb is the missing purchase.
	reading.notBought = reading.notBought || (reading.hasCart && reading.unsure)
	for _, duration := range reading.durations {
		if subject := nearestSubject(subjects, duration.Token); subject != "" {
			reading.bound[subject] = append(reading.bound[subject], duration)
		}
	}
	return reading
}

var textWord = regexp.MustCompile(`^(yaz|write|mesaj|message|metin|text|sms|email|eposta)`)

// aboutTheText tells "İngilizce yaz" from "İngilizce konuşan": the first is the
// language of the message and says nothing about the people.
func aboutTheText(tokens []string, index int) bool {
	if index+1 < len(tokens) && strings.HasPrefix(tokens[index+1], "konus") {
		return false
	}
	for near := max(0, index-3); near < min(index+3, len(tokens)); near++ {
		if near != index && textWord.MatchString(tokens[near]) {
			return true
		}
	}
	return false
}

// negatedAt reads "did not buy" before the word and "alışveriş yapmayan" after it.
func negatedAt(tokens []string, index int) bool {
	for near := max(0, index-3); near < min(index+4, len(tokens)); near++ {
		if near == index {
			continue
		}
		if slices.Contains(notWords, tokens[near]) {
			return true
		}
		if near > index && turkishNot.MatchString(tokens[near]) {
			return true
		}
	}
	return turkishNot.MatchString(tokens[index])
}

func nearestSubject(subjects map[int]string, token int) string {
	nearest, distance := "", maxBindingDistance+1
	for index, subject := range subjects {
		gap := index - token
		if gap < 0 {
			gap = -gap
		}
		if gap < distance || gap == distance && index > token {
			nearest, distance = subject, gap
		}
	}
	return nearest
}

func saysDays(durations []matching.Duration, days int) bool {
	return slices.ContainsFunc(durations, func(duration matching.Duration) bool { return duration.Contains(days) })
}

type window struct {
	field   string
	subject string
	days    *int
}

func checkWindows(proposal *Proposal, problems *ValidationError, reading briefReading, chosenQuietDays *int) {
	spec := proposal.Segment
	windows := []window{
		{"cart_added_within_days", subjectCart, spec.CartAddedWithinDays},
		{"purchased_within_days", subjectPurchase, spec.PurchasedWithinDays},
		{"not_purchased_within_days", subjectPurchase, spec.NotPurchasedWithinDays},
		{"not_messaged_within_days", subjectMessage, spec.NotMessagedWithinDays},
	}
	for _, current := range windows {
		if current.days == nil || saysDays(reading.durations, *current.days) {
			continue
		}
		if current.subject == subjectMessage && chosenQuietDays != nil && *chosenQuietDays == *current.days {
			continue
		}
		// A model may assume a window only where the brief gives none.
		assumed := slices.ContainsFunc(proposal.Assumptions, func(assumption Assumption) bool { return assumption.Field == current.field })
		if assumed && len(reading.durations) == 0 {
			continue
		}
		problems.add("window_not_in_brief", fmt.Sprintf("%s is %d but the brief does not say that; use the time it gives, or record an assumption if it gives none", current.field, *current.days))
	}
	for _, first := range windows {
		for _, second := range windows {
			if first.days == nil || second.days == nil || first.subject == second.subject {
				continue
			}
			if !saysDays(reading.bound[first.subject], *first.days) && saysDays(reading.bound[first.subject], *second.days) && saysDays(reading.bound[second.subject], *first.days) {
				problems.add("window_swapped", fmt.Sprintf("%s and %s have each other's time in the brief", first.field, second.field))
				return
			}
		}
	}
}

// checkBehavior sends a rule back only for a clear opposite, or when the brief says
// nothing that could mean it. A wrong correction is worse than none: a model will obey
// it, which turns a right answer into a silently wrong one.
func checkBehavior(proposal *Proposal, problems *ValidationError, reading briefReading) {
	spec := proposal.Segment
	if spec.CartAddedWithinDays != nil && !reading.hasCart {
		problems.add("behavior_not_in_brief", "cart_added_within_days is set but the brief does not mention a cart")
	}
	if spec.PurchasedWithinDays != nil && !reading.boughtWord {
		switch {
		case reading.notBought:
			problems.add("behavior_flipped", "purchased_within_days is set but the brief asks for people who did not buy")
		case reading.unsure || partSaysMoreThanTime(proposal, "purchased_within_days"):
			noteUnsure(proposal, "purchased_within_days")
		default:
			problems.add("behavior_not_in_brief", "purchased_within_days is set but the brief does not mention a purchase")
		}
	}
	if (spec.NotPurchasedWithinDays != nil || spec.noPurchaseSinceCart()) && !reading.notBought {
		field := "not_purchased_within_days"
		if spec.NotPurchasedWithinDays == nil {
			field = "no_purchase_since_cart"
		}
		switch {
		case reading.unsure:
			noteUnsure(proposal, field)
		case reading.boughtWord:
			problems.add("behavior_flipped", "a no purchase filter is set but the brief asks for people who bought")
		case len(reading.durations) == 0 && !partSaysMoreThanTime(proposal, field):
			problems.add("behavior_not_in_brief", "a no purchase filter is set but the brief does not say so")
		default:
			noteUnsure(proposal, field)
		}
	}
}

func checkLanguage(proposal *Proposal, problems *ValidationError, reading briefReading) {
	if language := proposal.Segment.Language; language != nil && !reading.languages[*language] {
		problems.add("language_not_in_brief", fmt.Sprintf("language is %q but the brief does not ask for it", *language))
	}
}

func checkLeftOut(proposal *Proposal, reading briefReading) {
	spec := proposal.Segment
	leftOut := func(note string) { proposal.Unmatched = append(proposal.Unmatched, note) }
	// A stated condition or a claim that names the word deals with it. A model may word
	// age and gender as one condition, and these words can be about the message.
	dealtWith := func(words []string) bool {
		return len(proposal.UnsupportedConditions) > 0 || slices.ContainsFunc(words, func(word string) bool {
			return mentionedIn(proposal.NotAudience, word)
		})
	}
	if len(reading.ageWords) > 0 && !dealtWith(reading.ageWords) {
		leftOut("age")
	}
	if len(reading.genderWords) > 0 && !dealtWith(reading.genderWords) {
		leftOut("gender")
	}
	if len(reading.loyaltyWords) > 0 && !dealtWith(reading.loyaltyWords) {
		leftOut("loyalty")
	}
	if reading.hasCart && spec.CartAddedWithinDays == nil && !dealtWith(reading.cartWords) {
		leftOut("cart activity")
	}
	noPurchaseFilter := spec.NotPurchasedWithinDays != nil || spec.noPurchaseSinceCart()
	switch {
	case reading.hasCart && reading.notBought && !noPurchaseFilter && !dealtWith(reading.cartWords):
		leftOut("no purchase")
	case len(reading.bound[subjectPurchase]) > 0 && spec.PurchasedWithinDays == nil && !noPurchaseFilter:
		leftOut("purchase activity")
	}
	if len(reading.bound[subjectMessage]) > 0 && spec.NotMessagedWithinDays == nil {
		leftOut("recent messages")
	}
}

// checkParts holds the model's split of the brief to the brief and the rule. An
// audience part with no filter blocks like any other condition the filters cannot
// express. Words no part covers are flagged, so leaving them out hides nothing.
func checkParts(proposal *Proposal, brief string) {
	if len(proposal.BriefParts) == 0 {
		return
	}
	applied := Describe(proposal.Segment)
	rest := " " + foldWords(brief) + " "
	kept := make([]BriefPart, 0, len(proposal.BriefParts))
	for _, part := range proposal.BriefParts {
		part.Text = strings.TrimSpace(part.Text)
		folded := foldWords(part.Text)
		if folded == "" || !strings.Contains(foldWords(brief), folded) {
			continue
		}
		rest = strings.Replace(rest, folded, " ", 1)
		if matching.OnlyFiller(part.Text) {
			continue
		}
		part.UsedAs = filtersOf(proposal, part, applied)
		if part.Kind == PartAudience && len(part.UsedAs) == 0 && !alreadyReported(proposal.UnsupportedConditions, part.Text) {
			proposal.UnsupportedConditions = append(proposal.UnsupportedConditions, part.Text)
		}
		kept = append(kept, part)
	}
	proposal.BriefParts = kept
	left := slices.DeleteFunc(strings.Fields(rest), matching.OnlyFiller)
	if words := strings.Trim(strings.Join(left, " "), " .,;:!?"); matching.Key(words) != "" {
		proposal.Unsure = append(proposal.Unsure, words)
	}
}

// filtersOf keeps what a part may claim and adds what its own evidence says, since a
// model can name one filter for words that gave two, or forget to name any.
func filtersOf(proposal *Proposal, part BriefPart, applied []Condition) FilterList {
	fields := FilterList{}
	for _, condition := range applied {
		claimed := slices.Contains(part.UsedAs, condition.Field)
		quotedHere := slices.ContainsFunc(proposal.Evidence, func(evidence Evidence) bool {
			quote, text := foldWords(evidence.Quote), foldWords(part.Text)
			return evidence.Field == condition.Field && quote != "" && (strings.Contains(text, quote) || strings.Contains(quote, text))
		})
		if claimed || quotedHere {
			fields = append(fields, condition.Field)
		}
	}
	return fields
}

func alreadyReported(conditions []string, text string) bool {
	return slices.ContainsFunc(conditions, func(condition string) bool {
		return strings.Contains(matching.Key(condition), matching.Key(text)) || strings.Contains(matching.Key(text), matching.Key(condition))
	})
}

// partFor finds the words a filter came from.
func partFor(proposal *Proposal, field string) string {
	for _, part := range proposal.BriefParts {
		if slices.Contains(part.UsedAs, field) {
			return part.Text
		}
	}
	for _, evidence := range proposal.Evidence {
		if evidence.Field == field {
			return evidence.Quote
		}
	}
	return ""
}

// partSaysMoreThanTime tells a part that names a behaviour in its own words from one
// that only repeats how long, which says nothing about what people did.
func partSaysMoreThanTime(proposal *Proposal, field string) bool {
	text := partFor(proposal, field)
	return text != "" && !matching.OnlyTime(text)
}

func noteUnsure(proposal *Proposal, field string) {
	text := partFor(proposal, field)
	if text == "" {
		for _, condition := range Describe(proposal.Segment) {
			if condition.Field == field {
				text = condition.Label + " " + condition.Value
			}
		}
	}
	proposal.Unsure = append(proposal.Unsure, text)
}
