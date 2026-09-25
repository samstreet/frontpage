package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const PromptVersion = "v1"

type Client struct {
	baseURL, model string
	http           *http.Client
}

func New(baseURL, model string, timeout time.Duration) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), model: model, http: &http.Client{Timeout: timeout, Transport: transport}}
}
func (c *Client) Model() string { return c.model }
func (c *Client) Available(ctx context.Context) bool {
	check, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(check, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := http.Client{Timeout: 2 * time.Second, Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type chatRequest struct {
	Model    string         `json:"model"`
	Messages []message      `json:"messages"`
	Stream   bool           `json:"stream"`
	Format   any            `json:"format,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
}
type chatResponse struct {
	Message message `json:"message"`
	Error   string  `json:"error,omitempty"`
}

func (c *Client) GenerateJSON(ctx context.Context, system, user string, out any) error {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		prompt := user
		if attempt > 0 {
			prompt = "Your previous response did not match the required JSON schema. Return only valid JSON matching this schema: " + mustSchema(out) + "\nOriginal task and evidence:\n" + user
		}
		raw, err := c.call(ctx, system, prompt)
		if err != nil {
			last = err
			break
		}
		clean := stripFences(raw)
		if err = json.Unmarshal([]byte(clean), out); err == nil {
			return nil
		}
		last = fmt.Errorf("invalid model JSON: %w", err)
	}
	return last
}
func mustSchema(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "JSON object"
	}
	return string(b)
}
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}
func (c *Client) GenerateText(ctx context.Context, system, user string) (string, error) {
	return c.call(ctx, system, user)
}
func (c *Client) call(ctx context.Context, system, user string) (string, error) {
	body := chatRequest{Model: c.model, Messages: []message{{Role: "system", Content: system}, {Role: "user", Content: user}}, Stream: false, Format: "json", Options: map[string]any{"temperature": 0.2, "num_ctx": 4096, "num_predict": 700}}
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama returned HTTP %d", resp.StatusCode)
	}
	var result chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", errors.New(result.Error)
	}
	if strings.TrimSpace(result.Message.Content) == "" {
		return "", errors.New("ollama returned an empty response")
	}
	return result.Message.Content, nil
}
