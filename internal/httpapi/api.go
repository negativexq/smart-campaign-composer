// Package httpapi exposes campaign operations as JSON over HTTP.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

const maxBodyBytes = 64 << 10

type api struct {
	campaigns *campaign.Service
	provider  ai.Provider
}

// Register adds the campaign routes to mux.
func Register(mux *http.ServeMux, campaigns *campaign.Service, provider ai.Provider) {
	a := &api{campaigns, provider}
	mux.HandleFunc("GET /api/config", a.config)
	mux.HandleFunc("GET /api/overview", a.overview)
	mux.HandleFunc("GET /api/campaigns", a.list)
	mux.HandleFunc("POST /api/campaigns", a.create)
	mux.HandleFunc("GET /api/campaigns/{id}", a.get)
	mux.HandleFunc("POST /api/campaigns/{id}/segment", a.proposeSegment)
	mux.HandleFunc("GET /api/campaigns/{id}/audience", a.audience)
	mux.HandleFunc("GET /api/campaigns/{id}/activity", a.activity)
}

func (a *api) config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"llm_provider": a.provider.Name(), "llm_model": a.provider.Model()})
}

func (a *api) overview(w http.ResponseWriter, r *http.Request) {
	overview, err := a.campaigns.Overview(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (a *api) list(w http.ResponseWriter, r *http.Request) {
	campaigns, err := a.campaigns.List(r.Context(), campaign.ListFilter{
		Group: r.URL.Query().Get("group"),
		Query: r.URL.Query().Get("q"),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaigns": campaigns})
}

func (a *api) activity(w http.ResponseWriter, r *http.Request) {
	entries, err := a.campaigns.Activity(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": entries})
}

func (a *api) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Brief string `json:"brief"`
	}
	if !decode(w, r, &body) {
		return
	}
	id, err := a.campaigns.Create(r.Context(), body.Brief)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (a *api) get(w http.ResponseWriter, r *http.Request) {
	view, err := a.campaigns.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *api) proposeSegment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Brief      *string `json:"brief"`
		Refinement string  `json:"refinement"`
	}
	if !decode(w, r, &body) {
		return
	}
	view, err := a.campaigns.ProposeSegment(r.Context(), r.PathValue("id"), campaign.ProposeInput{
		Brief: body.Brief, Refinement: body.Refinement,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *api) audience(w http.ResponseWriter, r *http.Request) {
	view, err := a.campaigns.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if view.Proposal == nil {
		writeJSON(w, http.StatusConflict, errorBody("This campaign has no audience yet."))
		return
	}
	writeJSON(w, http.StatusOK, view.Audience)
}

// decode reads a strict JSON body. An empty body is allowed and means defaults.
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errorBody("The request body is not valid."))
		return false
	}
	return true
}

// writeError maps failures to messages a marketer can act on. Anything not
// listed is logged and reported without detail.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, message := http.StatusInternalServerError, "Something went wrong on our side. Try again."
	switch {
	case errors.Is(err, campaign.ErrInvalidBrief):
		status, message = http.StatusBadRequest, "Write a brief of up to 2000 characters."
	case errors.Is(err, campaign.ErrUnknownGroup):
		status, message = http.StatusBadRequest, "This campaign filter does not exist."
	case errors.Is(err, campaign.ErrNotFound):
		status, message = http.StatusNotFound, "This campaign does not exist."
	case errors.Is(err, campaign.ErrNotEditable):
		status, message = http.StatusConflict, "This campaign has moved past drafting and can no longer be edited."
	case errors.Is(err, campaign.ErrProposalRejected):
		status, message = http.StatusUnprocessableEntity, "We could not turn this brief into a valid audience: "+err.Error()+". Try naming a city, a time window or cart and purchase behaviour."
	case errors.Is(err, ai.ErrRefused):
		status, message = http.StatusUnprocessableEntity, "The AI declined this brief. Rephrase it and try again."
	case errors.Is(err, ai.ErrTruncated), errors.Is(err, ai.ErrInvalidOutput):
		status, message = http.StatusBadGateway, "The AI returned an unusable response. Try again."
	case errors.Is(err, ai.ErrRateLimited), errors.Is(err, ai.ErrUnavailable):
		status, message = http.StatusServiceUnavailable, "The AI service is unavailable right now. Your brief is saved; try again shortly."
	default:
		slog.ErrorContext(r.Context(), "request failed", "error", err)
	}
	writeJSON(w, status, errorBody(message))
}

func errorBody(message string) map[string]string {
	return map[string]string{"error": message}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response", "error", err)
	}
}
