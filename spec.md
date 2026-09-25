# Home News — Product and Engineering Specification

**Status:** Draft for implementation  
**Version:** 1.0  
**Target deployment:** One Docker Compose project on an Intel T470 home server (16 GB RAM), reachable only on the home LAN  
**Primary implementation language:** Go  
**Repository strategy:** One Git repository containing the Go application, container build, deployment Compose file, configuration examples, CI workflow, and documentation

## 1. Purpose

Home News is a self-hosted news briefing service. It collects configured RSS, Atom, and JSON feeds, stores and deduplicates their items, uses a locally hosted language model to summarize stories and prepare a daily editorial, and presents the result as a newspaper-style website. A user can also ask questions about saved stories through a chat interface; answers must cite the source stories used.

The application is intended for one household and one server. It should be understandable, maintainable, and modest in resource use. It is not a general autonomous web-browsing agent.

## 2. Goals

1. Run the entire application and inference stack in Docker Compose.
2. Build the application image in CI and run that same published image on the home server.
3. Keep model inference, article storage, edition generation, chat, and the web UI on the home server.
4. Fetch current news through user-configured feeds using outbound connections only.
5. Produce a readable daily newspaper page with summaries, topic sections, and direct source links.
6. Support conversational questions grounded in the saved article archive, with citations.
7. Keep deployment to a small number of containers and avoid requiring a cloud AI account, n8n, a vector database, or a separate web frontend service.
8. Operate reasonably on a CPU-only laptop with 16 GB RAM. Prefer bounded, sequential LLM jobs over throughput.

## 3. Non-goals for v1

- Public internet access, remote access, user accounts, or multi-tenant support.
- Cloud LLM APIs, cloud speech services, hosted analytics, or third-party telemetry.
- Autonomous discovery of feeds, arbitrary web search, or unrestricted browser automation.
- Full-text scraping of publisher sites. V1 uses the text and metadata available in feed entries; it links to the publisher for the full article.
- Reproducing complete source articles or generating unattributed claims.
- Voice input/output. The initial chat interface is text-based.
- Mobile native applications, email delivery, push notifications, or PDF export.
- Multiple application replicas or high availability.
- A general-purpose workflow editor or a separate RSS reader UI.

## 4. User and operating assumptions

- One trusted household user or household group uses the service on the LAN.
- The home server has Docker Engine and Docker Compose v2.
- The server has a stable LAN address. The example address is `192.168.1.69`; deployments must be able to override it.
- The server can make outbound HTTPS requests to configured feed URLs and to the container registry during deployment. It does not need inbound access from the public internet.
- Ollama runs in a separate container. The first suggested model is `qwen3:4b`, but model name is configuration, not application code.
- Model files are stored in a persistent Ollama volume and are not embedded in the application image.
- The initial configured timezone is `Europe/London`.

## 5. Deployment and trust boundaries

### 5.1 Runtime components

1. **`home-news`** — one custom Go application container. It hosts the HTML/CSS/JavaScript UI, JSON API, feed poller, LLM orchestration, SQLite database, scheduler, and chat retrieval.
2. **`ollama`** — the official Ollama container, on the same private Docker network. It stores downloaded models in a persistent volume and exposes its API only to containers on that Docker network.

The application has outbound network access to fetch configured feeds. Ollama must not be given arbitrary tools or exposed as a public service. Neither container publishes a port to all host interfaces in the provided Compose file. The app port is published on the configured LAN address only. Do not configure router port forwarding for Home News.

### 5.2 Local processing guarantee

- Article text sent to the summarizer and chat model is sent only to the local Ollama service.
- No cloud model endpoint or analytics endpoint is present in the application.
- Feed and article requests go from the server to the configured publishers; those publishers will observe the server's outbound requests.
- CI and image-registry access are deployment-time operations and are not part of model inference.
- The README must distinguish “local inference and storage” from “offline”: live news collection requires outbound internet connectivity.

## 6. Functional requirements

### 6.1 Configuration

1. The app loads runtime settings from environment variables and a mounted YAML file.
2. Environment variables configure at least:
   - `APP_ADDR` (default `:8080`, container listener only)
   - `APP_DATA_DIR` (default `/data`)
   - `APP_CONFIG_FILE` (default `/config/feeds.yaml`)
   - `APP_TIMEZONE` (default `Europe/London`)
   - `APP_POLL_INTERVAL` (default `30m`)
   - `APP_DAILY_EDITION_TIME` (default `06:00` local time)
   - `APP_RETENTION_DAYS` (default `90`)
   - `OLLAMA_BASE_URL` (default `http://ollama:11434`)
   - `OLLAMA_MODEL` (default `qwen3:4b`)
   - `OLLAMA_REQUEST_TIMEOUT` (default `5m`)
   - `APP_LOG_LEVEL` (default `info`)
