package news

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/home-news/home-news/internal/config"
	"github.com/home-news/home-news/internal/domain"
	"github.com/home-news/home-news/internal/llm"
	"github.com/home-news/home-news/internal/store"
)

func TestEditionEventsPersistAndGainNews(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "news.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var calls atomic.Int32
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "PRIVATE") {
			t.Error("personal events were sent to Ollama")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": `{"lead_headline":"Today's news","lead":{"text":"A new discovery.","story_ids":["story-1"]},"sections":[]}`}})
	}))
	defer modelServer.Close()
	// Pick a location whose calendar date differs from UTC right now.
	offset := -12 * 3600
	if time.Now().UTC().Hour() >= 12 {
		offset = 14 * 3600
	}
	location := time.FixedZone("edition timezone", offset)
	day := time.Now().In(location)
	date := day.Format(time.DateOnly)
	cfg := config.Config{Location: location, Source: domain.SourceConfig{
		Feeds: []domain.Feed{{ID: "source", Name: "Source"}},
		Events: []domain.Event{
			{Name: "PRIVATE birthday", Type: "birthday", Date: fmt.Sprintf("%04d-%s", day.Year()-30, day.Format("01-02"))},
			{Name: "PRIVATE party", Date: date, Repeat: "once", Description: "Bring a cake."},
			{Name: "Tomorrow", Date: day.AddDate(0, 0, 1).Format(time.DateOnly), Repeat: "once"},
		},
	}}
	model := llm.New(modelServer.URL, "test-model", time.Second)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := New(cfg, db, model, log)
	if err := service.GenerateEdition(ctx, false); err != nil {
		t.Fatal(err)
	}
	ed, err := db.GetEdition(ctx, date)
	if err != nil {
		t.Fatal(err)
	}
	if ed.Status != "ready" || len(ed.Document.Events) != 2 || ed.Document.Events[0].Milestone != "Turns 30 today" || ed.Document.Events[1].Description != "Bring a cake." {
		t.Fatalf("unexpected events edition: %+v", ed)
	}
	if calls.Load() != 0 {
		t.Fatal("events-only edition called Ollama")
	}
	generatedAt := ed.GeneratedAt
	changedConfig := cfg
	changedConfig.Source.Events = nil
	if err := New(changedConfig, db, model, log).GenerateEdition(ctx, false); err != nil {
		t.Fatal(err)
	}
	ed, err = db.GetEdition(ctx, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(ed.Document.Events) != 2 || !ed.GeneratedAt.Equal(generatedAt) {
		t.Fatal("poll changed a published events-only edition")
	}

	if err := db.SyncFeeds(ctx, cfg.Source.Feeds); err != nil {
		t.Fatal(err)
	}
	story := domain.Story{ID: "story-1", FeedID: "source", URL: "https://example.com/story", Title: "Discovery", PublishedAt: day, SourceText: "A new discovery."}
	if _, _, err := db.UpsertStory(ctx, story); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSummary(ctx, story.ID, "test-model", llm.PromptVersion, domain.StorySummary{Headline: story.Title, Summary: "A new discovery."}); err != nil {
		t.Fatal(err)
	}
	if err := service.GenerateEdition(ctx, false); err != nil {
		t.Fatal(err)
	}
	ed, err = db.GetEdition(ctx, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(ed.Stories) != 1 || len(ed.Document.Events) != 2 || ed.Document.LeadHeadline != "Today's news" || calls.Load() != 1 {
		t.Fatalf("news was not added to events edition: %+v, model calls %d", ed, calls.Load())
	}

	// New configuration must not alter published notices until forced regeneration.
	cfg.Source.Events = []domain.Event{{Name: "Replacement", Date: date, Repeat: "once"}}
	changedService := New(cfg, db, model, log)
	if err := changedService.GenerateEdition(ctx, false); err != nil {
		t.Fatal(err)
	}
	ed, err = changedService.Edition(ctx, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(ed.Document.Events) != 2 || ed.Document.Events[0].Name != "PRIVATE birthday" || calls.Load() != 1 {
		t.Fatal("published events changed with configuration")
	}
	if err := changedService.GenerateEdition(ctx, true); err != nil {
		t.Fatal(err)
	}
	ed, err = db.GetEdition(ctx, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(ed.Document.Events) != 1 || ed.Document.Events[0].Name != "Replacement" || calls.Load() != 2 {
		t.Fatal("forced generation did not use updated events")
	}

	cfg.Source.Events = nil
	if err := New(cfg, db, model, log).GenerateEdition(ctx, true); err != nil {
		t.Fatal(err)
	}
	ed, err = db.GetEdition(ctx, date)
	if err != nil {
		t.Fatal(err)
	}
	if len(ed.Document.Events) != 0 || len(ed.Stories) != 1 {
		t.Fatal("edition without events regressed")
	}
}
