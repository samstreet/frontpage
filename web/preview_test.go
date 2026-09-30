package web

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/home-news/home-news/internal/config"
	"github.com/home-news/home-news/internal/domain"
	"github.com/home-news/home-news/internal/llm"
	"github.com/home-news/home-news/internal/media"
	"github.com/home-news/home-news/internal/news"
	"github.com/home-news/home-news/internal/store"
)

// TestWritePreview renders a fully populated edition to a directory so the page
// layout can be inspected in a browser and printed to PDF. It is a development
// aid, skipped unless HOME_NEWS_PREVIEW_DIR names an output directory:
//
//	HOME_NEWS_PREVIEW_DIR=/tmp/preview go test ./web -run TestWritePreview
//
// Serve the directory over HTTP (the page uses absolute /static and /media
// paths) and open it.
func TestWritePreview(t *testing.T) {
	outDir := os.Getenv("HOME_NEWS_PREVIEW_DIR")
	if outDir == "" {
		t.Skip("set HOME_NEWS_PREVIEW_DIR to write a layout preview")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "news.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{Location: time.UTC, Source: domain.SourceConfig{
		Interests: []domain.Interest{
			{ID: "world", Name: "World"},
			{ID: "science", Name: "Science & technology"},
			{ID: "culture", Name: "Culture"},
		},
	}}
	service := news.New(cfg, db, llm.New("http://localhost:11434", "test", time.Second), log)
	server, err := New(service, log)
	if err != nil {
		t.Fatal(err)
	}

	feeds := []domain.Feed{
		{ID: "chronicle", Name: "The Chronicle"},
		{ID: "gazette", Name: "Evening Gazette"},
		{ID: "observer", Name: "Science Observer"},
	}
	if err := db.SyncFeeds(ctx, feeds); err != nil {
		t.Fatal(err)
	}

	type seed struct {
		id, feed, headline, summary, why, topic string
		photo                                   bool
		wide                                    bool
	}
	seeds := []seed{
		{"s01", "chronicle", "Harbour wall reopens after eighteen months of repair", "The eastern harbour wall reopened on Friday morning, ending an eighteen-month closure that had forced the fishing fleet to berth two miles along the coast. Engineers replaced four hundred metres of facing stone and rebuilt the pumping station beneath the quay.", "The fleet regains its home berth before the autumn season, and the coastal path reopens along its full length for the first time since spring last year.", "world", true, true},
		{"s02", "gazette", "Night buses to run seven days a week from October", "The transport authority confirmed that the four night routes will run every night from the first of October, rather than at weekends only. The change follows a trial that recorded higher weekday use than forecast.", "", "world", true, false},
		{"s03", "observer", "Seabed survey finds cold-water reef in unexpectedly shallow water", "A survey team mapping the outer bank has recorded a cold-water coral reef at eighty metres, far shallower than the depths at which the species is usually found. The reef covers roughly nine hectares and appears to be in good condition.", "A reef this shallow is far easier to monitor, and it sits inside waters already closed to bottom trawling.", "science", true, false},
		{"s04", "observer", "Weather model rewritten to run on a single machine", "Researchers have rebuilt a regional forecasting model so that it runs overnight on one workstation rather than on a shared cluster. Accuracy over a six-month comparison was within a tenth of a degree of the original.", "Smaller institutions can now run their own regional forecasts without buying cluster time.", "science", false, false},
		{"s05", "chronicle", "Library lending returns to pre-closure levels", "Borrowing across the county's fourteen branches has returned to the level recorded before the closures, with children's lending up by a fifth on the same period last year.", "", "culture", true, false},
		{"s06", "gazette", "Restored organ to be heard again at the winter concerts", "The parish organ, silent since a water leak damaged its bellows, has been restored by a workshop in the next county and will be played at the winter concert series in December.", "", "culture", false, false},
		{"s07", "gazette", "Allotment waiting list falls for the first time in a decade", "Forty new plots at the western site have cut the waiting list from three hundred names to two hundred and twelve, the first fall recorded since the list was opened.", "", "", true, false},
		{"s08", "chronicle", "Bridge inspection brings a weekend of diversions", "The river bridge will close for inspection over two weekends this month, with traffic diverted through the upper crossing. The inspection is routine and runs on a six-year cycle.", "", "", false, false},
		{"s09", "observer", "Rain radar gains a second dish on the northern ridge", "A second radar dish on the northern ridge has closed a long-standing gap in coverage over the upper valley, where rainfall had previously been estimated rather than measured.", "Flood warnings for the upper valley can now be issued from measurements rather than inference.", "", true, false},
	}

	published := time.Date(2026, 5, 14, 7, 30, 0, 0, time.UTC)
	var stories []domain.Story
	for i, s := range seeds {
		st := domain.Story{
			ID: s.id, FeedID: s.feed, URL: "https://example.com/" + s.id,
			Title: s.headline, PublishedAt: published.Add(-time.Duration(i) * 37 * time.Minute),
			SourceText: s.summary,
		}
		if s.topic != "" {
			st.Topics = []string{s.topic}
		}
		if s.photo {
			st.ImageURL = "https://example.com/" + s.id + ".jpg"
		}
		if _, _, err := db.UpsertStory(ctx, st); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveSummary(ctx, s.id, "preview", llm.PromptVersion, domain.StorySummary{
			Headline: s.headline, Summary: s.summary, WhyMatters: s.why, Topics: st.Topics,
		}); err != nil {
			t.Fatal(err)
		}
		if s.photo {
			width, height := 1200, 800
			if s.wide {
				width, height = 1400, 600
			}
			prepared, err := media.Prepare(syntheticJPEG(t, width, height, i))
			if err != nil {
				t.Fatal(err)
			}
			prepared.StoryID = s.id
			prepared.SourceURL = st.ImageURL
			prepared.FetchedAt = published
			if err := db.SaveStoryImage(ctx, prepared); err != nil {
				t.Fatal(err)
			}
		}
		loaded, err := db.GetStory(ctx, s.id)
		if err != nil {
			t.Fatal(err)
		}
		stories = append(stories, loaded)
	}

	doc := domain.EditionDocument{
		LeadHeadline: "Harbour reopens, and the fleet comes home",
		Lead: domain.EditionParagraph{
			Text:     "Eighteen months after the eastern wall was closed for repair, the harbour is working again — and the week's other news runs from a reef found in the shallows to a night bus timetable that finally covers the whole week.",
			StoryIDs: []string{"s01", "s02"},
		},
		Sections: []domain.EditionSection{
			{Topic: "world", Title: "World", StoryIDs: []string{"s01", "s02"}, Paragraphs: []domain.EditionParagraph{
				{Text: "Infrastructure dominated the week close to home. The harbour reopening drew the crowds, but the quieter decision on night buses will touch more people over a year: four routes, every night, from October."},
				{Text: "Both changes were signed off in the same council session, which also set aside money for the bridge inspection programme running this month."},
			}},
			{Topic: "science", Title: "Science & technology", StoryIDs: []string{"s03", "s04", "s09"}, Paragraphs: []domain.EditionParagraph{
				{Text: "Two results this week came from doing more with less: a forecasting model rebuilt to run on a single workstation, and a radar dish that replaces an estimate with a measurement."},
			}},
			{Topic: "culture", Title: "Culture", StoryIDs: []string{"s05", "s06"}, Paragraphs: []domain.EditionParagraph{
				{Text: "Lending figures and a restored organ: the county's cultural recovery is being counted in borrowed books and in bellows repaired three hundred miles away."},
			}},
		},
		Events: []domain.EditionEvent{
			{Name: "Alex's birthday", Type: "birthday", Milestone: "Turns 36 today", Description: "Cake at seven, and the good plates."},
			{Name: "Our wedding anniversary", Type: "anniversary", Milestone: "14 years today"},
			{Name: "Village show", Type: "event", Description: "Marquee opens at ten on the green."},
		},
	}
	if err := db.SaveEdition(ctx, "2026-05-14", "preview", llm.PromptVersion, doc, stories, "ready"); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(outDir, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/archive", "/status"} {
		name := "index.html"
		if path != "/" {
			name = path[1:] + ".html"
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("GET %s status %d: %s", path, w.Code, w.Body.String())
		}
		if err := os.WriteFile(filepath.Join(outDir, name), w.Body.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range seeds {
		if !s.photo {
			continue
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/media/"+s.id, nil))
		if w.Code != 200 {
			t.Fatalf("GET /media/%s status %d", s.id, w.Code)
		}
		if err := os.WriteFile(filepath.Join(outDir, "media", s.id), w.Body.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The waiting state, from a server that has collected nothing yet, so the
	// empty front page can be checked too.
	emptyDB, err := store.Open(ctx, filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer emptyDB.Close()
	emptyServer, err := New(news.New(cfg, emptyDB, llm.New("http://localhost:11434", "test", time.Second), log), log)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	emptyServer.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 {
		t.Fatalf("empty edition status %d", w.Code)
	}
	if err := os.WriteFile(filepath.Join(outDir, "empty.html"), w.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	staticFiles, err := fs.ReadDir(assets, "static")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outDir, "static"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range staticFiles {
		b, err := fs.ReadFile(assets, "static/"+entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outDir, "static", entry.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("preview written to %s", outDir)
}

// syntheticJPEG draws a recognisable test photograph without any network access.
func syntheticJPEG(t *testing.T, width, height, variant int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	base := []color.RGBA{
		{62, 84, 108, 255}, {120, 84, 62, 255}, {70, 96, 74, 255},
		{110, 96, 120, 255}, {130, 112, 70, 255}, {90, 96, 104, 255},
	}[variant%6]
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			fade := float64(y) / float64(height)
			wave := 0.5 + 0.5*math.Sin(float64(x)/float64(width)*math.Pi*float64(2+variant))
			shade := func(c uint8, k float64) uint8 {
				v := float64(c)*(0.55+0.5*fade) + 70*k*(1-fade)
				return uint8(math.Max(0, math.Min(255, v)))
			}
			img.Set(x, y, color.RGBA{shade(base.R, wave), shade(base.G, wave), shade(base.B, 1-wave), 255})
		}
	}
	// A horizon line, so cropping is obvious at a glance.
	for x := 0; x < width; x++ {
		for y := height/2 - 2; y < height/2+2; y++ {
			img.Set(x, y, color.RGBA{240, 236, 228, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