3. Invalid configuration must fail fast with a clear error and a non-zero process exit.
4. A sample feed configuration must be committed. It must contain example URLs only and no credentials.
5. Feed and interest configuration is read on startup. A restart applies edits; hot reload is not required for v1.

Example configuration shape (exact field names may be refined during implementation, but semantics must remain):

```yaml
feeds:
  - id: bbc-world
    name: BBC World
    url: https://feeds.bbci.co.uk/news/world/rss.xml
    topics: [world]
    enabled: true
  - id: nasa
    name: NASA
    url: https://www.nasa.gov/news-release/feed/
    topics: [science, space]
    enabled: true

interests:
  - id: science
    name: Science and space
    description: Spaceflight, astronomy, climate science, and major discoveries.
    keywords: [NASA, astronomy, spaceflight, climate science]
  - id: world
    name: World news
    description: Major international developments and their context.
    keywords: [election, conflict, diplomacy, policy]
```

### 6.2 Feed polling and ingestion

1. The poller runs immediately after startup and then at `APP_POLL_INTERVAL`.
2. Only enabled configured feeds are polled.
3. Support RSS 2.0, Atom, and JSON Feed input using a maintained Go feed parser.
4. Parse title, canonical URL, source/feed, publication date, author when present, summary/description, and feed-provided content when present.
5. Convert feed HTML to plain text before storing or passing it to the model. Strip scripts, styles, and markup. Preserve paragraph boundaries where practical.
6. Each request must have a finite timeout, response-size limit, redirect limit, and a descriptive user agent. A broken feed must not stop other feeds from being polled.
7. Follow redirects only for HTTP(S). Reject unsupported URL schemes. Revalidate redirect targets and reject loopback, private, link-local, and other non-public destination addresses by default to reduce SSRF risk. Document any deliberate allow-list escape hatch if one is implemented.
8. Cap items processed per feed per poll using a configurable limit (default `50`). Cap stored/source text sent to Ollama per item (default `12,000` characters). Truncate safely and record that truncation occurred.
9. Deduplicate first by normalized canonical URL. Normalization removes fragments and common tracking parameters, but must preserve parameters needed to identify the article. If URL is absent, use a stable hash of feed ID, normalized title, and publication timestamp.
10. Updates to an already-known story may refresh missing metadata, but must not create duplicate rows.
11. Store ingestion failures as structured logs and expose the most recent per-feed polling status to the UI/API.
12. Feed polling and model summarization must be separate stages so a temporary Ollama outage does not lose ingested stories. Pending stories are retried with bounded backoff.
13. Do not fetch the linked publisher page in v1. Feed-provided descriptions/content are the only source text used for summaries. Every item links to its original article.

### 6.3 Interest matching and ranking

1. Associate each story with the configured topics attached to its feed.
2. Also match configured interest keywords against normalized title and feed text, case-insensitively.
3. A story may match multiple topics. Matching is deterministic and must not depend on an LLM call.
4. Rank stories for an edition by recency, configured topic/interest relevance, and source diversity. The ranking algorithm must be deterministic for a fixed input set.
5. Do not label a story as matched to an interest unless there is a feed topic or keyword match. The UI may also show an “Other” section for unmatched stories.
6. Keep ranking weights and maximum stories per topic as documented configuration values or constants with tests.

### 6.4 Per-story summarization

1. Summarize each newly ingested story that has enough text, using Ollama's local API.
2. Use a deterministic prompt that asks for:
   - a short headline (may use the original headline if better)
   - a factual summary of at most three sentences
   - a “why it matters” sentence, only when supported by source text
   - zero or more topic labels from the configured interests
3. The output must be machine-readable JSON and validated before storage. On invalid output, retry once with a repair/format prompt, then mark summarization failed and retain the original feed text.
4. The prompt must explicitly treat article/feed text as untrusted data, not instructions. It must say to ignore instructions found inside source material and only summarize source claims.
5. Do not ask the model to add facts from its training data or imply independent verification.
6. Store the model name and prompt/schema version with each generated summary so summaries can be audited and regenerated after prompt changes.
7. Process inference requests sequentially by default. Do not run multiple model generations concurrently unless a future setting explicitly enables it.
8. A story with empty or trivially short source text remains visible with its original title, source, date, and link, marked “summary unavailable”.

