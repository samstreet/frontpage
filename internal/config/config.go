package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/home-news/home-news/internal/domain"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Addr              string
	DataDir           string
	ConfigFile        string
	Timezone          string
	PollInterval      time.Duration
	EditionTime       string
	RetentionDays     int
	ChatRetentionDays int
	OllamaURL         string
	OllamaModel       string
	OllamaTimeout     time.Duration
	LogLevel          string
	MaxItemsPerFeed   int
	MaxItemChars      int
	ChatResultLimit   int
	ChatContextChars  int
	Source            domain.SourceConfig
	Location          *time.Location
}

func Load() (Config, error) {
	c := Config{
		Addr: ":8080", DataDir: "/data", ConfigFile: "/config/feeds.yaml",
		Timezone: "Europe/London", PollInterval: 30 * time.Minute, EditionTime: "06:00",
		RetentionDays: 90, ChatRetentionDays: 30, OllamaURL: "http://ollama:11434",
		OllamaModel: "qwen3:4b", OllamaTimeout: 5 * time.Minute, LogLevel: "info",
		MaxItemsPerFeed: 50, MaxItemChars: 12000, ChatResultLimit: 8, ChatContextChars: 12000,
	}
	setString(&c.Addr, "APP_ADDR")
	setString(&c.DataDir, "APP_DATA_DIR")
	setString(&c.ConfigFile, "APP_CONFIG_FILE")
	setString(&c.Timezone, "APP_TIMEZONE")
	setString(&c.EditionTime, "APP_DAILY_EDITION_TIME")
	setString(&c.OllamaURL, "OLLAMA_BASE_URL")
	setString(&c.OllamaModel, "OLLAMA_MODEL")
	setString(&c.LogLevel, "APP_LOG_LEVEL")
	if err := setDuration(&c.PollInterval, "APP_POLL_INTERVAL"); err != nil {
		return c, err
	}
	if err := setDuration(&c.OllamaTimeout, "OLLAMA_REQUEST_TIMEOUT"); err != nil {
		return c, err
	}
	for key, target := range map[string]*int{
		"APP_RETENTION_DAYS": &c.RetentionDays, "APP_CHAT_RETENTION_DAYS": &c.ChatRetentionDays,
		"APP_MAX_ITEMS_PER_FEED": &c.MaxItemsPerFeed, "APP_MAX_ITEM_CHARS": &c.MaxItemChars,
		"APP_CHAT_RESULT_LIMIT": &c.ChatResultLimit, "APP_CHAT_CONTEXT_CHARS": &c.ChatContextChars,
	} {
		if value := os.Getenv(key); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				return c, fmt.Errorf("%s: %w", key, err)
			}
			*target = n
		}
	}
	if c.ConfigFile != "" {
		b, err := os.ReadFile(c.ConfigFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return c, fmt.Errorf("read config: %w", err)
		}
		if err == nil {
			if err := yaml.Unmarshal(b, &c.Source); err != nil {
				return c, fmt.Errorf("parse config: %w", err)
			}
		}
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	c.Location, _ = time.LoadLocation(c.Timezone)
	return c, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" || strings.TrimSpace(c.DataDir) == "" {
		return errors.New("APP_ADDR and APP_DATA_DIR must not be empty")
	}
	if c.PollInterval < time.Minute {
		return errors.New("APP_POLL_INTERVAL must be at least 1m")
	}
	if _, err := time.Parse("15:04", c.EditionTime); err != nil {
		return fmt.Errorf("APP_DAILY_EDITION_TIME must be HH:MM: %w", err)
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("APP_TIMEZONE: %w", err)
	}
	if c.RetentionDays < 1 || c.ChatRetentionDays < 1 || c.MaxItemsPerFeed < 1 || c.MaxItemChars < 1000 {
		return errors.New("retention and ingestion limits must be positive (item text limit at least 1000)")
	}
	if c.MaxItemsPerFeed > 500 || c.MaxItemChars > 50000 {
		return errors.New("feed limits are too large (maximum 500 items per feed and 50000 source characters per item)")
	}
	if c.ChatResultLimit < 1 || c.ChatResultLimit > 25 || c.ChatContextChars < 1000 {
		return errors.New("chat limits are invalid")
	}
	if c.ChatContextChars > 30000 {
		return errors.New("APP_CHAT_CONTEXT_CHARS must not exceed 30000")
	}
	if c.OllamaTimeout <= 0 {
		return errors.New("OLLAMA_REQUEST_TIMEOUT must be positive")
	}
	if c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error" {
		return errors.New("APP_LOG_LEVEL must be debug, info, warn, or error")
	}
	ollamaURL, err := url.Parse(c.OllamaURL)
	if err != nil || (ollamaURL.Scheme != "http" && ollamaURL.Scheme != "https") || ollamaURL.Hostname() == "" {
		return errors.New("OLLAMA_BASE_URL must be an HTTP URL for the local Ollama service")
	}
	host := strings.TrimSuffix(strings.ToLower(ollamaURL.Hostname()), ".")
	if host != "ollama" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return errors.New("OLLAMA_BASE_URL must target the local Ollama service (ollama, localhost, or loopback)")
	}
	if ollamaURL.User != nil || (ollamaURL.Path != "" && ollamaURL.Path != "/") {
		return errors.New("OLLAMA_BASE_URL must not contain credentials or a path")
	}
	seen := map[string]bool{}
	interestIDs := map[string]bool{}
	for _, interest := range c.Source.Interests {
		if strings.TrimSpace(interest.ID) == "" || strings.TrimSpace(interest.Name) == "" {
			return errors.New("each interest needs an id and name")
		}
		if interestIDs[interest.ID] {
			return fmt.Errorf("duplicate interest id %q", interest.ID)
		}
		interestIDs[interest.ID] = true
	}
	for _, f := range c.Source.Feeds {
		if strings.TrimSpace(f.ID) == "" || strings.TrimSpace(f.Name) == "" {
			return errors.New("each feed needs an id and name")
		}
		if seen[f.ID] {
			return fmt.Errorf("duplicate feed id %q", f.ID)
		}
		seen[f.ID] = true
		if f.Enabled {
			if err := validateFeedURL(f.URL); err != nil {
				return fmt.Errorf("feed %s: %w", f.ID, err)
			}
		}
		for _, topic := range f.Topics {
			if !interestIDs[topic] {
				return fmt.Errorf("feed %s references unknown interest %q", f.ID, topic)
			}
		}
	}
	return nil
}

func validateFeedURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return errors.New("URL must use http or https")
	}
	if u.Hostname() == "" || u.User != nil {
		return errors.New("URL must have a host and must not contain credentials")
	}
	return nil
}

func setString(target *string, key string) {
	if v := os.Getenv(key); v != "" {
		*target = v
	}
}
func setDuration(target *time.Duration, key string) error {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*target = d
	}
	return nil
}
