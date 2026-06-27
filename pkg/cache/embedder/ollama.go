package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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

func (c *OllamaConfig) Validate() error {
	if c.URL == "" {
		c.URL = defaultOllamaURL
	}
	if _, err := url.Parse(c.URL); err != nil {
		return fmt.Errorf("embedder ollama: invalid url %q: %w", c.URL, err)
	}
	if c.Model == "" {
		c.Model = defaultOllamaModel
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
	if err := cfg.Validate(); err != nil {
		return nil, err
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

	u, _ := url.JoinPath(e.cfg.URL, ollamaEmbedPath)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return Embedding{}, fmt.Errorf("ollama embed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return Embedding{}, fmt.Errorf("ollama embed call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return Embedding{}, fmt.Errorf("ollama embed: status %d: %s", resp.StatusCode, string(respBody))
	}

	var embedResp ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return Embedding{}, fmt.Errorf("ollama embed decode: %w", err)
	}

	if len(embedResp.Embeddings) == 0 {
		return Embedding{}, fmt.Errorf("ollama embed: empty embeddings response")
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