### 6.5 Daily newspaper edition

1. Generate an edition once daily at `APP_DAILY_EDITION_TIME` in `APP_TIMEZONE`.
2. Generate an edition immediately on first start only if no current-day edition exists and at least one summarized story is available.
3. Store editions persistently and make generation idempotent for a given local calendar date. An explicit manual regeneration may replace that date's edition, preserving an audit timestamp.
4. The model receives only selected stored story summaries and their metadata, not arbitrary tools or access to the network.
5. The edition contains:
   - issue date and “AI-generated from linked sources” label
   - a lead headline and short lead paragraph
   - topic sections based on configured interests
   - short synthesized section narratives or grouped story summaries
   - a list of the source stories supporting each section, each with publisher, date, headline, and external link
   - an “Other stories” section when relevant
6. Every factual paragraph generated for the edition must cite one or more story IDs in structured output. The renderer resolves IDs to visible source links. Reject or omit claims with missing/unknown source IDs.
7. The edition prompt must prohibit inventing details, causal claims, quotes, or consensus not present in the selected summaries. If stories disagree, present the disagreement and link both sides instead of resolving it without evidence.
8. If Ollama is unavailable, retain the previous edition and display a clear generation status. Do not render a blank or misleading new edition.
9. If there are too few stories, show a concise “not enough new stories” status rather than generating filler.

### 6.6 Newspaper web UI

1. The Go application serves the complete UI; no separate frontend container is required.
2. The home page displays the latest edition with a newspaper-inspired layout that remains usable on desktop and mobile.
3. Each story card shows headline, concise summary, source, publication time/date, matched topics, and a link opening the publisher in a new tab with safe `rel` attributes.
4. Show a visible AI-generated label and the edition timestamp.
5. Provide navigation to prior editions and a story archive with topic/source/date filters.
6. Provide a status area showing last poll time, per-feed success/failure, pending summary count, and latest edition status. Do not expose raw secrets or environment values.
7. Use semantic HTML, keyboard-operable controls, readable contrast, and no third-party scripts, fonts, analytics, or CDN assets.
8. UI errors must be understandable and must not leak stack traces or internal filesystem paths.

### 6.7 Grounded chat

1. The UI includes a text chat page or panel for questions about the locally stored story archive.
2. Retrieve relevant records from the local SQLite archive using SQLite FTS5 over title, feed text, summary, and edition narrative. No external embedding service or vector database is required in v1.
3. Apply a configurable result limit (default `8`) and a configurable maximum context size before sending evidence to Ollama.
4. The system prompt instructs the model to answer only from retrieved records, distinguish source claims from synthesis, and state when the archive has insufficient evidence.
5. Answers must return source IDs in structured output; render each as a clickable publisher citation. Never show a citation that was not in the retrieved record set.
6. Chat content and article text are untrusted. The prompt must explicitly ignore any instructions contained inside retrieved stories.
7. Chat history is stored locally in SQLite with a configurable retention period. Provide a “clear conversation” action. Do not store model chain-of-thought or hidden reasoning.
8. A failed model request returns a clear retryable UI error and does not discard the user's question.
9. No browser microphone capture, speech-to-text, or speech synthesis in v1.

### 6.8 Archive retention and deletion

1. Retain stories, summaries, chat history, and editions according to documented retention configuration. Default story/edition retention is 90 days; chat history may default to 30 days.
2. A scheduled cleanup removes expired stories, FTS rows, chat sessions/messages, and editions consistently.
3. Deleting a story must also remove its FTS index row and any link rows that are no longer valid.
4. Support an explicit “delete all data” operational procedure in the README by stopping the stack and deleting the mounted application data directory. Clearly state that this is destructive.

## 7. Web/API contract

All routes are served by the same application listener. JSON endpoints use `/api/v1`.

### Read endpoints

- `GET /api/v1/healthz` — liveness only; returns HTTP 200 if the process can respond.
- `GET /api/v1/readyz` — readiness; confirms configuration and database availability. Ollama unavailability should be reported in status but should not make the app unready for archive/UI reads.
- `GET /api/v1/status` — poll, feed, summarization, and edition status.
- `GET /api/v1/feeds` — configured feed IDs/names/enabled state and latest poll result; no credentials.
- `GET /api/v1/editions` — paginated edition summaries, newest first.
- `GET /api/v1/editions/{date}` — one edition by local `YYYY-MM-DD` date.
- `GET /api/v1/stories` — paginated/filterable archive (`topic`, `source`, `from`, `to`, `q`).
- `GET /api/v1/stories/{id}` — one story and its summaries/source metadata.

