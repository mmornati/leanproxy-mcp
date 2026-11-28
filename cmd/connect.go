package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/bridge"
	"github.com/mmornati/leanproxy-mcp/pkg/streamhttp"
	"github.com/spf13/cobra"
)

var connectFlags struct {
	url         string
	tokenFile   string
	noAutostart bool
	idleExit    time.Duration
	dashboard   string
}

var connectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Bridge a stdio-only MCP client to the shared gateway",
	Long: `Relay one MCP client, over stdin/stdout, to the shared LeanProxy gateway.

Use it as the command of a client that cannot connect to a URL, such as
Claude Desktop's config file:

  { "mcpServers": { "leanproxy": { "command": "leanproxy-mcp", "args": ["connect"] } } }

Unlike "server run --stdio", it starts no upstream server: it is a thin
relay to the one gateway of this machine, so the client shares the
gateway's servers with every other client. If the gateway restarts, the
bridge initializes again on the next request.

When no gateway is running, connect starts it: no service to install. The
gateway it starts is shared by every later connect and stops by itself
--idle-exit after its last client left; the next request starts it again.
With the service installed ("leanproxy-mcp daemon install"), with
--no-autostart or for a gateway on another machine, connect only relays.

The gateway's URL comes from --url, else from the running gateway, else
http://127.0.0.1:8765/mcp; a gateway started by connect listens there. The token comes from $LEANPROXY_SERVE_TOKEN,
else from --token-file (default ~/.config/leanproxy/serve.token).`,
	Args: cobra.NoArgs,
	RunE: runConnect,
}

func init() {
	connectCmd.Flags().StringVar(&connectFlags.url, "url", "", "MCP endpoint of the gateway (default: the running gateway, else http://"+defaultDaemonHTTP+streamhttp.DefaultEndpoint+")")
	connectCmd.Flags().StringVar(&connectFlags.tokenFile, "token-file", "", "File holding the gateway's bearer token (default ~/.config/leanproxy/serve.token)")
	connectCmd.Flags().BoolVar(&connectFlags.noAutostart, "no-autostart", false, "Never start the gateway: only relay to one that is already running")
	connectCmd.Flags().DurationVar(&connectFlags.idleExit, "idle-exit", defaultGatewayIdleExit, "How long a gateway started by connect keeps running after its last client left (0: until stopped)")
	connectCmd.Flags().StringVar(&connectFlags.dashboard, "dashboard", defaultDaemonDash, "Address of the dashboard of a gateway started by connect (loopback only), or 'off'")
	RootCmd.AddCommand(connectCmd)
}

// connectURL resolves the gateway endpoint.
func connectURL(flag string) string {
	if flag != "" {
		return flag
	}
	if info := gatewayStatus(); info != nil && info.HTTP != nil && info.HTTP.URL != "" {
		return info.HTTP.URL
	}
	return "http://" + defaultDaemonHTTP + streamhttp.DefaultEndpoint
}

// connectToken resolves the gateway token: the environment, else the token
// file. A missing file means a gateway without authentication; it is never
// created here.
func connectToken(env, file, home string) (string, error) {
	if env != "" {
		return env, nil
	}
	if file == "" {
		if home == "" {
			return "", nil
		}
		file = serveTokenPath(home)
	}
	data, err := os.ReadFile(file) // #nosec G304 -- the user's own token file, or the one they named
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the gateway token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// connectAutostart reports whether connect may start the gateway behind
// endpoint itself: not when told not to, not when the service manager
// owns the gateway (it restarts it, and two starters would fight over the
// address), and only on this machine (a loopback address).
func connectAutostart(noAutostart bool, endpoint string, serviceInstalled bool) bool {
	if noAutostart || serviceInstalled {
		return false
	}
	u, err := url.Parse(endpoint)
	return err == nil && u.Host != "" && checkLoopbackAddr("http", u.Host) == nil
}

// newConnectStarter builds the starter of the gateway behind endpoint.
// extraEnv is added to the gateway's environment.
func newConnectStarter(endpoint, home string, idleExit time.Duration, dashboard string, extraEnv []string) (*gatewayStarter, error) {
	if idleExit < 0 {
		return nil, fmt.Errorf("--idle-exit must not be negative, got %s", idleExit)
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("cannot start a gateway for %q: not a URL", endpoint)
	}
	if err := checkLoopbackAddr("http", u.Host); err != nil {
		return nil, err
	}
	if dashboard == "off" {
		dashboard = ""
	}
	if dashboard != "" {
		if err := checkLoopbackAddr("dashboard", dashboard); err != nil {
			return nil, err
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot locate the leanproxy-mcp executable: %w", err)
	}
	spec := daemonSpec{
		Executable:    stableExecutable(exe, os.Args[0]),
		HTTPAddr:      u.Host,
		DashAddr:      dashboard,
		TokenFile:     serveTokenPath(home),
		DashTokenFile: dashboardTokenPath(home),
		LogFile:       daemonLogFile(home),
		LogLevel:      "info",
		Home:          home,
	}
	return &gatewayStarter{
		addr:      u.Host,
		lockPath:  filepath.Join(filepath.Dir(spec.TokenFile), gatewayLockName),
		logFile:   spec.LogFile,
		timeout:   gatewayStartTimeout,
		poll:      gatewayStartPoll,
		reachable: tcpReachable,
		spawn:     func() (<-chan error, error) { return spawnGateway(spec, idleExit, extraEnv) },
	}, nil
}

func runConnect(cmd *cobra.Command, args []string) error {
	// stdout carries the protocol: logs go to stderr or --log-file only.
	initLogger(cmd)
	home, _ := os.UserHomeDir()
	env := os.Getenv(serveTokenEnv)
	endpoint := connectURL(connectFlags.url)
	autostart := home != "" && connectAutostart(connectFlags.noAutostart, endpoint, daemonInstalled(home))
	if autostart && env == "" && connectFlags.tokenFile == "" {
		// The gateway is about to be started: create its token now, so
		// the bridge and the gateway agree on it from the first request.
		if _, err := loadOrCreateServeToken(serveTokenPath(home)); err != nil {
			return err
		}
	}
	token, err := connectToken(env, connectFlags.tokenFile, home)
	if err != nil {
		return err
	}
	opts := bridge.Options{URL: endpoint, Token: token}
	if autostart {
		var extraEnv []string
		if connectFlags.tokenFile != "" && token != "" {
			// A token from a file of the user's choosing: the gateway
			// would read the default file, so hand it over.
			extraEnv = append(extraEnv, serveTokenEnv+"="+token)
		}
		starter, err := newConnectStarter(opts.URL, home, connectFlags.idleExit, connectFlags.dashboard, extraEnv)
		if err != nil {
			return err
		}
		opts.Wake = starter.ensure
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	b := bridge.New(opts, os.Stdout)
	return b.Run(ctx, os.Stdin)
}
