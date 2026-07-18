package vectordb

import (
	"context"
	"io"
	"log/slog"
	"net/http"
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
	}, discardLogger())
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
	}, discardLogger())
	require.NoError(t, err)
	defer store.Close()

	err = store.Upsert(context.Background())
	require.NoError(t, err)
}

func TestSQLiteStore_EmptyDelete(t *testing.T) {
	store, err := newSQLiteStore(&migrate.SQLiteVectorConfig{
		Path: ":memory:",
	}, discardLogger())
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
	}, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
	assert.Contains(t, err.Error(), "connection failed")
}

func TestQdrantStore_NoURL(t *testing.T) {
	store, err := newQdrantStore(nil, discardLogger())
	require.Error(t, err)
	require.Nil(t, store)
	assert.Contains(t, err.Error(), "url required")
}

func TestQdrantStore_EmptyURL(t *testing.T) {
	store, err := newQdrantStore(&migrate.QdrantVectorConfig{}, discardLogger())
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

func TestStringsTrimRight(t *testing.T) {
	assert.Equal(t, "http://localhost", stringsTrimRight("http://localhost/", "/"))
	assert.Equal(t, "http://localhost", stringsTrimRight("http://localhost", "/"))
	assert.Equal(t, "", stringsTrimRight("", "/"))
}
