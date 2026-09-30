# Home News

Home News collects configured RSS/Atom/JSON feeds, creates locally generated summaries and newspaper editions with Ollama, and serves the archive and newspaper from one Go application. The daily edition is set as an actual newspaper: a nameplate, columned copy, photographs and numbered pages that you can turn on screen or print to A4. The application, database, model inference, and UI run on your server. Feed polling requires outbound internet access; no public inbound access or router port forwarding is needed.

## Requirements

- Docker Engine and Docker Compose v2 on a Linux `amd64` host.
- A stable server LAN address. The example is `192.168.1.69`.
- Outbound network access to configured feeds, GHCR when pulling the app image, and Ollama model distribution on first setup.
- Enough disk space for the application database and Ollama model. Model files persist in a separate Docker volume.

## First deployment

From the repository root on the server:

1. Copy and edit the feed configuration:

   ```sh
   cp config/feeds.example.yaml config/feeds.yaml
   $EDITOR config/feeds.yaml
   ```

2. Create a `.env` file. Set the image to a published immutable SHA tag from the CI run, and set the server's LAN address:

   ```dotenv
   HOME_NEWS_IMAGE=ghcr.io/OWNER/REPOSITORY:sha-COMMIT_SHA
   HOME_NEWS_LAN_IP=192.168.1.69
   HOME_NEWS_PORT=8091
   OLLAMA_MODEL=qwen3:4b
   APP_TIMEZONE=Europe/London
   ```

   Replace `OWNER/REPOSITORY` with the lowercase GitHub owner and repository. If the GHCR package is private, authenticate Docker on the server before pulling. The workflow publishes SHA tags on pushes to `main` and release tags on `v*` tags.

3. Start the stack and download the chosen model once:

   ```sh
   docker compose --env-file .env -f deploy/compose.yaml up -d
   docker compose --env-file .env -f deploy/compose.yaml exec ollama ollama pull qwen3:4b
   ```

4. Open `http://192.168.1.69:8091` from a device on your home network. Change the IP/port in `.env` to match your network. The sample port `8091` avoids the `3000` port already used by Homepage in the existing stack.

The application port is published only on `HOME_NEWS_LAN_IP`; Ollama has no published host port and is reachable only over the Compose network. Do not configure router port forwarding for this service. Docker host firewall rules should also limit access to your trusted home network.

## Local image build

Build the same application image locally from the repository root:

```sh
docker build --platform linux/amd64 -t home-news:local .
```

CI checks `gofmt`, runs `go vet` and `go test`, builds the binary and image, then publishes only the application image to GHCR. CI does not build or publish Ollama or model weights. GitHub Actions needs package write permission enabled for the repository.

## Configuration

Compose settings are stored in the repository-root `.env`. Because the Compose file
is under `deploy/`, commands explicitly pass `--env-file .env`; keep running them
from the repository root.

- `HOME_NEWS_IMAGE`: application image; use an immutable SHA tag to deploy/rollback.
- `HOME_NEWS_LAN_IP`: host address to bind, default `192.168.1.69`.
- `HOME_NEWS_PORT`: host port, default `8091`.
- `OLLAMA_MODEL`: local model name, default `qwen3:4b`.
- `APP_TIMEZONE`, `APP_POLL_INTERVAL`, `APP_DAILY_EDITION_TIME`, `APP_RETENTION_DAYS`, `OLLAMA_REQUEST_TIMEOUT`, and `APP_LOG_LEVEL`: optional runtime settings.
- `APP_FETCH_IMAGES`: `true` (default) or `false`. Set `false` to stop downloading photographs entirely; editions are then set without pictures.

`config/feeds.yaml` is mounted read-only. Edit it and recreate/restart the app to apply changes:

```sh
docker compose --env-file .env -f deploy/compose.yaml restart home-news
```

The application stores its database in the `home_news_data` volume. Ollama stores downloaded models in `ollama_models`. Both survive container recreation.

### Birthdays, anniversaries, and other occasions

Add an optional top-level `events` list to `config/feeds.yaml`, alongside `feeds` and `interests`:

```yaml
events:
  - name: "Alex's birthday"
    type: birthday
    date: "1990-05-14"
    description: "Happy birthday, Alex!"
  - name: "Our wedding anniversary"
    type: anniversary
    date: "2012-08-23"
  - name: "Family day"
    date: "07-12"
  - name: "Graduation celebration"
    date: "2027-07-10"
    repeat: once
```

Events appear in **Occasions & celebrations** when their month and day match the edition's date in `APP_TIMEZONE`, in configuration order. They repeat yearly by default (`repeat: yearly`); use `repeat: once` with a full date for a one-off occasion. `type` can be `birthday`, `anniversary`, or `event` (the default), and `description` is optional.

