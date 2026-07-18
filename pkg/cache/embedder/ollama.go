package embedder

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
	"os"
	"strings"
	"time"
)

const (
	defaultOllamaURL   = "http://localhost:11434"
	defaultOllamaModel = "nomic-embed-text"
	ollamaEmbedPath    = "/api/embed"
	ollamaTimeout      = 5 * time.Second
)

type OllamaConfig struct {
	URL   string `yaml:"url"`
	Model string `yaml:"model"`
}

func (c *OllamaConfig) withDefaults() {
	if c.URL == "" {
		c.URL = defaultOllamaURL
	}
	if c.Model == "" {
		c.Model = defaultOllamaModel
	}
}

func validateOllamaURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("url must not be empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url must include a host")
	}
	return nil
}

type ollamaEmbedRequest struct {
	Model    string   `json:"model"`
	Input    []string `json:"input"`
	Truncate bool     `json:"truncate"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

type OllamaEmbedder struct {
	cfg    OllamaConfig
	client *http.Client
	logger *slog.Logger
}

func NewOllamaEmbedder(cfg OllamaConfig, logger *slog.Logger) (*OllamaEmbedder, error) {
	cfg.withDefaults()
	if err := validateOllamaURL(cfg.URL); err != nil {
		return nil, fmt.Errorf("embedder ollama: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &OllamaEmbedder{
		cfg: cfg,
		client: &http.Client{
			Timeout: ollamaTimeout,
			Transport: &http.Transport{
				MaxIdleConns:    10,
				IdleConnTimeout: 30 * time.Second,
			},
		},
		logger: logger,
	}, nil
}

func (e *OllamaEmbedder) Embed(ctx context.Context, req EmbedRequest) (Embedding, error) {
	input := req.Input()
	if len(input) > MaxPayloadBytes {
		return Embedding{}, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(input), MaxPayloadBytes)
	}
	e.logger.Debug("ollama embed", "tool", req.ToolName, "input_length", len(input))

	body := ollamaEmbedRequest{
		Model:    e.cfg.Model,
		Input:    []string{input},
		Truncate: true,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Embedding{}, fmt.Errorf("ollama embed marshal: %w", err)
	}

	u, err := url.JoinPath(e.cfg.URL, ollamaEmbedPath)
	if err != nil {
		return Embedding{}, fmt.Errorf("ollama embed join path: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return Embedding{}, fmt.Errorf("ollama embed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(httpReq)
	if err != nil {
		if isHostUnreachable(err) {
			return Embedding{}, fmt.Errorf("%w: %v", ErrEmbedderUnavailable, err)
		}
		return Embedding{}, fmt.Errorf("ollama embed call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return Embedding{}, fmt.Errorf("ollama embed: status %d: %s", resp.StatusCode, string(respBody))
	}

	var embedResp ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return Embedding{}, fmt.Errorf("ollama embed decode: %w", err)
	}

	if len(embedResp.Embeddings) == 0 || len(embedResp.Embeddings[0]) == 0 {
		return Embedding{}, errors.New("ollama embed: empty vector in response")
	}

	return Embedding{
		Vector: embedResp.Embeddings[0],
		Model:  e.cfg.Model,
	}, nil
}

func (e *OllamaEmbedder) Provider() Provider {
	return ProviderOllama
}

func (e *OllamaEmbedder) Close() error {
	e.client.CloseIdleConnections()
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

// IsAPIAvailable probes the Ollama server with a GET on the root URL.
// Returns nil if reachable, an error otherwise. Used by startup checks.
func IsAPIAvailable(ctx context.Context, baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("ollama probe: %w", err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("ollama probe: %w", err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama probe: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 500 {
		return fmt.Errorf("ollama probe: status %d", resp.StatusCode)
	}
	_ = os.Getenv("") // keep os import for future probe extension
	return nil
}
