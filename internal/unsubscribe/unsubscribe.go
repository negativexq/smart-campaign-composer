// Package unsubscribe signs the links in campaign emails, so a link can only
// withdraw consent for the person it was sent to.
package unsubscribe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// ErrInvalid means a token was not signed with this secret.
var ErrInvalid = errors.New("unsubscribe token is not valid")

// Token is what goes in the link for one user.
func Token(secret string, userID int64) string {
	id := strconv.FormatInt(userID, 10)
	return id + "." + base64.RawURLEncoding.EncodeToString(sign(secret, id))
}

// Verify returns the user a token was issued for.
func Verify(secret, token string) (int64, error) {
	id, signature, found := strings.Cut(token, ".")
	if !found {
		return 0, ErrInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || !hmac.Equal(decoded, sign(secret, id)) {
		return 0, ErrInvalid
	}
	userID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, ErrInvalid
	}
	return userID, nil
}

func sign(secret, id string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(id))
	return mac.Sum(nil)
}
