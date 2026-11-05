package mockprovider

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestAKeyThatCannotBeWrittenIsAnsweredAsATemporaryFailure(t *testing.T) {
	t.Parallel()
	provider, err := Open(Behavior{Roll: func() float64 { return 0.9 }}, filepath.Join(t.TempDir(), "keys.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.keys.file.Close(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	provider.Register(mux)
	body, _ := json.Marshal(request{Channel: "sms", To: "+90500", Body: "Hi"})
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	request.Header.Set("Idempotency-Key", "k1")
	answer := httptest.NewRecorder()
	mux.ServeHTTP(answer, request)
	if answer.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", answer.Code)
	}
	if stats := provider.Stats()["sms"]; stats.Accepted != 0 || stats.ServerErrors != 1 {
		t.Fatalf("stats: %+v", stats)
	}
	if _, known := provider.accepted["k1"]; known {
		t.Fatal("a key that was not saved must not count as remembered")
	}
}
