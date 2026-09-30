// Package media downloads publisher photographs and re-encodes them for local
// serving. Nothing a publisher sends is stored or served verbatim: every image
// is decoded, resampled, flattened onto white and re-encoded as JPEG, so the
// bytes the newspaper serves are produced by this process alone.
package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/home-news/home-news/internal/domain"
	"github.com/home-news/home-news/internal/safehttp"

	// Decoders for the formats publishers actually put in feeds. WebP is not
	// in the standard library; a WebP-only item is stored without a photograph
	// rather than pulling in a third-party decoder. Adding one here is enough
	// to support it.
	_ "image/gif"
	_ "image/png"
)

const (
	// MaxDownloadBytes caps a single download.
	MaxDownloadBytes = 8 << 20
	// MaxSourcePixels rejects decompression bombs before resampling.
	MaxSourcePixels = 40 << 20
	// MaxWidth is the widest stored image. A page column on A4 is under 45mm,
	// and a full-width plate under 185mm, so this is generous at print density.
	MaxWidth = 1400
	// MaxHeight bounds very tall images after the width fit.
	MaxHeight = 1600
	// MinWidth and MinHeight reject logos, avatars and tracking beacons.
	MinWidth  = 300
	MinHeight = 180
	// Quality is the stored JPEG quality.
	Quality = 82
)

// ErrTooSmall reports an image that is not worth printing.
var ErrTooSmall = errors.New("image is smaller than the printable minimum")

// Fetcher downloads images over an SSRF-guarded client.
type Fetcher struct{ client *http.Client }

// NewFetcher returns a Fetcher bound by timeout per request.
func NewFetcher(timeout time.Duration) *Fetcher {
	return &Fetcher{client: safehttp.NewClient(timeout)}
}

// Fetch downloads rawURL and returns it re-encoded for storage.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (domain.StoryImage, error) {
	var out domain.StoryImage
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return out, err
	}
	if err := safehttp.ValidatePublicURL(u); err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("User-Agent", "HomeNews/1.0 (+self-hosted RSS reader)")
	req.Header.Set("Accept", "image/jpeg,image/png,image/gif;q=0.8,*/*;q=0.1")
	resp, err := f.client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("image returned HTTP %d", resp.StatusCode)
	}
	if mime := resp.Header.Get("Content-Type"); mime != "" && !strings.HasPrefix(strings.ToLower(mime), "image/") {
		return out, fmt.Errorf("image URL served %q", mime)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxDownloadBytes+1))
	if err != nil {
		return out, err
	}
	if len(raw) > MaxDownloadBytes {
		return out, errors.New("image exceeds the download size limit")
	}
	out, err = Prepare(raw)
	if err != nil {
		return out, err
	}
	out.SourceURL = u.String()
	out.FetchedAt = time.Now().UTC()
	return out, nil
}

// Prepare decodes raw image bytes, resamples them to printable dimensions and
// returns a JPEG. It is separate from Fetch so it can be tested without a
// network, and it is the only path by which bytes reach storage.
func Prepare(raw []byte) (domain.StoryImage, error) {
	var out domain.StoryImage
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return out, fmt.Errorf("unsupported image format: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return out, errors.New("image has no dimensions")
	}
	if config.Width*config.Height > MaxSourcePixels {
		return out, errors.New("image exceeds the decoded pixel limit")
	}
	if config.Width < MinWidth || config.Height < MinHeight {
		return out, ErrTooSmall
	}
	source, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return out, fmt.Errorf("decode image: %w", err)
	}
	width, height := fit(source.Bounds().Dx(), source.Bounds().Dy())
	resampled := resample(source, width, height)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, resampled, &jpeg.Options{Quality: Quality}); err != nil {
		return out, err
	}
	return domain.StoryImage{ContentType: "image/jpeg", Width: width, Height: height, Bytes: encoded.Bytes()}, nil
}

// fit scales width and height down to the stored maximums, preserving aspect.
func fit(width, height int) (int, int) {
	if width > MaxWidth {
		height = max(1, height*MaxWidth/width)
		width = MaxWidth
	}
	if height > MaxHeight {
		width = max(1, width*MaxHeight/height)
		height = MaxHeight
	}
	return width, height
}

// resample box-averages the source into a width x height image and flattens any
// transparency onto white, which is what a printed page assumes.
func resample(source image.Image, width, height int) *image.RGBA {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		top := bounds.Min.Y + y*sourceHeight/height
		bottom := bounds.Min.Y + (y+1)*sourceHeight/height
		if bottom <= top {
			bottom = top + 1
		}
		for x := 0; x < width; x++ {
			left := bounds.Min.X + x*sourceWidth/width
			right := bounds.Min.X + (x+1)*sourceWidth/width
			if right <= left {
				right = left + 1
			}
			var sumR, sumG, sumB, sumA, count uint64
			for sy := top; sy < bottom; sy++ {
				for sx := left; sx < right; sx++ {
					r, g, b, a := source.At(sx, sy).RGBA()
					sumR += uint64(r)
					sumG += uint64(g)
					sumB += uint64(b)
					sumA += uint64(a)
					count++
				}
			}
			// RGBA() is alpha-premultiplied, so averaging is linear in the
			// premultiplied channels; compositing over white then adds the
			// uncovered remainder back in.
			alpha := sumA / count
			gap := uint64(0xffff) - alpha
			offset := dst.PixOffset(x, y)
			dst.Pix[offset+0] = uint8((sumR/count + gap) >> 8)
			dst.Pix[offset+1] = uint8((sumG/count + gap) >> 8)
			dst.Pix[offset+2] = uint8((sumB/count + gap) >> 8)
			dst.Pix[offset+3] = 0xff
		}
	}
	return dst
}
