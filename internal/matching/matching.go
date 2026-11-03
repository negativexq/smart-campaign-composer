// Package matching reads facts out of a marketer's text without a model, so what
// a model produced can be compared with what the person wrote. It only reads
// closed lists, and leaves anything it cannot tell to the caller.
package matching

import (
	"slices"
	"strconv"
	"strings"
	"unicode"
)

var folder = strings.NewReplacer(
	"İ", "i", "I", "i", "ı", "i", "Ş", "s", "ş", "s", "Ğ", "g", "ğ", "g",
	"Ü", "u", "ü", "u", "Ö", "o", "ö", "o", "Ç", "c", "ç", "c",
)

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

// Provinces lets a city be recognised even when no user lives there yet, so a brief
// naming one is not read as naming no city. Names that are also everyday words
// (Van, Ordu, Muş, Ağrı, Kars, Aydın, Batman) are left out on purpose.
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
		count, ok := countBefore(tokens[index-1])
		if !ok {
			continue
		}
		switch {
		case slices.Contains(dayWords, token):
			found = append(found, Duration{count, count, index})
		case slices.Contains(weekWords, token):
			found = append(found, Duration{count * 7, count * 7, index})
		case slices.Contains(monthWords, token):
			found = append(found, Duration{count * 28, count * 31, index})
		case slices.Contains(yearWords, token):
			found = append(found, Duration{count * 360, count * 366, index})
		}
	}
	return found
}

func countBefore(token string) (int, bool) {
	if number, err := strconv.Atoi(token); err == nil {
		return number, true
	}
	if number, ok := numberWords[token]; ok {
		return number, true
	}
	return 1, slices.Contains(impliedOne, token)
}

// Numbers lists the numbers in the text.
func Numbers(text string) []string {
	return slices.DeleteFunc(Tokens(text), func(token string) bool {
		_, err := strconv.Atoi(token)
		return err != nil
	})
}
