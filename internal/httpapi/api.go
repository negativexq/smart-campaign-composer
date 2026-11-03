// Package httpapi exposes campaign operations as JSON over HTTP.
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/content"
	"github.com/negativexq/smart-campaign-composer/internal/schedule"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
)

const (
	maxBodyBytes = 64 << 10
	liveInterval = time.Second
	keepAlive    = 15 * time.Second
)

type api struct {
	campaigns *campaign.Service
	provider  ai.Provider
	metrics   *telemetry.Metrics
}

// Register adds the campaign routes to mux.
func Register(mux *http.ServeMux, campaigns *campaign.Service, provider ai.Provider, metrics *telemetry.Metrics) {
	a := &api{campaigns, provider, metrics}
	mux.HandleFunc("GET /api/config", a.config)
	mux.HandleFunc("GET /api/session", a.session)
	mux.HandleFunc("POST /api/session", a.signIn)
	mux.HandleFunc("DELETE /api/session", a.signOut)
	for pattern, handler := range map[string]http.HandlerFunc{
		"GET /api/overview":                                      a.overview,
		"GET /api/campaigns":                                     a.list,
		"POST /api/campaigns":                                    a.create,
		"GET /api/campaigns/{id}":                                a.get,
		"PATCH /api/campaigns/{id}":                              a.rename,
		"POST /api/campaigns/{id}/segment":                       a.proposeSegment,
		"GET /api/campaigns/{id}/audience":                       a.audience,
		"GET /api/campaigns/{id}/audience/breakdown":             a.audienceBreakdown,
		"POST /api/campaigns/{id}/generate":                      a.generate,
		"PUT /api/campaigns/{id}/content":                        a.saveContent,
		"GET /api/campaigns/{id}/sample-recipients":              a.sampleRecipients,
		"POST /api/email-preview":                                a.emailPreview,
		"POST /api/campaigns/{id}/approve":                       a.approve,
		"POST /api/campaigns/{id}/dispatch":                      a.dispatch,
		"POST /api/campaigns/{id}/schedule":                      a.schedule,
		"DELETE /api/campaigns/{id}/schedule":                    a.cancelSchedule,
		"GET /api/campaigns/{id}/progress":                       a.progress,
		"GET /api/campaigns/{id}/events":                         a.events,
		"GET /api/campaigns/{id}/deliveries":                     a.deliveries,
		"GET /api/campaigns/{id}/report":                         a.report,
		"GET /api/campaigns/{id}/deliveries/{delivery}/attempts": a.deliveryAttempts,
		"GET /api/campaigns/{id}/activity":                       a.activity,
	} {
		mux.HandleFunc(pattern, signedIn(handler))
	}
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
	query := r.URL.Query()
	days := 0
	if value := query.Get("created_within_days"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			writeError(w, r, campaign.ErrUnknownFilter)
			return
		}
		days = parsed
	}
	campaigns, err := a.campaigns.List(r.Context(), campaign.ListFilter{
		Group:             query.Get("group"),
		Query:             query.Get("q"),
		Channel:           query.Get("channel"),
		CreatedWithinDays: days,
		Sort:              query.Get("sort"),
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
		Brief    string   `json:"brief"`
		Channels []string `json:"channels"`
		Tone     string   `json:"tone"`
		Offer    *int     `json:"offer_percent"`
		Skip     *int     `json:"not_messaged_within_days"`
	}
	if !decode(w, r, &body) {
		return
	}
	id, err := a.campaigns.CreateWith(r.Context(), campaign.NewCampaign{
		Brief: body.Brief, Channels: body.Channels, Tone: body.Tone, OfferPercent: body.Offer, NotMessagedWithinDays: body.Skip,
	})
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

func (a *api) rename(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	view, err := a.campaigns.Rename(r.Context(), r.PathValue("id"), body.Name)
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

func (a *api) audienceBreakdown(w http.ResponseWriter, r *http.Request) {
	breakdown, err := a.campaigns.AudienceBreakdown(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, breakdown)
}

// generate streams one generation as server-sent events. Anything that stops it
// from starting is an ordinary JSON error; after that the status is already sent,
// so failures travel as an error event.
func (a *api) generate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Instruction string `json:"instruction"`
		Channel     string `json:"channel"`
	}
	if !decode(w, r, &body) {
		return
	}
	generation, err := a.campaigns.StartGeneration(r.Context(), r.PathValue("id"), campaign.GenerateInput{Instruction: body.Instruction, Channel: body.Channel})
	if err != nil {
		writeError(w, r, err)
		return
	}

	a.metrics.SSEConnections.Inc()
	defer a.metrics.SSEConnections.Dec()
	stream := newEventStream(w, r)
	stream.send("started", struct{}{})
	view, err := generation.Run(r.Context(), func(event campaign.GenerationEvent) {
		if event.Type == campaign.EventReset {
			stream.send("reset", struct{}{})
			return
		}
		stream.send("delta", map[string]string{"field": event.Field, "text": event.Text})
	})
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		_, message := describeError(r, err)
		stream.send("error", errorBody(message))
		return
	}
	stream.send("done", view)
}

