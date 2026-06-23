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
	"sync"
	"syscall"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/cache"
	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/gateway"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/router"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
	"github.com/mmornati/leanproxy-mcp/pkg/toolstore"
	"github.com/mmornati/leanproxy-mcp/pkg/utils/dryrun"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the JSON-RPC streaming proxy server",
	Long:  `Start the LeanProxy MCP proxy server which listens for incoming connections and forwards JSON-RPC requests.`,
	Run:   runServe,
}

var serveFlags struct {
	listenAddr      string
	upstreamURL     string
	providersConfig string
}

var providerDetector = cache.NewProviderDetector()

var (
	serverReg    registry.Registry
	toolReg      router.ToolRegistry
	gatewayTools gateway.GatewayTools
	stdioPool    *pool.StdioPool
	httpPool     *pool.HTTPClientPool
	ssePool      *pool.SSEPool
	unifiedPool  *pool.UnifiedPool
	statusStore  *statusfile.FileStatusStore
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
	serveCmd.Flags().StringVar(&serveFlags.providersConfig, "providers-config", "", "Path to providers config file for provider detection")
	RootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	initLogger(cmd)
	ctx := context.Background()

	dr := dryrun.NewDryRunner(DryRunEnabled)
	if dr.ShouldSkip() {
		dr.Preview("serve_start", map[string]interface{}{
			"listen":   serveFlags.listenAddr,
			"upstream": serveFlags.upstreamURL,
			"config":   GlobalConfigPath,
			"message":  "Would start leanproxy server",
		})
		fmt.Println("Dry-run mode: server start skipped")
		return
	}

	serverReg = registry.NewRegistry(slog.Default(), "")
	toolReg = router.NewToolRegistry()
	r := router.NewRouter(toolReg, serverReg, slog.Default())
	gatewayTools = gateway.NewGatewayTools(serverReg, toolReg, r, slog.Default())
	stdioPool = pool.NewStdioPool(5, 5*time.Minute, slog.Default())
	httpPool = pool.NewHTTPClientPool(slog.Default())
	ssePool = pool.NewSSEPool(slog.Default())
	unifiedPool = pool.NewUnifiedPool(stdioPool, httpPool, ssePool, slog.Default())

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
				if srv.Enabled == nil || !*srv.Enabled {
					continue
				}
				switch srv.Transport {
				case registry.TransportStdio:
					if err := stdioPool.StartServer(ctx, srv); err != nil {
						slog.Warn("failed to start stdio server", "name", srv.Name, "error", err)
					}
				case registry.TransportHTTP:
					if err := httpPool.StartServer(ctx, srv); err != nil {
						slog.Warn("failed to start HTTP server", "name", srv.Name, "error", err)
					}
				case registry.TransportSSE:
					if err := ssePool.StartServer(ctx, srv); err != nil {
						slog.Warn("failed to start SSE server", "name", srv.Name, "error", err)
					}
				}
			}
		}
	} else {
		slog.Info("no config file specified, starting in passthrough mode")
	}

	var toolStore toolstore.Cache
	fileCache, err := toolstore.NewFileCache(slog.Default())
	if err != nil {
		slog.Warn("failed to create tool cache, using no-op cache", "error", err)
		toolStore = toolstore.NewNoOpCache()
	} else {
		toolStore = fileCache
		slog.Info("tool cache enabled", "path", fileCache.GetCacheDir())
	}

	if serveFlags.providersConfig != "" {
		providerDetector = cache.NewProviderDetector(
			cache.WithLogger(slog.Default()),
			cache.WithConfigPath(serveFlags.providersConfig),
		)
	} else {
		providerDetector = cache.NewProviderDetector(cache.WithLogger(slog.Default()))
	}

	handler := mcp.NewHandlerWithToolStore(unifiedPool, slog.Default(), toolStore)

	cacheCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	handler.PopulateToolCache(cacheCtx)

	slog.Info("starting server", "listen", serveFlags.listenAddr, "upstream", serveFlags.upstreamURL)

	ln, err := net.Listen("tcp", serveFlags.listenAddr)
	if err != nil {
		logError("failed to listen: %v", err)
	}

	statusStore, err = statusfile.NewFileStatusStore(serveFlags.listenAddr, slog.Default())
	if err != nil {
		slog.Warn("failed to create status store", "error", err)
	} else {
		slog.Info("status file enabled", "path", statusStore.GetFilePath())
		go updateServerStatusPeriodically()
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGHUP:
				slog.Info("reloading provider config on SIGHUP")
				providerDetector.Reload()
			case syscall.SIGINT, syscall.SIGTERM:
				slog.Info("shutting down server")
				if statusStore != nil {
					statusStore.RemoveFile()
				}
				ln.Close()
				if stdioPool != nil {
					stdioPool.Close()
				}
				if httpPool != nil {
					httpPool.Close()
				}
				if ssePool != nil {
					ssePool.Close()
				}
				os.Exit(0)
			}
		}
	}()

	slog.Info("server ready", "address", ln.Addr().String())

	for {
		conn, err := ln.Accept()
		if err != nil {
			slog.Warn("accept error", "error", err)
			continue
		}
		slog.Debug("connection accepted", "remote", conn.RemoteAddr())
		go handleConnection(conn, r, gatewayTools, unifiedPool)
	}
}

