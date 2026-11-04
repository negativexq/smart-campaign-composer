package asset_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/asset"
)

func sample(t *testing.T, width, height int, encode func(io.Writer, image.Image) error) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	picture.Set(0, 0, color.White)
	var out bytes.Buffer
	if err := encode(&out, picture); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestAnUploadIsJudgedByItsContentNotItsClaims(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string][]byte{
		"svg":        []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html":       []byte("<html><script>alert(1)</script>"),
		"gif":        sample(t, 2, 2, func(w io.Writer, m image.Image) error { return gif.Encode(w, m, nil) }),
		"png header": []byte("\x89PNG\r\n\x1a\nnot really"),
	} {
		if _, err := asset.Process(raw); !errors.Is(err, asset.ErrNotAnImage) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := asset.Process(sample(t, asset.MaxSide+1, 1, png.Encode)); !errors.Is(err, asset.ErrTooManyPixels) {
		t.Errorf("too wide: %v", err)
	}
}

func TestAnImageIsDrawnAgainWithoutItsMetadata(t *testing.T) {
	t.Parallel()
	raw := sample(t, 3, 2, func(w io.Writer, m image.Image) error { return jpeg.Encode(w, m, nil) })
	// EXIF saying "rotate 90 degrees", with a marker that must not survive.
	exif := append([]byte("Exif\x00\x00II*\x00\x08\x00\x00\x00\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x06\x00\x00\x00\x00\x00\x00\x00"), []byte("SECRET-GPS")...)
	segment := append([]byte{0xFF, 0xE1, 0, byte(len(exif) + 2)}, exif...)
	tagged := append(append(append([]byte{}, raw[:2]...), segment...), raw[2:]...)

	result, err := asset.Process(tagged)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(result.Data, []byte("SECRET-GPS")) {
		t.Error("metadata survived")
	}
	if result.MIME != "image/jpeg" || result.Width != 2 || result.Height != 3 {
		t.Errorf("not turned upright: %+v", result.MIME)
	}
	again, err := asset.Process(tagged)
	if err != nil || again.FileName() != result.FileName() {
		t.Errorf("equal uploads should share a file: %v", err)
	}
}

func TestDiskStoreKeepsFilesInsideItsDirectory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := asset.DiskStore{Dir: t.TempDir()}
	if err := store.Put(ctx, "../../escape.png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	file, err := store.Open(ctx, "escape.png")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close() //nolint:errcheck // read only
	if err := store.Remove(ctx, "escape.png"); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(ctx, "escape.png"); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}
