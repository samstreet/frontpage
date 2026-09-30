package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func solidJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPrepareResizesToPrintableWidth(t *testing.T) {
	out, err := Prepare(solidJPEG(t, 3000, 2000))
	if err != nil {
		t.Fatal(err)
	}
	if out.Width != MaxWidth {
		t.Fatalf("width = %d, want %d", out.Width, MaxWidth)
	}
	if want := 2000 * MaxWidth / 3000; out.Height != want {
		t.Fatalf("height = %d, want %d (aspect ratio not preserved)", out.Height, want)
	}
	if out.ContentType != "image/jpeg" {
		t.Fatalf("content type = %q", out.ContentType)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(out.Bytes))
	if err != nil {
		t.Fatalf("stored bytes do not decode: %v", err)
	}
	if format != "jpeg" || config.Width != out.Width || config.Height != out.Height {
		t.Fatalf("stored %s %dx%d, recorded %dx%d", format, config.Width, config.Height, out.Width, out.Height)
	}
}

func TestPrepareKeepsSmallEnoughImages(t *testing.T) {
	out, err := Prepare(solidJPEG(t, 640, 480))
	if err != nil {
		t.Fatal(err)
	}
	if out.Width != 640 || out.Height != 480 {
		t.Fatalf("got %dx%d, want 640x480", out.Width, out.Height)
	}
}

func TestPrepareRejectsUnprintableImages(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bytes []byte
	}{
		{"logo sized", solidJPEG(t, 80, 60)},
		{"short but wide", solidJPEG(t, 900, 40)},
		{"not an image", []byte("<html>not an image at all</html>")},
		{"empty", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Prepare(tc.bytes); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// A transparent PNG must come back as opaque JPEG composited on white, because
// a printed page has no transparency to fall back on.
func TestPrepareFlattensTransparencyOntoWhite(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{0, 0, 0, 0}) // fully transparent
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	out, err := Prepare(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(out.Bytes))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, a := decoded.At(10, 10).RGBA()
	if a != 0xffff {
		t.Fatalf("alpha = %d, want opaque", a)
	}
	// JPEG is lossy, so allow a little drift from pure white.
	for _, channel := range []uint32{r, g, b} {
		if channel < 0xf000 {
			t.Fatalf("transparent pixel came out as %d,%d,%d; expected near white", r, g, b)
		}
	}
}
