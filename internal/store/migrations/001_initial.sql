CREATE TABLE IF NOT EXISTS feeds (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    enabled INTEGER NOT NULL,
    last_attempt TEXT,
    last_success TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    items_seen INTEGER NOT NULL DEFAULT 0,
    etag TEXT NOT NULL DEFAULT '',
    last_modified TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS stories (
    id TEXT PRIMARY KEY,
    feed_id TEXT NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    title TEXT NOT NULL,
    author TEXT NOT NULL DEFAULT '',
    published_at TEXT NOT NULL,
    first_seen_at TEXT NOT NULL,
    source_text TEXT NOT NULL DEFAULT '',
    truncated INTEGER NOT NULL DEFAULT 0,
    topics TEXT NOT NULL DEFAULT '[]',
    content_hash TEXT NOT NULL,
    summary_status TEXT NOT NULL DEFAULT 'pending',
    summary_attempts INTEGER NOT NULL DEFAULT 0,
    retry_after TEXT,
    UNIQUE(feed_id, url)
);
CREATE INDEX IF NOT EXISTS stories_published_idx ON stories(published_at DESC);
CREATE INDEX IF NOT EXISTS stories_status_idx ON stories(summary_status, first_seen_at);
CREATE INDEX IF NOT EXISTS stories_feed_idx ON stories(feed_id);

CREATE TABLE IF NOT EXISTS summaries (
    story_id TEXT PRIMARY KEY REFERENCES stories(id) ON DELETE CASCADE,
    headline TEXT NOT NULL,
    summary TEXT NOT NULL,
    why_matters TEXT NOT NULL DEFAULT '',
    topics TEXT NOT NULL DEFAULT '[]',
    model TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS editions (
    date TEXT PRIMARY KEY,
    generated_at TEXT NOT NULL,
    status TEXT NOT NULL,
    document TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS edition_stories (
    edition_date TEXT NOT NULL REFERENCES editions(date) ON DELETE CASCADE,
    story_id TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    topic TEXT NOT NULL DEFAULT '',
    rank INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(edition_date, story_id, topic)
);

CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS chat_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    citations TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS chat_messages_conversation_idx ON chat_messages(conversation_id, id);

CREATE VIRTUAL TABLE IF NOT EXISTS stories_fts USING fts5(
    story_id UNINDEXED,
    title,
    source_text,
    summary,
    tokenize='unicode61'
);
CREATE VIRTUAL TABLE IF NOT EXISTS editions_fts USING fts5(
    edition_date UNINDEXED,
    content,
    tokenize='unicode61'
);
