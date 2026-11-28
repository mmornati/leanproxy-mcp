package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// On-demand gateway: `connect` starts the shared gateway when nothing
// listens at its address, instead of a service manager keeping it up from
// login. The gateway it starts runs with --idle-exit, so it stops by
// itself some time after its last client left.
//
// There is still one gateway per machine: every `connect` (one per client
// session) relays to the same address, a lock file lets only one of them
// start the gateway when several open at once, and a second gateway could
// not bind the address anyway.

const (
	// defaultGatewayIdleExit is how long an on-demand gateway outlives
	// its last client.
	defaultGatewayIdleExit = 10 * time.Minute
	// gatewayStartTimeout bounds the wait for a started gateway to
	// accept connections.
	gatewayStartTimeout = 20 * time.Second
	// gatewayLockStale is the age past which a start lock is taken to be
	// left over from a `connect` that died while starting the gateway.
	gatewayLockStale = gatewayStartTimeout + 10*time.Second
	gatewayStartPoll = 100 * time.Millisecond
	gatewayLockName  = "gateway.start.lock"
)

// gatewayStarter makes sure a gateway listens at addr.
type gatewayStarter struct {
	addr     string
	lockPath string
	logFile  string
	timeout  time.Duration
	poll     time.Duration
	// reachable reports whether something accepts connections at addr.
	reachable func(addr string) bool
	// spawn starts the gateway, detached, and returns a channel that
	// receives its exit (so a gateway that dies while starting is not
	// waited for until the timeout).
	spawn func() (<-chan error, error)

	mu sync.Mutex
}

// tcpReachable reports whether a TCP connection to addr succeeds.
func tcpReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ensure returns once a gateway accepts connections at addr, starting one
// if needed. It is the bridge's Wake hook: calls in this process are
// serialized, and the lock file serializes the processes.
func (g *gatewayStarter) ensure(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	deadline := time.Now().Add(g.timeout)
	for {
		if g.reachable(g.addr) {
			return nil
		}
		release, err := g.tryLock()
		if err != nil {
			return err
		}
		if release != nil {
			defer release()
			// Another `connect` may have finished starting it between
			// the check above and the lock.
			if g.reachable(g.addr) {
				return nil
			}
			return g.start(ctx, deadline)
		}
		// Another `connect` is starting the gateway: wait for it.
		if err := g.sleep(ctx, deadline); err != nil {
			return err
		}
	}
}

// start spawns the gateway and waits until it accepts connections.
func (g *gatewayStarter) start(ctx context.Context, deadline time.Time) error {
	exited, err := g.spawn()
	if err != nil {
		return fmt.Errorf("cannot start the LeanProxy gateway: %w", err)
	}
	for {
		if g.reachable(g.addr) {
			return nil
		}
		select {
		case exitErr := <-exited:
			// It may have lost the address to a gateway started another
			// way (the service, a terminal): that one serves us too.
			if g.reachable(g.addr) {
				return nil
			}
			return fmt.Errorf("the LeanProxy gateway stopped while starting (%v): check %s and %s.stderr", exitErr, g.logFile, g.logFile)
		default:
		}
		if err := g.sleep(ctx, deadline); err != nil {
			return err
		}
	}
}

func (g *gatewayStarter) sleep(ctx context.Context, deadline time.Time) error {
	if time.Now().After(deadline) {
		return fmt.Errorf("the LeanProxy gateway did not come up at %s within %s: check %s", g.addr, g.timeout, g.logFile)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(g.poll):
		return nil
	}
}

// tryLock takes the start lock. It returns a nil release when another
// process holds it. A lock older than gatewayLockStale is removed: its
// owner died before releasing it.
func (g *gatewayStarter) tryLock() (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(g.lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("create the gateway lock directory: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(g.lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- a fixed name under the user's own config directory
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(g.lockPath) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("take the gateway start lock: %w", err)
		}
		info, statErr := os.Stat(g.lockPath)
		if statErr != nil || time.Since(info.ModTime()) < gatewayLockStale {
			return nil, nil
		}
		_ = os.Remove(g.lockPath)
	}
	return nil, nil
}

// spawnGateway starts the gateway described by spec as a process of its
// own session, so it outlives the `connect` (and the MCP client) that
// started it. extraEnv adds to the environment it inherits.
func spawnGateway(spec daemonSpec, idleExit time.Duration, extraEnv []string) (<-chan error, error) {
	if err := os.MkdirAll(filepath.Dir(spec.LogFile), 0o700); err != nil {
		return nil, err
	}
	if spec.DashAddr != "" {
		if _, err := loadOrCreateServeToken(spec.DashTokenFile); err != nil {
			return nil, err
		}
	}
	args := append(spec.args(), "--idle-exit", idleExit.String())
	cmd := exec.Command(spec.Executable, args...) // #nosec G204 -- our own executable and arguments built here
	cmd.Dir = spec.Home
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.SysProcAttr = detachedProcAttr()
	// What the gateway prints before its log file is open (a flag or
	// config error) lands next to the log, like under launchd.
	stderr, err := os.OpenFile(spec.LogFile+".stderr", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- under the user's own log directory
	if err != nil {
		return nil, err
	}
	defer stderr.Close()
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
}
