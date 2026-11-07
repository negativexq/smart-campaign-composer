package httpapi

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestImageTurnsLetOnlySoManyDrawAtOnce(t *testing.T) {
	t.Parallel()
	turns := newImageTurns(2, 5*time.Second)
	var drawing, most atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !turns.take(context.Background()) {
				t.Error("an upload got no turn although each waits far longer than the work takes")
				return
			}
			now := drawing.Add(1)
			for seen := most.Load(); now > seen && !most.CompareAndSwap(seen, now); seen = most.Load() {
			}
			time.Sleep(5 * time.Millisecond)
			drawing.Add(-1)
			turns.give()
		}()
	}
	wg.Wait()
	if most.Load() != 2 {
		t.Fatalf("at most %d drew at once, want 2", most.Load())
	}
}

func TestAnUploadThatGetsNoTurnInTimeIsRefused(t *testing.T) {
	t.Parallel()
	turns := newImageTurns(1, 20*time.Millisecond)
	if !turns.take(context.Background()) {
		t.Fatal("the first turn was not free")
	}
	started := time.Now()
	if turns.take(context.Background()) {
		t.Fatal("a second upload drew while the only turn was taken")
	}
	if waited := time.Since(started); waited < 20*time.Millisecond {
		t.Fatalf("refused after %v, before the wait was up", waited)
	}

	// A request that ends stops waiting at once.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slow := newImageTurns(1, time.Hour)
	slow.take(context.Background())
	if slow.take(ctx) {
		t.Fatal("a cancelled request got a turn")
	}

	turns.give()
	if !turns.take(context.Background()) {
		t.Fatal("a turn given back could not be taken again")
	}
}

func TestABusyUploadIsAnsweredWith503AndRetryAfter(t *testing.T) {
	t.Parallel()
	turns := newImageTurns(1, 20*time.Millisecond)
	turns.take(context.Background())

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("\x89PNG not drawn, since it never gets a turn")) //nolint:errcheck // in memory
	form.Close()                                                       //nolint:errcheck // in memory
	request := httptest.NewRequest(http.MethodPost, "/api/assets", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	recorder := httptest.NewRecorder()

	// No service or store: an upload without a turn must not reach either.
	uploadAsset(recorder, request, nil, nil, turns)
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") != imageRetryAfter {
		t.Fatalf("got %d, Retry-After %q: %s", recorder.Code, recorder.Header().Get("Retry-After"), recorder.Body)
	}
}