### Write endpoints

- `POST /api/v1/chat` — request `{ "message": "...", "conversation_id": "optional-id" }`; response contains `conversation_id`, `answer`, and cited source records.
- `DELETE /api/v1/chat/{conversation_id}` — delete one local conversation.
- `POST /api/v1/admin/poll` — trigger one poll; local admin operation.
- `POST /api/v1/admin/edition` — trigger/regenerate current-day edition; local admin operation.

Write endpoints must enforce request body limits, validate inputs, and return structured errors. Since v1 is LAN-only and single-household, authentication is not required, but admin endpoints must be clearly documented as trusted-LAN operations and must not be made publicly reachable. Do not use GET requests for state-changing actions.

## 8. Data model

Use SQLite in `/data/news.db` and enable FTS5. Use migrations tracked in the repository. The implementation may add fields but must preserve these concepts:

- **Feed:** stable ID, display name, URL, enabled flag, topics, last attempt, last success, latest error summary, ETag/Last-Modified when available.
- **Story:** stable ID, feed ID, canonical URL, title, author, published/first-seen/updated timestamps, plain-text source excerpt, truncation flag, content hash, processing status.
- **StorySummary:** story ID, model, prompt version, short headline, summary, why-it-matters, topic labels, created timestamp, validation status.
- **Edition:** local edition date, generation timestamp, status, model, prompt version, structured editorial document, error summary.
- **EditionStory:** edition ID, story ID, section/topic, rank, citation/lead role.
- **Conversation and ChatMessage:** local conversation ID, timestamps, role, user text/assistant answer, structured cited story IDs.
- **FTS index:** story title, source excerpt, story summary, and edition text with stable row mapping and deletion synchronization.

Use UTC timestamps in storage and convert to configured timezone only for scheduling/display. Use transactions for ingestion, summary update, edition write, and cleanup operations. Back up the SQLite database by copying it with a SQLite-aware backup mechanism or a quiesced database; do not assume copying only the main file while WAL writes are active is safe.

## 9. Non-functional requirements

### 9.1 Resource limits and resilience

- One app instance and one Ollama instance.
- Default to one active model generation at a time.
- Use bounded queues and bounded HTTP request/response sizes.
- Feed errors, malformed model output, and transient Ollama failures are recoverable and isolated per item/feed.
- Application restart must resume pending summaries and scheduled work without duplicating stories or editions.
- Provide Compose resource limit examples, but do not hard-code a memory ceiling that prevents a selected model from running. Document how to tune model/context for the T470.
- Configure model context to a modest value by default (e.g. 4096 tokens) and allow operators to adjust it.

### 9.2 Security and privacy

- Run the Go container as a non-root UID/GID.
- Use a read-only root filesystem where practical; mount only `/data` writable and configuration read-only.
- Do not run privileged, mount the Docker socket, or use host networking.
- Ollama is not host-published in Compose. It is reachable only on the Compose network.
- Bind the application host port to an operator-configured LAN IP. Document that the router must not forward it.
- No secrets are required for the base service. Never commit feed credentials or registry credentials.
- Escape all feed-provided strings when rendering HTML. Use parameterized SQL. Sanitize source HTML before display/model use.
- Treat feed text and retrieved stories as adversarial prompt input. Do not grant the model shell, filesystem, network, or arbitrary tool access.
- Validate outbound feed URLs and redirect targets to reduce server-side request forgery risk.
- Set security headers appropriate to the same-origin UI (CSP, `X-Content-Type-Options`, frame policy, referrer policy) without breaking local functionality.

### 9.3 Accessibility and performance

- Main edition should render without client-side JavaScript where practical; chat/filter interactions may use small same-origin scripts.
- No third-party assets are loaded in the browser.
- Poll and status endpoints must remain responsive while a model is generating; background work must not block the HTTP server.
- Add database indexes for feed, published date, topic, edition date, and status queries.

## 10. Repository layout

Keep all code and deployable artifacts in a single repository, for example:

