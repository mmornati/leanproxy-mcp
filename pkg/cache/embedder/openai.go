package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

const (
	envOpenAIAPIKey    = "OPENAI_API_KEY"
	defaultOpenAIModel = "text-embedding-3-small"
	openAIEmbedURL     = "https://api.openai.com/v1/embeddings"
	openAITimeout      = 10 * time.Second
)

type OpenAIConfig struct {
	Model  string `yaml:"model"`
	APIKey string `yaml:"api_key,omitempty"`
}

func (c *OpenAIConfig) Validate() error {
	if c.Model == "" {
		c.Model = defaultOpenAIModel
	}
	key := c.APIKey
	if key == "" {
		key = os.Getenv(envOpenAIAPIKey)
	}
	if key == "" {
		return fmt.Errorf("embedder openai: %s env var not set and no api_key in config", envOpenAIAPIKey)
	}
	return nil
}

func (c *OpenAIConfig) apiKey() string {
	if c.APIKey != "" {
		return c.APIKey
	}
	return os.Getenv(envOpenAIAPIKey)
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
	client *http.Client
	logger *slog.Logger
}

func NewOpenAIEmbedder(cfg OpenAIConfig, logger *slog.Logger) (*OpenAIEmbedder, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &OpenAIEmbedder{
		cfg: cfg,
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
	httpReq.Header.Set("Authorization", "Bearer "+e.cfg.apiKey())

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return Embedding{}, fmt.Errorf("openai embed call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return Embedding{}, fmt.Errorf("openai embed: status %d: %s", resp.StatusCode, string(respBody))
	}

	var embedResp openAIEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return Embedding{}, fmt.Errorf("openai embed decode: %w", err)
	}

	if len(embedResp.Data) == 0 {
		return Embedding{}, fmt.Errorf("openai embed: empty data response")
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
