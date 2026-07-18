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
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

const maxErrorBody = 4096

type qdrantStore struct {
	client     *http.Client
	baseURL    string
	apiKey     string
	collection string
	dim        int
	logger     *slog.Logger
	closed     atomic.Bool
}

func newQdrantStore(cfg *migrate.QdrantVectorConfig, dim int, logger *slog.Logger) (*qdrantStore, error) {
	if cfg == nil || cfg.URL == "" {
		return nil, fmt.Errorf("vectordb qdrant: url required")
	}

	collection := cfg.Collection
	if collection == "" {
		collection = "leanproxy_cache"
	}

	apiKey := cfg.APIKey
	if apiKey == "" && cfg.APIKeyEnv != "" {
		apiKey = os.Getenv(cfg.APIKeyEnv)
	}

	s := &qdrantStore{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		baseURL:    strings.TrimRight(cfg.URL, "/"),
		apiKey:     apiKey,
		collection: collection,
		dim:        dim,
		logger:     logger,
	}

	if err := s.validateConnection(context.Background()); err != nil {
		return nil, fmt.Errorf("vectordb qdrant: connection failed: %w", err)
	}

	logger.Info("vectordb qdrant initialized", "url", cfg.URL, "collection", collection, "dimension", dim)
	return s, nil
}

func (s *qdrantStore) doRequest(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	s.setHeaders(req)
	return s.client.Do(req)
}

func (s *qdrantStore) readErrorBody(resp *http.Response) string {
	limited := io.LimitReader(resp.Body, maxErrorBody)
	body, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Sprintf("(read error: %v)", err)
	}
	return string(body)
}

func (s *qdrantStore) validateConnection(ctx context.Context) error {
	resp, err := s.doRequest(ctx, http.MethodGet, s.baseURL+"/collections/"+s.collection, nil)
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
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, s.readErrorBody(resp))
	}

	return nil
}

func (s *qdrantStore) createCollection(ctx context.Context) error {
	body := map[string]interface{}{
		"name": s.collection,
		"vectors": map[string]interface{}{
			"size":     s.dim,
			"distance": "Cosine",
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	resp, err := s.doRequest(ctx, http.MethodPut, s.baseURL+"/collections/"+s.collection, data)
	if err != nil {
		return fmt.Errorf("create collection request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("create collection status %d: %s", resp.StatusCode, s.readErrorBody(resp))
	}

	return nil
}

type qdrantPoint struct {
	ID      string                 `json:"id"`
	Vector  []float32              `json:"vector"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

func qdrantPointID(id string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(id)).String()
}

func (s *qdrantStore) Upsert(ctx context.Context, records ...VectorRecord) error {
	if len(records) == 0 {
		return nil
	}

	if s.closed.Load() {
		return fmt.Errorf("vectordb qdrant: store closed")
	}

	points := make([]qdrantPoint, len(records))
	for i, rec := range records {
		payload := map[string]interface{}{
			"_original_id": rec.ID,
		}
		for k, v := range rec.Metadata {
			payload[k] = v
		}
		points[i] = qdrantPoint{
			ID:      qdrantPointID(rec.ID),
			Vector:  rec.Vector,
			Payload: payload,
		}
	}

	body := map[string]interface{}{
		"points": points,
		"wait":   true,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	resp, err := s.doRequest(ctx, http.MethodPut, s.baseURL+"/collections/"+s.collection+"/points", data)
	if err != nil {
		return fmt.Errorf("upsert request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upsert status %d: %s", resp.StatusCode, s.readErrorBody(resp))
	}

	return nil
}

type qdrantSearchResponse struct {
	Result []struct {
		ID      string                 `json:"id"`
		Score   float64                `json:"score"`
		Vector  []float32              `json:"vector,omitempty"`
		Payload map[string]interface{} `json:"payload,omitempty"`
	} `json:"result"`
}

func (s *qdrantStore) Search(ctx context.Context, vector []float32, k int) ([]SearchResult, error) {
	if k <= 0 {
		k = 10
	}

	if s.closed.Load() {
		return nil, fmt.Errorf("vectordb qdrant: store closed")
	}

	body := map[string]interface{}{
		"vector":       vector,
		"limit":        k,
		"with_payload": true,
		"with_vector":  true,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	resp, err := s.doRequest(ctx, http.MethodPost, s.baseURL+"/collections/"+s.collection+"/points/search", data)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search status %d: %s", resp.StatusCode, s.readErrorBody(resp))
	}

	var sr qdrantSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	results := make([]SearchResult, 0, len(sr.Result))
	for _, r := range sr.Result {
		origID, _ := r.Payload["_original_id"].(string)
		if origID == "" {
			origID = r.ID
		}

		meta := make(map[string]string)
		for k, v := range r.Payload {
			if k == "_original_id" {
				continue
			}
			meta[k] = fmt.Sprintf("%v", v)
		}

		results = append(results, SearchResult{
			Record: VectorRecord{
				ID:       origID,
				Vector:   r.Vector,
				Metadata: meta,
			},
			Score: r.Score,
		})
	}

	return results, nil
}

func (s *qdrantStore) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}

	if s.closed.Load() {
		return fmt.Errorf("vectordb qdrant: store closed")
	}

	pointIDs := make([]string, len(ids))
	for i, id := range ids {
		pointIDs[i] = qdrantPointID(id)
	}

	body := map[string]interface{}{
		"points": pointIDs,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	resp, err := s.doRequest(ctx, http.MethodPost, s.baseURL+"/collections/"+s.collection+"/points/delete", data)
	if err != nil {
		return fmt.Errorf("delete request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete status %d: %s", resp.StatusCode, s.readErrorBody(resp))
	}

	return nil
}

func (s *qdrantStore) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	s.client.CloseIdleConnections()
	return nil
}

func (s *qdrantStore) setHeaders(req *http.Request) {
	if s.apiKey != "" {
		req.Header.Set("api-key", s.apiKey)
	}
}
