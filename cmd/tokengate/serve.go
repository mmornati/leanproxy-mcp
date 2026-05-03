package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy/socket"
)

var (
	socketPath   string
	socketPerm   uint32
	socketEnable bool
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start tokengate socket server",
	Long: `Start the tokengate background socket server for IDE extensions.
The server listens on a Unix domain socket and handles JSON-RPC requests
for token resolution, validation, and proxy management.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

		go func() {
			<-sigChan
			cancel()
		}()

		config := socket.ServerConfig{
			Path:       socketPath,
			Perm:       socketPerm,
			MaxMsgSize: 1024 * 1024,
			RateLimit:  100,
		}

		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))

		server, err := socket.NewServer(config, logger)
		if err != nil {
			return err
		}

		handler := socket.NewHandler(nil, nil, nil, nil, func() {
			cancel()
		})
		handler.RegisterMethods(server)

		logger.Info("starting socket server", "path", socketPath)

		go func() {
			if err := server.Serve(ctx); err != nil {
				logger.Error("socket server error", "error", err)
			}
		}()

		<-ctx.Done()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5)
		defer shutdownCancel()

		return server.Shutdown(shutdownCtx)
	},
}

func init() {
	serveCmd.Flags().StringVar(&socketPath, "socket-path", "~/.tokengate/tokengate.sock", "Path to Unix socket")
	serveCmd.Flags().Uint32Var(&socketPerm, "socket-perm", 0700, "Socket file permissions")
	serveCmd.Flags().BoolVar(&socketEnable, "enable", true, "Enable socket server")

	RootCmd.AddCommand(serveCmd)
}