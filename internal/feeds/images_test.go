package feeds

import (
	"testing"

	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"
)

func TestItemImageURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		item *gofeed.Item
		want string
	}{
		{"no image", &gofeed.Item{Description: "<p>Just words.</p>"}, ""},
		{
			"item image wins",
			&gofeed.Item{
				Image:       &gofeed.Image{URL: "https://cdn.example.com/lead.jpg"},
				Description: `<img src="https://cdn.example.com/inline.jpg">`,
			},
			"https://cdn.example.com/lead.jpg",
		},
		{
			"image enclosure",
			&gofeed.Item{Enclosures: []*gofeed.Enclosure{
				{URL: "https://cdn.example.com/audio.mp3", Type: "audio/mpeg"},
				{URL: "https://cdn.example.com/photo.jpg", Type: "image/jpeg"},
			}},
			"https://cdn.example.com/photo.jpg",
		},
		{
			"media rss content",
			&gofeed.Item{Extensions: ext.Extensions{"media": {
				"content": {{Attrs: map[string]string{"url": "https://cdn.example.com/media.jpg", "medium": "image"}}},
			}}},
			"https://cdn.example.com/media.jpg",
		},
		{
			"media rss group children",
			&gofeed.Item{Extensions: ext.Extensions{"media": {
				"group": {{Children: map[string][]ext.Extension{
					"thumbnail": {{Attrs: map[string]string{"url": "https://cdn.example.com/thumb.jpg"}}},
				}}},
			}}},
			"https://cdn.example.com/thumb.jpg",
		},
		{
			"media rss video is not a photograph",
			&gofeed.Item{Extensions: ext.Extensions{"media": {
				"content": {{Attrs: map[string]string{"url": "https://cdn.example.com/clip.mp4", "medium": "video"}}},
			}}},
			"",
		},
		{
			"inline html",
			&gofeed.Item{Content: `<p>Text</p><figure><img src="https://cdn.example.com/body.png" alt=""></figure>`},
			"https://cdn.example.com/body.png",
		},
		{
			"srcset when there is no src",
			&gofeed.Item{Content: `<img srcset="https://cdn.example.com/w800.jpg 800w, https://cdn.example.com/w400.jpg 400w">`},
			"https://cdn.example.com/w800.jpg",
		},
		{
			"tracking pixel skipped for the real photograph",
			&gofeed.Item{Content: `<img src="https://stats.example.com/pixel.gif"><img src="https://cdn.example.com/real.jpg">`},
			"https://cdn.example.com/real.jpg",
		},
		{
			"one-pixel query skipped",
			&gofeed.Item{Content: `<img src="https://stats.example.com/t.gif?w=1"><img src="https://cdn.example.com/real.jpg">`},
			"https://cdn.example.com/real.jpg",
		},
		{
			"undecodable formats skipped",
			&gofeed.Item{Content: `<img src="https://cdn.example.com/logo.svg"><img src="https://cdn.example.com/real.jpg">`},
			"https://cdn.example.com/real.jpg",
		},
		{
			"data and relative URLs are not usable",
			&gofeed.Item{Content: `<img src="data:image/gif;base64,R0lGOD"><img src="/local/photo.jpg">`},
			"",
		},
		{
			"fragment removed",
			&gofeed.Item{Image: &gofeed.Image{URL: "https://cdn.example.com/photo.jpg#top"}},
			"https://cdn.example.com/photo.jpg",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ItemImageURL(tc.item); got != tc.want {
				t.Fatalf("ItemImageURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestItemImageURLHandlesNil(t *testing.T) {
	if got := ItemImageURL(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