func handleConnection(conn io.ReadWriter, r Router, gt gateway.GatewayTools, p Pool) {
	defer func() {
		if closer, ok := conn.(net.Conn); ok {
			closer.Close()
		}
	}()

	connCtx, connCancel := context.WithCancel(context.Background())
	defer connCancel()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writerMu := &sync.Mutex{}

	var wg sync.WaitGroup

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			slog.Warn("read error", "error", err)
			break
		}

		if len(line) == 0 {
			continue
		}

		line = trimNewline(line)

		wg.Add(1)
		if isBatchRequest(line) {
			go func(l []byte) {
				defer wg.Done()
				handleBatchRequestAsync(connCtx, l, writer, writerMu, r, gt, p)
			}(line)
		} else {
			go func(l []byte) {
				defer wg.Done()
				handleSingleRequestAsync(connCtx, l, writer, writerMu, r, gt, p)
			}(line)
		}
	}

	wg.Wait()
}

func handleSingleRequest(ctx context.Context, line []byte, writer *bufio.Writer, r Router, gt gateway.GatewayTools, p Pool) {
	req, err := proxy.ParseJSONRPCRequest(line)
	if err != nil {
		writeError(writer, errors.ErrCodeParseError, "Parse error")
		return
	}

	if isGatewayTool(req.Method) {
		handleGatewayTool(ctx, req, writer, gt)
		return
	}

	server, err := r.Route(ctx, req.Method)
	if err != nil {
		writeError(writer, errors.ErrCodeMethodNotFound, "Method not found")
		return
	}

	targetURL := server.Address
	if targetURL != "" {
		provider := providerDetector.Detect(targetURL)
		slog.Debug("provider detected", "server", server.ID, "provider", provider, "url", targetURL)
	}

	timeout := GetConfig().RequestTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	resp, err := p.SendRequest(ctx, server.ID, req, timeout)
	if err != nil {
		writeError(writer, errors.ErrCodeInternalError, err.Error())
		return
	}

	writeResponse(writer, resp)
}

func handleSingleRequestAsync(ctx context.Context, line []byte, writer *bufio.Writer, writerMu *sync.Mutex, r Router, gt gateway.GatewayTools, p Pool) {
	req, err := proxy.ParseJSONRPCRequest(line)
	if err != nil {
		writeErrorAsync(writer, writerMu, errors.ErrCodeParseError, "Parse error")
		return
	}

	if isGatewayTool(req.Method) {
		handleGatewayTool(ctx, req, writer, gt)
		writerMu.Lock()
		writer.Flush()
		writerMu.Unlock()
		return
	}

	server, err := r.Route(ctx, req.Method)
	if err != nil {
		writeErrorAsync(writer, writerMu, errors.ErrCodeMethodNotFound, "Method not found")
		return
	}

	targetURL := server.Address
	if targetURL != "" {
		provider := providerDetector.Detect(targetURL)
		slog.Debug("provider detected", "server", server.ID, "provider", provider, "url", targetURL)
	}

	timeout := GetConfig().RequestTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	resp, err := p.SendRequest(ctx, server.ID, req, timeout)
	if err != nil {
		writeErrorAsync(writer, writerMu, errors.ErrCodeInternalError, err.Error())
		return
	}

	writeResponseAsync(writer, writerMu, resp)
}

