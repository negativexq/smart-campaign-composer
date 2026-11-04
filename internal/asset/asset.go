// Package asset checks uploaded images and keeps their files.
package asset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

const (
	// MaxBytes is the largest upload accepted.
	MaxBytes = 5 << 20
	// MaxSide is the longest side accepted, checked before the image is decoded.
	MaxSide = 6000
	// MaxPixels bounds the memory one upload takes once its pixels are read. A file of
	// one colour is tiny on the way in and still costs four bytes a pixel.
	MaxPixels = 24_000_000
	// MaxWidth is twice the email column, which stays sharp on high-density screens.
	MaxWidth    = 1200
	jpegQuality = 85
)

// ErrNotAnImage means the bytes are not a PNG or JPEG, whatever the file was called.
var ErrNotAnImage = errors.New("not a PNG or JPEG image")

// ErrTooManyPixels means the image is larger than MaxSide.
var ErrTooManyPixels = errors.New("image dimensions too large")

// Image is an upload drawn again from its pixels, so nothing else in the file survives:
// no metadata, and no script hidden in a file that only claims to be an image.
type Image struct {
	MIME          string
	Extension     string
	Width, Height int
	Hash          string
	Data          []byte
}

// FileName is where the image is kept. Equal images share one file.
func (i Image) FileName() string { return i.Hash + i.Extension }

// Process checks an upload by its content and encodes it again.
func Process(raw []byte) (Image, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") {
		return Image{}, ErrNotAnImage
	}
	if config.Width > MaxSide || config.Height > MaxSide || config.Width*config.Height > MaxPixels {
		return Image{}, ErrTooManyPixels
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Image{}, ErrNotAnImage
	}
	// The tag saying which way up a photo goes is about to be dropped.
	turn := 0
	if format == "jpeg" {
		turn = orientation(raw)
	}
	// Narrowing first leaves far fewer pixels to turn.
	decoded = upright(narrowed(decoded, turn), turn)
	var out bytes.Buffer
	result := Image{MIME: "image/png", Extension: ".png"}
	if format == "jpeg" {
		result = Image{MIME: "image/jpeg", Extension: ".jpg"}
		err = jpeg.Encode(&out, decoded, &jpeg.Options{Quality: jpegQuality})
	} else {
		err = png.Encode(&out, decoded)
	}
	if err != nil {
		return Image{}, fmt.Errorf("encode image: %w", err)
	}
	sum := sha256.Sum256(out.Bytes())
	result.Width, result.Height = decoded.Bounds().Dx(), decoded.Bounds().Dy()
	result.Hash, result.Data = hex.EncodeToString(sum[:]), out.Bytes()
	return result, nil
}

// orientation reads the EXIF orientation of a JPEG, or 1 when it has none.
func orientation(raw []byte) int {
	const marker, app1, startOfScan = 0xFF, 0xE1, 0xDA
	for i := 2; i+4 <= len(raw) && raw[i] == marker && raw[i+1] != startOfScan; {
		size := int(binary.BigEndian.Uint16(raw[i+2:]))
		end := i + 2 + size
		if end > len(raw) || size < 2 {
			return 1
		}
		if raw[i+1] == app1 {
			if value := exifOrientation(raw[i+4 : end]); value != 0 {
				return value
			}
		}
		i = end
	}
	return 1
}

func exifOrientation(segment []byte) int {
	const header, entrySize, orientationTag = 6, 12, 0x0112
	if len(segment) < header+8 || string(segment[:header]) != "Exif\x00\x00" {
		return 0
	}
	tiff := segment[header:]
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	start := int64(order.Uint32(tiff[4:]))
	if start+2 > int64(len(tiff)) {
		return 0
	}
	count := int64(order.Uint16(tiff[start:]))
	for n := range count {
		at := start + 2 + n*entrySize
		if at+entrySize > int64(len(tiff)) {
			return 0
		}
		if order.Uint16(tiff[at:]) == orientationTag {
			if value := int(order.Uint16(tiff[at+8:])); value >= 1 && value <= 8 {
				return value
			}
			return 0
		}
	}
	return 0
}