```text
.
├── cmd/home-news/              # executable entry point
├── internal/
│   ├── config/                 # env/YAML loading and validation
│   ├── feeds/                  # polling, parsing, normalization, SSRF guards
│   ├── store/                  # SQLite schema, migrations, repositories, FTS
│   ├── matching/               # topic matching and ranking
│   ├── llm/                    # Ollama client, prompts, structured output
│   ├── edition/                # daily issue assembly and citation validation
│   ├── chat/                   # retrieval, chat orchestration, citations
│   ├── web/                    # handlers, templates, middleware, assets
│   └── scheduler/              # polling, generation, retention scheduling
├── web/templates/              # Go HTML templates
├── web/static/                 # local CSS/JS/icons only
├── migrations/                 # ordered SQLite migrations
├── config/feeds.example.yaml
├── deploy/compose.yaml
├── .github/workflows/ci.yml
├── Dockerfile
├── Makefile
├── README.md
└── spec.md
```

The exact package split may change, but keep business logic separate from HTTP handlers and persistence. Do not create separate repositories for the app, Compose file, or CI workflow.

## 11. Container and deployment requirements

### 11.1 Application image

- Multi-stage Docker build: compile in a Go builder stage and copy only the executable, templates/static assets, CA certificates, and required timezone data to the runtime stage.
- Image architecture target: `linux/amd64` for the T470. CI may additionally publish `linux/arm64` only if the build is verified.
- Pin the Go base image by supported major/minor tag and use a non-root runtime user.
- Provide an OCI health check calling `/api/v1/healthz` or an equivalent lightweight endpoint.
- Tag image with immutable Git SHA and a human-friendly release tag. Avoid relying only on `latest` for deployment.
- Store app data in `/data`; never write durable state into the container layer.

### 11.2 Compose deployment

`deploy/compose.yaml` must include:

- `home-news` using an overridable image, e.g. `${HOME_NEWS_IMAGE:-ghcr.io/<owner>/<repo>:latest}`.
- `ollama` using the official image and persistent model storage mounted at `/root/.ollama`.
- A persistent app data bind mount/volume for `/data`.
- A read-only mount of `config/feeds.yaml` into the app container.
- An internal Compose network for app-to-Ollama traffic.
- App port binding using an overrideable LAN host IP and host port. Suggested example: `192.168.1.69:8091:8080`.
- No published Ollama port.
- `restart: unless-stopped` and health checks where supported.
- No privileged mode, host networking, Docker socket mount, or public-facing reverse proxy requirement.
- Document model initialization as a one-time operator command, e.g. `docker compose exec ollama ollama pull qwen3:4b` after starting Ollama. The model volume preserves the download across container/image updates.

The deployment README must explain that Open WebUI's current Homepage mapping already occupies host port 3000 in the user's existing stack; this service's suggested port is 8091 and can be changed. The Home News port should bind to the LAN address, not `0.0.0.0` on the host.

### 11.3 Configuration and first-run behavior

- Include `config/feeds.example.yaml`; document copying it to `config/feeds.yaml` and editing feed URLs/interests.
- Compose must not contain hard-coded secrets.
- On first run, create schema and FTS indexes automatically.
- If there are no enabled feeds, show an actionable setup page and keep health endpoints working; do not crash-loop.
- If Ollama is not ready yet, retry with backoff and keep the web UI/archive available.

## 12. CI/CD requirements

One CI workflow in the same repository must:

1. Run on pull requests and pushes to the default branch.
2. Check formatting (`gofmt`), run static analysis (`go vet`), run unit/integration tests, and build the application binary/image.
3. Build the Docker image for `linux/amd64` on the T470 target architecture.
4. On authorized default-branch pushes/tags, authenticate to GHCR using GitHub-provided token permissions and publish immutable SHA tags; publish a release tag for version tags.
5. Use least-privilege workflow permissions. Do not store server SSH keys or home-server credentials in CI; deployment/pull on the server is a separate operator action for v1.
6. Avoid building or publishing Ollama/model images. CI is only responsible for the Home News application image.
7. Add a README deployment example that updates the image tag and restarts the Compose service on the server.

## 13. Observability and operations

- Emit structured JSON logs to stdout with timestamp, severity, component, and operation identifiers.
- Include poll run ID, feed ID, story ID, edition date, and conversation ID where appropriate; never log full article content or full user prompts by default.
- Expose health, readiness, feed polling, queue counts, last edition status, and Ollama reachability in `/api/v1/status`.
- Log model name and prompt version, but not secrets or private config content.
- Document backup, restore, update, rollback, log review, and data deletion procedures.
- A backup should cover `/data` and `config/feeds.yaml`; Ollama model files are reproducible and need not be backed up unless the operator chooses to do so.

## 14. Testing and acceptance criteria

Implementation is complete when all of the following are true:

