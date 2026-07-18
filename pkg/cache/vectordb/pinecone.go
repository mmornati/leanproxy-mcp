package vectordb

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

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

type pineconeStore struct {
	client  *http.Client
	baseURL string
	apiKey  string
	logger  *slog.Logger
	closed  bool
}

const pineconeBase = "https://api.pinecone.io"

func newPineconeStore(cfg *migrate.PineconeVectorConfig, logger *slog.Logger) (*pineconeStore, error) {
	if cfg == nil || cfg.Index == "" {
		return nil, fmt.Errorf("vectordb pinecone: index name required")
	}

	apiKeyEnv := cfg.APIKeyEnv
	if apiKeyEnv == "" {
		apiKeyEnv = "PINECONE_API_KEY"
	}

	apiKey := os.Getenv(apiKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("vectordb pinecone: %s not set", apiKeyEnv)
	}

	s := &pineconeStore{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		apiKey: apiKey,
		logger: logger,
	}

	host, err := s.describeIndex(context.Background(), cfg.Index)
	if err != nil {
		return nil, fmt.Errorf("vectordb pinecone: describe index: %w", err)
	}

	s.baseURL = "https://" + host

	logger.Info("vectordb pinecone initialized", "index", cfg.Index, "host", host)
	return s, nil
}

type pineconeIndexResponse struct {
	Name   string `json:"name"`
	Host   string `json:"host"`
	Status struct {
		Ready bool   `json:"ready"`
		State string `json:"state"`
	} `json:"status"`
}

type pineconeListResponse struct {
	Indexes []pineconeIndexResponse `json:"indexes"`
}

func (s *pineconeStore) describeIndex(ctx context.Context, index string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pineconeBase+"/indexes/"+index, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Api-Key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("describe index status %d: %s", resp.StatusCode, string(body))
	}

	var idx pineconeIndexResponse
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if idx.Host == "" {
		return "", fmt.Errorf("index %q not ready or not found", index)
	}

	return idx.Host, nil
}

type pineconeVector struct {
	ID     string                 `json:"id"`
	Values []float32              `json:"values"`
	Metadata map[string]string    `json:"metadata,omitempty"`
}

type pineconeUpsertRequest struct {
	Vectors   []pineconeVector `json:"vectors"`
	Namespace string           `json:"namespace,omitempty"`
}

type pineconeQueryRequest struct {
	Vector    []float32 `json:"vector"`
	TopK      int       `json:"topK"`
	Namespace string    `json:"namespace,omitempty"`
	IncludeValues bool `json:"includeValues"`
	IncludeMetadata bool `json:"includeMetadata"`
}

type pineconeQueryResponse struct {
	Matches []struct {
		ID       string            `json:"id"`
		Score    float64           `json:"score"`
		Values   []float32         `json:"values"`
		Metadata map[string]string `json:"metadata"`
	} `json:"matches"`
}

func (s *pineconeStore) Upsert(ctx context.Context, records ...VectorRecord) error {
	if len(records) == 0 {
		return nil
	}

	vectors := make([]pineconeVector, len(records))
	for i, rec := range records {
		vectors[i] = pineconeVector{
			ID:       rec.ID,
			Values:   rec.Vector,
			Metadata: rec.Metadata,
		}
	}

	body := pineconeUpsertRequest{Vectors: vectors}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/vectors/upsert", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("upsert request: %w", err)
	}
	req.Header.Set("Api-Key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("upsert request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upsert status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (s *pineconeStore) Search(ctx context.Context, vector []float32, k int) ([]SearchResult, error) {
	if k <= 0 {
		k = 10
	}

	body := pineconeQueryRequest{
		Vector:         vector,
		TopK:           k,
		IncludeValues:  true,
		IncludeMetadata: true,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/query", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	req.Header.Set("Api-Key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search status %d: %s", resp.StatusCode, string(respBody))
	}

	var qr pineconeQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]SearchResult, 0, len(qr.Matches))
	for _, m := range qr.Matches {
		if m.Metadata == nil {
			m.Metadata = make(map[string]string)
		}
		results = append(results, SearchResult{
			Record: VectorRecord{
				ID:       m.ID,
				Vector:   m.Values,
				Metadata: m.Metadata,
			},
			Score: m.Score,
		})
	}

	return results, nil
}

type pineconeDeleteRequest struct {
	IDs       []string `json:"ids"`
	Namespace string   `json:"namespace,omitempty"`
}

func (s *pineconeStore) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}

	body := pineconeDeleteRequest{IDs: ids}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/vectors/delete", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("delete request: %w", err)
	}
	req.Header.Set("Api-Key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (s *pineconeStore) Close() error {
	s.closed = true
	return nil
}
