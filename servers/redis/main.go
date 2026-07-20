package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/redistools"
)

const serverName = "leanproxy-mcp-redis"
const serverVersion = "1.0.0"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := getConfig()
	client, err := redistools.NewRedisClient(logger, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		fmt.Fprintf(os.Stderr, "Set LEANPROXY_REDIS_ADDRESS environment variable with your Redis address.\n")
		fmt.Fprintf(os.Stderr, "Example: LEANPROXY_REDIS_ADDRESS=127.0.0.1:6379\n")
		os.Exit(1)
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("shutting down redis server")
		cancel()
		os.Exit(0)
	}()

	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	initialized := false

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				logger.Info("stdin closed, shutting down")
				return
			}
			logger.Error("read error", "error", err)
			return
		}

		line = trimNewline(line)
		if len(line) == 0 {
			continue
		}

		var req mcp.Request
		if err := json.Unmarshal(line, &req); err != nil {
			logger.Warn("invalid JSON-RPC", "error", err)
			resp := mcp.Response{
				JSONRPC: mcp.JSONRPCVersion,
				Error:   mcp.NewError(mcp.ErrCodeParseError, "invalid JSON-RPC request"),
				ID:      nil,
			}
			writeResponse(writer, &resp)
			continue
		}

		resp := handleRequest(ctx, logger, client, &req, &initialized)
		if resp != nil {
			writeResponse(writer, resp)
		}
	}
}

func getConfig() redistools.Config {
	cfg := redistools.DefaultConfig()

	if addr := os.Getenv("LEANPROXY_REDIS_ADDRESS"); addr != "" {
		cfg.Address = addr
	}

	if pw := os.Getenv("LEANPROXY_REDIS_PASSWORD"); pw != "" {
		cfg.Password = pw
	}

	if ps := os.Getenv("LEANPROXY_REDIS_POOL_SIZE"); ps != "" {
		if n, err := parseInt(ps); err == nil && n > 0 {
			cfg.PoolSize = n
		}
	}

	if tls := os.Getenv("LEANPROXY_REDIS_TLS"); tls == "true" || tls == "1" {
		cfg.UseTLS = true
	}

	return cfg
}

func parseInt(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty string")
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %s", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func handleRequest(ctx context.Context, logger *slog.Logger, client *redistools.RedisClient, req *mcp.Request, initialized *bool) *mcp.Response {
	switch req.Method {
	case "initialize":
		return handleInitialize(req, initialized)
	case "notifications/initialized":
		*initialized = true
		logger.Info("client initialized")
		return nil
	case "tools/list":
		return handleToolsList(client, req)
	case "tools/call":
		return handleToolsCall(ctx, logger, client, req)
	case "ping":
		return handlePing(req)
	case "shutdown":
		logger.Info("shutdown requested")
		os.Exit(0)
		return nil
	default:
		return &mcp.Response{
			JSONRPC: mcp.JSONRPCVersion,
			Error:   mcp.NewError(mcp.ErrCodeMethodNotFound, fmt.Sprintf("method not found: %s", req.Method)),
			ID:      req.ID,
		}
	}
}

func handleInitialize(req *mcp.Request, initialized *bool) *mcp.Response {
	*initialized = true

	result := mcp.InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: mcp.ServerCapabilities{
			Tools: &mcp.ToolsCapability{ListChanged: false},
		},
		ServerInfo: mcp.ServerInfo{
			Name:    serverName,
			Version: serverVersion,
		},
	}

	resultBytes, _ := json.Marshal(result)
	return &mcp.Response{
		JSONRPC: mcp.JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}
}

func handleToolsList(client *redistools.RedisClient, req *mcp.Request) *mcp.Response {
	defs := client.GetTools()
	tools := make([]mcp.Tool, 0, len(defs))
	for _, d := range defs {
		tools = append(tools, mcp.Tool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
		})
	}

	result := mcp.ToolsListResult{Tools: tools}
	resultBytes, _ := json.Marshal(result)

	return &mcp.Response{
		JSONRPC: mcp.JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}
}

func handleToolsCall(ctx context.Context, logger *slog.Logger, client *redistools.RedisClient, req *mcp.Request) *mcp.Response {
	var params mcp.ToolsCallParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp := &mcp.Response{
				JSONRPC: mcp.JSONRPCVersion,
				Error:   mcp.NewError(mcp.ErrCodeInvalidParams, fmt.Sprintf("invalid params: %v", err)),
				ID:      req.ID,
			}
			return resp
		}
	}

	if params.Name == "" {
		return &mcp.Response{
			JSONRPC: mcp.JSONRPCVersion,
			Error:   mcp.NewError(mcp.ErrCodeInvalidParams, "tool name is required"),
			ID:      req.ID,
		}
	}

	result, err := client.CallTool(ctx, params.Name, params.Arguments)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "i/o timeout") {
			return &mcp.Response{
				JSONRPC: mcp.JSONRPCVersion,
				Error:   mcp.NewError(mcp.ErrCodeInternalError, fmt.Sprintf("redis connection error: %s", errMsg)),
				ID:      req.ID,
			}
		}
		return &mcp.Response{
			JSONRPC: mcp.JSONRPCVersion,
			Error:   mcp.NewError(mcp.ErrCodeInternalError, errMsg),
			ID:      req.ID,
		}
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return &mcp.Response{
			JSONRPC: mcp.JSONRPCVersion,
			Error:   mcp.NewError(mcp.ErrCodeInternalError, fmt.Sprintf("marshal result: %v", err)),
			ID:      req.ID,
		}
	}

	content := mcp.ToolsCallResult{
		Content: []mcp.ContentBlock{
			{Type: "text", Text: string(resultBytes)},
		},
	}

	contentBytes, _ := json.Marshal(content)
	return &mcp.Response{
		JSONRPC: mcp.JSONRPCVersion,
		Result:  contentBytes,
		ID:      req.ID,
	}
}

func handlePing(req *mcp.Request) *mcp.Response {
	result := map[string]string{"status": "ok"}
	resultBytes, _ := json.Marshal(result)
	return &mcp.Response{
		JSONRPC: mcp.JSONRPCVersion,
		Result:  resultBytes,
		ID:      req.ID,
	}
}

func writeResponse(writer *bufio.Writer, resp *mcp.Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("marshal response", "error", err)
		return
	}
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
