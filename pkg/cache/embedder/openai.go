package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	envOpenAIAPIKey    = "OPENAI_API_KEY"
	defaultOpenAIModel = "text-embedding-3-small"
	openAIEmbedURL     = "https://api.openai.com/v1/embeddings"
	openAITimeout      = 10 * time.Second
	maxErrorBodyBytes  = 4 * 1024
)

type OpenAIConfig struct {
	Model  string `yaml:"model"`
	APIKey string `yaml:"api_key,omitempty"`
}

func (c *OpenAIConfig) withDefaults() {
	if c.Model == "" {
		c.Model = defaultOpenAIModel
	}
}

func (c *OpenAIConfig) validateKey() error {
	key := strings.TrimSpace(c.APIKey)
	if key == "" {
		key = strings.TrimSpace(os.Getenv(envOpenAIAPIKey))
	}
	if key == "" {
		return fmt.Errorf("embedder openai: %s env var not set and no api_key in config", envOpenAIAPIKey)
	}
	return nil
}

func (c *OpenAIConfig) resolvedAPIKey() string {
	key := strings.TrimSpace(c.APIKey)
	if key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv(envOpenAIAPIKey))
}

type openAIEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []openAIEmbedData `json:"data"`
}

type openAIEmbedData struct {
	Index  int       `json:"index"`
	Vector []float32 `json:"embedding"`
}

type OpenAIEmbedder struct {
	cfg    OpenAIConfig
	apiKey string
	client *http.Client
	logger *slog.Logger
}

func NewOpenAIEmbedder(cfg OpenAIConfig, logger *slog.Logger) (*OpenAIEmbedder, error) {
	cfg.withDefaults()
	if err := cfg.validateKey(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	key := cfg.resolvedAPIKey()
	if key == "" {
		return nil, errors.New("embedder openai: resolved api key is empty")
	}
	return &OpenAIEmbedder{
		cfg:    cfg,
		apiKey: key,
		client: &http.Client{
			Timeout: openAITimeout,
			Transport: &http.Transport{
				MaxIdleConns:    10,
				IdleConnTimeout: 30 * time.Second,
			},
		},
		logger: logger,
	}, nil
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, req EmbedRequest) (Embedding, error) {
	input := req.Input()
	if len(input) > MaxPayloadBytes {
		return Embedding{}, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(input), MaxPayloadBytes)
	}

	body := openAIEmbedRequest{
		Model: e.cfg.Model,
		Input: []string{input},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Embedding{}, fmt.Errorf("openai embed marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIEmbedURL, bytes.NewReader(payload))
	if err != nil {
		return Embedding{}, fmt.Errorf("openai embed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return Embedding{}, fmt.Errorf("%w: %v", ErrEmbedderUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return Embedding{}, fmt.Errorf("openai embed: status %d: %s", resp.StatusCode, string(respBody))
	}

	var embedResp openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return Embedding{}, fmt.Errorf("openai embed decode: %w", err)
	}

	if len(embedResp.Data) == 0 || len(embedResp.Data[0].Vector) == 0 {
		return Embedding{}, errors.New("openai embed: empty vector in response")
	}

	return Embedding{
		Vector: embedResp.Data[0].Vector,
		Model:  e.cfg.Model,
	}, nil
}

func (e *OpenAIEmbedder) Provider() Provider {
	return ProviderOpenAI
}

func (e *OpenAIEmbedder) Close() error {
	e.client.CloseIdleConnections()
	return nil
}
