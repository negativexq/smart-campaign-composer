// Package matching reads facts out of a marketer's text without a model, so what
// a model produced can be compared with what the person wrote. It only reads
// closed lists, and leaves anything it cannot tell to the caller.
package matching

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/negativexq/smart-campaign-composer/internal/language"
)

var folder = strings.NewReplacer(
	"İ", "i", "I", "i", "ı", "i", "Ş", "s", "ş", "s", "Ğ", "g", "ğ", "g",
	"Ü", "u", "ü", "u", "Ö", "o", "ö", "o", "Ç", "c", "ç", "c",
)

// link is a web address as a brief gives one: with a scheme, starting with www., or
// a domain ending in letters followed by a path, so 3.5/5 or 1.11/30 stay words. A
// label right before it, as in "Link:", goes with it.
var link = regexp.MustCompile(`(?i)(?:\b(?:link|linki|bağlantı|url)\s*:?\s*)?(?:(?:\bhttps?://|\bwww\.)\S+|\b(?:[a-z0-9-]+\.)+[a-z]{2,}/\S*)`)

// WithoutLinks drops the web addresses from text. A link's path is a page name, not
// a word about the audience: /sepet or /izmir says nothing about who should get it.
func WithoutLinks(text string) string {
	return link.ReplaceAllString(text, " ")
}

// Key makes spellings comparable: İzmir, IZMIR and izmir are one word here.
func Key(text string) string {
	return strings.ToLower(folder.Replace(text))
}

// Tokens splits text into folded words and numbers, so that Ankara'daki, %30 and
// 14-day all come apart where a person would read them apart.
func Tokens(text string) []string {
	tokens := []string{}
	var current strings.Builder
	var kind rune
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, char := range Key(text) {
		switch {
		case unicode.IsDigit(char):
			if kind != 'd' {
				flush()
			}
			kind = 'd'
			current.WriteRune(char)
		case unicode.IsLetter(char):
			if kind != 'l' {
				flush()
			}
			kind = 'l'
			current.WriteRune(char)
		default:
			flush()
			kind = 0
		}
	}
	flush()
	return tokens
}

// The endings a city name takes in Turkish, listed so that a longer word that
// merely starts the same way does not count.
var citySuffixes = []string{
	"", "da", "de", "ta", "te", "daki", "deki", "taki", "teki", "dan", "den", "tan", "ten",
	"a", "e", "ya", "ye", "yi", "i", "u", "nin", "nun", "in", "un", "li", "lu", "liler", "lular",
	"lilar", "luler", "lilere", "lulara", "lilara", "lulere", "lilerin", "lularin", "dakiler", "dekiler",
	"dakilere", "dekilere", "dakilerin", "dekilerin", "lilari", "lileri", "lulari", "luleri",
}

var (
	afterExclusion  = []string{"haric", "disinda", "except", "excluded"}
	beforeExclusion = []string{"except", "excluding", "outside", "without"}
)

// provinces lets a bare city name be recognised even when no user lives there yet.
// Names that are also everyday words (Van, Ordu, Kars) are left to Places, which
// needs the sentence to use them as a place.
var provinces = []string{
	"Adana", "Adıyaman", "Afyon", "Aksaray", "Amasya", "Ankara", "Antalya", "Ardahan", "Artvin", "Balıkesir",
	"Bartın", "Bayburt", "Bilecik", "Bingöl", "Bitlis", "Bolu", "Burdur", "Bursa", "Çanakkale", "Çankırı",
	"Çorum", "Denizli", "Diyarbakır", "Düzce", "Edirne", "Elazığ", "Erzincan", "Erzurum", "Eskişehir", "Gaziantep",
	"Giresun", "Gümüşhane", "Hakkari", "Hatay", "Iğdır", "Isparta", "İstanbul", "İzmir", "Kahramanmaraş", "Karabük",
	"Karaman", "Kastamonu", "Kayseri", "Kırıkkale", "Kırklareli", "Kırşehir", "Kilis", "Kocaeli", "Konya", "Kütahya",
	"Malatya", "Manisa", "Mardin", "Mersin", "Muğla", "Nevşehir", "Niğde", "Osmaniye", "Rize", "Sakarya",
	"Samsun", "Siirt", "Sinop", "Sivas", "Şanlıurfa", "Şırnak", "Tekirdağ", "Tokat", "Trabzon", "Tunceli",
	"Uşak", "Yalova", "Yozgat", "Zonguldak",
}

// CityMention is a city the text names. Excluded means the text leaves it out.
type CityMention struct {
	City     string
	Excluded bool
}

