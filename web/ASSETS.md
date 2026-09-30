# Web asset contract

The UI is designed for Go's `html/template`. Parse all `web/templates/*.html` files together so the shared `story-card` definition is available to each page. Execute the named root templates `home`, `archive`, `status`, and `chat`. The values below are ordinary Go structs/slices; pre-format timestamps and any derived labels in the handler. `html/template` handles contextual escaping. Do not mark feed content or model output as `template.HTML`.

Serve `web/static` at `/static/`, so assets resolve as `/static/site.css`, `/static/newspaper.css`, `/static/app.js` and `/static/compositor.js`. No external assets, fonts, libraries, or endpoints are used.

Story photographs are served from `/media/{story id}` by the application itself, from bytes it downloaded and re-encoded. Templates must never emit a publisher URL in an `img` tag: the content security policy allows `img-src 'self' data:` only, and third-party image loads would leak reading behaviour to publishers and would be missing from a printed page.

## The composed newspaper

`home` renders the edition twice over. `[data-copy]` holds the copy as one flat run of sibling blocks; `[data-sheets]` is filled by `compositor.js`, which moves those blocks onto A4 sheets until each sheet's text block overflows. Everything the compositor needs is on the blocks themselves:

- `data-block` names the kind of block (`nameplate`, `index`, `band`, `head`, `figure`, `deck`, `para`, `brief`, `occasion`, `colophon`, …). It is descriptive; the compositor does not branch on it.
- `data-span="all"` makes a block run the full measure rather than sitting in one column. The stylesheet turns this into `column-span: all`.
- `data-group="<id>"` keeps consecutive blocks sharing an id on the same page.
- `data-keep-next` makes a block hold on to whatever group follows it, which is what stops a section band being stranded at the foot of a page.
- `data-article="<id>"` marks which article a block belongs to, so a "Continued on page N" line can be added where an article runs over. `data-headline` on an article's first block supplies the text for the matching "continued from" line.
- `data-anchor="<slug>"` on a section band is how the "Inside today" box learns which page a section landed on; the matching entry carries `data-index-for="<slug>"` and a `[data-index-page]` slot.

Lead copy is emitted as separate blocks so it flows across columns and pages. Every other article is emitted as a single `.brief` element with `break-inside: avoid`, so a headline is never stranded from its own text.

Blocks must be direct children of `[data-copy]`; the compositor moves children, not descendants. Any block added to the template works without a code change as long as it can stand alone on a page.

`TestWritePreview` in `web` writes a fully populated edition, the waiting state, the archive and the status page to a directory, with real photographs, so a layout change can be opened in a browser and printed. It is skipped unless an output directory is given:

```sh
HOME_NEWS_PREVIEW_DIR=/tmp/preview go test ./web -run TestWritePreview
python3 -m http.server 8777 --directory /tmp/preview
```

Serve it rather than opening the files directly; the pages use absolute `/static` and `/media` paths.

## Shared story card

The `story-card` template is used by the archive listing only; the newspaper sets its own articles. It expects a story-like value with:

- `SourceName`, `PublishedAt`, `Headline`, `URL`, `Summary`, `WhyItMatters`, `ImageURL` as strings
- `Topics` as `[]string`

`ImageURL` is empty when the story has no stored photograph, and otherwise a local `/media/{id}` path.

All URLs must be validated as absolute HTTP(S) publisher URLs before rendering. `URL` is used for `href` and will still be escaped by `html/template`.

## `home`

```text
HomePageData {
  Today string
  EmptyMessage string
  Edition *EditionView
}
EditionView {
  Date, IssueDate, Folio, Dateline string
  Intro, GeneratedAt, StatusMessage string
  StoryCount, SourceCount, PhotoCount int
  Lead *ArticleView
  Sections []SectionView {
    Name, Anchor string
    Narrative []string
    StoryCount int
    Articles []ArticleView
  }
  Other *SectionView
  Events []EditionEvent { Name, Type, Description, Milestone string }
  PreviousDate, NextDate string
}
ArticleView {
  ID, Kicker, Headline, Deck string
  Byline, SourceName, URL, PublishedAt string
  Paragraphs []string
  WhyItMatters string
  ImageURL, ImageCaption string
  ImageWide, Lead bool
  Sources []SourceLink { Name, URL string }
}
```

`Paragraphs` is a slice rather than one string because the compositor breaks copy between columns and pages a paragraph at a time. `Lead` selects the flowing treatment; `ImageWide` sets a photograph across the full measure. `ImageURL` must be a local `/media/{id}` path. `Anchor` must be safe to use as an element id.

An absent edition renders a nameplate and a useful empty state, which still composes to one printable sheet. `Edition` should be the latest published edition; it also drives `/edition/{date}`, where `PreviousDate` and `NextDate` link to the issues either side.

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
  Photographs { Stored, Pending int }
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
