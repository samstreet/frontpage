package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/home-news/home-news/internal/config"
	"github.com/home-news/home-news/internal/llm"
	"github.com/home-news/home-news/internal/news"
	"github.com/home-news/home-news/internal/store"
	"github.com/home-news/home-news/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("home-news stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	level := new(slog.LevelVar)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch cfg.LogLevel {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}
	if err := os.MkdirAll(cfg.DataDir, 0750); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(cfg.DataDir, "news.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	model := llm.New(cfg.OllamaURL, cfg.OllamaModel, cfg.OllamaTimeout)
	service := news.New(cfg, db, model, logger)
	if err := db.SyncFeeds(ctx, cfg.Source.Feeds); err != nil {
		return err
	}
	webServer, err := web.New(service, logger)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.Addr, Handler: webServer.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 6 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("HTTP server starting", "addr", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			serverErr <- err
			cancel()
		}
	}()
	go pollLoop(ctx, service, cfg, logger)
	go editionLoop(ctx, service, cfg, logger)
	go cleanupLoop(ctx, service, logger)
	<-ctx.Done()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdown); err != nil {
		return err
	}
	select {
	case err := <-serverErr:
		return err
	default:
		return nil
	}
}

func pollLoop(ctx context.Context, service *news.Service, cfg config.Config, log *slog.Logger) {
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	run := func() {
		job, cancel := context.WithTimeout(ctx, cfg.OllamaTimeout*3)
		defer cancel()
		if err := service.Poll(job); err != nil {
			log.Warn("poll cycle completed with errors", "error", err)
		} else {
			log.Info("poll cycle completed")
		}
		if err := service.GenerateEdition(job, false); err != nil {
			log.Warn("edition generation failed", "error", err)
		}
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
func editionLoop(ctx context.Context, service *news.Service, cfg config.Config, log *slog.Logger) {
	for {
		wait := time.Until(nextEdition(time.Now(), cfg.Location, cfg.EditionTime))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			job, cancel := context.WithTimeout(ctx, cfg.OllamaTimeout*3)
			if err := service.GenerateEdition(job, true); err != nil {
				log.Warn("scheduled edition failed", "error", err)
			}
			cancel()
		}
	}
}
func nextEdition(now time.Time, loc *time.Location, clock string) time.Time {
	local := now.In(loc)
	var hour, minute int
	_, _ = fmtSscanf(clock, &hour, &minute)
	next := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
func fmtSscanf(s string, hour, minute *int) (int, error) { return fmt.Sscanf(s, "%d:%d", hour, minute) }
func cleanupLoop(ctx context.Context, service *news.Service, log *slog.Logger) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := service.Cleanup(ctx); err != nil {
				log.Warn("retention cleanup failed", "error", err)
			}
		}
	}
}
