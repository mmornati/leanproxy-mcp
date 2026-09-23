package toolstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func TestNewFileCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "toolcache")
	t.Setenv(CacheDirEnv, dir)
	fc, err := NewFileCache(nil)
	require.NoError(t, err)
	assert.Equal(t, dir, fc.GetCacheDir())
	_, err = os.Stat(dir)
	require.NoError(t, err, "the cache dir from the environment must be created")
}

// TestDefaultCacheDir_HonorsHOME: without the override the cache lives
// under $HOME (not the passwd entry), so tests that redirect HOME never
// write to the real home directory.
func TestDefaultCacheDir_HonorsHOME(t *testing.T) {
	home := t.TempDir()
	t.Setenv(CacheDirEnv, "")
	t.Setenv("HOME", home)
	dir, err := DefaultCacheDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".config", "leanproxy", "toolcache"), dir)
}

func TestGetCacheDir(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	assert.Equal(t, tmpDir, fc.GetCacheDir())
}

func TestGetTools_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	tools, err := fc.GetTools("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, tools)
}

func TestGetTools_Error(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	filePath := fc.filePath("badserver")
	err = os.WriteFile(filePath, []byte("invalid json"), 0644)
	require.NoError(t, err)

	_, err = fc.GetTools("badserver")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestSetTools_Error(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	// Pre-create the cache file's path as a directory so the write fails
	// regardless of the user's privileges (including root, which ignores
	// permission bits such as chmod 0000).
	filePath := fc.filePath("server")
	require.NoError(t, os.MkdirAll(filePath, 0755))

	err = fc.SetTools("server", []CachedTool{{Name: "tool"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write")
}

func TestInvalidate_Error(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	cachedTools := []CachedTool{{Name: "tool1"}}
	err = fc.SetTools("server1", cachedTools)
	require.NoError(t, err)

	tmpDir2 := t.TempDir()
	fc2, err := newFileCacheWithDir(nil, tmpDir2)
	require.NoError(t, err)

	_, err = fc2.GetTools("server1")
	require.NoError(t, err)

	err = fc2.Invalidate("nonexistent")
	require.NoError(t, err)
}

func TestListCachedServers_Error(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	// Replace the cache directory with a regular file so ReadDir fails
	// regardless of the user's privileges (including root, which ignores
	// permission bits such as chmod 0000).
	require.NoError(t, os.RemoveAll(tmpDir))
	require.NoError(t, os.WriteFile(tmpDir, []byte("x"), 0644))

	_, err = fc.ListCachedServers()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read")
}

func TestFileCache_Concurrency(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	var wg sync.WaitGroup
	numGoroutines := 10
	iterations := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				serverName := fmt.Sprintf("server-%d", j%5)
				tools := []CachedTool{{Name: fmt.Sprintf("tool-%d-%d", id, j)}}
				_ = fc.SetTools(serverName, tools)
				_, _ = fc.GetTools(serverName)
				_ = fc.Invalidate(serverName)
			}
		}(i)
	}

	wg.Wait()
}

func TestNewFileCacheWithDir_InvalidCacheDir(t *testing.T) {
	tmpDir := t.TempDir()
	blockingFile := filepath.Join(tmpDir, "blocker")
	require.NoError(t, os.WriteFile(blockingFile, []byte("x"), 0644))

	// blockingFile is a regular file, so using it as a directory component
	// makes MkdirAll fail regardless of the user's privileges (including root).
	_, err := newFileCacheWithDir(nil, filepath.Join(blockingFile, "sub"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create cache dir")
}

func TestGetCacheDir_EmptyCache(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	dir := fc.GetCacheDir()
	assert.Equal(t, tmpDir, dir)
}

func TestListCachedServers_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	fc, err := newFileCacheWithDir(nil, tmpDir)
	require.NoError(t, err)

	servers, err := fc.ListCachedServers()
	require.NoError(t, err)
	assert.Empty(t, servers)
}
