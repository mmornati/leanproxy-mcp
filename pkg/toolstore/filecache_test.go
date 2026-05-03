package toolstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileCacheBasic(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	cachedTools := []CachedTool{
		{
			Name:        "test_tool",
			Description: "A test tool",
			InputSchema: []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`),
		},
	}

	err = fc.SetTools("testserver", cachedTools)
	require.NoError(t, err)

	gotTools, err := fc.GetTools("testserver")
	require.NoError(t, err)
	require.Len(t, gotTools, 1)
	assert.Equal(t, "test_tool", gotTools[0].Name)
	assert.Equal(t, "A test tool", gotTools[0].Description)
}

func TestFileCacheListServers(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	cachedTools := []CachedTool{{Name: "tool1"}}
	err = fc.SetTools("server1", cachedTools)
	require.NoError(t, err)

	cachedTools = []CachedTool{{Name: "tool2"}}
	err = fc.SetTools("server2", cachedTools)
	require.NoError(t, err)

	servers, err := fc.ListCachedServers()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"server1", "server2"}, servers)
}

func TestFileCacheInvalidate(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	cachedTools := []CachedTool{{Name: "tool1"}}
	err = fc.SetTools("server1", cachedTools)
	require.NoError(t, err)

	err = fc.Invalidate("server1")
	require.NoError(t, err)

	gotTools, err := fc.GetTools("server1")
	require.NoError(t, err)
	assert.Nil(t, gotTools)
}

func TestFileCacheExpiry(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	cachedTools := []CachedTool{{Name: "tool1"}}
	err = fc.SetTools("server1", cachedTools)
	require.NoError(t, err)

	cachedFile := cachedToolsFile{
		Tools:    cachedTools,
		CachedAt: time.Now().Add(-48 * time.Hour),
	}

	data, err := json.Marshal(cachedFile)
	require.NoError(t, err)

	filePath := filepath.Join(tmpDir, "server1.json")
	err = os.WriteFile(filePath, data, 0644)
	require.NoError(t, err)

	fc2, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	gotTools, err := fc2.GetTools("server1")
	require.NoError(t, err)
	assert.Nil(t, gotTools)
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"with-dash", "with-dash"},
		{"with_underscore", "with_underscore"},
		{"UPPERCASE", "UPPERCASE"},
		{"with spaces", "with_spaces"},
		{"with.dot", "with_dot"},
		{"with@ special!", "with__special_"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := sanitizeFilename(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestNoOpCache(t *testing.T) {
	cache := NewNoOpCache()

	tools, err := cache.GetTools("anyserver")
	require.NoError(t, err)
	assert.Nil(t, tools)

	err = cache.SetTools("anyserver", []CachedTool{{Name: "tool"}})
	require.NoError(t, err)

	err = cache.Invalidate("anyserver")
	require.NoError(t, err)

	assert.Equal(t, "", cache.GetCacheDir())
}