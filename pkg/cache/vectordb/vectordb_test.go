package vectordb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewStore_DefaultSQLite(t *testing.T) {
	store, err := NewStore(nil, discardLogger())
	require.NoError(t, err)
	require.NotNil(t, store)
	defer store.Close()

	_, ok := store.(*sqliteStore)
	assert.True(t, ok, "expected *sqliteStore")
}

func TestNewStore_SQLiteBackend(t *testing.T) {
	store, err := NewStore(&migrate.VectorStoreConfig{
		Backend: "sqlite-vec",
	}, discardLogger())
	require.NoError(t, err)
	require.NotNil(t, store)
	defer store.Close()
}

func TestNewStore_UnknownBackend(t *testing.T) {
	store, err := NewStore(&migrate.VectorStoreConfig{
		Backend: "nosql",
	}, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
	assert.Contains(t, err.Error(), "unknown backend")
}

func TestVectorRecord(t *testing.T) {
	rec := VectorRecord{
		ID:     "test-id",
		Vector: []float32{0.1, 0.2, 0.3},
		Metadata: map[string]string{
			"key": "value",
		},
	}
	assert.Equal(t, "test-id", rec.ID)
	assert.Len(t, rec.Vector, 3)
	assert.Equal(t, "value", rec.Metadata["key"])
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	assert.InDelta(t, 1.0, cosineSimilarity(a, b), 0.001)

	a = []float32{1, 0, 0}
	b = []float32{0, 1, 0}
	assert.InDelta(t, 0.0, cosineSimilarity(a, b), 0.001)

	a = []float32{1, 0, 0}
	b = []float32{-1, 0, 0}
	assert.InDelta(t, -1.0, cosineSimilarity(a, b), 0.001)
}

func TestCosineSimilarity_Empty(t *testing.T) {
	assert.InDelta(t, 0.0, cosineSimilarity(nil, []float32{1, 2}), 0.001)
	assert.InDelta(t, 0.0, cosineSimilarity([]float32{1, 2}, nil), 0.001)
	assert.InDelta(t, 0.0, cosineSimilarity([]float32{}, []float32{}), 0.001)
}

func TestFloat32Conversions(t *testing.T) {
	original := []float32{1.5, -2.5, 3.0, 0.0}
	bytes := float32SliceToBytes(original)
	restored := bytesToFloat32Slice(bytes)
	assert.Equal(t, original, restored)
}

func TestFloat32SliceToString(t *testing.T) {
	result := float32SliceToString([]float32{1.5, -2.5})
	assert.Contains(t, result, "1.5")
	assert.Contains(t, result, "-2.5")
	assert.Contains(t, result, "[")
	assert.Contains(t, result, "]")

	result = float32SliceToString(nil)
	assert.Equal(t, "[]", result)
}

func TestSortResults(t *testing.T) {
	results := []SearchResult{
		{Score: 0.3},
		{Score: 0.9},
		{Score: 0.5},
	}
	sortResults(results)
	assert.InDelta(t, 0.9, results[0].Score, 0.001)
	assert.InDelta(t, 0.5, results[1].Score, 0.001)
	assert.InDelta(t, 0.3, results[2].Score, 0.001)
}

func TestMarshalUnmarshalMetadata(t *testing.T) {
	meta := map[string]string{
		"key1": "val1",
		"key2": "val2",
	}
	data := marshalMetadata(meta)
	restored := unmarshalMetadata(data)
	assert.Equal(t, meta, restored)
}

func TestMarshalMetadata_Nil(t *testing.T) {
	data := marshalMetadata(nil)
	assert.Equal(t, "{}", string(data))
}

func TestUnmarshalMetadata_Empty(t *testing.T) {
	result := unmarshalMetadata([]byte("{}"))
	assert.Empty(t, result)
}

func TestSQLiteStore(t *testing.T) {
	store, err := newSQLiteStore(&migrate.SQLiteVectorConfig{
		Path: ":memory:",
	}, 5, discardLogger())
	require.NoError(t, err)
	defer store.Close()

	vector := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	err = store.Upsert(context.Background(), VectorRecord{
		ID:     "doc1",
		Vector: vector,
		Metadata: map[string]string{
			"title": "test document",
		},
	})
	require.NoError(t, err)

	err = store.Upsert(context.Background(), VectorRecord{
		ID:     "doc2",
		Vector: []float32{0.5, 0.4, 0.3, 0.2, 0.1},
	})
	require.NoError(t, err)

	results, err := store.Search(context.Background(), vector, 5)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "doc1", results[0].Record.ID)
	assert.Greater(t, results[0].Score, 0.9)

	results, err = store.Search(context.Background(), []float32{0.5, 0.4, 0.3, 0.2, 0.1}, 5)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	assert.Equal(t, "doc2", results[0].Record.ID)

	err = store.Delete(context.Background(), "doc1")
	require.NoError(t, err)

	results, err = store.Search(context.Background(), vector, 5)
	require.NoError(t, err)
	assert.Equal(t, 1, len(results))
	assert.Equal(t, "doc2", results[0].Record.ID)
}

