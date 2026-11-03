package mockprovider

import (
	_ "embed"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	defaultInboxSize = 5000
	defaultInboxPage = 200
	maxInboxPage     = 1000
)

//go:embed inbox.html
var inboxPage []byte

// InboxMessage is a message as the provider received it, with its variables
// filled in. Repeats counts calls that reused its idempotency key: they reached
// the provider, but no person would get a second message.
type InboxMessage struct {
	ID         string     `json:"id"`
	Channel    string     `json:"channel"`
	To         string     `json:"to"`
	Subject    string     `json:"subject,omitempty"`
	Body       string     `json:"body"`
	Repeats    int        `json:"repeats"`
	ReceivedAt time.Time  `json:"received_at"`
	OpenedAt   *time.Time `json:"opened_at"`
	key        string
}

// InboxPage is one read of the inbox, newest first. Totals ignore the filters.
type InboxPage struct {
	Messages []InboxMessage `json:"messages"`
	Matching int            `json:"matching"`
	SMS      int            `json:"sms"`
	Email    int            `json:"email"`
	Unopened int            `json:"unopened"`
	Dropped  int            `json:"dropped"`
}

// inbox keeps recent messages in memory so a person can read what was sent. It is
// a test tool: restarting the provider empties it. The Server's mutex guards it.
type inbox struct {
	size     int
	messages []*InboxMessage
	byKey    map[string]*InboxMessage
	dropped  int
}

func (b *inbox) add(key, id string, message request) {
	entry := &InboxMessage{
		ID: id, Channel: message.Channel, To: message.To, Subject: message.Subject, Body: message.Body,
		ReceivedAt: time.Now().UTC(), key: key,
	}
	b.messages = append(b.messages, entry)
	b.byKey[key] = entry
	if len(b.messages) > b.size {
		delete(b.byKey, b.messages[0].key)
		b.messages = slices.Delete(b.messages, 0, 1)
		b.dropped++
	}
}

func (b *inbox) repeat(key string) {
	if entry, found := b.byKey[key]; found {
		entry.Repeats++
	}
}

// Inbox reads the inbox. Search looks in the recipient, subject and body.
func (s *Server) Inbox(channel, search string, limit int) InboxPage {
	s.mu.Lock()
	defer s.mu.Unlock()
	page := InboxPage{Messages: []InboxMessage{}, Dropped: s.inbox.dropped}
	search = strings.ToLower(strings.TrimSpace(search))
	for _, entry := range slices.Backward(s.inbox.messages) {
		if entry.OpenedAt == nil {
			page.Unopened++
		}
		if entry.Channel == "sms" {
			page.SMS++
		} else {
			page.Email++
		}
		if channel != "" && entry.Channel != channel {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(entry.To+"\n"+entry.Subject+"\n"+entry.Body), search) {
			continue
		}
		page.Matching++
		if len(page.Messages) < limit {
			page.Messages = append(page.Messages, *entry)
		}
	}
	return page
}

// ClearInbox empties the inbox for a new test. Idempotency keys and statistics
// stay, because the provider has still seen those messages.
func (s *Server) ClearInbox() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inbox.messages = nil
	s.inbox.byKey = map[string]*InboxMessage{}
	s.inbox.dropped = 0
}

// Open marks a message as opened and reports it the first time. It says whether
// the message exists.
func (s *Server) Open(id string) bool {
	s.mu.Lock()
	var event *Event
	found := false
	for _, entry := range s.inbox.messages {
		if entry.ID != id {
			continue
		}
		found = true
		if entry.OpenedAt == nil {
			now := time.Now().UTC()
			entry.OpenedAt = &now
			event = &Event{Event: eventOpened, Channel: entry.Channel, MessageID: entry.ID, IdempotencyKey: entry.key, OccurredAt: now}
		}
		break
	}
	s.mu.Unlock()
	if event != nil && s.behavior.OnEvent != nil {
		s.behavior.OnEvent(*event)
	}
	return found
}

func (s *Server) registerInbox(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/inbox/{id}/open", s.openMessage)
	mux.HandleFunc("GET /{$}", serveInboxPage)
	mux.HandleFunc("GET /v1/inbox", s.getInbox)
	mux.HandleFunc("DELETE /v1/inbox", s.deleteInbox)
}

func serveInboxPage(w http.ResponseWriter, _ *http.Request) {
	// Message text comes from a model and from people, so the page runs only its
	// own script and never loads anything from elsewhere.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(inboxPage)
}

func (s *Server) getInbox(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	channel := query.Get("channel")
	if channel != "" && channel != "sms" && channel != "email" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown channel"})
		return
	}
	limit, err := strconv.Atoi(query.Get("limit"))
	if err != nil || limit <= 0 {
		limit = defaultInboxPage
	}
	writeJSON(w, http.StatusOK, s.Inbox(channel, query.Get("q"), min(limit, maxInboxPage)))
}

func (s *Server) openMessage(w http.ResponseWriter, r *http.Request) {
	if !s.Open(r.PathValue("id")) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such message"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteInbox(w http.ResponseWriter, _ *http.Request) {
	s.ClearInbox()
	w.WriteHeader(http.StatusNoContent)
}
