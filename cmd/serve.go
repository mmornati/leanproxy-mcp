package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"

	"github.com/mmornati/leanproxy-mcp/pkg/gateway"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/router"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the JSON-RPC streaming proxy server",
	Long:  `Start the LeanProxy MCP proxy server which listens for incoming connections and forwards JSON-RPC requests.`,
	Run:   runServe,
}

var serveFlags struct {
	listenAddr string
	upstreamURL string
}

var (
	serverReg   registry.Registry
	toolReg     router.ToolRegistry
	gatewayTools gateway.GatewayTools
)

func init() {
	serveCmd.Flags().StringVar(&serveFlags.listenAddr, "listen", "127.0.0.1:8080", "Address to listen on")
	serveCmd.Flags().StringVar(&serveFlags.upstreamURL, "upstream", "http://localhost:8081", "Upstream JSON-RPC server URL")
	RootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	serverReg = registry.NewRegistry(slog.Default(), "")
	toolReg = router.NewToolRegistry()
	r := router.NewRouter(toolReg, serverReg, slog.Default())
	gatewayTools = gateway.NewGatewayTools(serverReg, toolReg, r, slog.Default())

	configPath := GlobalConfigPath
	if configPath == "" {
		usr, err := user.Current()
		if err == nil {
			configPath = filepath.Join(usr.HomeDir, ".config", "leanproxy_servers.yaml")
		}
	}

	if configPath != "" {
		cfg, err := migrate.LoadConfig(ctx, configPath)
		if err != nil {
			slog.Warn("failed to load config", "path", configPath, "error", err)
		} else if cfg != nil {
			slog.Info("loaded server config", "path", configPath, "server_count", len(cfg.Servers))
			for _, srv := range cfg.Servers {
				slog.Info("server configured",
					"name", srv.Name,
					"transport", srv.Transport,
					"enabled", srv.Enabled != nil && *srv.Enabled,
				)
			}
		}
	} else {
		slog.Info("no config file specified, starting in passthrough mode")
	}

	slog.Info("starting server", "listen", serveFlags.listenAddr, "upstream", serveFlags.upstreamURL)

	ln, err := net.Listen("tcp", serveFlags.listenAddr)
	if err != nil {
		logError("failed to listen: %v", err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		slog.Info("shutting down server")
		ln.Close()
		os.Exit(0)
	}()

	slog.Info("server ready", "address", ln.Addr().String())

	for {
		conn, err := ln.Accept()
		if err != nil {
			slog.Warn("accept error", "error", err)
			continue
		}
		slog.Debug("connection accepted", "remote", conn.RemoteAddr())
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	fmt.Fprintf(conn, "JSON-RPC streaming proxy ready\n")
}