package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mmornati/leanproxy-mcp/pkg/bridge"
	"github.com/mmornati/leanproxy-mcp/pkg/streamhttp"
	"github.com/spf13/cobra"
)

var connectFlags struct {
	url       string
	tokenFile string
}

var connectCmd = &cobra.Command{
	Use:   "connect",
	Short: "Bridge a stdio-only MCP client to the shared gateway",
	Long: `Relay one MCP client, over stdin/stdout, to the shared LeanProxy gateway.

Use it as the command of a client that cannot connect to a URL, such as
Claude Desktop's config file:

  { "mcpServers": { "leanproxy": { "command": "leanproxy-mcp", "args": ["connect"] } } }

Unlike "server run --stdio", it starts no upstream server: it is a thin
relay to the gateway installed with "leanproxy-mcp daemon install", so the
client shares the gateway's servers with every other client. If the
gateway restarts, the bridge initializes again on the next request.

The gateway's URL comes from --url, else from the running gateway, else
http://127.0.0.1:8765/mcp. The token comes from $LEANPROXY_SERVE_TOKEN,
else from --token-file (default ~/.config/leanproxy/serve.token).`,
	Args: cobra.NoArgs,
	RunE: runConnect,
}

func init() {
	connectCmd.Flags().StringVar(&connectFlags.url, "url", "", "MCP endpoint of the gateway (default: the running gateway, else http://"+defaultDaemonHTTP+streamhttp.DefaultEndpoint+")")
	connectCmd.Flags().StringVar(&connectFlags.tokenFile, "token-file", "", "File holding the gateway's bearer token (default ~/.config/leanproxy/serve.token)")
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

func runConnect(cmd *cobra.Command, args []string) error {
	// stdout carries the protocol: logs go to stderr or --log-file only.
	initLogger(cmd)
	home, _ := os.UserHomeDir()
	token, err := connectToken(os.Getenv(serveTokenEnv), connectFlags.tokenFile, home)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	b := bridge.New(bridge.Options{URL: connectURL(connectFlags.url), Token: token}, os.Stdout)
	return b.Run(ctx, os.Stdin)
}
