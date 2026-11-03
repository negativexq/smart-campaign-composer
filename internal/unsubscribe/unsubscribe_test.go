package unsubscribe_test

import (
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/unsubscribe"
)

func TestATokenNamesItsUser(t *testing.T) {
	t.Parallel()
	got, err := unsubscribe.Verify("secret", unsubscribe.Token("secret", 42))
	if err != nil || got != 42 {
		t.Fatalf("got %d, %v", got, err)
	}
}

func TestATokenCannotBeForged(t *testing.T) {
	t.Parallel()
	token := unsubscribe.Token("secret", 42)
	other := unsubscribe.Token("secret", 43)
	for name, candidate := range map[string]string{
		"signed with another secret": unsubscribe.Token("other", 42),
		"another user's signature":   "42." + other[strings.Index(other, ".")+1:],
		"cut short":                  token[:len(token)-4],
		"no signature":               "42",
		"empty":                      "",
		"not a number":               "abc." + token[strings.Index(token, ".")+1:],
	} {
		if _, err := unsubscribe.Verify("secret", candidate); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
