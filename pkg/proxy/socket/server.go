package socket

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	ID      interface{}     `json:"id"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

type MethodHandler func(ctx context.Context, params json.RawMessage) (interface{}, error)

type Server struct {
	config     ServerConfig
	listener   net.Listener
	logger     *slog.Logger
	methods    map[string]MethodHandler
	mu         sync.RWMutex
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	activeConn int64
}

func NewServer(config ServerConfig, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

	absPath := config.Path
	if config.Path[:1] == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("socket: resolve home dir: %w", err)
		}
		absPath = filepath.Join(home, config.Path[1:])
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return nil, fmt.Errorf("socket: create dir: %w", err)
	}

	return &Server{
		config:  config,
		methods: make(map[string]MethodHandler),
		logger:  logger,
	}, nil
}

func (s *Server) RegisterMethod(name string, handler MethodHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods[name] = handler
}

func (s *Server) Serve(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	network, addr := s.getTransport()
	listener, err := net.Listen(network, addr)
	if err != nil {
		return fmt.Errorf("socket: listen: %w", err)
	}
	s.listener = listener

	if network == "unix" {
		os.Chmod(addr, os.FileMode(s.config.Perm))
	}

	s.logger.Info("socket server started", "path", addr)

	go s.acceptLoop()
	<-s.ctx.Done()

	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				s.logger.Error("accept connection", "error", err)
				continue
			}
		}

		atomic.AddInt64(&s.activeConn, 1)
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() {
		conn.Close()
		atomic.AddInt64(&s.activeConn, -1)
		s.wg.Done()
	}()

	s.logger.Debug("client connected", "remote", conn.RemoteAddr())

	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				s.logger.Debug("read error", "error", err)
			}
			return
		}

		if len(line) > int(s.config.MaxMsgSize) {
			s.sendError(conn, nil, ErrCodeInvalidRequest, "message too large")
			continue
		}

		go s.handleRequest(conn, line)
	}
}

func (s *Server) handleRequest(conn net.Conn, data []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var req jsonRPCRequest
	if err := json.Unmarshal(data, &req); err != nil {
		s.sendError(conn, nil, ErrCodeParseError, "parse error")
		return
	}

	s.mu.RLock()
	handler, ok := s.methods[req.Method]
	s.mu.RUnlock()

	if !ok {
		s.sendError(conn, req.ID, ErrCodeMethodNotFound, "method not found")
		return
	}

	result, err := handler(ctx, req.Params)
	if err != nil {
		s.sendError(conn, req.ID, ErrCodeInternalError, err.Error())
		return
	}

	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}
	if result != nil {
		resultBytes, _ := json.Marshal(result)
		resp.Result = resultBytes
	}

	respBytes, _ := json.Marshal(resp)
	fmt.Fprintf(conn, "%s\n", respBytes)
}

func (s *Server) sendError(conn net.Conn, id interface{}, code int, message string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonRPCError{
			Code:    code,
			Message: message,
		},
	}
	respBytes, _ := json.Marshal(resp)
	fmt.Fprintf(conn, "%s\n", respBytes)
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}

	s.wg.Wait()

	if s.listener != nil {
		s.listener.Close()
	}

	if s.config.Path[:1] == "~" {
		home, _ := os.UserHomeDir()
		absPath := filepath.Join(home, s.config.Path[1:])
		os.Remove(absPath)
	} else {
		os.Remove(s.config.Path)
	}

	s.logger.Info("socket server stopped")
	return nil
}

func (s *Server) Addr() net.Addr {
	return s.listener.Addr()
}

func (s *Server) getTransport() (string, string) {
	if runtime.GOOS == "windows" {
		return "unix", s.config.Path
	}
	return "unix", s.config.Path
}

func (s *Server) ActiveConnections() int64 {
	return atomic.LoadInt64(&s.activeConn)
}