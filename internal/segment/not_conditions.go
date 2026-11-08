package segment

import (
	"regexp"
	"slices"

	"github.com/negativexq/smart-campaign-composer/internal/matching"
)

var (
	// "yaş fark etmeksizin", "erkek kadın farketmez": the trait before these words
	// does not matter, so it is no condition at all.
	indifferentAfter = regexp.MustCompile(`^(farketmeksizin|farketmez|farketmeden|gozetmeksizin)$`)
	indifferentVerb  = regexp.MustCompile(`^(etmeksizin|etmez|etmeden)$`)
	// "regardless of age", "no matter their gender": the trait after these words.
	traitJoiner = regexp.MustCompile(`^(ve|veya|ya|da|de|yada|ile|or|and|their|any|of)$`)
	// "sepet özelliğimiz", "cart page": the cart is part of the product the message
	// is about, not something the people did.
	featureWord = regexp.MustCompile(`^(ozellig|ozellik|feature|sayfa|page|buton|button|ikon|icon|tasarim|design|deneyim|experience)`)
)

// Only the bare name of a trait can be the one that does not matter: in
// "kadınlara, yaş fark etmeksizin" the women are still the audience.
var bareTrait = regexp.MustCompile(`^(yas|cinsiyet|kadin|erkek|genc|yasli|age|gender|sex|male|female|men|women)$`)

func isTrait(token string) bool {
	return bareTrait.MatchString(token)
}

// notConditionWords returns the positions of words that look like a condition but
// say none: a trait the brief says does not matter, and a cart named as a feature.
// These are read from the words alone, so a model cannot hide a condition by
// calling it one of these.
func notConditionWords(tokens []string) map[int]bool {
	skip := map[int]bool{}
	markTraits := func(from, step int) {
		marked := []int{}
		for index := from; index >= 0 && index < len(tokens); index += step {
			if !isTrait(tokens[index]) && !traitJoiner.MatchString(tokens[index]) {
				break
			}
			marked = append(marked, index)
		}
		if slices.ContainsFunc(marked, func(index int) bool { return isTrait(tokens[index]) }) {
			for _, index := range marked {
				skip[index] = true
			}
		}
	}
	for index, token := range tokens {
		next := ""
		if index+1 < len(tokens) {
			next = tokens[index+1]
		}
		switch {
		case indifferentAfter.MatchString(token):
			skip[index] = true
			markTraits(index-1, -1)
		case (token == "fark" || token == "ayirt") && indifferentVerb.MatchString(next):
			skip[index], skip[index+1] = true, true
			markTraits(index-1, -1)
		case (token == "regardless" || token == "irrespective") && next == "of":
			skip[index], skip[index+1] = true, true
			markTraits(index+2, 1)
		case token == "no" && next == "matter":
			skip[index], skip[index+1] = true, true
			markTraits(index+2, 1)
		case cartWord.MatchString(token) && featureWord.MatchString(next):
			skip[index] = true
			if index > 0 && (tokens[index-1] == "alisveris" || tokens[index-1] == "shopping") {
				skip[index-1] = true
			}
		}
	}
	return skip
}

// saysNoCondition is true for text made only of such words, like "yaş fark
// etmeksizin". A model may list it as a condition no filter covers.
func saysNoCondition(text string) bool {
	tokens := matching.Tokens(text)
	skip := notConditionWords(tokens)
	if len(skip) == 0 {
		return false
	}
	for index, token := range tokens {
		if !skip[index] && !matching.OnlyFiller(token) && !matching.OnlyPeople(token) {
			return false
		}
	}
	return true
}
