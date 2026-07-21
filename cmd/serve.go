package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	"github.com/mmornati/leanproxy-mcp/pkg/dashboard"
	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
	"github.com/mmornati/leanproxy-mcp/pkg/cache"
	"github.com/mmornati/leanproxy-mcp/pkg/cache/embedder"
	"github.com/mmornati/leanproxy-mcp/pkg/cache/vectordb"
	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/gateway"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/modelrouter"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/router"
	"github.com/mmornati/leanproxy-mcp/pkg/sidecar"
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
	listenAddr         string
	upstreamURL        string
	providersConfig    string
	cacheStrategy      string
	embedProvider      string
	ollamaURL          string
	ollamaModel        string
	openAIModel        string
	embedPoolSize      int
	metricsBind        string
	modelRouterEnabled bool
	modelRouterConfig  string
	sidecarProvider    string
	sidecarModel       string
	sidecarURL         string
	dashboardBind      string
	dashboardToken     string
}

var metricsServer *http.Server
var dashboardServer *http.Server

var providerDetector atomic.Pointer[cache.ProviderDetector]
var breakpointInjector atomic.Pointer[cache.BreakpointInjector]
var globalVectorStore atomic.Value
var globalInjectionClassifier atomic.Pointer[injection.Classifier]
var globalInjectionDispatcher atomic.Pointer[injection.Dispatcher]

func init() {
	providerDetector.Store(cache.NewProviderDetector())
}

