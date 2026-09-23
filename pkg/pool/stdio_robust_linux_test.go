//go:build linux

package pool

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestStdioRobust_StopKillsProcessGroup: stopping a server started through
// a wrapper that leaves a grandchild behind (sh -c 'sleep 1000 & exec
// <mock>') kills the grandchild too.
func TestStdioRobust_StopKillsProcessGroup(t *testing.T) {
	bin := concurrentMCPBinary(t)

	cases := map[string]func(pidFile string) fakeOpts{
		"sh wrapper": func(pidFile string) fakeOpts {
			return fakeOpts{command: "sh", args: []string{"-c", fmt.Sprintf("sleep 1000 & echo $! > %q; exec %q", pidFile, bin)}}
		},
		"spawned child": func(pidFile string) fakeOpts {
			return fakeOpts{args: []string{"--spawn-child", pidFile}}
		},
		"child ignoring SIGTERM": func(pidFile string) fakeOpts {
			return fakeOpts{command: "sh", args: []string{"-c", fmt.Sprintf("sh -c 'trap \"\" TERM; echo $$ > %q; exec sleep 1000' & exec %q", pidFile, bin)}}
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			p := newTestPool(t)
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			startFake(t, p, "wrapped", mk(pidFile))

			resp, err := callTool(context.Background(), p, "wrapped", "echo", map[string]interface{}{"tag": "up"}, 5*time.Second)
			require.NoError(t, err)
			require.Equal(t, "up", resultTag(t, resp))

			var pid int
			require.Eventually(t, func() bool {
				data, err := os.ReadFile(pidFile)
				if err != nil {
					return false
				}
				pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
				return err == nil && pid > 0
			}, 5*time.Second, 10*time.Millisecond)
			require.True(t, pidRunning(pid), "grandchild should be running before stop")

			require.NoError(t, p.StopServer("wrapped"))
			require.Eventually(t, func() bool { return !pidRunning(pid) }, 5*time.Second, 20*time.Millisecond,
				"grandchild %d survived stop", pid)
		})
	}
}

// pidRunning reports whether pid is alive and not a zombie.
func pidRunning(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// Field 3 (after "(comm)") is the state; Z is a zombie.
	if i := bytes.LastIndexByte(data, ')'); i >= 0 && i+2 < len(data) {
		return data[i+2] != 'Z'
	}
	return true
}