func TestSQLiteStore_EmptyUpsert(t *testing.T) {
	store, err := newSQLiteStore(&migrate.SQLiteVectorConfig{
		Path: ":memory:",
	}, 5, discardLogger())
	require.NoError(t, err)
	defer store.Close()

	err = store.Upsert(context.Background())
	require.NoError(t, err)
}

func TestSQLiteStore_EmptyDelete(t *testing.T) {
	store, err := newSQLiteStore(&migrate.SQLiteVectorConfig{
		Path: ":memory:",
	}, 5, discardLogger())
	require.NoError(t, err)
	defer store.Close()

	err = store.Delete(context.Background())
	require.NoError(t, err)
}

func TestSQLiteStore_DefaultPath(t *testing.T) {
	path := defaultSQLitePath()
	assert.Contains(t, path, ".leanproxy")
	assert.Contains(t, path, "cache")
	assert.Contains(t, path, "vectors.db")
}

func TestQdrantStore(t *testing.T) {
	store, err := newQdrantStore(&migrate.QdrantVectorConfig{
		URL: "http://localhost:99999",
	}, 1536, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
	assert.Contains(t, err.Error(), "connection failed")
}

func TestQdrantStore_NoURL(t *testing.T) {
	store, err := newQdrantStore(nil, 1536, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
	assert.Contains(t, err.Error(), "url required")
}

func TestQdrantStore_EmptyURL(t *testing.T) {
	store, err := newQdrantStore(&migrate.QdrantVectorConfig{}, 1536, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
	assert.Contains(t, err.Error(), "url required")
}

func TestPineconeStore_NoIndex(t *testing.T) {
	store, err := newPineconeStore(nil, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
	assert.Contains(t, err.Error(), "index name required")
}

func TestPineconeStore_EmptyIndex(t *testing.T) {
	store, err := newPineconeStore(&migrate.PineconeVectorConfig{}, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
	assert.Contains(t, err.Error(), "index name required")
}

func TestPineconeStore_NoAPIKey(t *testing.T) {
	t.Setenv("PINECONE_API_KEY", "")
	store, err := newPineconeStore(&migrate.PineconeVectorConfig{
		Index: "test-index",
	}, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
	assert.Contains(t, err.Error(), "PINECONE_API_KEY not set")
}

func TestPineconeStore_CustomKeyEnv(t *testing.T) {
	t.Setenv("MY_PINECONE_KEY", "")
	store, err := newPineconeStore(&migrate.PineconeVectorConfig{
		Index:     "test-index",
		APIKeyEnv: "MY_PINECONE_KEY",
	}, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
	assert.Contains(t, err.Error(), "MY_PINECONE_KEY not set")
}

func TestNewStore_WithQdrantConfig(t *testing.T) {
	store, err := NewStore(&migrate.VectorStoreConfig{
		Backend: "qdrant",
		Qdrant: &migrate.QdrantVectorConfig{
			URL: "http://localhost:99999",
		},
	}, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
}

func TestNewStore_WithPineconeConfig(t *testing.T) {
	t.Setenv("PINECONE_API_KEY", "")
	store, err := NewStore(&migrate.VectorStoreConfig{
		Backend: "pinecone",
		Pinecone: &migrate.PineconeVectorConfig{
			Index: "test-index",
		},
	}, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
}

func TestQdrantSearch_Unreachable(t *testing.T) {
	store := &qdrantStore{
		client:     &http.Client{},
		baseURL:    "http://localhost:99999",
		collection: "test",
		logger:     discardLogger(),
	}

	results, err := store.Search(context.Background(), []float32{0.1, 0.2}, 10)
	require.Error(t, err)
	require.Nil(t, results)
}

func TestPineconeSearch_Unreachable(t *testing.T) {
	store := &pineconeStore{
		client:  &http.Client{},
		baseURL: "https://nonexistent.pinecone.io",
		apiKey:  "test-key",
		logger:  discardLogger(),
	}

	results, err := store.Search(context.Background(), []float32{0.1, 0.2}, 10)
	require.Error(t, err)
	require.Nil(t, results)
}

func BenchmarkCosineSimilarity(b *testing.B) {
	a := make([]float32, 1536)
	vec := make([]float32, 1536)
	for i := range a {
		a[i] = float32(i) / 1536
		vec[i] = float32(1536-i) / 1536
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cosineSimilarity(a, vec)
	}
}

func BenchmarkSQLiteSearch(b *testing.B) {
	store, err := newSQLiteStore(&migrate.SQLiteVectorConfig{
		Path: ":memory:",
	}, 1536, discardLogger())
	require.NoError(b, err)
	defer store.Close()

	vec := make([]float32, 1536)
	for i := 0; i < 100; i++ {
		for j := range vec {
			vec[j] = float32(i+j) / 1536
		}
		store.Upsert(context.Background(), VectorRecord{
			ID:     fmt.Sprintf("doc-%d", i),
			Vector: vec,
		})
	}

	query := make([]float32, 1536)
	for i := range query {
		query[i] = float32(i) / 1536
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.Search(context.Background(), query, 10)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestQdrantMockServer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/collections/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/collections/test/points", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		pts, ok := body["points"].([]interface{})
		if !ok || len(pts) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		pt := pts[0].(map[string]interface{})
		id, _ := pt["id"].(string)
		_ = id
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/collections/test/points/search", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"result": []map[string]interface{}{
				{
					"id":     qdrantPointID("doc1"),
					"score":  0.95,
					"vector": []float32{0.1, 0.2, 0.3},
					"payload": map[string]interface{}{
						"_original_id": "doc1",
						"title":        "test",
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/collections/test/points/delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	store := &qdrantStore{
		client:     server.Client(),
		baseURL:    server.URL,
		collection: "test",
		dim:        3,
		logger:     discardLogger(),
	}

	err := store.Upsert(context.Background(), VectorRecord{
		ID:     "doc1",
		Vector: []float32{0.1, 0.2, 0.3},
		Metadata: map[string]string{"title": "test"},
	})
	require.NoError(t, err)

	results, err := store.Search(context.Background(), []float32{0.1, 0.2, 0.3}, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "doc1", results[0].Record.ID)
	assert.InDelta(t, 0.95, results[0].Score, 0.001)
	assert.Equal(t, "test", results[0].Record.Metadata["title"])

	err = store.Delete(context.Background(), "doc1")
	require.NoError(t, err)
}

func TestPineconeMockServer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/vectors/upsert", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]int{"upsertedCount": 1})
	})
	mux.HandleFunc("/query", func(w http.ResponseWriter, r *http.Request) {
		resp := pineconeQueryResponse{
			Matches: []struct {
				ID       string            `json:"id"`
				Score    float64           `json:"score"`
				Values   []float32         `json:"values"`
				Metadata map[string]string `json:"metadata"`
			}{
				{
					ID:     "doc1",
					Score:  0.95,
					Values: []float32{0.1, 0.2, 0.3},
					Metadata: map[string]string{
						"title": "test",
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/vectors/delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	store := &pineconeStore{
		client:  server.Client(),
		baseURL: server.URL,
		apiKey:  "test-key",
		logger:  discardLogger(),
	}

	err := store.Upsert(context.Background(), VectorRecord{
		ID:     "doc1",
		Vector: []float32{0.1, 0.2, 0.3},
		Metadata: map[string]string{"title": "test"},
	})
	require.NoError(t, err)

	results, err := store.Search(context.Background(), []float32{0.1, 0.2, 0.3}, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "doc1", results[0].Record.ID)
	assert.InDelta(t, 0.95, results[0].Score, 0.001)

	err = store.Delete(context.Background(), "doc1")
	require.NoError(t, err)
}


