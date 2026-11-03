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
	notPurchased   = regexp.MustCompile(`^(yap|al|et|ver|alisveris)m[ae](yan|mis|di)`)
	messageWord    = regexp.MustCompile(`^(mesaj|messag|ileti)`)
	englishNot     = []string{"not", "never", "no", "without", "didn", "haven", "hasn", "dont", "havent", "hasnt", "didnt"}

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
	proposal.Unmatched = nil
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
		if !mentionedIn(proposal.UnsupportedConditions, mention.City) {
			proposal.Unmatched = append(proposal.Unmatched, mention.City)
		}
	}
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
	durations  []matching.Duration
	bound      map[string][]matching.Duration
	hasCart    bool
	boughtWord bool
	notBought  bool
	hasMessage bool
	hasAge     bool
	hasGender  bool
	hasLoyalty bool
	languages  map[string]bool
}

func readBrief(brief string) briefReading {
	tokens := matching.Tokens(brief)
	reading := briefReading{durations: matching.Durations(brief), bound: map[string][]matching.Duration{}, languages: map[string]bool{}}
	subjects := map[int]string{}
	for index, token := range tokens {
		switch {
		case notPurchased.MatchString(token):
			reading.notBought = true
			subjects[index] = subjectPurchase
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
		case messageWord.MatchString(token):
			reading.hasMessage = true
			subjects[index] = subjectMessage
		}
		reading.hasAge = reading.hasAge || ageWord.MatchString(token)
		reading.hasGender = reading.hasGender || genderWord.MatchString(token)
		reading.hasLoyalty = reading.hasLoyalty || loyaltyWord.MatchString(token)
		for code, word := range languageWords {
			if word.MatchString(token) {
				reading.languages[code] = true
			}
		}
	}
	for _, duration := range reading.durations {
		if subject := nearestSubject(subjects, duration.Token); subject != "" {
			reading.bound[subject] = append(reading.bound[subject], duration)
		}
	}
	return reading
}

// negatedAt reads "did not buy" before the word and "alışveriş yapmayan" after it.
func negatedAt(tokens []string, index int) bool {
	for before := max(0, index-3); before < index; before++ {
		if slices.Contains(englishNot, tokens[before]) {
			return true
		}
	}
	for after := index + 1; after < min(index+3, len(tokens)); after++ {
		if notPurchased.MatchString(tokens[after]) {
			return true
		}
	}
	return false
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

func checkBehavior(proposal *Proposal, problems *ValidationError, reading briefReading) {
	spec := proposal.Segment
	if spec.CartAddedWithinDays != nil && !reading.hasCart {
		problems.add("behavior_not_in_brief", "cart_added_within_days is set but the brief does not mention a cart")
	}
	if spec.PurchasedWithinDays != nil && !reading.boughtWord {
		if reading.notBought {
			problems.add("behavior_flipped", "purchased_within_days is set but the brief asks for people who did not buy")
		} else {
			problems.add("behavior_not_in_brief", "purchased_within_days is set but the brief does not mention a purchase")
		}
	}
	if (spec.NotPurchasedWithinDays != nil || spec.noPurchaseSinceCart()) && !reading.notBought {
		if reading.boughtWord {
			problems.add("behavior_flipped", "a no purchase filter is set but the brief asks for people who bought")
		} else {
			problems.add("behavior_not_in_brief", "a no purchase filter is set but the brief does not say so")
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
	// Any stated condition counts: a model may word age and gender as one.
	if len(proposal.UnsupportedConditions) == 0 {
		if reading.hasAge {
			leftOut("age")
		}
		if reading.hasGender {
			leftOut("gender")
		}
		if reading.hasLoyalty {
			leftOut("loyalty")
		}
	}
	if reading.hasCart && spec.CartAddedWithinDays == nil && len(proposal.UnsupportedConditions) == 0 {
		leftOut("cart activity")
	}
	noPurchaseFilter := spec.NotPurchasedWithinDays != nil || spec.noPurchaseSinceCart()
	switch {
	case reading.hasCart && reading.notBought && !noPurchaseFilter:
		leftOut("no purchase")
	case len(reading.bound[subjectPurchase]) > 0 && spec.PurchasedWithinDays == nil && !noPurchaseFilter:
		leftOut("purchase activity")
	}
	if len(reading.bound[subjectMessage]) > 0 && spec.NotMessagedWithinDays == nil {
		leftOut("recent messages")
	}
}