// Cities finds which cities the text names, spelled as in known where it can be.
func Cities(text string, known []string) []CityMention {
	tokens := Tokens(text)
	found := []CityMention{}
	known = slices.CompactFunc(slices.SortedStableFunc(slices.Values(slices.Concat(known, provinces)), func(a, b string) int {
		return strings.Compare(Key(a), Key(b))
	}), func(a, b string) bool { return Key(a) == Key(b) })
	for index, token := range tokens {
		for _, city := range known {
			key := Key(city)
			if !strings.HasPrefix(token, key) || !slices.Contains(citySuffixes, token[len(key):]) {
				continue
			}
			mention := CityMention{City: city, Excluded: excludedAt(tokens, index)}
			existing := slices.IndexFunc(found, func(other CityMention) bool { return other.City == city })
			if existing < 0 {
				found = append(found, mention)
			} else if !mention.Excluded {
				found[existing].Excluded = false
			}
		}
	}
	return found
}

func excludedAt(tokens []string, index int) bool {
	for next := index + 1; next < min(index+3, len(tokens)); next++ {
		if slices.Contains(afterExclusion, tokens[next]) {
			return true
		}
	}
	return index > 0 && slices.Contains(beforeExclusion, tokens[index-1])
}

var (
	placeEndings = regexp.MustCompile(`(\p{Lu}\p{L}+)['’](?:da|de|ta|te|daki|deki|taki|teki|li|lı|lu|lü)(?:[^\p{L}]|$)`)
	placeAfter   = regexp.MustCompile(`(?:^|[^\p{L}])(?:in|from)\s+(\p{Lu}\p{L}+)`)
	placeBefore  = regexp.MustCompile(`(\p{Lu}\p{L}+)\s+(?:customers?|users?|people|shoppers?|m[üu]şteri\p{L}*|kullan[ıi]c[ıi]\p{L}*)`)

	// Words a sentence uses like a place that are times, languages or plain words.
	notPlaces = []string{
		"ocak", "subat", "mart", "nisan", "mayis", "haziran", "temmuz", "agustos", "eylul", "ekim", "kasim", "aralik",
		"pazartesi", "sali", "carsamba", "persembe", "cuma", "cumartesi", "pazar", "ramazan", "bayram", "yilbasi",
		"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december",
		"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday",
		"all", "our", "the", "these", "those", "any", "some", "my", "your", "tum", "butun", "her", "bazi", "bu", "su", "o", "degerli", "sevgili",
		// A sentence starting with a command capitalises it: "Remind users".
		"remind", "notify", "email", "message", "text", "send", "tell", "reach", "target", "contact", "alert", "invite", "thank", "ask", "inform", "help", "show", "offer", "give", "find", "welcome", "only",
	}
)

// Places finds what a sentence uses as a place or a group of people but known does
// not list: Paris'teki, Van'da, in Berlin, Tokyo customers. It reads how the name is
// used, so unlike a list it holds for places nobody thought of. A capitalised
// qualifier such as "Mobile users" reads the same way, and is equally something
// a rule must not silently drop.
func Places(text string, known []string) []string {
	skip := slices.Concat(notPlaces, language.Words(), keysOf(known), keysOf(provinces))
	found := []string{}
	for _, pattern := range []*regexp.Regexp{placeEndings, placeAfter, placeBefore} {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if !slices.Contains(skip, Key(match[1])) && !slices.Contains(found, match[1]) && len(Cities(match[1], known)) == 0 {
				found = append(found, match[1])
			}
		}
	}
	return found
}

// fillerWords join a sentence or ask for the selection, and say nothing about who.
var fillerWords = []string{
	"ama", "fakat", "ancak", "ve", "ile", "veya", "ya", "da", "de", "ise", "icin", "bir", "bu", "su", "o", "daki", "deki",
	"olan", "olanlar", "olanlari", "olanlara", "secelim", "sec", "secin", "yapalim", "kampanya", "kampanyasi",
	"gonder", "gonderelim", "gonderin", "herkes", "herkese",
	"and", "or", "but", "the", "a", "an", "to", "for", "of", "in", "who", "that", "which", "are", "is", "with", "select", "send", "campaign", "everyone",
}

// OnlyFiller reports whether the text has no word that could decide who receives a
// message. An empty text counts: punctuation alone says nothing either.
func OnlyFiller(text string) bool {
	return !slices.ContainsFunc(Tokens(text), func(token string) bool {
		return !slices.Contains(fillerWords, token) && !audienceWord.MatchString(token) && !slices.Contains(courtesyWords, token)
	})
}

// OnlyTime reports whether the text says only how long, such as "son 30 gün".
func OnlyTime(text string) bool {
	tokens := Tokens(text)
	return !slices.ContainsFunc(tokens, func(token string) bool {
		_, isNumber := wholeNumber(token)
		return !isNumber && !slices.Contains(impliedOne, token) && !slices.Contains(fillerWords, token) &&
			!slices.Contains(hourWords, token) && !slices.Contains(dayWords, token) && !slices.Contains(weekWords, token) &&
			!slices.Contains(monthWords, token) && !slices.Contains(yearWords, token)
	})
}

// courtesyWords describe the people without narrowing them down.
var courtesyWords = []string{"tum", "butun", "degerli", "sevgili", "all", "our", "dear", "valued", "bizim"}

