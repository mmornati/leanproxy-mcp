package statusfile

import (
	"encoding/json"
	"log/slog"
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

func TestListStatusFiles(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.MkdirAll(filepath.Join(tmpDir, "status"), 0700)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "status", "file1.json"), []byte("{}"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "status", "file2.json"), []byte("{}"), 0644)
	require.NoError(t, err)

	files, err := listStatusFilesFromDir(tmpDir)
	require.NoError(t, err)
	assert.Len(t, files, 2)
	assert.Contains(t, files, "file1.json")
	assert.Contains(t, files, "file2.json")
}

func TestListStatusFilesEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.MkdirAll(filepath.Join(tmpDir, "status"), 0700)
	require.NoError(t, err)

	files, err := listStatusFilesFromDir(tmpDir)
	require.NoError(t, err)
	assert.Len(t, files, 0)
}

func TestListStatusFilesNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	files, err := listStatusFilesFromDir(tmpDir)
	require.NoError(t, err)
	assert.Nil(t, files)
}

func TestListStatusFilesWithSubdirs(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.MkdirAll(filepath.Join(tmpDir, "status", "subdir"), 0700)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "status", "file1.json"), []byte("{}"), 0644)
	require.NoError(t, err)

	files, err := listStatusFilesFromDir(tmpDir)
	require.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Contains(t, files, "file1.json")
}

func TestWriteLocked_MarshalError(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(nil, tmpDir)
	require.NoError(t, err)

	store.info.StartedAt = time.Time{}
	store.writeLocked()
}

func TestUpdateServers_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(nil, tmpDir)
	require.NoError(t, err)

	store.UpdateServers([]ServerStatus{})

	data, err := os.ReadFile(store.GetFilePath())
	require.NoError(t, err)

	var info StatusInfo
	err = json.Unmarshal(data, &info)
	require.NoError(t, err)

	assert.Len(t, info.Servers, 0)
}

func TestUpdateServers_Error(t *testing.T) {
	tmpDir := t.TempDir()
	invalidDir := filepath.Join(tmpDir, "nonexistent", "subdir")
	store := &FileStatusStore{
		statusFile: filepath.Join(invalidDir, "current.json"),
		logger:     slog.Default(),
		info: StatusInfo{
			PID:        os.Getpid(),
			StartedAt:  time.Now(),
			ListenAddr: "test",
			Servers:    []ServerStatus{},
		},
	}

	store.UpdateServers([]ServerStatus{{Name: "test"}})
}

func TestWriteLocked_WriteFileError(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(slog.Default(), tmpDir)
	require.NoError(t, err)

	os.Chmod(tmpDir, 0000)
	defer os.Chmod(tmpDir, 0700)

	store.UpdateServers([]ServerStatus{{Name: "test"}})
}

func TestWriteLocked_Direct(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(slog.Default(), tmpDir)
	require.NoError(t, err)

	subDir := filepath.Join(tmpDir, "subdir")
	store.statusFile = filepath.Join(subDir, "nonexistent", "current.json")

	store.writeLocked()
}

func TestWriteLocked_FileExistsAsDir(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(slog.Default(), tmpDir)
	require.NoError(t, err)

	err = os.MkdirAll(store.GetFilePath(), 0700)
	require.NoError(t, err)

	store.writeLocked()
}

func TestWriteLocked_WriteErrorPath(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "cannotwrite")
	err := os.MkdirAll(subDir, 0555)
	require.NoError(t, err)

	store := &FileStatusStore{
		statusFile: filepath.Join(subDir, "current.json"),
		logger:     slog.Default(),
		info: StatusInfo{
			PID:        os.Getpid(),
			StartedAt:  time.Now(),
			ListenAddr: "test",
			Servers:    []ServerStatus{},
		},
	}

	store.writeLocked()
}

func TestRemoveFile_Error(t *testing.T) {
	store := &FileStatusStore{
		statusFile: "/invalid/path/that/cannot/be/removed/current.json",
		logger:     slog.Default(),
		info: StatusInfo{
			PID:        os.Getpid(),
			StartedAt:  time.Now(),
			ListenAddr: "test",
			Servers:    []ServerStatus{},
		},
	}

	store.RemoveFile()
}

func TestNewFileStatusStoreNilLogger(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(nil, tmpDir)
	require.NoError(t, err)
	assert.NotNil(t, store)
	assert.NotNil(t, store.logger)
}

func TestReadCurrentStatusFromFile(t *testing.T) {
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

	statusFile := filepath.Join(tmpDir, "status.json")
	err = os.WriteFile(statusFile, data, 0644)
	require.NoError(t, err)

	readInfo, err := readCurrentStatusFromFile(statusFile)
	require.NoError(t, err)
	require.NotNil(t, readInfo)
	assert.Equal(t, 99999, readInfo.PID)
	assert.Equal(t, "127.0.0.1:9999", readInfo.ListenAddr)
	assert.Len(t, readInfo.Servers, 1)
}

func TestReadCurrentStatusFromFileNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	readInfo, err := readCurrentStatusFromFile(filepath.Join(tmpDir, "nonexistent.json"))
	require.NoError(t, err)
	assert.Nil(t, readInfo)
}

func TestReadCurrentStatusFromFileInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	statusFile := filepath.Join(tmpDir, "invalid.json")
	err := os.WriteFile(statusFile, []byte("not valid json"), 0644)
	require.NoError(t, err)

	_, err = readCurrentStatusFromFile(statusFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestNewFileStatusStoreWithDirError(t *testing.T) {
	store, err := newFileStatusStoreWithDir(nil, "/invalid/path/that/cannot/be/created")
	assert.Nil(t, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create status dir")
}

func TestNewFileStatusStoreFromConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewFileStatusStoreFromConfigDir("127.0.0.1:8080", nil, tmpDir)
	require.NoError(t, err)
	assert.NotNil(t, store)
	assert.Contains(t, store.GetFilePath(), "status")
	assert.Contains(t, store.GetFilePath(), "current.json")
}

func TestNewFileStatusStoreFromConfigDirError(t *testing.T) {
	store, err := NewFileStatusStoreFromConfigDir("127.0.0.1:8080", nil, "/invalid/path")
	assert.Nil(t, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create status dir")
}

func TestReadCurrentStatusFromConfigDir(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.MkdirAll(filepath.Join(tmpDir, "status"), 0700)
	require.NoError(t, err)

	info := StatusInfo{
		PID:        12345,
		StartedAt:  time.Now(),
		ListenAddr: "127.0.0.1:9000",
		Servers:    []ServerStatus{{Name: "test", Status: "running"}},
	}
	data, err := json.MarshalIndent(info, "", "  ")
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "status", "current.json"), data, 0644)
	require.NoError(t, err)

	readInfo, err := ReadCurrentStatusFromConfigDir(tmpDir)
	require.NoError(t, err)
	require.NotNil(t, readInfo)
	assert.Equal(t, 12345, readInfo.PID)
	assert.Equal(t, "127.0.0.1:9000", readInfo.ListenAddr)
}

func TestReadCurrentStatusFromConfigDirNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	readInfo, err := ReadCurrentStatusFromConfigDir(tmpDir)
	require.NoError(t, err)
	assert.Nil(t, readInfo)
}

func TestReadCurrentStatusFromConfigDirError(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.MkdirAll(filepath.Join(tmpDir, "status"), 0700)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "status", "current.json"), []byte("invalid"), 0644)
	require.NoError(t, err)

	_, err = ReadCurrentStatusFromConfigDir(tmpDir)
	require.Error(t, err)
}

func TestListStatusFilesFromConfigDir(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.MkdirAll(filepath.Join(tmpDir, "status"), 0700)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "status", "file1.json"), []byte("{}"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "status", "file2.json"), []byte("{}"), 0644)
	require.NoError(t, err)

	files, err := ListStatusFilesFromConfigDir(tmpDir)
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

func TestListStatusFilesFromConfigDirNotExist(t *testing.T) {
	tmpDir := t.TempDir()

	files, err := ListStatusFilesFromConfigDir(tmpDir)
	require.NoError(t, err)
	assert.Nil(t, files)
}

func TestListStatusFilesFromConfigDirWithSubdirs(t *testing.T) {
	tmpDir := t.TempDir()

	err := os.MkdirAll(filepath.Join(tmpDir, "status", "subdir"), 0700)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, "status", "file.json"), []byte("{}"), 0644)
	require.NoError(t, err)

	files, err := ListStatusFilesFromConfigDir(tmpDir)
	require.NoError(t, err)
	assert.Len(t, files, 1)
}

func TestUpdateServers_SingleServer(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(nil, tmpDir)
	require.NoError(t, err)

	store.UpdateServers([]ServerStatus{{Name: "single", Status: "active"}})

	data, err := os.ReadFile(store.GetFilePath())
	require.NoError(t, err)

	var info StatusInfo
	err = json.Unmarshal(data, &info)
	require.NoError(t, err)
	assert.Len(t, info.Servers, 1)
	assert.Equal(t, "single", info.Servers[0].Name)
}

func TestUpdateServers_MultipleServers(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(nil, tmpDir)
	require.NoError(t, err)

	servers := []ServerStatus{
		{Name: "server1", Status: "running"},
		{Name: "server2", Status: "stopped"},
		{Name: "server3", Status: "running"},
	}
	store.UpdateServers(servers)

	data, err := os.ReadFile(store.GetFilePath())
	require.NoError(t, err)

	var info StatusInfo
	err = json.Unmarshal(data, &info)
	require.NoError(t, err)
	assert.Len(t, info.Servers, 3)
}

func TestRemoveFile_AfterMultipleUpdates(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(nil, tmpDir)
	require.NoError(t, err)

	store.UpdateServers([]ServerStatus{{Name: "test1"}})
	store.UpdateServers([]ServerStatus{{Name: "test2"}})
	store.UpdateServers([]ServerStatus{{Name: "test3"}})

	store.RemoveFile()

	_, err = os.Stat(store.GetFilePath())
	assert.True(t, os.IsNotExist(err))
}

func TestWriteLocked_CheckInfo(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(slog.Default(), tmpDir)
	require.NoError(t, err)

	store.info.PID = 99999
	store.info.ListenAddr = "192.168.1.1:5555"

	store.writeLocked()

	data, err := os.ReadFile(store.GetFilePath())
	require.NoError(t, err)

	var info StatusInfo
	err = json.Unmarshal(data, &info)
	require.NoError(t, err)
	assert.Equal(t, 99999, info.PID)
	assert.Equal(t, "192.168.1.1:5555", info.ListenAddr)
}

func TestWriteLocked_FileAsDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newFileStatusStoreWithDir(slog.Default(), tmpDir)
	require.NoError(t, err)

	existingFile := store.GetFilePath()
	err = os.MkdirAll(existingFile, 0700)
	require.NoError(t, err)

	store.writeLocked()
}
