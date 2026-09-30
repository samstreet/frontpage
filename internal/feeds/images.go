package feeds

import (
	"net/url"
	"path"
	"strings"

	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"
	"golang.org/x/net/html"
)

// pixelNames are filenames publishers use for tracking beacons and spacers.
var pixelNames = map[string]bool{"pixel": true, "spacer": true, "blank": true, "clear": true, "dot": true, "beacon": true, "1x1": true, "transparent": true}

// undecodableExtensions are formats the media package cannot turn into a
// printable plate. An unknown extension is allowed through, because many CDNs
// serve images from extensionless paths; the decoder is the real gate.
var undecodableExtensions = map[string]bool{".svg": true, ".svgz": true, ".ico": true, ".bmp": true, ".tif": true, ".tiff": true, ".avif": true, ".heic": true, ".heif": true, ".mp4": true, ".webm": true, ".pdf": true}

// ItemImageURL picks the most likely lead photograph for a feed item, checking
// the dedicated image element, image enclosures, Media RSS, then any image
// embedded in the item's HTML. It returns "" when the item has no usable image.
func ItemImageURL(item *gofeed.Item) string {
	if item == nil {
		return ""
	}
	var candidates []string
	if item.Image != nil {
		candidates = append(candidates, item.Image.URL)
	}
	for _, enclosure := range item.Enclosures {
		if enclosure != nil && strings.HasPrefix(strings.ToLower(enclosure.Type), "image/") {
			candidates = append(candidates, enclosure.URL)
		}
	}
	candidates = append(candidates, mediaImageURLs(item.Extensions["media"])...)
	candidates = append(candidates, htmlImageURLs(item.Content)...)
	candidates = append(candidates, htmlImageURLs(item.Description)...)
	for _, candidate := range candidates {
		if picked := normalizeImageURL(candidate); picked != "" {
			return picked
		}
	}
	return ""
}

// mediaImageURLs walks a Media RSS namespace, including media:group children,
// collecting content and thumbnail URLs that describe an image.
func mediaImageURLs(namespace map[string][]ext.Extension) []string {
	var out []string
	var walk func(map[string][]ext.Extension)
	walk = func(node map[string][]ext.Extension) {
		for name, list := range node {
			for _, element := range list {
				if name == "content" || name == "thumbnail" {
					mime := strings.ToLower(element.Attrs["type"])
					medium := strings.ToLower(element.Attrs["medium"])
					imageMIME := mime == "" || strings.HasPrefix(mime, "image/")
					imageMedium := medium == "" || medium == "image"
					if imageMIME && imageMedium {
						out = append(out, element.Attrs["url"])
					}
				}
				if len(element.Children) > 0 {
					walk(element.Children)
				}
			}
		}
	}
	walk(namespace)
	return out
}

// htmlImageURLs returns the sources of every <img> in a fragment of item HTML,
// falling back to the first srcset candidate when there is no src.
func htmlImageURLs(fragment string) []string {
	if !strings.Contains(fragment, "<img") {
		return nil
	}
	doc, err := html.Parse(strings.NewReader(fragment))
	if err != nil {
		return nil
	}
	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			src, srcset := "", ""
			for _, attr := range n.Attr {
				switch attr.Key {
				case "src":
					src = attr.Val
				case "srcset":
					srcset = attr.Val
				}
			}
			if src == "" {
				src = firstSrcsetURL(srcset)
			}
			if src != "" {
				out = append(out, src)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

func firstSrcsetURL(srcset string) string {
	for _, candidate := range strings.Split(srcset, ",") {
		fields := strings.Fields(candidate)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// normalizeImageURL returns an absolute http(s) image URL with the fragment
// removed, or "" if the candidate is unusable or looks like a tracking pixel.
func normalizeImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	u.Fragment = ""
	name := path.Base(u.Path)
	extension := strings.ToLower(path.Ext(name))
	if undecodableExtensions[extension] {
		return ""
	}
	if pixelNames[strings.ToLower(strings.TrimSuffix(name, extension))] {
		return ""
	}
	query := u.Query()
	if query.Get("w") == "1" || query.Get("width") == "1" || query.Get("h") == "1" {
		return ""
	}
	return u.String()
}
