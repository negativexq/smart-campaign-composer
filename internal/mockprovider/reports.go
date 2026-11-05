package mockprovider

import (
	"log/slog"
	"math/rand/v2"
	"time"
)

const (
	// reportConcurrency limits the calls at once, so a restart with many unsent reports
	// does not arrive at the sender as one burst.
	reportConcurrency  = 16
	maxReportRetryWait = time.Minute
	// reportGiveUpAfter is how long a report is tried before it is dropped.
	reportGiveUpAfter = 24 * time.Hour
)

func reportID(event Event) string {
	return event.MessageID + "/" + event.Event
}

// owe takes on reports. The caller holds the mutex and has saved them already.
func (s *Server) owe(events []Event) {
	for _, event := range events {
		s.pending[reportID(event)] = event
		s.schedule(event)
	}
}

func (s *Server) schedule(event Event) {
	if s.behavior.OnEvent == nil {
		return
	}
	time.AfterFunc(time.Until(event.OccurredAt), func() { s.deliver(event) })
}

// deliver tries until the sender has the report. Reports are sent at least once: a crash
// right after a success sends it again, so a sender must accept a repeat, as with any
// webhook. Jitter stops reports that failed together from retrying together.
func (s *Server) deliver(event Event) {
	wait := s.behavior.ReportRetryWait
	for {
		select {
		case <-s.stop:
			return
		case s.slots <- struct{}{}:
		}
		err := s.behavior.OnEvent(event)
		<-s.slots
		if err == nil {
			s.settle(event)
			return
		}
		if time.Since(event.OccurredAt) >= reportGiveUpAfter {
			slog.Warn("report given up", "event", event.Event, "error", err)
			s.settle(event)
			return
		}
		select {
		case <-s.stop:
			return
		case <-time.After(wait/2 + rand.N(wait/2+1)):
		}
		wait = min(wait*2, maxReportRetryWait)
	}
}

// settle records that a report is finished. Losing this line only sends it once more,
// so it does not wait for the disk.
func (s *Server) settle(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	delete(s.pending, reportID(event))
	s.save(record{Kind: recordReported, ID: event.MessageID, Event: event.Event}, false)
	s.compactIfLarge()
}

// save writes a record whose loss would not break a promise to the sender, so a failure
// is logged rather than failing the call. The caller holds the mutex.
func (s *Server) save(entry record, durable bool) {
	if s.journal == nil || s.closed {
		return
	}
	if err := s.journal.add(entry, durable); err != nil {
		slog.Error("write journal", "kind", entry.Kind, "error", err)
	}
}

func (s *Server) compactIfLarge() {
	if s.journal == nil || s.journal.size < s.journal.compactAt {
		return
	}
	if err := s.journal.replace(s.snapshot()); err != nil {
		slog.Error("compact journal", "error", err)
		// Try again once it has grown further rather than on every message.
		s.journal.compactAt = 2 * s.journal.size
	}
}

// replay rebuilds the provider from a journal.
func (s *Server) replay(records []record) {
	for _, entry := range records {
		switch entry.Kind {
		case recordAccepted:
			s.accepted[entry.Key] = entry.ID
			if entry.Message != nil {
				s.inbox.add(entry.Key, entry.Message)
			}
		case recordRepeated:
			s.inbox.repeat(entry.Key)
		case recordOpened:
			if message, found := s.inbox.byKey[entry.Key]; found {
				message.OpenedAt = entry.At
			}
		case recordReported:
			delete(s.pending, entry.ID+"/"+entry.Event)
		case recordCleared:
			s.inbox.clear()
		case recordDropped:
			s.inbox.dropped = entry.Count
		}
		for _, event := range entry.Reports {
			s.pending[reportID(event)] = event
		}
	}
}

// snapshot is the shortest journal that replays to the current state: every key, the
// inbox in its order and the reports still owed.
func (s *Server) snapshot() []record {
	reports := map[string][]Event{}
	for _, event := range s.pending {
		reports[event.IdempotencyKey] = append(reports[event.IdempotencyKey], event)
	}
	records := make([]record, 0, len(s.accepted)+1)
	for key, id := range s.accepted {
		if _, inInbox := s.inbox.byKey[key]; !inInbox {
			records = append(records, record{Kind: recordAccepted, Key: key, ID: id, Reports: reports[key]})
		}
	}
	for _, message := range s.inbox.messages {
		records = append(records, record{Kind: recordAccepted, Key: message.key, ID: message.ID, Message: message, Reports: reports[message.key]})
	}
	return append(records, record{Kind: recordDropped, Count: s.inbox.dropped})
}
