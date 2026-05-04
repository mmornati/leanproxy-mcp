package statusfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusInfo(t *testing.T) {
	info := StatusInfo{
		PID:        12345,
		StartedAt:  time.Now(),
		ListenAddr: "127.0.0.1:8080",
		Servers: []ServerStatus{
			{
				Name:         "testserver",
				Status:       "running",
				RequestCount: 100,
				ErrorCount:   5,
				RestartCount: 2,
				Uptime:       "5m30s",
			},
		},
	}

	data, err := json.MarshalIndent(info, "", "  ")
	require.NoError(t, err)

	var decoded StatusInfo
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, 12345, decoded.PID)
	assert.Equal(t, "127.0.0.1:8080", decoded.ListenAddr)
	assert.Len(t, decoded.Servers, 1)
	assert.Equal(t, "testserver", decoded.Servers[0].Name)
	assert.Equal(t, "running", decoded.Servers[0].Status)
}

func TestNewFileStatusStore(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(nil, tmpDir)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(tmpDir, "status", "current.json"), store.GetFilePath())
}

func TestUpdateServers(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(nil, tmpDir)
	require.NoError(t, err)

	servers := []ServerStatus{
		{
			Name:         "server1",
			Status:       "running",
			RequestCount: 50,
			ErrorCount:   2,
		},
		{
			Name:         "server2",
			Status:       "stopped",
			RequestCount: 10,
			ErrorCount:   1,
		},
	}

	store.UpdateServers(servers)

	data, err := os.ReadFile(store.GetFilePath())
	require.NoError(t, err)

	var info StatusInfo
	err = json.Unmarshal(data, &info)
	require.NoError(t, err)

	assert.Len(t, info.Servers, 2)
	assert.Equal(t, "server1", info.Servers[0].Name)
	assert.Equal(t, int64(50), info.Servers[0].RequestCount)
	assert.Equal(t, "server2", info.Servers[1].Name)
	assert.Equal(t, "stopped", info.Servers[1].Status)
}

func TestReadCurrentStatus(t *testing.T) {
	tmpDir := t.TempDir()

	info := StatusInfo{
		PID:        99999,
		StartedAt:  time.Now(),
		ListenAddr: "127.0.0.1:9999",
		Servers: []ServerStatus{
			{
				Name:         "readtest",
				Status:       "running",
				RequestCount: 42,
			},
		},
	}
	data, err := json.MarshalIndent(info, "", "  ")
	require.NoError(t, err)

	statusFile := filepath.Join(tmpDir, "status", "current.json")
	err = os.MkdirAll(filepath.Dir(statusFile), 0700)
	require.NoError(t, err)
	err = os.WriteFile(statusFile, data, 0644)
	require.NoError(t, err)

	readInfo, err := readCurrentStatusFromDir(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, readInfo)
	assert.Equal(t, 99999, readInfo.PID)
	assert.Equal(t, "127.0.0.1:9999", readInfo.ListenAddr)
	assert.Len(t, readInfo.Servers, 1)
}

func TestReadCurrentStatusNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	readInfo, err := readCurrentStatusFromDir(tmpDir)
	require.NoError(t, err)
	assert.Nil(t, readInfo)
}

func TestRemoveFile(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(nil, tmpDir)
	require.NoError(t, err)

	servers := []ServerStatus{{Name: "test"}}
	store.UpdateServers(servers)

	_, err = os.Stat(store.GetFilePath())
	require.NoError(t, err)

	store.RemoveFile()

	_, err = os.Stat(store.GetFilePath())
	assert.True(t, os.IsNotExist(err))
}