package sidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	ollamaGeneratePath = "/api/generate"
	ollamaTimeout      = 30 * time.Second
	maxResponseBytes   = 256 * 1024
)

type generateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type generateResponse struct {
	Model     string `json:"model"`
	Response  string `json:"response"`
	Done      bool   `json:"done"`
}

type Client struct {
	cfg           Config
	client        *http.Client
	logger        *slog.Logger
	fallbackCount atomic.Int64
}

func NewClient(cfg Config, logger *slog.Logger) (*Client, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	cfg.withDefaults()
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		cfg: cfg,
		client: &http.Client{
			Timeout: ollamaTimeout,
			Transport: &http.Transport{
				MaxIdleConns:    5,
				IdleConnTimeout: 30 * time.Second,
			},
		},
		logger: logger,
	}, nil
}

func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("sidecar client not initialized")
	}

	body := generateRequest{
		Model:  c.cfg.Model,
		Prompt: prompt,
		Stream: false,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("sidecar marshal: %w", err)
	}

	u, err := url.JoinPath(c.cfg.URL, ollamaGeneratePath)
	if err != nil {
		return "", fmt.Errorf("sidecar join path: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("sidecar request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		if isHostUnreachable(err) {
			return "", fmt.Errorf("sidecar unreachable: %w", err)
		}
		return "", fmt.Errorf("sidecar call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("sidecar: status %d: %s", resp.StatusCode, string(body))
	}

	var genResp generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return "", fmt.Errorf("sidecar decode: %w", err)
	}

	return genResp.Response, nil
}

func (c *Client) Redact(ctx context.Context, content string) string {
	if c == nil || !c.cfg.Enabled() {
		return content
	}

	prompt := fmt.Sprintf(
		`You are a data redaction assistant. Your task is to redact sensitive information from the following text. Replace any sensitive data such as API keys, passwords, tokens, secrets, private keys, database credentials, connection strings, personally identifiable information (PII), or any other confidential data with "[VALUE_REDACTED]". Return ONLY the redacted text, nothing else.

Text to redact:
%s`,
		content,
	)

	result, err := c.Generate(ctx, prompt)
	if err != nil {
		c.fallbackCount.Add(1)
		c.logger.Warn("sidecar: redaction fallback to aggressive redact",
			"error", err)
		return c.aggressiveRedact(content)
	}

	if result == "" {
		c.fallbackCount.Add(1)
		c.logger.Warn("sidecar: empty response, falling back to aggressive redact")
		return c.aggressiveRedact(content)
	}

	return result
}

func (c *Client) aggressiveRedact(content string) string {
	if len(content) == 0 {
		return content
	}
	return "[VALUE_REDACTED]"
}

func (c *Client) FallbackCount() int64 {
	if c == nil {
		return 0
	}
	return c.fallbackCount.Load()
}

func (c *Client) Provider() string {
	if c == nil {
		return ""
	}
	return c.cfg.Provider
}

func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.cfg.Model
}

func (c *Client) Healthy(ctx context.Context) bool {
	if c == nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	u, err := url.Parse(c.cfg.URL)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func (c *Client) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	c.client.CloseIdleConnections()
	return nil
}

func isHostUnreachable(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if strings.Contains(s, "connection refused") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "i/o timeout") {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}
