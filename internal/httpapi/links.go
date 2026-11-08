package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

// RegisterShortLinks adds the address an SMS's short link leads to. A deployment
// points the short domain (content.ShortLinkBase) here; it needs no session, since
// whoever got the SMS opens it.
func RegisterShortLinks(mux *http.ServeMux, campaigns *campaign.Service) {
	mux.HandleFunc("GET /api/l/{code}", func(w http.ResponseWriter, r *http.Request) {
		link, err := campaigns.FollowLink(r.Context(), r.PathValue("code"), r.Method == http.MethodGet)
		switch {
		case errors.Is(err, campaign.ErrUnknownLink):
			http.Error(w, "This link is not valid.", http.StatusNotFound)
		case err != nil:
			slog.ErrorContext(r.Context(), "follow short link", "error", err)
			http.Error(w, "This link does not work right now. Try again in a moment.", http.StatusServiceUnavailable)
		default:
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, link, http.StatusFound)
		}
	})
}
