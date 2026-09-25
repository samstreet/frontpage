package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/home-news/home-news/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

const timestampLayout = "2006-01-02T15:04:05.000000000Z"

func formatTimestamp(t time.Time) string { return t.UTC().Format(timestampLayout) }

//go:embed migrations/*.sql
var migrationFS embed.FS

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	files, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	for i, name := range files {
		version := i + 1
		var applied int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, statement := range strings.Split(string(sqlBytes), ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %s: %w", name, err)
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, version, formatTimestamp(time.Now())); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SyncFeeds(ctx context.Context, feeds []domain.Feed) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, f := range feeds {
		_, err = tx.ExecContext(ctx, `INSERT INTO feeds(id,name,url,enabled) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,url=excluded.url,enabled=excluded.enabled`, f.ID, f.Name, f.URL, f.Enabled)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) FeedStatuses(ctx context.Context, feeds []domain.Feed) ([]domain.FeedStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,last_attempt,last_success,last_error,items_seen FROM feeds`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]domain.FeedStatus{}
	for rows.Next() {
		var id string
		var a, ok sql.NullString
		var msg string
		var count int
		if err := rows.Scan(&id, &a, &ok, &msg, &count); err != nil {
			return nil, err
		}
		st := domain.FeedStatus{LastError: msg, ItemsSeen: count}
		if a.Valid {
			t, _ := time.Parse(time.RFC3339Nano, a.String)
			st.LastAttempt = &t
		}
		if ok.Valid {
			t, _ := time.Parse(time.RFC3339Nano, ok.String)
			st.LastSuccess = &t
		}
		byID[id] = st
	}
	out := make([]domain.FeedStatus, 0, len(feeds))
	for _, f := range feeds {
		st := byID[f.ID]
		st.Feed = f
		out = append(out, st)
	}
	return out, rows.Err()
}
func (s *Store) MarkFeed(ctx context.Context, id string, attempt time.Time, success bool, count int, feedErr string) error {
	var ok any
	if success {
		ok = formatTimestamp(attempt)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE feeds SET last_attempt=?,last_success=COALESCE(?,last_success),last_error=?,items_seen=? WHERE id=?`, formatTimestamp(attempt), ok, feedErr, count, id)
	return err
}
func (s *Store) FeedValidators(ctx context.Context, id string) (string, string, error) {
	var etag, lastModified string
	err := s.db.QueryRowContext(ctx, `SELECT etag,last_modified FROM feeds WHERE id=?`, id).Scan(&etag, &lastModified)
	return etag, lastModified, err
}
func (s *Store) SaveFeedValidators(ctx context.Context, id, etag, lastModified string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE feeds SET etag=CASE WHEN ?='' THEN etag ELSE ? END,last_modified=CASE WHEN ?='' THEN last_modified ELSE ? END WHERE id=?`, etag, etag, lastModified, lastModified, id)
	return err
}

func (s *Store) UpsertStory(ctx context.Context, story domain.Story) (string, bool, error) {
	if story.ID == "" {
		return "", false, errors.New("story id required")
	}
	topics, _ := json.Marshal(story.Topics)
	now := formatTimestamp(time.Now())
	contentHash := sha256.Sum256([]byte(story.SourceText))
	contentHashHex := fmt.Sprintf("%x", contentHash)
	var oldID, oldHash string
	lookupErr := s.db.QueryRowContext(ctx, `SELECT id,content_hash FROM stories WHERE feed_id=? AND url=?`, story.FeedID, story.URL).Scan(&oldID, &oldHash)
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return "", false, lookupErr
	}
	existed := lookupErr == nil
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO stories(id,feed_id,url,title,author,published_at,first_seen_at,source_text,truncated,topics,content_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(feed_id,url) DO UPDATE SET title=CASE WHEN excluded.title='' THEN stories.title ELSE excluded.title END,author=CASE WHEN excluded.author='' THEN stories.author ELSE excluded.author END,source_text=CASE WHEN excluded.source_text='' THEN stories.source_text ELSE excluded.source_text END,topics=excluded.topics,content_hash=excluded.content_hash,truncated=excluded.truncated`, story.ID, story.FeedID, story.URL, story.Title, story.Author, formatTimestamp(story.PublishedAt), now, story.SourceText, story.Truncated, string(topics), contentHashHex)
	if err != nil {
		return "", false, err
	}
	id := story.ID
	if existed {
		id = oldID
		if oldHash != contentHashHex {
			if _, err := tx.ExecContext(ctx, `DELETE FROM summaries WHERE story_id=?`, id); err != nil {
				return "", false, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM stories_fts WHERE story_id=?`, id); err != nil {
				return "", false, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE stories SET summary_status='pending',summary_attempts=0,retry_after=NULL WHERE id=?`, id); err != nil {
				return "", false, err
			}
		}
	}
	var existingSummary string
	_ = tx.QueryRowContext(ctx, `SELECT summary FROM summaries WHERE story_id=?`, id).Scan(&existingSummary)
	if _, err := tx.ExecContext(ctx, `DELETE FROM stories_fts WHERE story_id=?`, id); err != nil {
		return "", false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO stories_fts(story_id,title,source_text,summary) SELECT id,title,source_text,? FROM stories WHERE id=?`, existingSummary, id); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	if id == "" {
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM stories WHERE feed_id=? AND url=?`, story.FeedID, story.URL).Scan(&id); err != nil {
			return "", false, err
		}
	}
	return id, !existed, nil
}