// eventStream writes server-sent events and pushes each one out immediately.
type eventStream struct {
	w          http.ResponseWriter
	controller *http.ResponseController
}

func newEventStream(w http.ResponseWriter, r *http.Request) *eventStream {
	controller := http.NewResponseController(w)
	// A generation can outlast any general write timeout on the server.
	if err := controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.WarnContext(r.Context(), "clear write deadline", "error", err)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return &eventStream{w, controller}
}

// send ignores write errors: a client that left cancels the request context,
// which is what stops the generation.
func (s *eventStream) send(event string, data any) {
	encoded, err := json.Marshal(data)
	if err != nil {
		slog.Error("encode event", "event", event, "error", err)
		return
	}
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, encoded) //nolint:errcheck // see above
	if err := s.controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.Debug("flush event", "error", err)
	}
}

// comment keeps idle connections open through proxies.
func (s *eventStream) comment(text string) {
	fmt.Fprintf(s.w, ": %s\n\n", text) //nolint:errcheck // see send
	if err := s.controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		slog.Debug("flush comment", "error", err)
	}
}

func (a *api) saveContent(w http.ResponseWriter, r *http.Request) {
	var body content.Content
	if !decode(w, r, &body) {
		return
	}
	view, err := a.campaigns.SaveContent(r.Context(), r.PathValue("id"), body)
	var invalid *content.ValidationError
	if errors.As(err, &invalid) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Fix the highlighted messages before saving.", "issues": invalid.Issues})
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *api) sampleRecipients(w http.ResponseWriter, r *http.Request) {
	samples, err := a.campaigns.SampleRecipients(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipients": samples})
}

func (a *api) approve(w http.ResponseWriter, r *http.Request) {
	view, err := a.campaigns.Approve(r.Context(), r.PathValue("id"))
	var invalid *content.ValidationError
	if errors.As(err, &invalid) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "These messages no longer fit the limits. Edit them before approving.", "issues": invalid.Issues})
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// dispatch answers 202 because sending happens afterwards, in the worker.
func (a *api) dispatch(w http.ResponseWriter, r *http.Request) {
	view, err := a.campaigns.Dispatch(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, view)
}

func (a *api) schedule(w http.ResponseWriter, r *http.Request) {
	var spec schedule.Spec
	if !decode(w, r, &spec) {
		return
	}
	view, err := a.campaigns.Schedule(r.Context(), r.PathValue("id"), spec)
	var invalid *schedule.InvalidError
	if errors.As(err, &invalid) {
		writeJSON(w, http.StatusBadRequest, errorBody(invalid.Message))
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *api) cancelSchedule(w http.ResponseWriter, r *http.Request) {
	view, err := a.campaigns.CancelSchedule(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *api) progress(w http.ResponseWriter, r *http.Request) {
	progress, err := a.campaigns.Progress(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, progress)
}

// events streams a campaign's state until it can no longer change. Workers may run
// anywhere, so the database is the source: every process reads the same truth and a
// reconnecting client starts from the current state with nothing to replay.
func (a *api) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	state, changing, err := a.campaigns.Live(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}

	a.metrics.SSEConnections.Inc()
	defer a.metrics.SSEConnections.Dec()
	stream := newEventStream(w, r)
	stream.send("update", state)
	last, _ := json.Marshal(state) //nolint:errcheck // a failure only means one extra update

	ticker := time.NewTicker(liveInterval)
	defer ticker.Stop()
	quietSince := time.Now()
	for changing {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		state, stillChanging, err := a.campaigns.Live(r.Context(), id)
		if err != nil {
			// The client keeps showing the last state until the database is back.
			if r.Context().Err() == nil {
				slog.WarnContext(r.Context(), "read live state", "error", err)
			}
			continue
		}
		changing = stillChanging
		encoded, err := json.Marshal(state)
		if err == nil && bytes.Equal(encoded, last) {
			if time.Since(quietSince) >= keepAlive {
				stream.comment("keep-alive")
				quietSince = time.Now()
			}
			continue
		}
		last, quietSince = encoded, time.Now()
		stream.send("update", state)
	}
	stream.send("end", struct{}{})
}

func (a *api) deliveries(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))   //nolint:errcheck // anything invalid means the default
	offset, _ := strconv.Atoi(query.Get("offset")) //nolint:errcheck // anything invalid means the start
	from, fromErr := parseTime(query.Get("from"))
	to, toErr := parseTime(query.Get("to"))
	if fromErr != nil || toErr != nil {
		writeError(w, r, campaign.ErrUnknownFilter)
		return
	}
	deliveries, err := a.campaigns.Deliveries(r.Context(), r.PathValue("id"), campaign.DeliveryFilter{
		Status: query.Get("status"), Channel: query.Get("channel"), Reason: query.Get("reason"), Outcome: query.Get("outcome"),
		From: from, To: to, Retried: query.Get("retried") == "true", Opened: query.Get("opened"), Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries})
}

