package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/home-news/home-news/internal/domain"
)

func openTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "news.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.SyncFeeds(ctx, []domain.Feed{{ID: "feed", Name: "Feed", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func TestStoryImageLifecycle(t *testing.T) {
	db, ctx := openTestStore(t)
	story := domain.Story{ID: "a", FeedID: "feed", URL: "https://example.com/a", Title: "A", PublishedAt: time.Now(), ImageURL: "https://cdn.example.com/a.jpg"}
	if _, _, err := db.UpsertStory(ctx, story); err != nil {
		t.Fatal(err)
	}
	queued, err := db.StoriesNeedingImages(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].ImageURL != story.ImageURL {
		t.Fatalf("queue = %+v, want the one story awaiting its photograph", queued)
	}

	image := domain.StoryImage{StoryID: "a", ContentType: "image/jpeg", Width: 800, Height: 600, SourceURL: story.ImageURL, FetchedAt: time.Now().UTC().Truncate(time.Millisecond), Bytes: []byte{0xff, 0xd8, 0xff}}
	if err := db.SaveStoryImage(ctx, image); err != nil {
		t.Fatal(err)
	}
	if queued, err = db.StoriesNeedingImages(ctx, 10); err != nil || len(queued) != 0 {
		t.Fatalf("queue = %+v (err %v), want empty once stored", queued, err)
	}
	loaded, err := db.StoryImage(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Width != 800 || loaded.Height != 600 || string(loaded.Bytes) != string(image.Bytes) {
		t.Fatalf("round trip lost data: %+v", loaded)
	}
	got, err := db.GetStory(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasImage {
		t.Fatal("story does not report its stored photograph")
	}
	stored, pending, err := db.ImageCounts(ctx)
	if err != nil || stored != 1 || pending != 0 {
		t.Fatalf("counts = %d stored, %d pending (err %v)", stored, pending, err)
	}
}

// A publisher swapping the photograph must requeue the download and drop the
// stale one, rather than leaving yesterday's picture on the story.
func TestChangedImageURLRequeuesAndDropsStalePhotograph(t *testing.T) {
	db, ctx := openTestStore(t)
	story := domain.Story{ID: "a", FeedID: "feed", URL: "https://example.com/a", Title: "A", PublishedAt: time.Now(), ImageURL: "https://cdn.example.com/old.jpg"}
	if _, _, err := db.UpsertStory(ctx, story); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveStoryImage(ctx, domain.StoryImage{StoryID: "a", ContentType: "image/jpeg", Width: 800, Height: 600, FetchedAt: time.Now(), Bytes: []byte{1}}); err != nil {
		t.Fatal(err)
	}
	story.ImageURL = "https://cdn.example.com/new.jpg"
	if _, _, err := db.UpsertStory(ctx, story); err != nil {
		t.Fatal(err)
	}
	if _, err := db.StoryImage(ctx, "a"); err == nil {
		t.Fatal("stale photograph was kept after the source image changed")
	}
	queued, err := db.StoriesNeedingImages(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 1 || queued[0].ImageURL != "https://cdn.example.com/new.jpg" {
		t.Fatalf("queue = %+v, want the new image queued", queued)
	}
}

func TestImageDownloadRetriesAreBounded(t *testing.T) {
	db, ctx := openTestStore(t)
	if _, _, err := db.UpsertStory(ctx, domain.Story{ID: "a", FeedID: "feed", URL: "https://example.com/a", Title: "A", PublishedAt: time.Now(), ImageURL: "https://cdn.example.com/gone.jpg"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxImageAttempts; i++ {
		queued, err := db.StoriesNeedingImages(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(queued) != 1 {
			t.Fatalf("attempt %d: queue = %d, want 1", i+1, len(queued))
		}
		if err := db.MarkImageUnavailable(ctx, "a"); err != nil {
			t.Fatal(err)
		}
	}
	queued, err := db.StoriesNeedingImages(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("queue = %d after %d failures, want the story to be left alone", len(queued), maxImageAttempts)
	}
}

// A story with no photograph must never enter the download queue.
func TestStoriesWithoutImagesAreNeverQueued(t *testing.T) {
	db, ctx := openTestStore(t)
	if _, _, err := db.UpsertStory(ctx, domain.Story{ID: "a", FeedID: "feed", URL: "https://example.com/a", Title: "A", PublishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	queued, err := db.StoriesNeedingImages(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("queue = %+v, want empty", queued)
	}
}