func (s *Store) PendingStories(ctx context.Context, limit int) ([]domain.Story, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.feed_id,f.name,s.url,s.title,s.author,s.published_at,s.first_seen_at,s.source_text,s.truncated,s.topics,s.summary_status FROM stories s JOIN feeds f ON f.id=s.feed_id WHERE s.summary_status='pending' OR (s.summary_status='failed' AND s.summary_attempts<5 AND (s.retry_after IS NULL OR s.retry_after<=?)) ORDER BY s.published_at DESC LIMIT ?`, formatTimestamp(time.Now()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Story
	for rows.Next() {
		var x domain.Story
		var pub, first, topics string
		var truncated int
		if err := rows.Scan(&x.ID, &x.FeedID, &x.FeedName, &x.URL, &x.Title, &x.Author, &pub, &first, &x.SourceText, &truncated, &topics, &x.SummaryStatus); err != nil {
			return nil, err
		}
		x.PublishedAt, _ = time.Parse(time.RFC3339Nano, pub)
		x.FirstSeenAt, _ = time.Parse(time.RFC3339Nano, first)
		x.Truncated = truncated != 0
		_ = json.Unmarshal([]byte(topics), &x.Topics)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) SaveSummary(ctx context.Context, storyID, model, version string, summary domain.StorySummary) error {
	topics, _ := json.Marshal(summary.Topics)
	now := formatTimestamp(time.Now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO summaries(story_id,headline,summary,why_matters,topics,model,prompt_version,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(story_id) DO UPDATE SET headline=excluded.headline,summary=excluded.summary,why_matters=excluded.why_matters,topics=excluded.topics,model=excluded.model,prompt_version=excluded.prompt_version,created_at=excluded.created_at`, storyID, summary.Headline, summary.Summary, summary.WhyMatters, string(topics), model, version, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE stories SET summary_status='summarized' WHERE id=?`, storyID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM stories_fts WHERE story_id=?`, storyID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO stories_fts(story_id,title,source_text,summary) SELECT s.id,s.title,s.source_text,? FROM stories s WHERE s.id=?`, summary.Summary, storyID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) MarkSummaryFailed(ctx context.Context, id string) error {
	var attempts int
	if err := s.db.QueryRowContext(ctx, `SELECT summary_attempts FROM stories WHERE id=?`, id).Scan(&attempts); err != nil {
		return err
	}
	delay := 5 * time.Minute * time.Duration(1<<min(attempts, 3))
	_, err := s.db.ExecContext(ctx, `UPDATE stories SET summary_attempts=summary_attempts+1,summary_status='failed',retry_after=? WHERE id=?`, formatTimestamp(time.Now().Add(delay)), id)
	return err
}
func (s *Store) MarkSummaryUnavailable(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE stories SET summary_status='unavailable' WHERE id=?`, id)
	return err
}
func (s *Store) PendingCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM stories WHERE summary_status='pending' OR (summary_status='failed' AND summary_attempts<5 AND (retry_after IS NULL OR retry_after<=?))`, formatTimestamp(time.Now())).Scan(&n)
	return n, err
}

const storySelect = `SELECT s.id,s.feed_id,f.name,s.url,s.title,s.author,s.published_at,s.first_seen_at,s.source_text,s.truncated,s.topics,s.summary_status,z.headline,z.summary,z.why_matters,z.topics,z.model,z.prompt_version,z.created_at FROM stories s JOIN feeds f ON f.id=s.feed_id LEFT JOIN summaries z ON z.story_id=s.id`

func scanStory(row interface{ Scan(...any) error }) (domain.Story, error) {
	var x domain.Story
	var pub, first, topics string
	var tr int
	var h, summary, why, st, model, version, created sql.NullString
	err := row.Scan(&x.ID, &x.FeedID, &x.FeedName, &x.URL, &x.Title, &x.Author, &pub, &first, &x.SourceText, &tr, &topics, &x.SummaryStatus, &h, &summary, &why, &st, &model, &version, &created)
	if err != nil {
		return x, err
	}
	x.PublishedAt, _ = time.Parse(time.RFC3339Nano, pub)
	x.FirstSeenAt, _ = time.Parse(time.RFC3339Nano, first)
	x.Truncated = tr != 0
	_ = json.Unmarshal([]byte(topics), &x.Topics)
	if h.Valid {
		x.Summary = &domain.Summary{StorySummary: domain.StorySummary{Headline: h.String, Summary: summary.String, WhyMatters: why.String}, Model: model.String, PromptVersion: version.String}
		_ = json.Unmarshal([]byte(st.String), &x.Summary.Topics)
		x.Summary.CreatedAt, _ = time.Parse(time.RFC3339Nano, created.String)
	}
	return x, nil
}
func (s *Store) GetStory(ctx context.Context, id string) (domain.Story, error) {
	return scanStory(s.db.QueryRowContext(ctx, storySelect+` WHERE s.id=?`, id))
}
func (s *Store) ListStories(ctx context.Context, query, topic string, limit, offset int) ([]domain.Story, error) {
	return s.ListStoriesFiltered(ctx, StoryFilter{Query: query, Topic: topic}, limit, offset)
}

type StoryFilter struct{ Query, Topic, FeedID, From, Before string }

func (s *Store) ListStoriesFiltered(ctx context.Context, filter StoryFilter, limit, offset int) ([]domain.Story, error) {
	if limit < 1 || limit > 100 {
		limit = 30
	}
	query, args, from := buildStoryFilter(filter)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, storySelect+from+query+` ORDER BY s.published_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Story
	for rows.Next() {
		x, e := scanStory(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) CountStories(ctx context.Context, filter StoryFilter) (int, error) {
	where, args, from := buildStoryFilter(filter)
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT s.id) FROM stories s`+from+where, args...).Scan(&count)
	return count, err
}

func buildStoryFilter(filter StoryFilter) (string, []any, string) {
	var conditions []string
	var args []any
	from := ""
	if filter.Query != "" {
		from += ` JOIN stories_fts ON stories_fts.story_id=s.id`
		conditions = append(conditions, `stories_fts MATCH ?`)
		args = append(args, escapeFTS(filter.Query))
	}
	if filter.Topic != "" {
		conditions = append(conditions, `instr(s.topics,?)>0`)
		args = append(args, `"`+filter.Topic+`"`)
	}
	if filter.FeedID != "" {
		conditions = append(conditions, `s.feed_id=?`)
		args = append(args, filter.FeedID)
	}
	if filter.From != "" {
		conditions = append(conditions, `s.published_at>=?`)
		args = append(args, filter.From)
	}
	if filter.Before != "" {
		conditions = append(conditions, `s.published_at<?`)
		args = append(args, filter.Before)
	}
	if len(conditions) == 0 {
		return "", args, from
	}
	return ` WHERE ` + strings.Join(conditions, ` AND `), args, from
}
func escapeFTS(q string) string {
	terms := strings.Fields(q)
	for i := range terms {
		terms[i] = `"` + strings.ReplaceAll(terms[i], `"`, `""`) + `"`
	}
	return strings.Join(terms, " AND ")
}
func (s *Store) SearchStories(ctx context.Context, q string, limit int) ([]domain.Story, error) {
	if strings.TrimSpace(q) == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, storySelect+` WHERE s.id IN (SELECT story_id FROM stories_fts WHERE stories_fts MATCH ? UNION SELECT es.story_id FROM editions_fts JOIN edition_stories es ON es.edition_date=editions_fts.edition_date WHERE editions_fts MATCH ?) ORDER BY s.published_at DESC LIMIT ?`, escapeFTS(q), escapeFTS(q), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Story
	for rows.Next() {
		x, e := scanStory(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) SaveEdition(ctx context.Context, date, model, version string, doc domain.EditionDocument, stories []domain.Story, status string) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := formatTimestamp(time.Now())
	_, err = tx.ExecContext(ctx, `INSERT INTO editions(date,generated_at,status,document,model,prompt_version) VALUES(?,?,?,?,?,?) ON CONFLICT(date) DO UPDATE SET generated_at=excluded.generated_at,status=excluded.status,document=excluded.document,model=excluded.model,prompt_version=excluded.prompt_version,error=''`, date, now, status, string(b), model, version)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM edition_stories WHERE edition_date=?`, date)
	if err != nil {
		return err
	}
	for i, st := range stories {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO edition_stories(edition_date,story_id,topic,rank) VALUES(?,?,?,?)`, date, st.ID, strings.Join(st.Topics, ","), i)
		if err != nil {
			return err
		}
	}
	var narrative strings.Builder
	narrative.WriteString(doc.LeadHeadline)
	narrative.WriteByte(' ')
	narrative.WriteString(doc.Lead.Text)
	for _, section := range doc.Sections {
		narrative.WriteByte(' ')
		narrative.WriteString(section.Title)
		for _, paragraph := range section.Paragraphs {
			narrative.WriteByte(' ')
			narrative.WriteString(paragraph.Text)
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM editions_fts WHERE edition_date=?`, date); err != nil {
		return err
	}
	if status == "ready" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO editions_fts(edition_date,content) VALUES(?,?)`, date, narrative.String()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) SetEditionError(ctx context.Context, date, msg string) error {
	now := formatTimestamp(time.Now())
	_, err := s.db.ExecContext(ctx, `INSERT INTO editions(date,generated_at,status,document,error) VALUES(?,?,'failed','{}',?) ON CONFLICT(date) DO UPDATE SET error=excluded.error`, date, now, msg)
	return err
}
func (s *Store) GetEdition(ctx context.Context, date string) (domain.Edition, error) {
	var out domain.Edition
	var raw, generated string
	err := s.db.QueryRowContext(ctx, `SELECT date,generated_at,status,document FROM editions WHERE date=?`, date).Scan(&out.Date, &generated, &out.Status, &raw)
	if err != nil {
		return out, err
	}
	out.GeneratedAt, _ = time.Parse(time.RFC3339Nano, generated)
	_ = json.Unmarshal([]byte(raw), &out.Document)
	rows, err := s.db.QueryContext(ctx, storySelect+` JOIN edition_stories e ON e.story_id=s.id WHERE e.edition_date=? ORDER BY e.rank`, date)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		st, e := scanStory(rows)
		if e != nil {
			return out, e
		}
		out.Stories = append(out.Stories, st)
	}
	return out, rows.Err()
}
func (s *Store) LatestEditionDate(ctx context.Context) (string, string, error) {
	var date, status string
	err := s.db.QueryRowContext(ctx, `SELECT date,status FROM editions ORDER BY date DESC LIMIT 1`).Scan(&date, &status)
	return date, status, err
}
func (s *Store) EditionDates(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT date FROM editions ORDER BY date DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) SaveChat(ctx context.Context, conversationID, user, answer string, citations []string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := formatTimestamp(time.Now())
	if conversationID == "" {
		conversationID = fmt.Sprintf("c-%d", time.Now().UnixNano())
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conversations(id,created_at,updated_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET updated_at=excluded.updated_at`, conversationID, now, now)
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO chat_messages(conversation_id,role,content,citations,created_at) VALUES(?, 'user',?,'[]',?),(?, 'assistant',?,?,?)`, conversationID, user, now, conversationID, answer, mustJSON(citations), now)
	if err != nil {
		return "", err
	}
	return conversationID, tx.Commit()
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func (s *Store) DeleteConversation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id=?`, id)
	return err
}

type ChatRecord struct {
	Role, Content string
	Citations     []string
}

func (s *Store) ChatHistory(ctx context.Context, id string, limit int) ([]ChatRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role,content,citations FROM chat_messages WHERE conversation_id=? ORDER BY id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rev []ChatRecord
	for rows.Next() {
		var x ChatRecord
		var raw string
		if err := rows.Scan(&x.Role, &x.Content, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &x.Citations)
		rev = append(rev, x)
	}
	out := make([]ChatRecord, len(rev))
	for i := range rev {
		out[len(rev)-1-i] = rev[i]
	}
	return out, rows.Err()
}
func (s *Store) Cleanup(ctx context.Context, storyDays, chatDays int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	storyCut := formatTimestamp(time.Now().AddDate(0, 0, -storyDays))
	chatCut := formatTimestamp(time.Now().AddDate(0, 0, -chatDays))
	if _, err = tx.ExecContext(ctx, `DELETE FROM stories_fts WHERE story_id IN (SELECT id FROM stories WHERE first_seen_at<?)`, storyCut); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM stories WHERE first_seen_at<?`, storyCut); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM editions_fts WHERE edition_date IN (SELECT date FROM editions WHERE date<?)`, time.Now().AddDate(0, 0, -storyDays).Format("2006-01-02")); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM editions WHERE date<?`, time.Now().AddDate(0, 0, -storyDays).Format("2006-01-02")); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM conversations WHERE updated_at<?`, chatCut); err != nil {
		return err
	}
	return tx.Commit()
}