func handleBatchRequest(ctx context.Context, line []byte, writer *bufio.Writer, r Router, gt gateway.GatewayTools, p Pool) {
	reqs, err := proxy.ParseJSONRPCBatchRequest(line, GetConfig().MaxBatchSize)
	if err != nil {
		writeError(writer, errors.ErrCodeParseError, "Parse error")
		return
	}

	if len(reqs) == 0 {
		writeError(writer, errors.ErrCodeInvalidRequest, "Empty batch")
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
				Error:   errors.NewJSONRPCError(errors.ErrCodeMethodNotFound, "Method not found"),
				ID:      req.ID,
			})
			continue
		}

		targetURL := server.Address

		if targetURL != "" {
			provider := providerDetector.Detect(targetURL)
			slog.Debug("provider detected", "server", server.ID, "provider", provider, "url", targetURL)
		}

		timeout := GetConfig().RequestTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		resp, err := p.SendRequest(ctx, server.ID, req, timeout)
		if err != nil {
			responses = append(responses, &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   errors.NewJSONRPCError(errors.ErrCodeInternalError, err.Error()),
				ID:      req.ID,
			})
			continue
		}

		responses = append(responses, resp)
	}

	data, err := json.Marshal(responses)
	if err != nil {
		writeError(writer, errors.ErrCodeInternalError, "Failed to marshal batch response")
		return
	}

	fmt.Fprintln(writer, string(data))
}

var ctx = context.Background()

func isGatewayTool(method string) bool {
	return method == "invoke_tool" || method == "list_tools" || method == "list_servers"
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
				Error:   errors.NewJSONRPCError(errors.ErrCodeInternalError, listErr.Error()),
				ID:      req.ID,
			}
		}
		result, _ = json.Marshal(servers)
	case "list_tools":
		var params struct {
			ServerName string `json:"server_name"`
		}
		if req.Params != nil {
			json.Unmarshal(req.Params, &params)
		}
		if params.ServerName == "" {
			return &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   errors.NewJSONRPCError(errors.ErrCodeInvalidParams, "server_name parameter is required. Use list_servers to get available servers."),
				ID:      req.ID,
			}
		}
		result, _ = json.Marshal(map[string]interface{}{
			"content": []map[string]string{
				{"type": "text", "text": fmt.Sprintf("list_tools for server '%s' is not available in simple gateway mode. Use stdio mode (leanproxy serve) for full list_tools functionality with tool caching.", params.ServerName)},
			},
		})
	case "invoke_tool":
		var params gateway.InvokeToolParams
		if req.Params != nil {
			json.Unmarshal(req.Params, &params)
		}
		invokeResult, invokeErr := gt.InvokeTool(ctx, params)
		if invokeErr != nil {
			if rpcErr, ok := invokeErr.(*errors.JSONRPCError); ok {
				return &proxy.JSONRPCResponse{
					JSONRPC: "2.0",
					Error:   rpcErr,
					ID:      req.ID,
				}
			}
			return &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   errors.NewJSONRPCError(errors.ErrCodeInternalError, invokeErr.Error()),
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
		Error:   errors.NewJSONRPCError(code, message),
		ID:      nil,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Warn("failed to marshal error response", "error", err)
		return
	}
	fmt.Fprintln(writer, string(data))
}

type ServeConfig struct {
	RequestTimeout time.Duration
	MaxBatchSize   int
}

var serveConfig = &ServeConfig{
	RequestTimeout: 30 * time.Second,
	MaxBatchSize:   100,
}

func GetConfig() *ServeConfig {
	return serveConfig
}

