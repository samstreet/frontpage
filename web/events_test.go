package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/home-news/home-news/internal/config"
	"github.com/home-news/home-news/internal/domain"
	"github.com/home-news/home-news/internal/llm"
	"github.com/home-news/home-news/internal/news"
	"github.com/home-news/home-news/internal/store"
)

func TestEditionOccasionsPages(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "news.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := news.New(config.Config{Location: time.UTC}, db, llm.New("http://localhost:11434", "test", time.Second), log)
	server, err := New(service, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SyncFeeds(ctx, []domain.Feed{{ID: "source", Name: "Source"}}); err != nil {
		t.Fatal(err)
	}
	story := domain.Story{ID: "story", FeedID: "source", URL: "https://example.com/story", Title: "News story", PublishedAt: time.Now(), SourceText: "Some news."}
	if _, _, err := db.UpsertStory(ctx, story); err != nil {
		t.Fatal(err)
	}
	events := []domain.EditionEvent{{Name: "Alex <script>alert(1)</script>", Type: "birthday", Milestone: "Turns 36 today", Description: "Hello <b>everyone</b>"}}
	for _, tc := range []struct {
		name    string
		events  []domain.EditionEvent
		stories []domain.Story
	}{
		{"with news", events, []domain.Story{story}},
		{"events only", events, nil},
		{"legacy without events", nil, []domain.Story{story}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := domain.EditionDocument{LeadHeadline: "Daily edition", Events: tc.events}
			if err := db.SaveEdition(ctx, "2026-05-14", "test", llm.PromptVersion, doc, tc.stories, "ready"); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/", "/edition/2026-05-14"} {
				w := httptest.NewRecorder()
				server.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				if w.Code != 200 {
					t.Fatalf("GET %s status %d: %s", path, w.Code, w.Body.String())
				}
				body := w.Body.String()
				if strings.Contains(body, "Occasions &amp; celebrations") != (len(tc.events) > 0) {
					t.Fatalf("GET %s: incorrect occasions visibility", path)
				}
				if strings.Contains(body, "Today's briefing") != (len(tc.stories) > 0) {
					t.Fatalf("GET %s: incorrect news visibility", path)
				}
				if len(tc.events) > 0 {
					for _, want := range []string{"Turns 36 today", "Alex &lt;script&gt;alert(1)&lt;/script&gt;", "Hello &lt;b&gt;everyone&lt;/b&gt;"} {
						if !strings.Contains(body, want) {
							t.Errorf("GET %s: missing %q", path, want)
						}
					}
					if strings.Contains(body, "<script>alert(1)</script>") {
						t.Fatal("event HTML was not escaped")
					}
				}
			}
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/editions/2026-05-14", nil))
			if w.Code != 200 {
				t.Fatalf("edition API status %d", w.Code)
			}
			var ed domain.Edition
			if err := json.Unmarshal(w.Body.Bytes(), &ed); err != nil {
				t.Fatal(err)
			}
			if len(ed.Document.Events) != len(tc.events) {
				t.Fatal("edition API lost events")
			}
		})
	}
}
