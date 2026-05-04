package statusfile

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"
)

type ServerStatus struct {
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	RequestCount int64     `json:"request_count"`
	ErrorCount   int64     `json:"error_count"`
	RestartCount int       `json:"restart_count"`
	Uptime       string    `json:"uptime"`
	ToolCount    int       `json:"tool_count"`
	LastActivity time.Time `json:"last_activity"`
}

type StatusInfo struct {
	PID        int            `json:"pid"`
	StartedAt  time.Time      `json:"started_at"`
	ListenAddr string         `json:"listen_addr"`
	Servers    []ServerStatus `json:"servers"`
}

type FileStatusStore struct {
	statusFile string
	logger     *slog.Logger
	mu         sync.RWMutex
	info       StatusInfo
}

func NewFileStatusStore(listenAddr string, logger *slog.Logger) (*FileStatusStore, error) {
	if logger == nil {
		logger = slog.Default()
	}

	usr, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("statusfile: get user home dir: %w", err)
	}

	statusDir := filepath.Join(usr.HomeDir, ".config", "leanproxy", "status")
	if err := os.MkdirAll(statusDir, 0700); err != nil {
		return nil, fmt.Errorf("statusfile: create status dir: %w", err)
	}

	store := &FileStatusStore{
		statusFile: filepath.Join(statusDir, "current.json"),
		logger:     logger,
		info: StatusInfo{
			PID:        os.Getpid(),
			StartedAt:  time.Now(),
			ListenAddr: listenAddr,
			Servers:    []ServerStatus{},
		},
	}

	return store, nil
}

func NewFileStatusStoreFromConfigDir(listenAddr string, logger *slog.Logger, configDir string) (*FileStatusStore, error) {
	if logger == nil {
		logger = slog.Default()
	}

	statusDir := filepath.Join(configDir, "status")
	if err := os.MkdirAll(statusDir, 0700); err != nil {
		return nil, fmt.Errorf("statusfile: create status dir: %w", err)
	}

	store := &FileStatusStore{
		statusFile: filepath.Join(statusDir, "current.json"),
		logger:     logger,
		info: StatusInfo{
			PID:        os.Getpid(),
			StartedAt:  time.Now(),
			ListenAddr: listenAddr,
			Servers:    []ServerStatus{},
		},
	}

	return store, nil
}

func (s *FileStatusStore) UpdateServers(servers []ServerStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info.Servers = servers
	s.writeLocked()
}

func (s *FileStatusStore) GetFilePath() string {
	return s.statusFile
}

func (s *FileStatusStore) RemoveFile() {
	os.Remove(s.statusFile)
}

func (s *FileStatusStore) writeLocked() {
	data, err := json.MarshalIndent(s.info, "", "  ")
	if err != nil {
		s.logger.Warn("failed to marshal status", "error", err)
		return
	}

	if err := os.WriteFile(s.statusFile, data, 0644); err != nil {
		s.logger.Warn("failed to write status file", "error", err)
	}
}

func ReadCurrentStatus() (*StatusInfo, error) {
	usr, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("statusfile: get user home dir: %w", err)
	}

	statusFile := filepath.Join(usr.HomeDir, ".config", "leanproxy", "status", "current.json")
	data, err := os.ReadFile(statusFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("statusfile: read status file: %w", err)
	}

	var info StatusInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("statusfile: unmarshal status: %w", err)
	}

	return &info, nil
}

func ReadCurrentStatusFromConfigDir(configDir string) (*StatusInfo, error) {
	statusFile := filepath.Join(configDir, "status", "current.json")
	data, err := os.ReadFile(statusFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("statusfile: read status file: %w", err)
	}

	var info StatusInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("statusfile: unmarshal status: %w", err)
	}

	return &info, nil
}

func ListStatusFiles() ([]string, error) {
	usr, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("statusfile: get user home dir: %w", err)
	}

	statusDir := filepath.Join(usr.HomeDir, ".config", "leanproxy", "status")
	entries, err := os.ReadDir(statusDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("statusfile: read status dir: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, entry.Name())
	}
	return files, nil
}

func ListStatusFilesFromConfigDir(configDir string) ([]string, error) {
	statusDir := filepath.Join(configDir, "status")
	entries, err := os.ReadDir(statusDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("statusfile: read status dir: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, entry.Name())
	}
	return files, nil
}

func newFileStatusStoreWithDir(logger *slog.Logger, dir string) (*FileStatusStore, error) {
	if logger == nil {
		logger = slog.Default()
	}

	statusDir := filepath.Join(dir, "status")
	if err := os.MkdirAll(statusDir, 0700); err != nil {
		return nil, fmt.Errorf("statusfile: create status dir: %w", err)
	}

	store := &FileStatusStore{
		statusFile: filepath.Join(statusDir, "current.json"),
		logger:     logger,
		info: StatusInfo{
			PID:        os.Getpid(),
			StartedAt:  time.Now(),
			ListenAddr: "test",
			Servers:    []ServerStatus{},
		},
	}

	return store, nil
}

func readCurrentStatusFromDir(dir string) (*StatusInfo, error) {
	statusFile := filepath.Join(dir, "status", "current.json")
	data, err := os.ReadFile(statusFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("statusfile: read status file: %w", err)
	}

	var info StatusInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("statusfile: unmarshal status: %w", err)
	}

	return &info, nil
}

func listStatusFilesFromDir(dir string) ([]string, error) {
	statusDir := filepath.Join(dir, "status")
	entries, err := os.ReadDir(statusDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("statusfile: read status dir: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, entry.Name())
	}
	return files, nil
}

func readCurrentStatusFromFile(filePath string) (*StatusInfo, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("statusfile: read status file: %w", err)
	}

	var info StatusInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("statusfile: unmarshal status: %w", err)
	}

	return &info, nil
}