var (
	serverReg         registry.Registry
	toolReg           router.ToolRegistry
	gatewayTools      gateway.GatewayTools
	stdioPool         *pool.StdioPool
	httpPool          *pool.HTTPClientPool
	ssePool           *pool.SSEPool
	unifiedPool       *pool.UnifiedPool
	statusStore       *statusfile.FileStatusStore
	globalModelRouter modelrouter.ModelRouter
	serverTiers       map[string]string
	globalSidecar     *sidecar.Manager
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
	serveCmd.Flags().StringVar(&serveFlags.cacheStrategy, "cache-strategy", "off", "Cache breakpoint injection strategy for Anthropic requests: off (default, no injection), aggressive (last system + last tool), balanced (largest block only)")
	serveCmd.Flags().StringVar(&serveFlags.embedProvider, "embed-provider", "", "Embedding provider: ollama or openai (empty = disabled)")
	serveCmd.Flags().StringVar(&serveFlags.ollamaURL, "ollama-url", "http://localhost:11434", "Ollama server URL")
	serveCmd.Flags().StringVar(&serveFlags.ollamaModel, "ollama-model", "nomic-embed-text", "Ollama embedding model")
	serveCmd.Flags().StringVar(&serveFlags.openAIModel, "openai-model", "text-embedding-3-small", "OpenAI embedding model")
	serveCmd.Flags().IntVar(&serveFlags.embedPoolSize, "embed-pool-size", 4, "Embedder worker pool size")
	serveCmd.Flags().StringVar(&serveFlags.metricsBind, "metrics-bind", "", "Metrics endpoint bind address (e.g. 127.0.0.1:9090). Set to 'off' or empty to disable.")
	serveCmd.Flags().BoolVar(&serveFlags.modelRouterEnabled, "model-router", false, "Enable per-tool model routing based on complexity_tier")
	serveCmd.Flags().StringVar(&serveFlags.modelRouterConfig, "model-router-config", "", "Path to model router YAML config (uses defaults if not set)")
	serveCmd.Flags().StringVar(&serveFlags.sidecarProvider, "sidecar-provider", "", "Sidecar provider (ollama) for local LLM redaction (empty = disabled)")
	serveCmd.Flags().StringVar(&serveFlags.sidecarModel, "sidecar-model", "llama3.1:8b", "Sidecar model name")
	serveCmd.Flags().StringVar(&serveFlags.sidecarURL, "sidecar-url", "http://localhost:11434", "Sidecar server URL")
	serveCmd.Flags().StringVar(&serveFlags.dashboardBind, "dashboard-bind", "127.0.0.1:9090", "Dashboard endpoint bind address (e.g. 127.0.0.1:9090). Set to 'off' or empty to disable.")
	serveCmd.Flags().StringVar(&serveFlags.dashboardToken, "dashboard-token", "", "Bearer token for dashboard access from non-loopback addresses")
	RootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	initLogger(cmd)
	ctx := context.Background()

	dr := dryrun.NewDryRunner(DryRunEnabled)
	if dr.ShouldSkip() {
		dr.Preview("serve_start", map[string]interface{}{
			"listen":           serveFlags.listenAddr,
			"upstream":         serveFlags.upstreamURL,
			"config":           GlobalConfigPath,
			"providers_config": serveFlags.providersConfig,
			"message":          "Would start leanproxy server",
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

	var loadedCfg *migrate.Config
	if configPath != "" {
		var err error
		loadedCfg, err = migrate.LoadConfig(ctx, configPath)
		if err != nil {
			slog.Warn("failed to load config", "path", configPath, "error", err)
		} else if loadedCfg != nil {
			slog.Info("loaded server config", "path", configPath, "server_count", len(loadedCfg.Servers))
			for _, srv := range loadedCfg.Servers {
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

	modelRouterCfg := modelrouter.DefaultConfig()
	if serveFlags.modelRouterConfig != "" {
		mrCfg, err := modelrouter.LoadConfig(serveFlags.modelRouterConfig)
		if err != nil {
			slog.Warn("failed to load model router config", "path", serveFlags.modelRouterConfig, "error", err)
		} else {
			modelRouterCfg = mrCfg
			slog.Info("loaded model router config", "path", serveFlags.modelRouterConfig)
		}
	}
	serverTiers = make(map[string]string)
	if loadedCfg != nil {
		for _, srv := range loadedCfg.Servers {
			if srv.ComplexityTier != "" {
				serverTiers[srv.Name] = srv.ComplexityTier
			}
		}
	}
	if serveFlags.modelRouterEnabled {
		globalModelRouter = modelrouter.NewWithEnvOverride(modelRouterCfg, slog.Default())
		slog.Info("model router enabled",
			"default_tier", modelRouterCfg.DefaultTier,
		)
	} else {
		slog.Debug("model router disabled")
	}

	{
		sidecarCfg := sidecar.Config{
			Provider: serveFlags.sidecarProvider,
			Model:    serveFlags.sidecarModel,
			URL:      serveFlags.sidecarURL,
		}
		var err error
		globalSidecar, err = sidecar.NewManager(sidecarCfg, slog.Default())
		if err != nil {
			slog.Warn("sidecar: initialization failed", "error", err)
		}
		if globalSidecar != nil && globalSidecar.Enabled() {
			slog.Info("sidecar enabled",
				"provider", globalSidecar.Provider(),
				"model", globalSidecar.Model(),
			)
		}
	}

	initVectorStore(loadedCfg)

	initSemanticCache(ctx)

	if loadedCfg != nil && loadedCfg.Injection != nil {
		classifier, err := loadedCfg.Injection.BuildClassifier()
		if err != nil {
			slog.Warn("injection: failed to build classifier", "error", err)
		} else if classifier != nil {
			globalInjectionClassifier.Store(classifier)
			slog.Info("injection classifier initialized",
				"threshold", loadedCfg.Injection.Threshold)
		}
		dispatcher := loadedCfg.Injection.BuildDispatcher()
		globalInjectionDispatcher.Store(dispatcher)
		slog.Info("injection dispatcher initialized",
			"policies", len(dispatcher.Rules()))
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
		providerDetector.Store(cache.NewProviderDetector(
			cache.WithLogger(slog.Default()),
			cache.WithConfigPath(serveFlags.providersConfig),
		))
	} else {
		providerDetector.Store(cache.NewProviderDetector(cache.WithLogger(slog.Default())))
	}

	{
		strategy := cache.InjectStrategy(serveFlags.cacheStrategy)
		switch strategy {
		case cache.StrategyOff, cache.StrategyAggressive, cache.StrategyBalanced:
		default:
			slog.Warn("invalid cache-strategy, falling back to off", "value", serveFlags.cacheStrategy)
			strategy = cache.StrategyOff
		}
		breakpointInjector.Store(cache.NewBreakpointInjector(
			cache.WithInjectLogger(slog.Default()),
			cache.WithStrategy(strategy),
		))
	}

	if serveFlags.embedProvider != "" {
		embedCfg := embedder.Config{Provider: embedder.Provider(serveFlags.embedProvider)}
		switch embedCfg.Provider {
		case embedder.ProviderOllama:
			embedCfg.Ollama = &embedder.OllamaConfig{
				URL:   serveFlags.ollamaURL,
				Model: serveFlags.ollamaModel,
			}
		case embedder.ProviderOpenAI:
			embedCfg.OpenAI = &embedder.OpenAIConfig{
				Model: serveFlags.openAIModel,
			}
		default:
			logError("unknown embed provider %q: must be 'ollama' or 'openai'", serveFlags.embedProvider)
		}
		if err := bouncer.SetupEmbedder(embedCfg, embedder.PoolConfig{Size: serveFlags.embedPoolSize}); err != nil {
			logError("embedder setup failed (failing startup): %v", err)
		}
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

	metricsServer, err = metrics.ListenAndServe(serveFlags.metricsBind, slog.Default())
	if err != nil {
		slog.Warn("failed to start metrics endpoint", "error", err)
	}

	dashboardServer, err = dashboard.ListenAndServe(dashboard.Config{
		Bind:  serveFlags.dashboardBind,
		Token: serveFlags.dashboardToken,
	}, slog.Default())
	if err != nil {
		slog.Warn("failed to start dashboard endpoint", "error", err)
	}

	go startRegistryFeedSync(ctx, func(entries []registry.RegistryFeedEntry) {
		if sc := cache.GlobalSemanticCache(); sc != nil {
			count := sc.PurgeAll()
			if count > 0 {
				slog.Info("registry refresh: purged semantic cache entries",
					"count", count,
					"entries_synced", len(entries),
				)
			}
		}
	})

	sigChan := make(chan os.Signal, 4)
	signal.Notify(sigChan, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	var shuttingDown atomic.Bool
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in signal handler", "panic", r)
			}
		}()
		for sig := range sigChan {
			switch sig {
			case syscall.SIGHUP:
				if shuttingDown.Load() {
					slog.Info("ignoring SIGHUP during shutdown")
					continue
				}
				slog.Info("reloading provider config on SIGHUP")
				det := providerDetector.Load()
				if det == nil {
					continue
				}
				if err := det.Reload(); err != nil {
					slog.Warn("provider reload reported error", "error", err)
				}
			case syscall.SIGINT, syscall.SIGTERM:
				if !shuttingDown.CompareAndSwap(false, true) {
					continue
				}
				slog.Info("shutting down server")
				signal.Stop(sigChan)
				if metricsServer != nil {
					metricsServer.Close()
				}
				if dashboardServer != nil {
					dashboardServer.Close()
				}
				if statusStore != nil {
					statusStore.RemoveFile()
				}
				if globalSidecar != nil {
					globalSidecar.Close()
				}
				ln.Close()
				if sc := cache.GlobalSemanticCache(); sc != nil {
					sc.Stop()
				}
				if stdioPool != nil {
					stdioPool.Close()
				}
				if httpPool != nil {
					httpPool.Close()
				}
				if ssePool != nil {
					ssePool.Close()
				}
				if v := globalVectorStore.Load(); v != nil {
					if store, ok := v.(vectordb.Store); ok {
						store.Close()
					}
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

	if resp := checkInjection(req); resp != nil {
		writeResponse(writer, resp)
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

	logModelSelection(ctx, server, req.Method)
	recordProvider(server)
	provider := injectBreakpoints(server, req)

	cached, prompt, embedding := semanticCacheLookup(ctx, req)
	if cached != nil && cached.HitType != cache.HitMiss {
		writeResponse(writer, cachedResponse(req, cached.Response))
		return
	}

	timeout := GetConfig().RequestTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	redactWithSidecar(ctx, req)

	resp, err := p.SendRequest(ctx, server.ID, req, timeout)
	if err != nil {
		writeError(writer, errors.ErrCodeInternalError, err.Error())
		return
	}

	if resp.Error == nil {
		cache.ProcessResponseFor(provider, resp.Result)
		semanticCacheStore(ctx, req, prompt, resp.Result, embedding)
	}

	writeResponse(writer, resp)
}

func handleSingleRequestAsync(ctx context.Context, line []byte, writer *bufio.Writer, writerMu *sync.Mutex, r Router, gt gateway.GatewayTools, p Pool) {
	req, err := proxy.ParseJSONRPCRequest(line)
	if err != nil {
		writeErrorAsync(writer, writerMu, errors.ErrCodeParseError, "Parse error")
		return
	}

	if resp := checkInjection(req); resp != nil {
		writeResponseAsync(writer, writerMu, resp)
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

	logModelSelection(ctx, server, req.Method)
	recordProvider(server)
	provider := injectBreakpoints(server, req)

	cached, prompt, embedding := semanticCacheLookup(ctx, req)
	if cached != nil && cached.HitType != cache.HitMiss {
		writeResponseAsync(writer, writerMu, cachedResponse(req, cached.Response))
		return
	}

	timeout := GetConfig().RequestTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	redactWithSidecar(ctx, req)

	resp, err := p.SendRequest(ctx, server.ID, req, timeout)
	if err != nil {
		writeErrorAsync(writer, writerMu, errors.ErrCodeInternalError, err.Error())
		return
	}

	if resp.Error == nil {
		cache.ProcessResponseFor(provider, resp.Result)
		semanticCacheStore(ctx, req, prompt, resp.Result, embedding)
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

		if resp := checkInjection(req); resp != nil {
			responses = append(responses, resp)
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

		logModelSelection(ctx, server, req.Method)
		recordProvider(server)
		provider := injectBreakpoints(server, req)

		cached, prompt, embedding := semanticCacheLookup(ctx, req)
		if cached != nil && cached.HitType != cache.HitMiss {
			responses = append(responses, cachedResponse(req, cached.Response))
			continue
		}

		timeout := GetConfig().RequestTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}

		redactWithSidecar(ctx, req)

		resp, err := p.SendRequest(ctx, server.ID, req, timeout)
		if err != nil {
			responses = append(responses, &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   errors.NewJSONRPCError(errors.ErrCodeInternalError, err.Error()),
				ID:      req.ID,
			})
			continue
		}

		if resp.Error == nil {
			cache.ProcessResponseFor(provider, resp.Result)
			semanticCacheStore(ctx, req, prompt, resp.Result, embedding)
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

func logModelSelection(ctx context.Context, server *registry.ServerEntry, method string) {
	if globalModelRouter == nil || server == nil {
		return
	}
	tier := serverTiers[server.ID]
	if tier == "" {
		tier = string(modelrouter.TierMedium)
	}
	sel, err := globalModelRouter.Select(ctx, modelrouter.Tier(tier))
	if err != nil {
		slog.Debug("model selection failed", "method", method, "tier", tier, "error", err)
		return
	}
	slog.Debug("model router selected",
		"method", method,
		"tier", tier,
		"provider", sel.Provider,
		"model", sel.Model,
	)
}

func recordProvider(server *registry.ServerEntry) {
	if server == nil || server.Address == "" {
		return
	}
	det := providerDetector.Load()
	if det == nil {
		return
	}
	provider := det.Detect(server.Address)
	slog.Debug("provider detected", "server", server.ID, "provider", provider, "url", server.Address)
}

func injectBreakpoints(server *registry.ServerEntry, req *proxy.JSONRPCRequest) cache.Provider {
	if server == nil || server.Address == "" || req == nil || len(req.Params) == 0 {
		embedOutboundPayload(req)
		return cache.ProviderOther
	}
	det := providerDetector.Load()
	if det == nil {
		embedOutboundPayload(req)
		return cache.ProviderOther
	}
	provider := det.Detect(server.Address)
	if provider != cache.ProviderAnthropic {
		embedOutboundPayload(req)
		return provider
	}

	inputEstimate := int64(len(req.Params)) / 4
	hasBreakpoint := false

	inj := breakpointInjector.Load()
	if inj != nil && inj.Strategy() != cache.StrategyOff {
		modified, err := inj.Inject(req.Params)
		if err != nil {
			slog.Debug("breakpoint injection skipped", "error", err)
			cache.GlobalCacheStatsTracker().RecordRequest(provider, false, inputEstimate)
			embedOutboundPayload(req)
			return provider
		}
		req.Params = modified
		hasBreakpoint = true
		slog.Debug("cache breakpoints injected", "server", server.ID)
	}

	embedOutboundPayload(req)

	cache.GlobalCacheStatsTracker().RecordRequest(provider, hasBreakpoint, inputEstimate)
	return provider
}

func checkInjection(req *proxy.JSONRPCRequest) *proxy.JSONRPCResponse {
	classifier := globalInjectionClassifier.Load()
	dispatcher := globalInjectionDispatcher.Load()
	if classifier == nil || dispatcher == nil {
		return nil
	}

	if len(req.Params) == 0 {
		return nil
	}

	payload := string(req.Params)
	result := classifier.Classify(payload)
	if result.RiskScore == 0 {
		return nil
	}

	actionResult := dispatcher.Dispatch(result)
	switch actionResult.Action {
	case injection.ActionBlock:
		return &proxy.JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   errors.NewJSONRPCError(errors.ErrCodeInvalidRequest, actionResult.Message),
			ID:      req.ID,
		}
	case injection.ActionQuarantine:
		data, _ := json.Marshal(actionResult.Message)
		return &proxy.JSONRPCResponse{
			JSONRPC: "2.0",
			Result:  data,
			ID:      req.ID,
		}
	case injection.ActionRedact:
		req.Params = json.RawMessage(actionResult.TransformedPayload)
		return nil
	default:
		return nil
	}
}

func embedOutboundPayload(req *proxy.JSONRPCRequest) {
	if req == nil || len(req.Params) == 0 {
		return
	}
	if bouncer.GlobalEmbedPool() == nil {
		return
	}
	toolName := extractToolName(req)
	bouncer.EmbedToolCall(context.Background(), bouncer.EmbedRequest{
		ToolName: toolName,
		Args:     req.Params,
	})
}

func redactWithSidecar(ctx context.Context, req *proxy.JSONRPCRequest) {
	if globalSidecar == nil || !globalSidecar.Enabled() {
		return
	}
	if req == nil || len(req.Params) == 0 {
		return
	}
	redacted, err := bouncer.RedactJSONWithSidecar(ctx, req.Params, nil, globalSidecar)
	if err != nil {
		slog.Warn("sidecar: redaction error", "error", err)
		return
	}
	req.Params = redacted
}

// semanticCacheDenylist lists JSON-RPC lifecycle/listing methods whose
// responses must never be cached.
var semanticCacheDenylist = map[string]bool{
	"initialize":                true,
	"ping":                      true,
	"notifications/initialized": true,
	"tools/list":                true,
	"resources/list":            true,
	"prompts/list":              true,
}

const semanticEmbedTimeout = 2 * time.Second

// semanticCacheLookup checks the semantic cache for a cached response to
// req. It returns the lookup result (miss when caching does not apply), the
// canonical prompt, and the request embedding so the caller can store a
// fresh response after upstream execution.
func semanticCacheLookup(ctx context.Context, req *proxy.JSONRPCRequest) (*cache.SemanticCacheResult, string, []float32) {
	sc := cache.GlobalSemanticCache()
	if sc == nil || req == nil || len(req.Params) == 0 || semanticCacheDenylist[req.Method] {
		return nil, "", nil
	}
	toolName := extractToolName(req)
	if toolName == "" {
		return nil, "", nil
	}
	prompt := toolName + ":" + string(req.Params)
	embedding := embedForCache(ctx, toolName, req.Params)
	result, err := sc.Get(ctx, prompt, toolName, embedding)
	if err != nil {
		slog.Debug("semantic cache lookup failed", "tool", toolName, "error", err)
		return nil, prompt, embedding
	}
	return result, prompt, embedding
}

// semanticCacheStore writes a fresh upstream response into the semantic
// cache using the prompt/embedding captured during lookup.
func semanticCacheStore(ctx context.Context, req *proxy.JSONRPCRequest, prompt string, result json.RawMessage, embedding []float32) {
	sc := cache.GlobalSemanticCache()
	if sc == nil || prompt == "" || len(result) == 0 {
		return
	}
	if err := sc.Set(ctx, prompt, result, extractToolName(req), embedding); err != nil {
		slog.Debug("semantic cache store failed", "error", err)
	}
}

// embedForCache embeds synchronously with a bounded timeout so cache lookups
// never stall the request path waiting on an embedding provider.
func embedForCache(ctx context.Context, toolName string, args json.RawMessage) []float32 {
	pool := bouncer.GlobalEmbedPool()
	if pool == nil {
		return nil
	}
	ectx, cancel := context.WithTimeout(ctx, semanticEmbedTimeout)
	defer cancel()
	select {
	case out, ok := <-pool.Embed(ectx, embedder.EmbedRequest{ToolName: toolName, Args: args}):
		if !ok || out.Err != nil {
			return nil
		}
		return out.Embedding.Vector
	case <-ectx.Done():
		return nil
	}
}

func cachedResponse(req *proxy.JSONRPCRequest, result json.RawMessage) *proxy.JSONRPCResponse {
	return &proxy.JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  result,
		ID:      req.ID,
	}
}

func extractToolName(req *proxy.JSONRPCRequest) string {
	if req == nil {
		return ""
	}
	if req.Method != "" && isToolCallMethod(req.Method) {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(req.Params, &p); err == nil && p.Name != "" {
			return p.Name
		}
	}
	return req.Method
}

func isToolCallMethod(method string) bool {
	return method == "tools/call" || method == "invoke_tool"
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

		if resp := checkInjection(req); resp != nil {
			responses = append(responses, resp)
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

		logModelSelection(ctx, server, req.Method)
		recordProvider(server)
		provider := injectBreakpoints(server, req)

		cached, prompt, embedding := semanticCacheLookup(ctx, req)
		if cached != nil && cached.HitType != cache.HitMiss {
			responses = append(responses, cachedResponse(req, cached.Response))
			continue
		}

		timeout := GetConfig().RequestTimeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}

		redactWithSidecar(ctx, req)

		resp, err := p.SendRequest(ctx, server.ID, req, timeout)
		if err != nil {
			responses = append(responses, &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   errors.NewJSONRPCError(errors.ErrCodeInternalError, err.Error()),
				ID:      req.ID,
			})
			continue
		}

		if resp.Error == nil {
			cache.ProcessResponseFor(provider, resp.Result)
			semanticCacheStore(ctx, req, prompt, resp.Result, embedding)
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

func startRegistryFeedSync(ctx context.Context, onSync func(entries []registry.RegistryFeedEntry)) {
	cacheDir, err := registry.LeanProxyDir()
	if err != nil {
		slog.Warn("registry feed: unable to determine cache dir", "error", err)
		return
	}
	if cacheDir == "" {
		slog.Warn("registry feed: empty cache dir; skipping")
		return
	}

	fetcher := registry.NewFeedFetcher(slog.Default(), cacheDir)

	if notice := fetcher.CacheStaleInfo(); notice != "" {
		slog.Warn(notice)
	}

	if onSync != nil {
		fetcher.OnSync(onSync)
	}

	fetcher.StartPeriodicRefresh(ctx)
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

func initSemanticCache(ctx context.Context) {
	vs := globalVectorStore.Load()
	var store vectordb.Store
	if vs != nil {
		store, _ = vs.(vectordb.Store)
	}

	sc := cache.NewSemanticCache(store, slog.Default(), 0)
	cache.SetGlobalSemanticCache(sc)
	sc.Start(ctx)
	slog.Info("semantic cache initialized",
		"ttl", cache.DefaultSemanticTTL,
		"vector_store", store != nil,
	)
}

func initVectorStore(cfg *migrate.Config) {
	var vsConfig *migrate.VectorStoreConfig
	if cfg != nil && cfg.Cache != nil {
		vsConfig = cfg.Cache.VectorStore
	}
	store, err := vectordb.NewStore(vsConfig, slog.Default())
	if err != nil {
		slog.Warn("vector store init failed, continuing without vector store", "error", err)
		return
	}
	globalVectorStore.Store(store)
	backend := "sqlite-vec"
	if vsConfig != nil && vsConfig.Backend != "" {
		backend = vsConfig.Backend
	}
	slog.Info("vector store initialized", "backend", backend)
}
