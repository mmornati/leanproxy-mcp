package vectordb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

type qdrantStore struct {
	client     *http.Client
	baseURL    string
	apiKey     string
	collection string
	logger     *slog.Logger
	closed     bool
}

func newQdrantStore(cfg *migrate.QdrantVectorConfig, logger *slog.Logger) (*qdrantStore, error) {
	if cfg == nil || cfg.URL == "" {
		return nil, fmt.Errorf("vectordb qdrant: url required")
	}

	collection := cfg.Collection
	if collection == "" {
		collection = "leanproxy_cache"
	}

	s := &qdrantStore{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseURL:    stringsTrimRight(cfg.URL, "/"),
		apiKey:     cfg.APIKey,
		collection: collection,
		logger:     logger,
	}

	if err := s.validateConnection(context.Background()); err != nil {
		return nil, fmt.Errorf("vectordb qdrant: connection failed: %w", err)
	}

	logger.Info("vectordb qdrant initialized", "url", cfg.URL, "collection", collection)
	return s, nil
}

func stringsTrimRight(s, cutset string) string {
	for len(s) > 0 && len(cutset) > 0 && s[len(s)-1] == cutset[len(cutset)-1] {
		s = s[:len(s)-1]
	}
	return s
}

func (s *qdrantStore) validateConnection(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/collections/"+s.collection, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	s.setHeaders(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		if err := s.createCollection(ctx); err != nil {
			return fmt.Errorf("create collection: %w", err)
		}
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (s *qdrantStore) createCollection(ctx context.Context) error {
	body := map[string]interface{}{
		"name": s.collection,
		"vectors": map[string]interface{}{
			"size":     1536,
			"distance": "Cosine",
		},
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.baseURL+"/collections/"+s.collection, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create collection request: %w", err)
	}
	s.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("create collection request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create collection status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

type qdrantPoint struct {
	ID      string                 `json:"id"`
	Vector  []float32              `json:"vector"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

func (s *qdrantStore) Upsert(ctx context.Context, records ...VectorRecord) error {
	if len(records) == 0 {
		return nil
	}

	points := make([]qdrantPoint, len(records))
	for i, rec := range records {
		payload := make(map[string]interface{})
		for k, v := range rec.Metadata {
			payload[k] = v
		}
		points[i] = qdrantPoint{
			ID:      rec.ID,
			Vector:  rec.Vector,
			Payload: payload,
		}
	}

	body := map[string]interface{}{
		"points": points,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.baseURL+"/collections/"+s.collection+"/points", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("upsert request: %w", err)
	}
	s.setHeaders(req)
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

type qdrantSearchResponse struct {
	Result []struct {
		ID     string                 `json:"id"`
		Score  float64                `json:"score"`
		Vector []float32              `json:"vector,omitempty"`
		Payload map[string]interface{} `json:"payload,omitempty"`
	} `json:"result"`
}

func (s *qdrantStore) Search(ctx context.Context, vector []float32, k int) ([]SearchResult, error) {
	if k <= 0 {
		k = 10
	}

	body := map[string]interface{}{
		"vector": vector,
		"limit":  k,
		"with_payload": true,
		"with_vector":  true,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/collections/"+s.collection+"/points/search", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	s.setHeaders(req)
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

	var sr qdrantSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]SearchResult, 0, len(sr.Result))
	for _, r := range sr.Result {
		meta := make(map[string]string)
		for k, v := range r.Payload {
			meta[k] = fmt.Sprintf("%v", v)
		}

		results = append(results, SearchResult{
			Record: VectorRecord{
				ID:       r.ID,
				Vector:   r.Vector,
				Metadata: meta,
			},
			Score: r.Score,
		})
	}

	return results, nil
}

type qdrantDeleteResponse struct {
	Result struct {
		Status string `json:"status"`
	} `json:"result"`
}

func (s *qdrantStore) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}

	body := map[string]interface{}{
		"points": ids,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/collections/"+s.collection+"/points/delete", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("delete request: %w", err)
	}
	s.setHeaders(req)
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

func (s *qdrantStore) Close() error {
	s.closed = true
	return nil
}

func (s *qdrantStore) setHeaders(req *http.Request) {
	if s.apiKey != "" {
		req.Header.Set("api-key", s.apiKey)
	}
}