1. A clean clone can build the application image using the documented command.
2. CI builds and publishes the app image for `linux/amd64` without building the model image.
3. Compose starts both containers, initializes storage, and serves the UI on the configured LAN IP/port.
4. Ollama's API is not published to the host and is reachable by the application over Compose networking.
5. A test RSS/Atom/JSON feed fixture is parsed, normalized, and ingested; malformed feeds do not block other feeds.
6. Re-polling the same fixture does not create duplicate stories.
7. HTML source content is stripped/escaped and cannot inject executable markup into the app UI.
8. A mocked Ollama response can be validated, persisted, and rendered with the correct source link; invalid output follows retry/failure behavior.
9. The daily edition references only known story IDs and is idempotent for a local calendar date.
10. Chat retrieval returns relevant local stories and renders only citations from retrieved records; when no supporting records exist, the UI shows an insufficient-evidence answer.
11. App data and Ollama model files survive container recreation.
12. App restart resumes work without duplicate rows or duplicate current-day editions.
13. No source code or configuration sends article content to a cloud AI API, analytics endpoint, or third-party browser asset.
14. The example Compose file binds the app to a LAN IP and does not expose Ollama.
15. README explains the outbound-internet requirement for live feeds and states that no public inbound access is needed.

Tests should cover unit boundaries (config, normalization, matching, prompt/output validation), integration boundaries (SQLite migrations/FTS, feed ingestion, citation mapping), HTTP handlers, and a Compose smoke path using a mocked Ollama endpoint. Automated tests must not require downloading a model.

## 15. Suggested implementation workstreams

These are separable assignments for parallel implementation. Agree on exported interfaces and shared types before parallel edits; merge in dependency order.

### Workstream A — Config, domain types, and application skeleton

- Establish module, `cmd/home-news`, configuration schema, validation, structured logging, startup/shutdown, and health endpoints.
- Deliver config examples and interfaces used by other packages.
- Depends on: none. Blocks: all other workstreams.

### Workstream B — SQLite store, migrations, and archive search

- Implement schema, migration runner, repositories, transaction boundaries, retention cleanup, and FTS5 search.
- Add fixture-backed store tests.
- Depends on: domain types from A.

### Workstream C — Feed polling and ingestion

- Implement feed scheduler/poller, RSS/Atom/JSON parsing, HTTP limits, URL validation, normalization, deduplication, feed status, and pending summary state.
- Depends on: interfaces/types from A and store from B.

### Workstream D — Ollama client and per-story summarization

- Implement local Ollama API client, structured prompts/output validation, retries, sequential processing, and model status.
- Use a mock HTTP server in tests; tests must not pull models.
- Depends on: types from A and story storage from B/C.

### Workstream E — Edition generation and citation validation

- Implement interest matching/ranking, daily scheduler, structured edition output, citation validation, idempotency, and previous-edition fallback.
- Depends on: B, C, and D.

### Workstream F — Web UI, API, and grounded chat

- Implement templates/static assets, API handlers, archive/edition pages, status view, filters, chat retrieval/orchestration, and citation rendering.
- Depends on: stable service interfaces from A–E. UI layout can start using fixtures before integration.

### Workstream G — Docker, Compose, CI, and operational docs

- Implement multi-stage Dockerfile, non-root runtime, Compose network/volumes/LAN binding, GitHub Actions image publishing, and README procedures.
- Can start once executable/config entrypoint conventions are agreed; final smoke checks depend on integrated app.

## 16. Decisions fixed for v1

- One repository and one custom application image.
- Go application with server-rendered local web UI.
- Ollama is a separate Docker service; model weights are persisted separately from the app image.
- SQLite with FTS5; no external database service.
- Feed-provided text only; no full-article scraping in v1.
- Local model for per-story summaries, editions, and chat.
- Chat answers cite locally stored source stories.
- RSS polling is outbound; application UI is LAN-only; no public ingress.
- No n8n, Open WebUI, vector database, or separate static web server in v1.
- Default target is the T470's `linux/amd64` architecture and a small quantized model.

## 17. Open implementation choices (must not block initial scaffolding)

- Exact Go SQLite driver, provided it supports FTS5 in the container image.
- Exact HTML template/CSS approach, provided it has no runtime CDN dependency.
- Exact structured-output mechanism for Ollama, provided it is validated and tested against a mock.
- Exact ranking weights and default maximum edition stories; begin with simple recency + topic match + source diversity and expose them as constants/config.
- Exact retention values for chat versus article archive; preserve the 90-day story/edition default unless the operator overrides it.

