# Home News

Home News collects configured RSS/Atom/JSON feeds, creates locally generated summaries and newspaper editions with Ollama, and serves the archive and newspaper from one Go application. The application, database, model inference, and UI run on your server. Feed polling requires outbound internet access; no public inbound access or router port forwarding is needed.

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
   docker compose -f deploy/compose.yaml up -d
   docker compose -f deploy/compose.yaml exec ollama ollama pull qwen3:4b
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

Compose settings can be placed in `.env` next to the compose file's project root (commands above run from the repository root):

- `HOME_NEWS_IMAGE`: application image; use an immutable SHA tag to deploy/rollback.
- `HOME_NEWS_LAN_IP`: host address to bind, default `192.168.1.69`.
- `HOME_NEWS_PORT`: host port, default `8091`.
- `OLLAMA_MODEL`: local model name, default `qwen3:4b`.
- `APP_TIMEZONE`, `APP_POLL_INTERVAL`, `APP_DAILY_EDITION_TIME`, `APP_RETENTION_DAYS`, `OLLAMA_REQUEST_TIMEOUT`, and `APP_LOG_LEVEL`: optional runtime settings.

`config/feeds.yaml` is mounted read-only. Edit it and recreate/restart the app to apply changes:

```sh
docker compose -f deploy/compose.yaml restart home-news
```

The application stores its database in the `home_news_data` volume. Ollama stores downloaded models in `ollama_models`. Both survive container recreation.

## Operations

View status and logs:

```sh
docker compose -f deploy/compose.yaml ps
docker compose -f deploy/compose.yaml logs -f home-news
docker compose -f deploy/compose.yaml logs -f ollama
```

Update to a new build by changing `HOME_NEWS_IMAGE` in `.env` to the desired published SHA tag, then run:

```sh
docker compose -f deploy/compose.yaml pull home-news
docker compose -f deploy/compose.yaml up -d home-news
```

Rollback by setting the previous known-good SHA tag in `.env` and repeating those commands. The model volume is independent of app image updates.

Back up the database volume and `config/feeds.yaml`. For a consistent SQLite backup, stop the application first, then archive the volume and config together. Example with GNU tar:

```sh
docker compose -f deploy/compose.yaml stop home-news
docker run --rm -v home-news_home_news_data:/data:ro -v "$PWD:/backup" alpine \
  tar -czf /backup/home-news-backup.tgz -C /data .
cp config/feeds.yaml home-news-feeds.yaml.backup
docker compose -f deploy/compose.yaml start home-news
```

The exact volume prefix depends on the Compose project name; inspect it with `docker volume ls` and substitute if needed. To restore, stop Home News, restore the archive into the data volume, restore `config/feeds.yaml`, and start the app. Ollama model files can be downloaded again and do not need backup.

To remove all application data, stop the stack and remove its volumes. **This permanently deletes the local story archive, editions, and Ollama models.**

```sh
docker compose -f deploy/compose.yaml down
docker compose -f deploy/compose.yaml down --volumes
```

The first command alone preserves persistent volumes; the second is destructive.