func (a *api) report(w http.ResponseWriter, r *http.Request) {
	report, err := a.campaigns.Report(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// parseTime reads an optional RFC 3339 query value.
func parseTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func (a *api) deliveryAttempts(w http.ResponseWriter, r *http.Request) {
	attempts, err := a.campaigns.DeliveryAttempts(r.Context(), r.PathValue("id"), r.PathValue("delivery"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attempts": attempts})
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

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, message := describeError(r, err)
	writeJSON(w, status, errorBody(message))
}

// describeError maps failures to messages a marketer can act on. Anything not
// listed is logged and reported without detail.
func describeError(r *http.Request, err error) (int, string) {
	status, message := http.StatusInternalServerError, "Something went wrong on our side. Try again."
	switch {
	case errors.Is(err, campaign.ErrInvalidBrief):
		status, message = http.StatusBadRequest, "Write a brief of up to 2000 characters."
	case errors.Is(err, campaign.ErrInvalidChannels):
		status, message = http.StatusBadRequest, "Choose SMS, email or both."
	case errors.Is(err, campaign.ErrInvalidOptions):
		status, message = http.StatusBadRequest, "Check the tone, the discount of 1 to 100 percent and the days to skip of 1 to 365."
	case errors.Is(err, campaign.ErrChannelDiscount):
		status, message = http.StatusConflict, "The SMS and the email would promise different discounts. Change the discount in the brief, or make both messages say the same."
	case errors.Is(err, campaign.ErrOfferConflict):
		status, message = http.StatusBadRequest, "The brief mentions a different discount than the one you entered. Change one of them."
	case errors.Is(err, campaign.ErrInvalidName):
		status, message = http.StatusBadRequest, "Write a campaign name of up to 80 characters."
	case errors.Is(err, campaign.ErrUnknownGroup):
		status, message = http.StatusBadRequest, "This campaign filter does not exist."
	case errors.Is(err, campaign.ErrNotFound):
		status, message = http.StatusNotFound, "This campaign does not exist."
	case errors.Is(err, campaign.ErrNotEditable):
		status, message = http.StatusConflict, "This campaign has moved past drafting and can no longer be edited."
	case errors.Is(err, campaign.ErrProposalRejected):
		status, message = http.StatusUnprocessableEntity, "We could not turn this brief into a valid audience: "+err.Error()+". Try naming a city, a time window or cart and purchase behaviour."
	case errors.Is(err, campaign.ErrNotReviewable):
		status, message = http.StatusConflict, "This campaign has no messages ready to approve."
	case errors.Is(err, campaign.ErrNotApproved):
		status, message = http.StatusConflict, "This campaign must be approved before it can be sent."
	case errors.Is(err, campaign.ErrAlreadyScheduled):
		status, message = http.StatusConflict, "This campaign is scheduled. Cancel the schedule to send it now."
	case errors.Is(err, campaign.ErrNoSchedule):
		status, message = http.StatusConflict, "This campaign has no schedule to cancel."
	case errors.Is(err, campaign.ErrUnknownFilter):
		status, message = http.StatusBadRequest, "This filter does not exist."
	case errors.Is(err, campaign.ErrInvalidInstruction):
		status, message = http.StatusBadRequest, "Write an instruction of up to 500 characters."
	case errors.Is(err, campaign.ErrNoAudience):
		status, message = http.StatusConflict, "This campaign has no audience yet."
	case errors.Is(err, campaign.ErrAudienceBlocked):
		status, message = http.StatusConflict, "Resolve the audience problems before writing messages."
	case errors.Is(err, campaign.ErrGenerationRunning):
		status, message = http.StatusConflict, "Messages are already being written for this campaign."
	case errors.Is(err, campaign.ErrContentRejected):
		status, message = http.StatusUnprocessableEntity, "We could not write messages that pass validation: "+err.Error()+". Try again, or give an instruction."
	case errors.Is(err, ai.ErrRefused):
		status, message = http.StatusUnprocessableEntity, "The AI declined this brief. Rephrase it and try again."
	case errors.Is(err, ai.ErrTruncated), errors.Is(err, ai.ErrInvalidOutput):
		status, message = http.StatusBadGateway, "The AI returned an unusable response. Try again."
	case errors.Is(err, ai.ErrRateLimited), errors.Is(err, ai.ErrUnavailable):
		status, message = http.StatusServiceUnavailable, "The AI service is unavailable right now. Your brief is saved; try again shortly."
	default:
		slog.ErrorContext(r.Context(), "request failed", "error", err)
	}
	return status, message
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
