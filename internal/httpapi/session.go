package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

const (
	sessionCookie = "scc_session"
	sessionMaxAge = 30 * 24 * 60 * 60
)

// identityFrom reads the sign-in name. This says who someone claims to be and proves
// nothing: there is no password, and the cookie is not signed.
func identityFrom(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	raw, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		return "", false
	}
	identity, err := campaign.NormalizeIdentity(raw)
	return identity, err == nil
}

// signedIn lets a request through with its person attached, so everything it does
// is recorded under that name.
func signedIn(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFrom(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, errorBody("Sign in to continue."))
			return
		}
		next(w, r.WithContext(campaign.WithActor(r.Context(), identity)))
	}
}

func setSession(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: url.QueryEscape(value), Path: "/", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (a *api) session(w http.ResponseWriter, r *http.Request) {
	identity, ok := identityFrom(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"identity": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identity": identity})
}

func (a *api) signIn(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Identity string `json:"identity"`
	}
	if !decode(w, r, &body) {
		return
	}
	identity, err := campaign.NormalizeIdentity(body.Identity)
	if errors.Is(err, campaign.ErrInvalidIdentity) {
		writeJSON(w, http.StatusBadRequest, errorBody("Enter a username or email, without spaces."))
		return
	}
	setSession(w, identity, sessionMaxAge)
	writeJSON(w, http.StatusOK, map[string]any{"identity": identity})
}

func (a *api) signOut(w http.ResponseWriter, _ *http.Request) {
	setSession(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}
