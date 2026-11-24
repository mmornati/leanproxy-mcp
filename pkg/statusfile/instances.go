package statusfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Each running proxy writes its own status file, proxy-<pid>.json. Before,
// every process wrote the same current.json: with one `server run --stdio`
// per IDE session, the file showed whichever process wrote last.

const (
	instancePrefix = "proxy-"
	legacyFile     = "current.json"
)

func instanceFileName(pid int) string {
	return fmt.Sprintf("%s%d.json", instancePrefix, pid)
}

func statusDirPath() (string, error) {
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "leanproxy", "status"), nil
}

// ReadAllStatus returns the status of every proxy process running for this
// user, the shared HTTP gateway first, then the most recently started. The
// file of a process that no longer exists (it crashed, or was killed
// without a chance to clean up) is skipped and removed.
func ReadAllStatus() ([]StatusInfo, error) {
	dir, err := statusDirPath()
	if err != nil {
		return nil, err
	}
	return readAllStatusIn(dir)
}

func readAllStatusIn(dir string) ([]StatusInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("statusfile: read status dir: %w", err)
	}
	var out []StatusInfo
	seen := make(map[int]bool)
	for _, entry := range entries {
		name := entry.Name()
		isInstance := strings.HasPrefix(name, instancePrefix) && strings.HasSuffix(name, ".json")
		if entry.IsDir() || (!isInstance && name != legacyFile) {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path) // #nosec G304 -- a file of the user's own status directory
		if err != nil {
			continue
		}
		var info StatusInfo
		if json.Unmarshal(data, &info) != nil || info.PID <= 0 {
			continue
		}
		if !processAlive(info.PID) {
			if isInstance {
				_ = os.Remove(path)
			}
			continue
		}
		// A legacy current.json and an instance file may describe the
		// same process during an upgrade.
		if seen[info.PID] {
			continue
		}
		seen[info.PID] = true
		out = append(out, info)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].HTTP != nil) != (out[j].HTTP != nil) {
			return out[i].HTTP != nil
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}
