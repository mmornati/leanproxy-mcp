package compactor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type LLMClient interface {
	Distill(ctx context.Context, manifest RawManifest) (*DistilledManifest, error)
}

type OpenAIClient struct {
	endpoint   string
	apiKey     string
	model      string
	httpClient *http.Client
	logger     *slog.Logger
}

type OpenAIClientConfig struct {
	Endpoint string
	APIKey   string
	Model    string
}

func NewOpenAIClient(cfg OpenAIClientConfig, logger *slog.Logger) *OpenAIClient {
	if logger == nil {
		logger = slog.Default()
	}

	return &OpenAIClient{
		endpoint: cfg.Endpoint,
		apiKey:   cfg.APIKey,
		model:    cfg.Model,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		logger: logger,
	}
}

func (c *OpenAIClient) Distill(ctx context.Context, manifest RawManifest) (*DistilledManifest, error) {
	if c.endpoint == "" {
		return nil, fmt.Errorf("compactor: LLM endpoint not configured: %w", ctx.Err())
	}
	if c.apiKey == "" {
		return nil, fmt.Errorf("compactor: LLM API key not configured: %w", ctx.Err())
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			c.logger.Debug("retrying LLM request", "attempt", attempt+1, "backoff", backoff)

			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("compactor: context cancelled during retry: %w", ctx.Err())
			case <-time.After(backoff):
			}
		}

		result, err := c.doDistill(ctx, manifest)
		if err == nil {
			return result, nil
		}
		lastErr = err
		c.logger.Warn("LLM distillation attempt failed", "attempt", attempt+1, "error", err)
	}

	return nil, fmt.Errorf("compactor: LLM distillation failed after 3 attempts: %w", lastErr)
}

func (c *OpenAIClient) doDistill(ctx context.Context, manifest RawManifest) (*DistilledManifest, error) {
	prompt := BuildDistillationPrompt(manifest)

	requestBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": SystemPrompt},
			{"role": "user", "content": prompt},
		},
		"max_tokens": 2000,
		"temperature": 0.3,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("compactor: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("compactor: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("compactor: HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("compactor: LLM API returned status %d", resp.StatusCode)
	}

	var openAIResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("compactor: decode response: %w", err)
	}

	if len(openAIResp.Choices) == 0 {
		return nil, fmt.Errorf("compactor: LLM returned no choices")
	}

	content := openAIResp.Choices[0].Message.Content

	var distilled DistilledManifest
	if err := json.Unmarshal([]byte(content), &distilled); err != nil {
		return nil, fmt.Errorf("compactor: parse distilled manifest: %w", err)
	}

	distilled.OriginalHash = manifest.Hash()
	distilled.DistilledAt = time.Now()

	return &distilled, nil
}
