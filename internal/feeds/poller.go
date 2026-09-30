package feeds

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/home-news/home-news/internal/domain"
	"github.com/home-news/home-news/internal/safehttp"
	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
)

const maxFeedBytes = 4 << 20

var trackingKeys = map[string]bool{"fbclid": true, "gclid": true, "mc_cid": true, "mc_eid": true, "ref": true, "ref_src": true, "token": true, "access_token": true, "auth": true, "api_key": true, "apikey": true, "password": true, "signature": true, "sig": true, "x-amz-signature": true}
var whitespace = regexp.MustCompile(`\s+`)

type Poller struct {
	client   *http.Client
	maxText  int
	maxItems int
}

type Result struct {
	Items              []domain.FeedItem
	ETag, LastModified string
	NotModified        bool
}

func New(timeout time.Duration, maxText, maxItems int) *Poller {
	return &Poller{client: safehttp.NewClient(timeout), maxText: maxText, maxItems: maxItems}
}
func (p *Poller) Poll(ctx context.Context, feed domain.Feed, etag, lastModified string) (Result, error) {
	u, err := url.Parse(feed.URL)
	if err != nil {
		return Result{}, err
	}
	if err = safehttp.ValidatePublicURL(u); err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "HomeNews/1.0 (+self-hosted RSS reader)")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	result := Result{ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}
	if resp.StatusCode == http.StatusNotModified {
		result.NotModified = true
		return result, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, fmt.Errorf("feed returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(b) > maxFeedBytes {
		return Result{}, errors.New("feed response exceeds size limit")
	}
	f, err := gofeed.NewParser().ParseString(string(b))
	if err != nil {
		return Result{}, err
	}
	items := make([]domain.FeedItem, 0, min(len(f.Items), p.maxItems))
	for _, item := range f.Items {
		if len(items) >= p.maxItems {
			break
		}
		title := strings.TrimSpace(item.Title)
		link := strings.TrimSpace(item.Link)
		if title == "" {
			continue
		}
		published := time.Now().UTC()
		if item.PublishedParsed != nil {
			published = item.PublishedParsed.UTC()
		} else if item.UpdatedParsed != nil {
			published = item.UpdatedParsed.UTC()
		}
		content := item.Content
		if len(strings.TrimSpace(content)) < len(strings.TrimSpace(item.Description)) {
			content = item.Description
		}
		text := PlainText(content)
		truncated := false
		if len(text) > p.maxText {
			text = truncateUTF8(text, p.maxText)
			truncated = true
		}
		author := ""
		if item.Author != nil {
			author = item.Author.Name
		}
		items = append(items, domain.FeedItem{URL: NormalizeURL(link), Title: title, Author: author, PublishedAt: published, Text: text, Truncated: truncated, ImageURL: ItemImageURL(item)})
	}
	result.Items = items
	return result, nil
}

func truncateUTF8(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	for maxBytes > 0 && !utf8.RuneStart(text[maxBytes]) {
		maxBytes--
	}
	return text[:maxBytes]
}

func NormalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		kl := strings.ToLower(k)
		if strings.HasPrefix(kl, "utm_") || trackingKeys[kl] {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	u.Host = strings.ToLower(u.Host)
	return u.String()
}
func StableID(feedID, link, title string, published time.Time) string {
	key := link
	if key == "" {
		key = feedID + "|" + strings.ToLower(strings.TrimSpace(title))
	}
	sum := sha256.Sum256([]byte(feedID + "|" + key))
	return hex.EncodeToString(sum[:16])
}
func PlainText(raw string) string {
	if raw == "" {
		return ""
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return clean(raw)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript" || n.Data == "svg") {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		} else if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "br" || n.Data == "li" || n.Data == "div") {
			b.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && (n.Data == "p" || n.Data == "li") {
			b.WriteByte('\n')
		}
	}
	walk(doc)
	return clean(b.String())
}
func clean(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(whitespace.ReplaceAllString(lines[i], " "))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func SortItems(items []domain.FeedItem) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].PublishedAt.After(items[j].PublishedAt) })
}
