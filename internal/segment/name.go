package segment

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxCampaignNameLength limits campaign names, in characters.
const MaxCampaignNameLength = 80

// CleanCampaignName normalizes whitespace and reports whether the result is a
// usable name. Line breaks count as whitespace; other control characters do not.
func CleanCampaignName(name string) (string, bool) {
	cleaned := strings.Join(strings.Fields(name), " ")
	if cleaned == "" || utf8.RuneCountInString(cleaned) > MaxCampaignNameLength {
		return "", false
	}
	if strings.IndexFunc(cleaned, unicode.IsControl) >= 0 {
		return "", false
	}
	return cleaned, true
}