// narrowed brings an image down to MaxWidth, measured the way up it will be shown.
func narrowed(source image.Image, orientation int) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	shown := width
	if orientation >= 5 {
		shown = height
	}
	if shown <= MaxWidth {
		return source
	}
	out := image.NewRGBA(image.Rect(0, 0, max(1, width*MaxWidth/shown), max(1, height*MaxWidth/shown)))
	draw.CatmullRom.Scale(out, out.Bounds(), source, bounds, draw.Src, nil)
	return out
}

// upright turns an image the way its EXIF orientation says.
func upright(source image.Image, orientation int) image.Image {
	if orientation <= 1 {
		return source
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	// Each case says where a source pixel (x, y) lands.
	place := map[int]func(x, y int) (int, int){
		2: func(x, y int) (int, int) { return width - 1 - x, y },
		3: func(x, y int) (int, int) { return width - 1 - x, height - 1 - y },
		4: func(x, y int) (int, int) { return x, height - 1 - y },
		5: func(x, y int) (int, int) { return y, x },
		6: func(x, y int) (int, int) { return height - 1 - y, x },
		7: func(x, y int) (int, int) { return height - 1 - y, width - 1 - x },
		8: func(x, y int) (int, int) { return y, width - 1 - x },
	}[orientation]
	outWidth, outHeight := width, height
	if orientation >= 5 {
		outWidth, outHeight = height, width
	}
	flat, ok := source.(*image.RGBA)
	if !ok {
		flat = image.NewRGBA(image.Rect(0, 0, width, height))
		draw.Draw(flat, flat.Bounds(), source, bounds.Min, draw.Src)
	}
	start := flat.Bounds().Min
	out := image.NewRGBA(image.Rect(0, 0, outWidth, outHeight))
	for y := range height {
		for x := range width {
			toX, toY := place(x, y)
			copy(out.Pix[out.PixOffset(toX, toY):], flat.Pix[flat.PixOffset(start.X+x, start.Y+y):][:4])
		}
	}
	return out
}

// Store keeps image files. A disk is enough for one machine; object storage can replace
// it behind the same two calls.
type Store interface {
	Put(ctx context.Context, name string, data []byte) error
	Open(ctx context.Context, name string) (io.ReadSeekCloser, error)
	Remove(ctx context.Context, name string) error
}

// DiskStore keeps files in one directory.
type DiskStore struct{ Dir string }

// Put writes the file whole or not at all.
func (s DiskStore) Put(_ context.Context, name string, data []byte) error {
	temporary, err := os.CreateTemp(s.Dir, "upload-*")
	if err != nil {
		return fmt.Errorf("store image: %w", err)
	}
	defer os.Remove(temporary.Name()) //nolint:errcheck // gone after the rename
	if _, err := temporary.Write(data); err != nil {
		temporary.Close() //nolint:errcheck,gosec // the write already failed
		return fmt.Errorf("store image: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("store image: %w", err)
	}
	if err := os.Rename(temporary.Name(), filepath.Join(s.Dir, filepath.Base(name))); err != nil {
		return fmt.Errorf("store image: %w", err)
	}
	return nil
}

// Open reads a stored file.
func (s DiskStore) Open(_ context.Context, name string) (io.ReadSeekCloser, error) {
	file, err := os.Open(filepath.Join(s.Dir, filepath.Base(name)))
	if err != nil {
		return nil, fmt.Errorf("open image: %w", err)
	}
	return file, nil
}

// Remove deletes a stored file. One already gone is fine.
func (s DiskStore) Remove(_ context.Context, name string) error {
	if err := os.Remove(filepath.Join(s.Dir, filepath.Base(name))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove image: %w", err)
	}
	return nil
}
