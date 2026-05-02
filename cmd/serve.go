package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/gateway"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
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
	serverReg    registry.Registry
	toolReg      router.ToolRegistry
	gatewayTools gateway.GatewayTools
	stdioPool    *pool.StdioPool
)

type Router interface {
	Route(ctx context.Context, method string) (*registry.ServerEntry, error)
	RouteBatch(ctx context.Context, methods []string) ([]*registry.ServerEntry, []error)
}

type Pool interface {
	SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error)
}

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
	stdioPool = pool.NewStdioPool(5, 5*time.Minute, slog.Default())

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
				if srv.Transport == registry.TransportStdio && srv.Enabled != nil && *srv.Enabled {
					if err := stdioPool.StartServer(ctx, srv); err != nil {
						slog.Warn("failed to start server", "name", srv.Name, "error", err)
					}
				}
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
		if stdioPool != nil {
			stdioPool.Close()
		}
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
		go handleConnection(conn, r, gatewayTools, stdioPool)
	}
}

func handleConnection(conn io.ReadWriter, r Router, gt gateway.GatewayTools, p Pool) {
	defer func() {
		if closer, ok := conn.(net.Conn); ok {
			closer.Close()
		}
	}()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			slog.Warn("read error", "error", err)
			return
		}

		if len(line) == 0 {
			continue
		}

		line = trimNewline(line)

		if isBatchRequest(line) {
			handleBatchRequest(ctx, line, writer, r, gt, p)
		} else {
			handleSingleRequest(ctx, line, writer, r, gt, p)
		}

		writer.Flush()
	}
}

func handleSingleRequest(ctx context.Context, line []byte, writer *bufio.Writer, r Router, gt gateway.GatewayTools, p Pool) {
	req, err := proxy.ParseJSONRPCRequest(line)
	if err != nil {
		writeError(writer, proxy.ErrCodeParseError, "Parse error")
		return
	}

	if isGatewayTool(req.Method) {
		handleGatewayTool(ctx, req, writer, gt)
		return
	}

	server, err := r.Route(ctx, req.Method)
	if err != nil {
		writeError(writer, proxy.ErrCodeMethodNotFound, "Method not found")
		return
	}

	resp, err := p.SendRequest(ctx, server.ID, req, 30*time.Second)
	if err != nil {
		writeError(writer, proxy.ErrCodeInternalError, err.Error())
		return
	}

	writeResponse(writer, resp)
}

func handleBatchRequest(ctx context.Context, line []byte, writer *bufio.Writer, r Router, gt gateway.GatewayTools, p Pool) {
	reqs, err := proxy.ParseJSONRPCBatchRequest(line)
	if err != nil {
		writeError(writer, proxy.ErrCodeParseError, "Parse error")
		return
	}

	if len(reqs) == 0 {
		writeError(writer, proxy.ErrCodeInvalidRequest, "Empty batch")
		return
	}

	responses := make([]*proxy.JSONRPCResponse, 0, len(reqs))
	for i := range reqs {
		req := &reqs[i]
		if req.ID == nil {
			continue
		}

		if isGatewayTool(req.Method) {
			resp := handleGatewayToolSync(ctx, req, gt)
			if resp != nil {
				responses = append(responses, resp)
			}
			continue
		}

		server, err := r.Route(ctx, req.Method)
		if err != nil {
			responses = append(responses, &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   proxy.NewJSONRPCError(proxy.ErrCodeMethodNotFound, "Method not found"),
				ID:      req.ID,
			})
			continue
		}

		resp, err := p.SendRequest(ctx, server.ID, req, 30*time.Second)
		if err != nil {
			responses = append(responses, &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   proxy.NewJSONRPCError(proxy.ErrCodeInternalError, err.Error()),
				ID:      req.ID,
			})
			continue
		}

		responses = append(responses, resp)
	}

	data, err := json.Marshal(responses)
	if err != nil {
		writeError(writer, proxy.ErrCodeInternalError, "Failed to marshal batch response")
		return
	}

	fmt.Fprintln(writer, string(data))
}

var ctx = context.Background()

func isGatewayTool(method string) bool {
	return method == "list_servers" || method == "invoke_tool" || method == "search_tools"
}

func handleGatewayTool(ctx context.Context, req *proxy.JSONRPCRequest, writer *bufio.Writer, gt gateway.GatewayTools) {
	resp := handleGatewayToolSync(ctx, req, gt)
	if resp != nil {
		writeResponse(writer, resp)
	}
}

func handleGatewayToolSync(ctx context.Context, req *proxy.JSONRPCRequest, gt gateway.GatewayTools) *proxy.JSONRPCResponse {
	var result json.RawMessage

	switch req.Method {
	case "list_servers":
		servers, listErr := gt.ListServers(ctx)
		if listErr != nil {
			return &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   proxy.NewJSONRPCError(proxy.ErrCodeInternalError, listErr.Error()),
				ID:      req.ID,
			}
		}
		result, _ = json.Marshal(servers)
	case "search_tools":
		var params struct {
			Query string `json:"query"`
		}
		if req.Params != nil {
			json.Unmarshal(req.Params, &params)
		}
		searchResults, searchErr := gt.SearchTools(ctx, params.Query)
		if searchErr != nil {
			return &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   proxy.NewJSONRPCError(proxy.ErrCodeInternalError, searchErr.Error()),
				ID:      req.ID,
			}
		}
		result, _ = json.Marshal(searchResults)
	case "invoke_tool":
		var params gateway.InvokeToolParams
		if req.Params != nil {
			json.Unmarshal(req.Params, &params)
		}
		invokeResult, invokeErr := gt.InvokeTool(ctx, params)
		if invokeErr != nil {
			if rpcErr, ok := invokeErr.(*proxy.JSONRPCError); ok {
				return &proxy.JSONRPCResponse{
					JSONRPC: "2.0",
					Error:   rpcErr,
					ID:      req.ID,
				}
			}
			return &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   proxy.NewJSONRPCError(proxy.ErrCodeInternalError, invokeErr.Error()),
				ID:      req.ID,
			}
		}
		result, _ = json.Marshal(invokeResult)
	}

	return &proxy.JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

func isBatchRequest(data []byte) bool {
	return proxy.IsBatchRequest(data)
}

func writeResponse(writer *bufio.Writer, resp *proxy.JSONRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Warn("failed to marshal response", "error", err)
		return
	}
	fmt.Fprintln(writer, string(data))
}

func writeError(writer *bufio.Writer, code int, message string) {
	resp := &proxy.JSONRPCResponse{
		JSONRPC: "2.0",
		Error:   proxy.NewJSONRPCError(code, message),
		ID:      nil,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Warn("failed to marshal error response", "error", err)
		return
	}
	fmt.Fprintln(writer, string(data))
}

func trimNewline(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}
	if len(data) > 0 && data[len(data)-1] == '\r' {
		data = data[:len(data)-1]
	}
	return data
}