var audienceWord = regexp.MustCompile(`^(customers?|users?|people|shoppers?|residents?|musteri|kullanici|yasayan|oturan|insan)`)

// NamesAudience reports whether the text uses this name for the people themselves:
// "İzmir kullanıcıları", "Paris'teki müşteriler", "people in Berlin".
func NamesAudience(text, name string) bool {
	tokens := Tokens(text)
	key := Key(name)
	for index, token := range tokens {
		if token != key && !(strings.HasPrefix(token, key) && slices.Contains(citySuffixes, token[len(key):])) {
			continue
		}
		next := index + 1
		// The ending after an apostrophe is a token of its own: Paris'teki.
		if next < len(tokens) && slices.Contains(citySuffixes, tokens[next]) {
			next++
		}
		if next < len(tokens) && audienceWord.MatchString(tokens[next]) {
			return true
		}
		if index >= 2 && tokens[index-1] == "in" && audienceWord.MatchString(tokens[index-2]) {
			return true
		}
	}
	return false
}

// OnlyPeople reports whether the text is nothing but a word for people, which
// says who a message is for without narrowing them down.
func OnlyPeople(text string) bool {
	tokens := Tokens(text)
	return len(tokens) > 0 && !slices.ContainsFunc(tokens, func(token string) bool {
		return !audienceWord.MatchString(token) && !slices.Contains(courtesyWords, token)
	})
}

func keysOf(words []string) []string {
	keys := make([]string, 0, len(words))
	for _, word := range words {
		keys = append(keys, Key(word))
	}
	return keys
}

// Duration is a length of time the text gives, in days. A month is not a fixed
// number of days, so it is a range. Token says where in the text it was.
type Duration struct {
	Min, Max int
	Token    int
}

// Contains reports whether days is what this duration says.
func (d Duration) Contains(days int) bool {
	return days >= d.Min && days <= d.Max
}

var (
	hourWords  = []string{"saat", "saatte", "saattir", "saatlik", "hour", "hours"}
	dayWords   = []string{"gun", "gunde", "gundur", "gunu", "gune", "gunun", "gunden", "gunluk", "gunler", "gunlerde", "day", "days"}
	weekWords  = []string{"hafta", "haftada", "haftadir", "haftayi", "haftaya", "haftalik", "haftanin", "week", "weeks"}
	monthWords = []string{"ay", "ayda", "aydir", "ayi", "aya", "aylik", "ayin", "aylar", "month", "months"}
	yearWords  = []string{"yil", "yilda", "yildir", "yilin", "yillik", "year", "years"}

	numberWords = map[string]int{
		"bir": 1, "iki": 2, "uc": 3, "dort": 4, "bes": 5, "alti": 6, "yedi": 7, "sekiz": 8, "dokuz": 9, "on": 10,
		"a": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
	}
	impliedOne = []string{"son", "gecen", "last", "past"}
)

// Durations finds the lengths of time in the text. "Hafta sonu" and weekends are
// days of the week rather than a length of time, so they do not count.
func Durations(text string) []Duration {
	tokens := Tokens(text)
	found := []Duration{}
	for index := 1; index < len(tokens); index++ {
		token := tokens[index]
		if index+1 < len(tokens) && tokens[index+1] == "sonu" {
			continue
		}
		count, ok := countBefore(tokens, index)
		if !ok {
			continue
		}
		scaled := func(low, high float64) Duration {
			return Duration{int(count * low), int(count*high + 0.5), index}
		}
		switch {
		case slices.Contains(hourWords, token):
			hours := count / 24
			found = append(found, Duration{max(int(hours), 1), max(int(hours+0.99), 1), index})
		case slices.Contains(dayWords, token):
			found = append(found, scaled(1, 1))
		case slices.Contains(weekWords, token):
			found = append(found, scaled(7, 7))
		case slices.Contains(monthWords, token):
			found = append(found, scaled(28, 31))
		case slices.Contains(yearWords, token):
			found = append(found, scaled(360, 366))
		}
	}
	return found
}

// countBefore reads how many of a unit come before it: 3, üç, bir buçuk, yarım, son.
func countBefore(tokens []string, index int) (float64, bool) {
	before := tokens[index-1]
	if before == "yarim" || before == "half" {
		return 0.5, true
	}
	if before == "bucuk" && index >= 2 {
		if whole, ok := wholeNumber(tokens[index-2]); ok {
			return whole + 0.5, true
		}
	}
	if number, ok := wholeNumber(before); ok {
		return number, true
	}
	return 1, slices.Contains(impliedOne, before)
}

func wholeNumber(token string) (float64, bool) {
	if number, err := strconv.Atoi(token); err == nil {
		return float64(number), true
	}
	number, ok := numberWords[token]
	return float64(number), ok
}

// Numbers lists the numbers in the text.
func Numbers(text string) []string {
	return slices.DeleteFunc(Tokens(text), func(token string) bool {
		_, err := strconv.Atoi(token)
		return err != nil
	})
}
