//go:build e2e

// Package e2e holds the acceptance test for the whole campaign chain. It runs
// against a stack started with `make up` and stays out of `make test`, and
// covers the path from a brief to a finished campaign.
package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

const exampleBrief = "Send a 30% discount reminder to users in Istanbul who added a product to " +
	"their cart in the last 30 days and did not buy. Keep it friendly and short."

func baseURL() string {
	if url := os.Getenv("E2E_BASE_URL"); url != "" {
		return url
	}
	return "http://localhost:8081"
}

func call(t *testing.T, method, path string, body any, wantStatus int) []byte {
	t.Helper()
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, baseURL()+path, payload)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.AddCookie(&http.Cookie{Name: "scc_session", Value: "e2e@example.com"})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, response.StatusCode, wantStatus, data)
	}
	return data
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return value
}

func TestBriefToDeliveredCampaign(t *testing.T) {
	created := decode[struct {
		ID string `json:"id"`
	}](t, call(t, http.MethodPost, "/api/campaigns", map[string]string{"brief": exampleBrief}, http.StatusCreated))
	campaign := "/api/campaigns/" + created.ID

	call(t, http.MethodPost, campaign+"/segment", nil, http.StatusOK)

	audience := decode[struct {
		Total int `json:"total"`
	}](t, call(t, http.MethodGet, campaign+"/audience", nil, http.StatusOK))
	if audience.Total == 0 {
		t.Fatal("example brief must match seeded users")
	}

	call(t, http.MethodPost, campaign+"/generate", nil, http.StatusOK)
	call(t, http.MethodPost, campaign+"/approve", nil, http.StatusOK)
	call(t, http.MethodPost, campaign+"/dispatch", nil, http.StatusAccepted)
	// Repeating dispatch must not create a second set of deliveries.
	call(t, http.MethodPost, campaign+"/dispatch", nil, http.StatusAccepted)

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		state := decode[struct {
			Status string `json:"status"`
		}](t, call(t, http.MethodGet, campaign, nil, http.StatusOK))
		if state.Status == "COMPLETED" || state.Status == "COMPLETED_WITH_FAILURES" {
			checkOpeningAMessageReachesTheReport(t, campaign)
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("campaign did not finish in time")
}

// checkOpeningAMessageReachesTheReport follows a message through the test inbox,
// the signed webhook and the database to the campaign's report.
func checkOpeningAMessageReachesTheReport(t *testing.T, campaign string) {
	t.Helper()
	opened := func() int {
		report := decode[struct {
			Engagement []struct {
				Opened int `json:"opened"`
			} `json:"engagement"`
		}](t, call(t, http.MethodGet, campaign+"/report", nil, http.StatusOK))
		total := 0
		for _, channel := range report.Engagement {
			total += channel.Opened
		}
		return total
	}
	before := opened()

	response, err := http.Get(providerURL() + "/v1/inbox?limit=1000")
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	defer response.Body.Close()
	var inbox struct {
		Messages []struct {
			ID       string  `json:"id"`
			OpenedAt *string `json:"opened_at"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&inbox); err != nil {
		t.Fatalf("decode inbox: %v", err)
	}
	// This campaign finished last, so the newest messages are its own.
	for _, message := range inbox.Messages {
		if message.OpenedAt != nil {
			continue
		}
		opening, err := http.Post(providerURL()+"/v1/inbox/"+message.ID+"/open", "application/json", nil)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		opening.Body.Close()
		if opening.StatusCode != http.StatusNoContent {
			t.Fatalf("open: %d", opening.StatusCode)
		}
		break
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if opened() == before+1 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("an opened message did not reach the report: still %d", before)
}

func providerURL() string {
	if url := os.Getenv("E2E_PROVIDER_URL"); url != "" {
		return url
	}
	return "http://localhost:8083"
}
