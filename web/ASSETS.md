# Web asset contract

The UI is designed for Go's `html/template`. Parse all `web/templates/*.html` files together so the shared `story-card` definition is available to each page. Execute the named root templates `home`, `archive`, `status`, and `chat`. The values below are ordinary Go structs/slices; pre-format timestamps and any derived labels in the handler. `html/template` handles contextual escaping. Do not mark feed content or model output as `template.HTML`.

Serve `web/static` at `/static/`, so the stylesheet and script resolve as `/static/site.css` and `/static/app.js`. No external assets, fonts, libraries, or endpoints are used.

## Shared story card

The `story-card` template expects a story-like value with:

- `SourceName`, `PublishedAt`, `Headline`, `URL`, `Summary`, `WhyItMatters` as strings
- `Topics` as `[]string`

All URLs must be validated as absolute HTTP(S) publisher URLs before rendering. `URL` is used for `href` and will still be escaped by `html/template`.

## `home`

```text
HomePageData {
  Today string
  EmptyMessage string
  Edition *EditionView
}
EditionView {
  IssueDate, LeadKicker, LeadHeadline, LeadSummary, Intro string
  GeneratedAt, StatusMessage string
  StoryCount int
  LeadSources []SourceLink { Name, URL string }
  Sections []SectionView {
    Name, Narrative string
    StoryCount int
    Stories []StoryCardView
  }
  OtherStories []StoryCardView
}
```

An absent edition renders a useful empty state. `Edition` should be the latest published edition; it can also drive the `/edition/{date}` route if that route uses the same front page.

## `archive`

```text
ArchivePageData {
  Editions []EditionRow { Date, LeadHeadline string; StoryCount int }
  TotalStories int
  Filters { Query, Topic, Source, From, To string }
  Topics, Sources []Option { ID, Name string }
  Stories []StoryCardView
  Page, PageCount int
  PreviousURL, NextURL string
}
```

The filter form submits `q`, `topic`, `source`, `from`, and `to` as GET query parameters to `/archive`. Use `page` (and preserve active filters) when building `PreviousURL` and `NextURL`. Empty pagination URLs suppress those links.

## `status`

```text
StatusPageData {
  Collection {
    StateLabel, Message, LastPoll, NextPoll string
    PendingSummaries int
  }
  Edition { StateLabel, GeneratedAt string }
  EnabledFeedCount int
  Feeds []FeedStatus {
    Name string
    Enabled bool
    Topics []string
    LastAttempt, ResultLabel, ResultClass, ErrorMessage string
  }
}
```

`ResultClass` is one of `is-ok`, `is-error`, or `is-pending` for styling. `ErrorMessage` must be a short safe summary, never a raw exception, URL credential, or environment value. A disabled feed can be included with `Enabled=false`.

## `chat`

```text
ChatPageData {
  ConversationID string
  Messages []ChatMessageView {
    RoleClass, RoleLabel, Text string
    Sources []CitationView { Headline, SourceName, URL string }
  }
}
```

The chat script POSTs JSON `{ "message": string, "conversation_id": string? }` to `/api/v1/chat`. It expects JSON `{ "conversation_id": string, "answer": string, "sources": [{ "headline": string, "source_name": string, "url": string }] }`; the server must return only citations drawn from the retrieved records and validate their URLs. Errors should be JSON with an `error` string suitable for display. Clear sends `DELETE /api/v1/chat/{conversation_id}`. Set the `data-conversation-id` attribute on the `[data-chat]` section when rendering an existing conversation.

`RoleClass` should be `user-message` or `assistant-message`; `RoleLabel` is display text such as “You” or “Home News”. Render only validated source citations. The script uses DOM `textContent` for API-returned strings.