Use a quoted `YYYY-MM-DD` date with the original birth or anniversary year to show a milestone such as “Turns 36 today” or “14 years today”. Use quoted `MM-DD` to omit the year and milestone. Events never appear before their original year. February 29 events appear only on February 29 in leap years. Invalid dates or event types prevent startup with a configuration error identifying the entry.

Events are added directly from your configuration without an Ollama request and saved with the edition, so archived editions keep their original notices and milestones. An edition can contain just occasions while waiting for summarized news; later polls can add news to it. Existing editions without events continue to work.

After editing the configuration, restart `home-news`. The next newly generated edition uses the changes. To update an edition already generated for today, call the regeneration endpoint after restarting (adjust the address to match your server):

```sh
curl -X POST http://192.168.1.69:8091/api/v1/admin/edition
```

Use `events: []` to leave the section disabled.

## Reading and printing the newspaper

The front page opens as a stack of A4 sheets. The controls above the paper are:

- **‹ / ›** and the page count: turn pages. The arrow keys, `Page Up`/`Page Down` and `Home`/`End` do the same.
- **Reading view**: abandon the pages and scroll one plain column instead. This is the default on a phone, where a whole A4 sheet is too small to read; switch back with **Page view**.
- **Print / Save as PDF**: opens the browser's print dialogue.

Pages are composed in millimetres against A4, so what is on the screen is what comes out of the printer, page break for page break. Print with margins set to **None** (the sheet carries its own margins) and background graphics enabled, which is what "Save as PDF" in Chrome and Firefox does by default. The result is one PDF page per sheet, with running folios, section bands, an "Inside today" contents box with real page numbers, and "Continued on page N" lines where an article runs over.

The page composer is JavaScript. Without it the edition still renders as a single readable column, and printing falls back to a plain columned layout — the copy and the photographs are all there, only the page breaks are the browser's choice rather than the paper's.

## Photographs

Home News looks for a lead photograph on every story, checking the feed item's image element, any image enclosure, Media RSS `media:content` and `media:thumbnail`, and finally any `<img>` in the item's own HTML. Tracking pixels, spacers and formats that cannot be decoded are skipped.

A candidate is then **downloaded by the server, decoded, resampled to at most 1400px, flattened onto white and re-encoded as JPEG**, and the result is stored in the application database. Pages reference `/media/<story id>` on this server only:

- Your browser never contacts a publisher's CDN, so reading the paper does not tell anyone what you read.
- The content security policy stays at `img-src 'self' data:`; nothing was loosened to make pictures work.
- Photographs are present in the PDF whether or not you are online when you print.
- Downloads use the same guarded HTTP client as feed polling, which refuses private, loopback and link-local addresses at both request and dial time.

JPEG, PNG and GIF are decoded. **WebP is not** — it is absent from the Go standard library, and a WebP-only story is simply set without a picture rather than adding a third-party decoder. A download is retried at most three times and then left alone.

Photographs are fetched in batches of twelve per poll, so a large backlog is worked through over several cycles rather than stalling one. Stored images are deleted with their stories under `APP_RETENTION_DAYS`. Budget roughly 150–400 KB per photograph when sizing the data volume. **Sources & status** reports how many photographs are stored and how many are still queued. Set `APP_FETCH_IMAGES=false` to turn the whole pipeline off.

## Operations

View status and logs:

```sh
docker compose --env-file .env -f deploy/compose.yaml ps
docker compose --env-file .env -f deploy/compose.yaml logs -f home-news
docker compose --env-file .env -f deploy/compose.yaml logs -f ollama
```

Update to a new build by changing `HOME_NEWS_IMAGE` in `.env` to the desired published SHA tag, then run:

```sh
docker compose --env-file .env -f deploy/compose.yaml pull home-news
docker compose --env-file .env -f deploy/compose.yaml up -d home-news
```

Rollback by setting the previous known-good SHA tag in `.env` and repeating those commands. The model volume is independent of app image updates.

Back up the database volume and `config/feeds.yaml`. For a consistent SQLite backup, stop the application first, then archive the volume and config together. Example with GNU tar:

```sh
docker compose --env-file .env -f deploy/compose.yaml stop home-news
docker run --rm -v home-news_home_news_data:/data:ro -v "$PWD:/backup" alpine \
  tar -czf /backup/home-news-backup.tgz -C /data .
cp config/feeds.yaml home-news-feeds.yaml.backup
docker compose --env-file .env -f deploy/compose.yaml start home-news
```

The exact volume prefix depends on the Compose project name; inspect it with `docker volume ls` and substitute if needed. To restore, stop Home News, restore the archive into the data volume, restore `config/feeds.yaml`, and start the app. Ollama model files can be downloaded again and do not need backup.

To remove all application data, stop the stack and remove its volumes. **This permanently deletes the local story archive, editions, and Ollama models.**

```sh
docker compose --env-file .env -f deploy/compose.yaml down
docker compose --env-file .env -f deploy/compose.yaml down --volumes
```

The first command alone preserves persistent volumes; the second is destructive.
