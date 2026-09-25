# Build a static Linux binary. Keep the Go version aligned with ci.yml.
FROM golang:1.25-alpine AS builder

WORKDIR /src
RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath -ldflags="-s -w" -o /out/home-news ./cmd/home-news

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 home-news \
    && adduser -S -D -H -u 10001 -G home-news home-news \
    && mkdir -p /data /config \
    && chown home-news:home-news /data

COPY --from=builder --chown=0:0 /out/home-news /usr/local/bin/home-news
# Keep these paths available for implementations that load templates/static
# files at runtime. Embedded assets can also be included in the executable.
COPY --from=builder --chown=0:0 /src/web/templates /app/web/templates
COPY --from=builder --chown=0:0 /src/web/static /app/web/static

ENV APP_ADDR=:8080 \
    APP_DATA_DIR=/data \
    APP_CONFIG_FILE=/config/feeds.yaml
WORKDIR /app
USER 10001:10001
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=20s --retries=3 \
  CMD wget -q -T 2 -O /dev/null http://127.0.0.1:8080/api/v1/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/home-news"]
