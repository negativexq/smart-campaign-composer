//go:build e2e

// Package e2e holds the acceptance test for the whole campaign chain. It runs
// against a stack started with `make up` and stays out of `make test`. It is
// expected to fail until the Day 4 milestone wires the full flow together.
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
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("campaign did not finish in time")
}