func SetConfig(cfg *ServeConfig) {
	if cfg != nil && cfg.RequestTimeout > 0 {
		serveConfig.RequestTimeout = cfg.RequestTimeout
	}
	if cfg != nil && cfg.MaxBatchSize > 0 {
		serveConfig.MaxBatchSize = cfg.MaxBatchSize
	}
}

func writeResponseAsync(writer *bufio.Writer, mu *sync.Mutex, resp *proxy.JSONRPCResponse) {
	mu.Lock()
	defer mu.Unlock()
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Warn("failed to marshal response", "error", err)
		return
	}
	fmt.Fprintln(writer, string(data))
	writer.Flush()
}

func writeErrorAsync(writer *bufio.Writer, mu *sync.Mutex, code int, message string) {
	resp := &proxy.JSONRPCResponse{
		JSONRPC: "2.0",
		Error:   errors.NewJSONRPCError(code, message),
		ID:      nil,
	}
	mu.Lock()
	defer mu.Unlock()
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Warn("failed to marshal error response", "error", err)
		return
	}
	fmt.Fprintln(writer, string(data))
	writer.Flush()
}

func handleBatchRequestAsync(ctx context.Context, line []byte, writer *bufio.Writer, writerMu *sync.Mutex, r Router, gt gateway.GatewayTools, p Pool) {
	reqs, err := proxy.ParseJSONRPCBatchRequest(line, GetConfig().MaxBatchSize)
	if err != nil {
		writeErrorAsync(writer, writerMu, errors.ErrCodeParseError, "Parse error")
		return
	}

	if len(reqs) == 0 {
		writeErrorAsync(writer, writerMu, errors.ErrCodeInvalidRequest, "Empty batch")
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
				Error:   errors.NewJSONRPCError(errors.ErrCodeMethodNotFound, "Method not found"),
				ID:      req.ID,
			})
			continue
		}

		targetURL := server.Address

		if targetURL != "" {
			provider := providerDetector.Detect(targetURL)
			slog.Debug("provider detected", "server", server.ID, "provider", provider, "url", targetURL)
		}

		timeout := GetConfig().RequestTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		resp, err := p.SendRequest(ctx, server.ID, req, timeout)
		if err != nil {
			responses = append(responses, &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   errors.NewJSONRPCError(errors.ErrCodeInternalError, err.Error()),
				ID:      req.ID,
			})
			continue
		}

		responses = append(responses, resp)
	}

	data, err := json.Marshal(responses)
	if err != nil {
		writeErrorAsync(writer, writerMu, errors.ErrCodeInternalError, "Failed to marshal batch response")
		return
	}

	writerMu.Lock()
	defer writerMu.Unlock()
	fmt.Fprintln(writer, string(data))
	writer.Flush()
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

func updateServerStatusPeriodically() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if statusStore == nil || unifiedPool == nil {
			continue
		}

		servers := unifiedPool.ListServers()
		statuses := make([]statusfile.ServerStatus, 0, len(servers))

		for _, name := range servers {
			state, _ := unifiedPool.GetServerState(name)

			stats := pool.ServerStats{}
			stdioStats, err := stdioPool.GetServerStats(name)
			if err == nil {
				stats = stdioStats
			}

			status := statusfile.ServerStatus{
				Name:         name,
				RequestCount: stats.RequestCount,
				ErrorCount:   stats.ErrorCount,
				RestartCount: stats.RestartCount,
				Uptime:       formatUptime(stats),
			}

			switch state {
			case pool.StateIdle, pool.StateRunning, pool.StateBusy:
				status.Status = "running"
			case pool.StateError:
				status.Status = "error"
			case pool.StateStopped, pool.StateStopping:
				status.Status = "stopped"
			default:
				status.Status = "unknown"
			}

			statuses = append(statuses, status)
		}

		statusStore.UpdateServers(statuses)
	}
}

func formatUptime(stats pool.ServerStats) string {
	if stats.LastRequestAt.IsZero() {
		return "0s"
	}
	duration := time.Since(stats.LastRequestAt)
	if duration < time.Minute {
		return duration.Round(time.Second).String()
	}
	return duration.Round(time.Minute).String()
}
