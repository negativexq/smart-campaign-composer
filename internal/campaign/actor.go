package campaign

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	actorUser   = "user"
	actorSystem = "system"

	minIdentity = 2
	maxIdentity = 254
)

// ErrInvalidIdentity means a sign-in name cannot be used.
var ErrInvalidIdentity = errors.New("invalid identity")

type actorKey struct{}

// WithActor marks work as done by this person. Work without one was started by the
// system itself, so every request from a person has to pass through here.
func WithActor(ctx context.Context, identity string) context.Context {
	return context.WithValue(ctx, actorKey{}, identity)
}

func personFrom(ctx context.Context) *string {
	identity, ok := ctx.Value(actorKey{}).(string)
	if !ok {
		return nil
	}
	return &identity
}

// NormalizeIdentity makes one person's username or email come out the same each time.
func NormalizeIdentity(raw string) (string, error) {
	identity := strings.ToLower(strings.TrimSpace(raw))
	if length := utf8.RuneCountInString(identity); length < minIdentity || length > maxIdentity {
		return "", ErrInvalidIdentity
	}
	if strings.IndexFunc(identity, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", ErrInvalidIdentity
	}
	return identity, nil
}
