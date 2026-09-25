package domain

import "time"

type Feed struct {
	ID      string   `yaml:"id" json:"id"`
	Name    string   `yaml:"name" json:"name"`
	URL     string   `yaml:"url" json:"url"`
	Topics  []string `yaml:"topics" json:"topics"`
	Enabled bool     `yaml:"enabled" json:"enabled"`
}

type Interest struct {
	ID          string   `yaml:"id" json:"id"`
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description"`
	Keywords    []string `yaml:"keywords" json:"keywords"`
}

type SourceConfig struct {
	Feeds     []Feed     `yaml:"feeds" json:"feeds"`
	Interests []Interest `yaml:"interests" json:"interests"`
}

type Story struct {
	ID            string    `json:"id"`
	FeedID        string    `json:"feed_id"`
	FeedName      string    `json:"source_name"`
	URL           string    `json:"url"`
	Title         string    `json:"title"`
	Author        string    `json:"author,omitempty"`
	PublishedAt   time.Time `json:"published_at"`
	FirstSeenAt   time.Time `json:"first_seen_at"`
	SourceText    string    `json:"source_text,omitempty"`
	Truncated     bool      `json:"truncated"`
	Topics        []string  `json:"topics"`
	SummaryStatus string    `json:"summary_status"`
	Summary       *Summary  `json:"summary,omitempty"`
}

type StorySummary struct {
	Headline   string   `json:"headline"`
	Summary    string   `json:"summary"`
	WhyMatters string   `json:"why_it_matters,omitempty"`
	Topics     []string `json:"topics"`
}

type Summary struct {
	StorySummary
	Model         string    `json:"model"`
	PromptVersion string    `json:"prompt_version"`
	CreatedAt     time.Time `json:"created_at"`
}

type EditionParagraph struct {
	Text     string   `json:"text"`
	StoryIDs []string `json:"story_ids"`
}

type EditionSection struct {
	Topic      string             `json:"topic"`
	Title      string             `json:"title"`
	Paragraphs []EditionParagraph `json:"paragraphs"`
	StoryIDs   []string           `json:"story_ids"`
}

type EditionDocument struct {
	LeadHeadline string           `json:"lead_headline"`
	Lead         EditionParagraph `json:"lead"`
	Sections     []EditionSection `json:"sections"`
}

type Edition struct {
	Date        string          `json:"date"`
	GeneratedAt time.Time       `json:"generated_at"`
	Status      string          `json:"status"`
	Document    EditionDocument `json:"document"`
	Stories     []Story         `json:"stories"`
}

type FeedStatus struct {
	Feed        Feed       `json:"feed"`
	LastAttempt *time.Time `json:"last_attempt,omitempty"`
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	ItemsSeen   int        `json:"items_seen"`
}

type Status struct {
	Now              time.Time    `json:"now"`
	OllamaAvailable  bool         `json:"ollama_available"`
	Feeds            []FeedStatus `json:"feeds"`
	PendingSummaries int          `json:"pending_summaries"`
	LastEditionDate  string       `json:"last_edition_date,omitempty"`
	EditionStatus    string       `json:"edition_status,omitempty"`
}

type ChatRequest struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id,omitempty"`
}

type ChatResponse struct {
	ConversationID string  `json:"conversation_id"`
	Answer         string  `json:"answer"`
	Sources        []Story `json:"sources"`
}

type FeedItem struct {
	URL         string
	Title       string
	Author      string
	PublishedAt time.Time
	Text        string
	Truncated   bool
}
