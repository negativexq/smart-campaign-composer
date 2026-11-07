package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/negativexq/smart-campaign-composer/internal/asset"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

const (
	maxAssetName  = 120
	formOverhead  = 1 << 16
	assetLifetime = "public, max-age=31536000, immutable"
)

// RegisterAssets adds image upload and serving. Serving needs no session, because the
// people who receive an email have to be able to load its images.
func RegisterAssets(mux *http.ServeMux, campaigns *campaign.Service, store asset.Store) {
	registerAssets(mux, campaigns, store, newImageTurns(imagesAtOnce, imageTurnWait))
}

func registerAssets(mux *http.ServeMux, campaigns *campaign.Service, store asset.Store, turns *imageTurns) {
	mux.HandleFunc("GET /api/assets", signedIn(func(w http.ResponseWriter, r *http.Request) {
		found, err := campaigns.Assets(r.Context())
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"assets": found})
	}))
	mux.HandleFunc("POST /api/assets", signedIn(func(w http.ResponseWriter, r *http.Request) {
		uploadAsset(w, r, campaigns, store, turns)
	}))
	mux.HandleFunc("DELETE /api/assets/{id}", signedIn(func(w http.ResponseWriter, r *http.Request) {
		fileName, err := campaigns.DeleteAsset(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		// The record is gone, so a file left behind is only wasted space.
		if err := store.Remove(r.Context(), fileName); err != nil {
			slog.WarnContext(r.Context(), "remove asset file", "error", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /api/assets/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		found, err := campaigns.AssetByID(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		file, err := store.Open(r.Context(), found.FileName())
		if err != nil {
			writeError(w, r, err)
			return
		}
		defer file.Close() //nolint:errcheck // read only
		w.Header().Set("Content-Type", found.MIME)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("Cache-Control", assetLifetime)
		http.ServeContent(w, r, "", time.Time{}, file)
	})
}

func uploadAsset(w http.ResponseWriter, r *http.Request, campaigns *campaign.Service, store asset.Store, turns *imageTurns) {
	r.Body = http.MaxBytesReader(w, r.Body, asset.MaxBytes+formOverhead)
	file, header, err := r.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, r, &campaign.DesignError{Message: "Choose an image of up to 5 MB."})
			return
		}
		writeError(w, r, &campaign.DesignError{Message: "Choose a PNG or JPEG image to upload."})
		return
	}
	defer file.Close() //nolint:errcheck // read only
	// Reading and drawing the image is what costs memory, so both wait for a turn.
	if !turns.take(r.Context()) {
		w.Header().Set("Retry-After", imageRetryAfter)
		writeJSON(w, http.StatusServiceUnavailable, errorBody("Too many images are being uploaded right now. Try again in a few seconds."))
		return
	}
	image, err := func() (asset.Image, error) {
		// Given back even if drawing panics, so a bad file cannot use up the turns.
		defer turns.give()
		return drawUpload(file)
	}()
	switch {
	case errors.Is(err, errUploadTooBig):
		writeError(w, r, &campaign.DesignError{Message: "Choose an image of up to 5 MB."})
		return
	case errors.Is(err, asset.ErrNotAnImage):
		writeError(w, r, &campaign.DesignError{Message: "Only PNG and JPEG images can be used in emails here."})
		return
	case errors.Is(err, asset.ErrTooManyPixels):
		writeError(w, r, &campaign.DesignError{Message: "Choose an image of up to 24 megapixels, no wider or taller than 6000 pixels."})
		return
	case err != nil:
		writeError(w, r, err)
		return
	}
	// The file goes first, so a record never points at nothing.
	if err := store.Put(r.Context(), image.FileName(), image.Data); err != nil {
		writeError(w, r, err)
		return
	}
	saved, err := campaigns.RecordAsset(r.Context(), assetName(header.Filename), image)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

// errUploadTooBig means the file is over asset.MaxBytes.
var errUploadTooBig = errors.New("upload too big")

// drawUpload reads an upload and draws it again. It is the part that costs memory.
func drawUpload(file io.Reader) (asset.Image, error) {
	raw, err := io.ReadAll(io.LimitReader(file, asset.MaxBytes+1))
	if err != nil {
		return asset.Image{}, err
	}
	if len(raw) > asset.MaxBytes {
		return asset.Image{}, errUploadTooBig
	}
	return asset.Process(raw)
}

func assetName(uploaded string) string {
	name := strings.TrimSpace(filepath.Base(strings.ReplaceAll(uploaded, "\\", "/")))
	if !utf8.ValidString(name) || name == "." || name == "/" || name == "" {
		return "image"
	}
	if runes := []rune(name); len(runes) > maxAssetName {
		return string(runes[:maxAssetName])
	}
	return name
}
