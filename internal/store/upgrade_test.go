package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/home-news/home-news/internal/domain"
)

// writeLegacyDatabase builds a database at the original schema and records it
// as such, exactly as a deployment predating photographs would have on disk.
func writeLegacyDatabase(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	initial, err := migrationFS.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{`CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`}
	statements = append(statements, strings.Split(string(initial), ";")...)
	statements = append(statements,
		`INSERT INTO schema_migrations(version,applied_at) VALUES(1,'2026-01-01T00:00:00.000000000Z')`,
		`INSERT INTO feeds(id,name,url,enabled) VALUES('feed','Feed','https://example.com/feed',1)`,
		`INSERT INTO stories(id,feed_id,url,title,published_at,first_seen_at,source_text,topics,content_hash) VALUES('old','feed','https://example.com/old','An older story','2026-01-02T00:00:00.000000000Z','2026-01-02T00:00:00.000000000Z','Filed before photographs existed.','[]','hash')`,
		`INSERT INTO summaries(story_id,headline,summary,topics,model,prompt_version,created_at) VALUES('old','An older story','Filed before photographs existed.','[]','m','v','2026-01-02T00:00:00.000000000Z')`,
		`INSERT INTO editions(date,generated_at,status,document) VALUES('2026-01-02','2026-01-02T00:00:00.000000000Z','ready','{"lead_headline":"Older edition"}')`,
		`INSERT INTO edition_stories(edition_date,story_id,topic,rank) VALUES('2026-01-02','old','',0)`,
	)
	for _, statement := range statements {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seeding legacy schema: %v", err)
		}
	}
}

// An existing installation must survive the upgrade that introduced
// photographs: its stories, summaries and editions stay readable, and nothing
// that never had a photograph is queued to download one.
func TestUpgradeFromLegacySchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	writeLegacyDatabase(t, path)

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("migration of an existing database failed: %v", err)
	}
	defer db.Close()

	story, err := db.GetStory(ctx, "old")
	if err != nil {
		t.Fatalf("existing story unreadable after upgrade: %v", err)
	}
	if story.Title != "An older story" || story.Summary == nil {
		t.Fatalf("existing story lost data: %+v", story)
	}
	if story.HasImage || story.ImageURL != "" {
		t.Fatalf("existing story claims a photograph: %+v", story)
	}
	edition, err := db.GetEdition(ctx, "2026-01-02")
	if err != nil || edition.Document.LeadHeadline != "Older edition" {
		t.Fatalf("existing edition unreadable after upgrade: %+v (err %v)", edition, err)
	}
	queued, err := db.StoriesNeedingImages(ctx, 10)
	if err != nil || len(queued) != 0 {
		t.Fatalf("a story that never had a photograph was queued: %+v (err %v)", queued, err)
	}

	// A refreshed item that now advertises a photograph is picked up.
	if _, _, err := db.UpsertStory(ctx, domain.Story{ID: "old", FeedID: "feed", URL: "https://example.com/old", Title: "An older story", PublishedAt: time.Now(), SourceText: "Filed before photographs existed.", ImageURL: "https://cdn.example.com/now.jpg"}); err != nil {
		t.Fatal(err)
	}
	if queued, err = db.StoriesNeedingImages(ctx, 10); err != nil || len(queued) != 1 {
		t.Fatalf("refreshed story was not queued: %+v (err %v)", queued, err)
	}

	// Reopening applies nothing further.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	defer again.Close()
	if _, err := again.GetStory(ctx, "old"); err != nil {
		t.Fatalf("story unreadable on second open: %v", err)
	}
}
