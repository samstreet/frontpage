package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/home-news/home-news/internal/domain"
	"github.com/home-news/home-news/internal/news"
	"github.com/home-news/home-news/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

type Server struct {
	service   *news.Service
	log       *slog.Logger
	templates *template.Template
	mux       *http.ServeMux
}

func New(service *news.Service, log *slog.Logger) (*Server, error) {
	t, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{service: service, log: log, templates: t, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}
func (s *Server) Handler() http.Handler { return s.security(s.mux) }
func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.home)
	s.mux.HandleFunc("GET /edition/{date}", s.editionPage)
	s.mux.HandleFunc("GET /archive", s.archive)
	s.mux.HandleFunc("GET /status", s.statusPage)
	s.mux.HandleFunc("GET /chat", s.chatPage)
	s.mux.HandleFunc("GET /media/{id}", s.storyImage)
	s.mux.Handle("GET /static/", http.FileServer(http.FS(assets)))
	s.mux.HandleFunc("GET /api/v1/healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"status": "ok"}) })
	s.mux.HandleFunc("GET /api/v1/readyz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"status": "ready"}) })
	s.mux.HandleFunc("GET /api/v1/status", s.apiStatus)
	s.mux.HandleFunc("GET /api/v1/feeds", s.apiFeeds)
	s.mux.HandleFunc("GET /api/v1/editions", s.apiEditions)
	s.mux.HandleFunc("GET /api/v1/editions/{date}", s.apiEdition)
	s.mux.HandleFunc("GET /api/v1/stories", s.apiStories)
	s.mux.HandleFunc("GET /api/v1/stories/{id}", s.apiStory)
	s.mux.HandleFunc("POST /api/v1/chat", s.apiChat)
	s.mux.HandleFunc("DELETE /api/v1/chat/{id}", s.apiDeleteChat)
	s.mux.HandleFunc("POST /api/v1/admin/poll", s.apiPoll)
	s.mux.HandleFunc("POST /api/v1/admin/edition", s.apiGenerateEdition)
}
func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	editions, err := s.service.Editions(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	var edition *domain.Edition
	for i := range editions {
		if editions[i].Status == "ready" {
			edition = &editions[i]
			break
		}
	}
	data := s.homeData(r.Context(), edition)
	if edition == nil {
		date := time.Now().In(s.service.Config().Location).Format("2006-01-02")
		if latest, err := s.service.Edition(r.Context(), date); err == nil {
			switch latest.Status {
			case "empty":
				data.EmptyMessage = "Feeds have been collected, but there are not enough summarized stories to prepare today's newspaper yet. Check Sources & status for the collection and summary queue."
			case "failed":
				data.EmptyMessage = "Today's edition could not be generated. Check Sources & status and the Home News logs, then try again."
			}
		}
	}
	s.render(w, "home", data)
}
func (s *Server) editionPage(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	ed, err := s.service.Edition(r.Context(), date)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ed.Status != "ready" {
		http.NotFound(w, r)
		return
	}
	s.render(w, "home", s.homeData(r.Context(), &ed))
}

type SourceLink struct{ Name, URL string }

// StoryCardView backs the shared card used by the archive listing.
type StoryCardView struct {
	SourceName, PublishedAt, Headline, URL, Summary, WhyItMatters, ImageURL string
	Topics                                                                  []string
}

// ArticleView is one piece of set copy on a newspaper page. Body text is kept
// as separate paragraphs because the page compositor flows an article across
// columns and pages a paragraph at a time.
type ArticleView struct {
	ID, Kicker, Headline, Deck string
	Byline, SourceName, URL    string
	PublishedAt                string
	Paragraphs                 []string
	WhyItMatters               string
	ImageURL, ImageCaption     string
	ImageWide                  bool
	// Lead copy is set as separate blocks so it flows across columns and
	// pages; every other article is set as one unbreakable brief.
	Lead    bool
	Sources []SourceLink
}

// SectionView is a titled run of articles, introduced by the edition's own
// narrative for that topic.
type SectionView struct {
	Name, Anchor string
	Narrative    []string
	StoryCount   int
	Articles     []ArticleView
}

// EditionView is a whole issue, ready to be composed into pages.
type EditionView struct {
	Date, IssueDate, Folio, Dateline string
	Intro, GeneratedAt               string
	StatusMessage                    string
	StoryCount, SourceCount          int
	PhotoCount                       int
	Lead                             *ArticleView
	Sections                         []SectionView
	Other                            *SectionView
	Events                           []domain.EditionEvent
	PreviousDate, NextDate           string
}

type HomePageData struct {
	Today, EmptyMessage string
	Edition             *EditionView
}

func (s *Server) homeData(ctx context.Context, ed *domain.Edition) HomePageData {
	loc := s.service.Config().Location
	out := HomePageData{Today: time.Now().In(loc).Format("Monday, 2 January 2006"), EmptyMessage: "Home News checks your configured feeds and prepares a local edition when stories are available."}
	if ed == nil {
		return out
	}
	issue, err := time.ParseInLocation("2006-01-02", ed.Date, loc)
	if err != nil {
		issue = ed.GeneratedAt.In(loc)
	}
	v := &EditionView{
		Date:        ed.Date,
		IssueDate:   issue.Format("Monday, 2 January 2006"),
		Folio:       fmt.Sprintf("Vol. %d · No. %d", issue.Year(), issue.YearDay()),
		Intro:       "A concise briefing built from the stories in your selected feeds.",
		GeneratedAt: ed.GeneratedAt.In(loc).Format("15:04 MST"),
		StoryCount:  len(ed.Stories),
		Events:      ed.Document.Events,
	}
	lookup := map[string]domain.Story{}
	sources := map[string]bool{}
	for _, st := range ed.Stories {
		lookup[st.ID] = st
		if st.FeedName != "" {
			sources[st.FeedName] = true
		}
		if st.HasImage {
			v.PhotoCount++
		}
	}
	v.SourceCount = len(sources)
	v.Dateline = "Home edition · " + plural(v.SourceCount, "source", "sources")
	if len(ed.Stories) == 0 && len(v.Events) > 0 {
		v.Intro = "Birthdays, anniversaries, and dates worth remembering."
		v.Dateline = "Home edition · From your personal calendar"
	}

	used := map[string]bool{}
	lead := &ArticleView{ID: "lead", Headline: ed.Document.LeadHeadline, Deck: ed.Document.Lead.Text, ImageWide: true, Lead: true}
	var leadNames []string
	for _, id := range ed.Document.Lead.StoryIDs {
		st, ok := lookup[id]
		if !ok || used[id] {
			continue
		}
		used[id] = true
		if st.Summary != nil && strings.TrimSpace(st.Summary.Summary) != "" {
			lead.Paragraphs = append(lead.Paragraphs, st.Summary.Summary)
		}
		if st.FeedName != "" && !slices.Contains(leadNames, st.FeedName) {
			leadNames = append(leadNames, st.FeedName)
		}
		if lead.ImageURL == "" && st.HasImage {
			lead.ImageURL = "/media/" + st.ID
			lead.ImageCaption = "Photograph: " + st.FeedName
		}
	}
	lead.Sources = sourceLinks(ed.Document.Lead.StoryIDs, lookup)
	if len(leadNames) > 0 {
		lead.Byline = "From " + joinNames(leadNames)
	}
	v.Lead = lead

	for _, sec := range ed.Document.Sections {
		name := topicName(s.service.Config().Source.Interests, sec.Topic)
		sv := SectionView{Name: name, Anchor: slug(sec.Topic, name), Narrative: paragraphTexts(sec.Paragraphs), StoryCount: len(sec.StoryIDs)}
		for _, id := range sec.StoryIDs {
			st, ok := lookup[id]
			if !ok || used[id] {
				continue
			}
			used[id] = true
			sv.Articles = append(sv.Articles, article(st, loc))
		}
		sv.StoryCount = len(sv.Articles)
		if len(sv.Narrative) == 0 && len(sv.Articles) == 0 {
			continue
		}
		v.Sections = append(v.Sections, sv)
	}

	other := SectionView{Name: "Also in the news", Anchor: "also"}
	for _, st := range ed.Stories {
		if !used[st.ID] {
			other.Articles = append(other.Articles, article(st, loc))
		}
	}
	other.StoryCount = len(other.Articles)
	if other.StoryCount > 0 {
		v.Other = &other
	}

	if dates, err := s.service.EditionDates(ctx, 400); err == nil {
		v.PreviousDate, v.NextDate = neighbouringEditions(dates, ed.Date)
	}
	out.Edition = v
	return out
}

// neighbouringEditions finds the issues either side of date in a newest-first
// list, so a reader can page from one day's paper to the next.
func neighbouringEditions(dates []string, date string) (previous, next string) {
	for i, d := range dates {
		if d != date {
			continue
		}
		if i+1 < len(dates) {
			previous = dates[i+1]
		}
		if i > 0 {
			next = dates[i-1]
		}
		return previous, next
	}
	return "", ""
}

// article turns a summarized story into a set article.
func article(st domain.Story, loc *time.Location) ArticleView {
	headline, summary, why := st.Title, "", ""
	if st.Summary != nil {
		if strings.TrimSpace(st.Summary.Headline) != "" {
			headline = st.Summary.Headline
		}
		summary = st.Summary.Summary
		why = st.Summary.WhyMatters
	}
	a := ArticleView{ID: st.ID, Headline: headline, SourceName: st.FeedName, WhyItMatters: why}
	if st.FeedName != "" {
		a.Byline = "By " + st.FeedName
	}
	if !st.PublishedAt.IsZero() {
		a.PublishedAt = st.PublishedAt.In(location(loc)).Format("2 January, 15:04")
	}
	if validExternalURL(st.URL) {
		a.URL = st.URL
		a.Sources = []SourceLink{{Name: st.FeedName, URL: st.URL}}
	}
	if strings.TrimSpace(summary) != "" {
		a.Paragraphs = []string{summary}
	}
	if st.HasImage {
		a.ImageURL = "/media/" + st.ID
		a.ImageCaption = "Photograph: " + st.FeedName
	}
	return a
}

func location(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

// slug reduces a topic to something usable as an element id.
func slug(id, fallback string) string {
	source := id
	if strings.TrimSpace(source) == "" {
		source = fallback
	}
	var b strings.Builder
	for _, r := range strings.ToLower(source) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// joinNames renders a byline list as "A, B and C".
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func sourceLinks(ids []string, lookup map[string]domain.Story) []SourceLink {
	var out []SourceLink
	seen := map[string]bool{}
	for _, id := range ids {
		st, ok := lookup[id]
		if ok && !seen[id] && validExternalURL(st.URL) {
			out = append(out, SourceLink{Name: st.FeedName, URL: st.URL})
			seen[id] = true
		}
	}
	return out
}

// paragraphTexts keeps an edition's narrative as separate paragraphs so the
// compositor can break it between columns.
func paragraphTexts(ps []domain.EditionParagraph) []string {
	var out []string
	for _, p := range ps {
		if text := strings.TrimSpace(p.Text); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func topicName(interests []domain.Interest, id string) string {
	for _, i := range interests {
		if i.ID == id {
			return i.Name
		}
	}
	return id
}

func card(st domain.Story, loc *time.Location) StoryCardView {
	headline := st.Title
	summary, why := "", ""
	if st.Summary != nil {
		headline = st.Summary.Headline
		summary = st.Summary.Summary
		why = st.Summary.WhyMatters
	}
	u := st.URL
	if !validExternalURL(u) {
		u = "#"
	}
	view := StoryCardView{SourceName: st.FeedName, PublishedAt: st.PublishedAt.In(location(loc)).Format("2 Jan 2006, 15:04"), Headline: headline, URL: u, Summary: summary, WhyItMatters: why, Topics: st.Topics}
	if st.HasImage {
		view.ImageURL = "/media/" + st.ID
	}
	return view
}

func validExternalURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") || strings.HasSuffix(host, ".lan") {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
	}
	return true
}

type EditionRow struct {
	Date, LeadHeadline string
	StoryCount         int
}
type Option struct{ ID, Name string }
type ArchivePageData struct {
	Editions             []EditionRow
	TotalStories         int
	Filters              struct{ Query, Topic, Source, From, To string }
	Topics, Sources      []Option
	Stories              []StoryCardView
	Page, PageCount      int
	PreviousURL, NextURL string
}

func (s *Server) archive(w http.ResponseWriter, r *http.Request) {
	editions, err := s.service.Editions(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	filter := archiveFilter(q, s.service.Config().Location)
	const pageSize = 24
	total, err := s.service.CountStories(r.Context(), filter)
	if err != nil {
		s.serverError(w, err)
		return
	}
	items, err := s.service.StoriesFiltered(r.Context(), filter, pageSize, (page-1)*pageSize)
	if err != nil {
		s.serverError(w, err)
		return
	}
	pageCount := (total + pageSize - 1) / pageSize
	if pageCount < 1 {
		pageCount = 1
	}
	d := ArchivePageData{Page: page, PageCount: pageCount, TotalStories: total}
	d.Filters.Query = q.Get("q")
	d.Filters.Topic = q.Get("topic")
	d.Filters.Source = q.Get("source")
	d.Filters.From = q.Get("from")
	d.Filters.To = q.Get("to")
	for _, e := range editions {
		d.Editions = append(d.Editions, EditionRow{e.Date, e.Document.LeadHeadline, len(e.Stories)})
	}
	for _, i := range s.service.Config().Source.Interests {
		d.Topics = append(d.Topics, Option{i.ID, i.Name})
	}
	for _, f := range s.service.Config().Source.Feeds {
		d.Sources = append(d.Sources, Option{f.ID, f.Name})
	}
	for _, st := range items {
		d.Stories = append(d.Stories, card(st, s.service.Config().Location))
	}
	if page > 1 {
		d.PreviousURL = archivePageURL(q, page-1)
	}
	if page < pageCount {
		d.NextURL = archivePageURL(q, page+1)
	}
	s.render(w, "archive", d)
}

func archiveFilter(q url.Values, loc *time.Location) store.StoryFilter {
	f := store.StoryFilter{Query: q.Get("q"), Topic: q.Get("topic"), FeedID: q.Get("source")}
	if from, err := time.ParseInLocation("2006-01-02", q.Get("from"), loc); err == nil {
		f.From = from.UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	if to, err := time.ParseInLocation("2006-01-02", q.Get("to"), loc); err == nil {
		f.Before = to.AddDate(0, 0, 1).UTC().Format("2006-01-02T15:04:05.000000000Z")
	}
	return f
}
func archivePageURL(q url.Values, page int) string {
	v := url.Values{}
	for _, key := range []string{"q", "topic", "source", "from", "to"} {
		if value := q.Get(key); value != "" {
			v.Set(key, value)
		}
	}
	v.Set("page", strconv.Itoa(page))
	return "/archive?" + v.Encode()
}

type FeedStatusView struct {
	Name                                                string
	Enabled                                             bool
	Topics                                              []string
	LastAttempt, ResultLabel, ResultClass, ErrorMessage string
}
type StatusPageData struct {
	Collection struct {
		StateLabel, Message, LastPoll, NextPoll string
		PendingSummaries                        int
	}
	Photographs      struct{ Stored, Pending int }
	Edition          struct{ StateLabel, GeneratedAt string }
	EnabledFeedCount int
	Feeds            []FeedStatusView
}

func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	status := s.service.Status(r.Context())
	cfg := s.service.Config()
	d := StatusPageData{}
	d.Collection.PendingSummaries = status.PendingSummaries
	d.Photographs.Stored = status.ImagesStored
	d.Photographs.Pending = status.ImagesPending
	d.Collection.StateLabel = "Waiting for first poll"
	d.Collection.Message = "Feed checks run on this server. Ollama handles summaries locally."
	if status.OllamaAvailable {
		d.Collection.StateLabel = "Local model ready"
	} else {
		d.Collection.StateLabel = "Waiting for local model"
	}
	d.Collection.NextPoll = "within " + cfg.PollInterval.String()
	d.Edition.StateLabel = "Not generated"
	if status.LastEditionDate != "" {
		switch status.EditionStatus {
		case "ready":
			d.Edition.StateLabel = "Ready"
		case "empty":
			d.Edition.StateLabel = "Waiting for summarized stories"
		case "failed":
			d.Edition.StateLabel = "Generation failed"
		default:
			d.Edition.StateLabel = status.EditionStatus
		}
		d.Edition.GeneratedAt = status.LastEditionDate
	}
	for _, f := range status.Feeds {
		v := FeedStatusView{Name: f.Feed.Name, Enabled: f.Feed.Enabled, Topics: f.Feed.Topics, ResultLabel: "Not checked", ResultClass: "is-pending"}
		if f.LastAttempt != nil {
			v.LastAttempt = f.LastAttempt.In(cfg.Location).Format("2 Jan 2006, 15:04")
			d.Collection.LastPoll = v.LastAttempt
		}
		if f.LastError != "" {
			v.ResultLabel = "Failed"
			v.ResultClass = "is-error"
			v.ErrorMessage = shortError(f.LastError)
		} else if f.LastSuccess != nil {
			v.ResultLabel = "OK"
			v.ResultClass = "is-ok"
		}
		if f.Feed.Enabled {
			d.EnabledFeedCount++
		}
		d.Feeds = append(d.Feeds, v)
	}
	s.render(w, "status", d)
}
func shortError(e string) string {
	if len(e) > 120 {
		return e[:120] + "…"
	}
	return e
}

type ChatMessageView struct {
	RoleClass, RoleLabel, Text string
	Sources                    []CitationView
}
type CitationView struct{ Headline, SourceName, URL string }
type ChatPageData struct {
	ConversationID string
	Messages       []ChatMessageView
}

func (s *Server) chatPage(w http.ResponseWriter, r *http.Request) {
	data := ChatPageData{ConversationID: r.URL.Query().Get("id")}
	if data.ConversationID != "" {
		messages, err := s.service.Conversation(r.Context(), data.ConversationID)
		if err != nil {
			s.serverError(w, err)
			return
		}
		for _, m := range messages {
			v := ChatMessageView{RoleClass: "assistant-message", RoleLabel: "Home News", Text: m.Content}
			if m.Role == "user" {
				v.RoleClass = "user-message"
				v.RoleLabel = "You"
			}
			for _, st := range m.Sources {
				if validExternalURL(st.URL) {
					v.Sources = append(v.Sources, CitationView{Headline: st.Title, SourceName: st.FeedName, URL: st.URL})
				}
			}
			data.Messages = append(data.Messages, v)
		}
	}
	s.render(w, "chat", data)
}

// storyImage serves a photograph that Home News downloaded and re-encoded
// itself. Nothing here is proxied live: the bytes come from the local database,
// which is why the page needs no third-party image permission in its CSP.
func (s *Server) storyImage(w http.ResponseWriter, r *http.Request) {
	image, err := s.service.StoryImage(r.Context(), r.PathValue("id"))
	if err != nil || len(image.Bytes) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", image.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=604800")
	http.ServeContent(w, r, "", image.FetchedAt, bytes.NewReader(image.Bytes))
}

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.service.Status(r.Context()))
}
func (s *Server) apiFeeds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.service.Feeds(r.Context()))
}
func (s *Server) apiEditions(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.Editions(r.Context())
	if e != nil {
		s.serverError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) apiEdition(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.Edition(r.Context(), r.PathValue("date"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) apiStories(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 30
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	v, e := s.service.StoriesFiltered(r.Context(), archiveFilter(r.URL.Query(), s.service.Config().Location), limit, offset)
	if e != nil {
		s.serverError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) apiStory(w http.ResponseWriter, r *http.Request) {
	v, e := s.service.Story(r.Context(), r.PathValue("id"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) apiChat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req domain.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Invalid chat request."})
		return
	}
	resp, err := s.service.Chat(r.Context(), req)
	if err != nil {
		s.log.Warn("chat request failed", "error", err)
		writeJSON(w, 503, map[string]string{"error": "The local model could not answer right now. Please try again."})
		return
	}
	writeJSON(w, 200, resp)
}
func (s *Server) apiDeleteChat(w http.ResponseWriter, r *http.Request) {
	if err := s.service.DeleteConversation(r.Context(), r.PathValue("id")); err != nil {
		s.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) apiPoll(w http.ResponseWriter, r *http.Request) {
	if err := s.service.Poll(r.Context()); err != nil {
		s.log.Warn("manual poll failed", "error", err)
	}
	writeJSON(w, 200, map[string]string{"status": "poll complete"})
}
func (s *Server) apiGenerateEdition(w http.ResponseWriter, r *http.Request) {
	if err := s.service.GenerateEdition(r.Context(), true); err != nil {
		s.log.Warn("manual edition generation failed", "error", err)
		writeJSON(w, 503, map[string]string{"error": "The edition could not be generated."})
		return
	}
	date := time.Now().In(s.service.Config().Location).Format("2006-01-02")
	edition, err := s.service.Edition(r.Context(), date)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if edition.Status != "ready" {
		writeJSON(w, 200, map[string]string{
			"status":  "insufficient_stories",
			"message": "Feeds may be collected, but there are not enough successfully summarized stories to build a newspaper yet.",
		})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("page render failed", "template", name, "error", err)
		http.Error(w, "Page rendering failed", http.StatusInternalServerError)
	}
}
func (s *Server) serverError(w http.ResponseWriter, err error) {
	s.log.Error("request failed", "error", err)
	writeJSON(w, 500, map[string]string{"error": "Home News could not complete the request."})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
