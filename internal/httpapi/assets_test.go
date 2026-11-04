package httpapi_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/asset"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/httpapi"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

func newAssetAPI(t *testing.T) http.Handler {
	t.Helper()
	metrics := telemetry.NewMetrics(prometheus.NewRegistry())
	service := campaign.NewService(testsupport.NewDatabase(t), ai.NewMock(), metrics, noop.NewTracerProvider().Tracer("test"), time.Now)
	mux := http.NewServeMux()
	httpapi.RegisterAssets(mux, service, asset.DiskStore{Dir: t.TempDir()})
	return mux
}

func upload(t *testing.T, api http.Handler, name string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/assets", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, signIn(request))
	return recorder
}

func TestAnUploadedImageCanBeLoadedByAnyoneButOnlyASignedInPersonCanUpload(t *testing.T) {
	t.Parallel()
	api := newAssetAPI(t)
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 8, 4))); err != nil {
		t.Fatal(err)
	}

	stranger := httptest.NewRecorder()
	api.ServeHTTP(stranger, httptest.NewRequest(http.MethodPost, "/api/assets", strings.NewReader("x")))
	if stranger.Code != http.StatusUnauthorized {
		t.Fatalf("upload without a session: %d", stranger.Code)
	}

	created := upload(t, api, `..\..\my logo.png`, picture.Bytes())
	if created.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", created.Code, created.Body)
	}
	var saved campaign.Asset
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Name != "my logo.png" || saved.Width != 8 || saved.Height != 4 {
		t.Fatalf("saved: %+v", saved)
	}

	// An email's reader has no session.
	loaded := httptest.NewRecorder()
	api.ServeHTTP(loaded, httptest.NewRequest(http.MethodGet, saved.URL, nil))
	if loaded.Code != http.StatusOK || loaded.Header().Get("Content-Type") != "image/png" || loaded.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("load: %d %v", loaded.Code, loaded.Header())
	}
	if _, _, err := image.Decode(loaded.Body); err != nil {
		t.Fatalf("served file: %v", err)
	}

	removed := httptest.NewRecorder()
	api.ServeHTTP(removed, signIn(httptest.NewRequest(http.MethodDelete, "/api/assets/"+saved.ID, nil)))
	if removed.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", removed.Code)
	}
	gone := httptest.NewRecorder()
	api.ServeHTTP(gone, httptest.NewRequest(http.MethodGet, saved.URL, nil))
	if gone.Code != http.StatusNotFound {
		t.Fatalf("after delete: %d", gone.Code)
	}
}

func TestAnUploadThatIsNotAPictureIsRefusedWithAClearMessage(t *testing.T) {
	t.Parallel()
	api := newAssetAPI(t)
	refused := upload(t, api, "logo.png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "PNG and JPEG") {
		t.Fatalf("svg called png: %d %s", refused.Code, refused.Body)
	}
	tooBig := upload(t, api, "big.png", bytes.Repeat([]byte("x"), asset.MaxBytes+1))
	if tooBig.Code != http.StatusBadRequest || !strings.Contains(tooBig.Body.String(), "5 MB") {
		t.Fatalf("too big: %d %s", tooBig.Code, tooBig.Body)
	}
	listed := httptest.NewRecorder()
	api.ServeHTTP(listed, signIn(httptest.NewRequest(http.MethodGet, "/api/assets", nil)))
	if !strings.Contains(listed.Body.String(), `"assets":[]`) && !strings.Contains(listed.Body.String(), `"assets":null`) {
		t.Fatalf("a refused upload was kept: %s", listed.Body)
	}
}
