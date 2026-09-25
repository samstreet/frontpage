package web

import (
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
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
	data := s.homeData(edition)
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
	s.render(w, "home", s.homeData(&ed))
}

type SourceLink struct{ Name, URL string }
type StoryCardView struct {
	SourceName, PublishedAt, Headline, URL, Summary, WhyItMatters string
	Topics                                                        []string
}
type SectionView struct {
	Name, Narrative string
	StoryCount      int
	Stories         []StoryCardView
}
type EditionView struct {
	IssueDate, LeadKicker, LeadHeadline, LeadSummary, Intro, GeneratedAt, StatusMessage string
	StoryCount                                                                          int
	LeadSources                                                                         []SourceLink
	Sections                                                                            []SectionView
	OtherStories                                                                        []StoryCardView
}
type HomePageData struct {
	Today, EmptyMessage string
	Edition             *EditionView
}

func (s *Server) homeData(ed *domain.Edition) HomePageData {
	out := HomePageData{Today: time.Now().In(s.service.Config().Location).Format("Monday, 2 January 2006"), EmptyMessage: "Home News checks your configured feeds and prepares a local edition when stories are available."}
	if ed == nil {
		return out
	}
	v := &EditionView{IssueDate: ed.Date, LeadKicker: "The daily briefing", LeadHeadline: ed.Document.LeadHeadline, LeadSummary: ed.Document.Lead.Text, Intro: "A concise briefing built from the stories in your selected feeds.", GeneratedAt: ed.GeneratedAt.In(s.service.Config().Location).Format("15:04 MST"), StoryCount: len(ed.Stories)}
	lookup := map[string]domain.Story{}
	for _, st := range ed.Stories {
		lookup[st.ID] = st
	}
	v.LeadSources = sourceLinks(ed.Document.Lead.StoryIDs, lookup)
	used := map[string]bool{}
	for _, sec := range ed.Document.Sections {
		sv := SectionView{Name: topicName(s.service.Config().Source.Interests, sec.Topic), Narrative: paragraphText(sec.Paragraphs), StoryCount: len(sec.StoryIDs)}
		sectionSeen := map[string]bool{}
		for _, id := range sec.StoryIDs {
			if st, ok := lookup[id]; ok && !sectionSeen[id] {
				sv.Stories = append(sv.Stories, card(st))
				used[id] = true
				sectionSeen[id] = true
			}
		}
		v.Sections = append(v.Sections, sv)
	}
	for _, st := range ed.Stories {
		if !used[st.ID] {
			v.OtherStories = append(v.OtherStories, card(st))
		}
	}
	out.Edition = v
	return out
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
func paragraphText(ps []domain.EditionParagraph) string {
	var parts []string
	for _, p := range ps {
		parts = append(parts, p.Text)
	}
	return strings.Join(parts, " ")
}
func topicName(interests []domain.Interest, id string) string {
	for _, i := range interests {
		if i.ID == id {
			return i.Name
		}
	}
	return id
}
func card(st domain.Story) StoryCardView {
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
	return StoryCardView{SourceName: st.FeedName, PublishedAt: st.PublishedAt.Format("2 Jan 2006, 15:04"), Headline: headline, URL: u, Summary: summary, WhyItMatters: why, Topics: st.Topics}
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
		d.Stories = append(d.Stories, card(st))
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
	Edition          struct{ StateLabel, GeneratedAt string }
	EnabledFeedCount int
	Feeds            []FeedStatusView
}

func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	status := s.service.Status(r.Context())
	cfg := s.service.Config()
	d := StatusPageData{}
	d.Collection.PendingSummaries = status.PendingSummaries
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
		d.Edition.StateLabel = status.EditionStatus
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
