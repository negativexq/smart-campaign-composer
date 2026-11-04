package asset_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
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

// withOrientation puts an EXIF segment naming the orientation into a JPEG.
func withOrientation(raw []byte, orientation byte, trailing string) []byte {
	exif := append([]byte("Exif\x00\x00II*\x00\x08\x00\x00\x00\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00"), orientation)
	exif = append(exif, []byte("\x00\x00\x00\x00\x00\x00\x00")...)
	exif = append(exif, trailing...)
	segment := append([]byte{0xFF, 0xE1, 0, byte(len(exif) + 2)}, exif...)
	return append(append(append([]byte{}, raw[:2]...), segment...), raw[2:]...)
}

// pngHeader is the start of a PNG claiming a size, with no pixels behind it.
func pngHeader(width, height uint32) []byte {
	chunk := []byte("IHDR")
	chunk = binary.BigEndian.AppendUint32(chunk, width)
	chunk = binary.BigEndian.AppendUint32(chunk, height)
	chunk = append(chunk, 8, 6, 0, 0, 0)
	out := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d")
	out = append(out, chunk...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return decoded
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
	// A marker after the EXIF data must not survive.
	tagged := withOrientation(raw, 6, "SECRET-GPS")

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

func TestTooManyPixelsAreRefusedBeforeAnyIsRead(t *testing.T) {
	t.Parallel()
	// Both sides are allowed, and there are no pixels to read: only the header can refuse it.
	if _, err := asset.Process(pngHeader(asset.MaxSide, asset.MaxSide)); !errors.Is(err, asset.ErrTooManyPixels) {
		t.Errorf("too many pixels: %v", err)
	}
	// Exactly at the limit it goes on to read the pixels, which are missing here.
	if _, err := asset.Process(pngHeader(asset.MaxPixels/asset.MaxSide, asset.MaxSide)); !errors.Is(err, asset.ErrNotAnImage) {
		t.Errorf("at the limit: %v", err)
	}
}

func TestAWideImageIsNarrowedAndKeepsItsShape(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct{ width, height, wantWidth, wantHeight int }{
		"wide":       {2400, 1000, asset.MaxWidth, 500},
		"at limit":   {asset.MaxWidth, 480, asset.MaxWidth, 480},
		"narrow":     {320, 100, 320, 100},
		"tall":       {1000, 3000, 1000, 3000},
		"thin strip": {asset.MaxSide, 1, asset.MaxWidth, 1},
	} {
		result, err := asset.Process(sample(t, test.width, test.height, png.Encode))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		bounds := decode(t, result.Data).Bounds()
		if result.Width != test.wantWidth || result.Height != test.wantHeight || bounds.Dx() != test.wantWidth || bounds.Dy() != test.wantHeight {
			t.Errorf("%s: recorded %dx%d, file %dx%d", name, result.Width, result.Height, bounds.Dx(), bounds.Dy())
		}
	}
}

func TestNarrowingAPNGKeepsItsTransparency(t *testing.T) {
	t.Parallel()
	picture := image.NewNRGBA(image.Rect(0, 0, 2400, 400))
	for y := range 400 {
		for x := range 1200 {
			picture.SetNRGBA(x, y, color.NRGBA{R: 200, A: 255})
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, picture); err != nil {
		t.Fatal(err)
	}
	result, err := asset.Process(raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	decoded := decode(t, result.Data)
	if _, _, _, alpha := decoded.At(100, 100).RGBA(); alpha != 0xFFFF {
		t.Errorf("solid side has alpha %x", alpha)
	}
	if _, _, _, alpha := decoded.At(1100, 100).RGBA(); alpha != 0 {
		t.Errorf("clear side has alpha %x", alpha)
	}
}

func TestAPhotoIsMeasuredTheWayUpItWillBeShown(t *testing.T) {
	t.Parallel()
	encode := func(w io.Writer, m image.Image) error { return jpeg.Encode(w, m, nil) }
	// On its side this is 1000 wide, so it is left alone.
	result, err := asset.Process(withOrientation(sample(t, 2400, 1000, encode), 6, ""))
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 1000 || result.Height != 2400 {
		t.Errorf("turned to narrow: %dx%d", result.Width, result.Height)
	}
	// On its side this is 2400 wide, so it is narrowed.
	result, err = asset.Process(withOrientation(sample(t, 1000, 2400, encode), 6, ""))
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != asset.MaxWidth || result.Height != 500 {
		t.Errorf("turned to wide: %dx%d", result.Width, result.Height)
	}
}

func TestEveryOrientationPutsThePictureTheRightWayUp(t *testing.T) {
	t.Parallel()
	// Red fills the top left quarter; the rest is blue.
	picture := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := range 32 {
		for x := range 64 {
			shade := color.RGBA{B: 255, A: 255}
			if x < 32 && y < 16 {
				shade = color.RGBA{R: 255, A: 255}
			}
			picture.SetRGBA(x, y, shade)
		}
	}
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, picture, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	const left, right, top, bottom = 0, 1, 0, 1
	for orientation, want := range map[byte][2]int{
		1: {left, top}, 2: {right, top}, 3: {right, bottom}, 4: {left, bottom},
		5: {left, top}, 6: {right, top}, 7: {right, bottom}, 8: {left, bottom},
	} {
		result, err := asset.Process(withOrientation(raw.Bytes(), orientation, ""))
		if err != nil {
			t.Fatal(err)
		}
		decoded := decode(t, result.Data)
		width, height := decoded.Bounds().Dx(), decoded.Bounds().Dy()
		if orientation >= 5 && (width != 32 || height != 64) {
			t.Errorf("orientation %d: size %dx%d", orientation, width, height)
		}
		for _, across := range []int{left, right} {
			for _, down := range []int{top, bottom} {
				red, _, _, _ := decoded.At(width/4+across*width/2, height/4+down*height/2).RGBA()
				if isRed := red > 0x8000; isRed != (across == want[0] && down == want[1]) {
					t.Errorf("orientation %d: corner (%d, %d) has red %x", orientation, across, down, red)
				}
			}
		}
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
