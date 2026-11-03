package httpapi

import (
	"html/template"
	"log/slog"
	"net/http"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/unsubscribe"
)

var unsubscribePage = template.Must(template.New("unsubscribe").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
body { margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 16px; box-sizing: border-box; background: #F5F7FA; color: #172033; font: 16px/1.5 Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Arial, sans-serif; }
main { max-width: 420px; padding: 32px; background: #FFFFFF; border: 1px solid #E4E8EF; border-radius: 12px; }
h1 { margin: 0 0 8px; font-size: 22px; line-height: 28px; font-weight: 600; }
p { margin: 0; color: #5B6577; }
button { margin-top: 20px; padding: 10px 18px; border: 0; border-radius: 8px; background: #315BE8; color: #FFFFFF; font: inherit; font-weight: 500; cursor: pointer; }
</style>
</head>
<body>
<main>
<h1>{{.Title}}</h1>
<p>{{.Text}}</p>
{{if .Confirm}}<form method="post"><button type="submit">Unsubscribe</button></form>{{end}}
</main>
</body>
</html>
`))

type unsubscribeContent struct {
	Title, Text string
	Confirm     bool
}

// RegisterUnsubscribe adds the links in campaign emails. They have no session: the
// signature is what proves whose link it is. Opening one only asks, because mail
// scanners open links, and repeating the confirmation changes nothing.
func RegisterUnsubscribe(mux *http.ServeMux, campaigns *campaign.Service, secret string) {
	notFound := unsubscribeContent{Title: "This link is not valid", Text: "Use the link from the email you received."}
	show := func(w http.ResponseWriter, r *http.Request, status int, page unsubscribeContent) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if err := unsubscribePage.Execute(w, page); err != nil {
			slog.ErrorContext(r.Context(), "render unsubscribe page", "error", err)
		}
	}
	mux.HandleFunc("GET /api/unsubscribe/{token}", func(w http.ResponseWriter, r *http.Request) {
		if _, err := unsubscribe.Verify(secret, r.PathValue("token")); err != nil {
			show(w, r, http.StatusNotFound, notFound)
			return
		}
		show(w, r, http.StatusOK, unsubscribeContent{Title: "Stop campaign emails?", Text: "You will no longer receive campaign emails from us.", Confirm: true})
	})
	mux.HandleFunc("POST /api/unsubscribe/{token}", func(w http.ResponseWriter, r *http.Request) {
		user, err := unsubscribe.Verify(secret, r.PathValue("token"))
		if err != nil {
			show(w, r, http.StatusNotFound, notFound)
			return
		}
		if err := campaigns.Unsubscribe(r.Context(), user); err != nil {
			slog.ErrorContext(r.Context(), "unsubscribe", "error", err)
			show(w, r, http.StatusInternalServerError, unsubscribeContent{Title: "We could not do that", Text: "Open the link again and retry in a moment."})
			return
		}
		show(w, r, http.StatusOK, unsubscribeContent{Title: "You are unsubscribed", Text: "Emails still waiting to go out are stopped too."})
	})
}

func (a *api) emailPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		content.Content
		FirstName string `json:"first_name"`
	}
	if !decode(w, r, &body) {
		return
	}
	email, err := content.RenderEmail(body.Content, body.FirstName, "")
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, email)
}
