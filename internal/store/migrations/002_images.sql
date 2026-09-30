ALTER TABLE stories ADD COLUMN image_url TEXT NOT NULL DEFAULT '';
ALTER TABLE stories ADD COLUMN image_status TEXT NOT NULL DEFAULT 'none';
ALTER TABLE stories ADD COLUMN image_attempts INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS stories_image_idx ON stories(image_status);

CREATE TABLE IF NOT EXISTS story_images (
    story_id TEXT PRIMARY KEY REFERENCES stories(id) ON DELETE CASCADE,
    content_type TEXT NOT NULL,
    width INTEGER NOT NULL,
    height INTEGER NOT NULL,
    source_url TEXT NOT NULL DEFAULT '',
    fetched_at TEXT NOT NULL,
    bytes BLOB NOT NULL
);
