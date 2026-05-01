package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/textproto"
	"sync"
	"time"
)

type Proxy struct {
	upstreamAddr string
	conn         net.Conn
	logger       *slog.Logger
	mu           sync.Mutex
}

func NewProxy(upstreamAddr string, logger *slog.Logger) *Proxy {
	return &Proxy{
		upstreamAddr: upstreamAddr,
		logger:       logger,
	}
}

func (p *Proxy) Connect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	conn, err := dialer.DialContext(ctx, "tcp", p.upstreamAddr)
	if err != nil {
		return fmt.Errorf("proxy: connect: %w", err)
	}

	p.conn = conn
	p.logger.Debug("connected to upstream", "addr", p.upstreamAddr)
	return nil
}

func (p *Proxy) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.conn != nil {
		p.conn.Close()
		p.logger.Debug("connection closed")
	}
	return nil
}

func (p *Proxy) ForwardLoop(ctx context.Context, ideConn net.Conn) error {
	if err := p.Connect(ctx); err != nil {
		return err
	}
	defer p.Close()

	errChan := make(chan error, 2)

	go func() {
		_, err := io.Copy(p.conn, ideConn)
		if err != nil {
			p.logger.Debug("ide to upstream copy done", "error", err)
		}
		p.conn.Close()
		errChan <- err
	}()

	go func() {
		_, err := io.Copy(ideConn, p.conn)
		if err != nil {
			p.logger.Debug("upstream to ide copy done", "error", err)
		}
		ideConn.Close()
		errChan <- err
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Proxy) ForwardLoopWithJSONRPC(ctx context.Context, ideConn net.Conn) error {
	if err := p.Connect(ctx); err != nil {
		return err
	}
	defer p.Close()

	ideReader := textproto.NewReader(bufio.NewReader(ideConn))
	upstreamWriter := bufio.NewWriter(p.conn)
	upstreamReader := textproto.NewReader(bufio.NewReader(p.conn))
	ideWriter := bufio.NewWriter(ideConn)

	var wg sync.WaitGroup
	errChan := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			line, err := ideReader.ReadLine()
			if err != nil {
				if err != io.EOF {
					errChan <- fmt.Errorf("proxy: read from ide: %w", err)
				}
				return
			}

			if _, err := fmt.Fprintln(upstreamWriter, line); err != nil {
				errChan <- fmt.Errorf("proxy: write to upstream: %w", err)
				return
			}
			if err := upstreamWriter.Flush(); err != nil {
				errChan <- fmt.Errorf("proxy: flush to upstream: %w", err)
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			line, err := upstreamReader.ReadLine()
			if err != nil {
				if err != io.EOF {
					errChan <- fmt.Errorf("proxy: read from upstream: %w", err)
				}
				return
			}

			if _, err := fmt.Fprintln(ideWriter, line); err != nil {
				errChan <- fmt.Errorf("proxy: write to ide: %w", err)
				return
			}
			if err := ideWriter.Flush(); err != nil {
				errChan <- fmt.Errorf("proxy: flush to ide: %w", err)
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(errChan)
	}()

	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func ParseJSONRPCRequest(data []byte) (*JSONRPCRequest, error) {
	var req JSONRPCRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("proxy: parse request: %w", err)
	}
	return &req, nil
}

func ParseJSONRPCResponse(data []byte) (*JSONRPCResponse, error) {
	var resp JSONRPCResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("proxy: parse response: %w", err)
	}
	return &resp, nil
}

func IsBatchRequest(data []byte) bool {
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err == nil && len(arr) > 0 {
		return true
	}
	return false
}

func ParseJSONRPCBatchRequest(data []byte) ([]JSONRPCRequest, error) {
	var reqs []JSONRPCRequest
	if err := json.Unmarshal(data, &reqs); err != nil {
		return nil, fmt.Errorf("proxy: parse batch request: %w", err)
	}
	return reqs, nil
}

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
	ID      interface{}     `json:"id"`
}

type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func NewJSONRPCError(code int, message string) *JSONRPCError {
	return &JSONRPCError{
		Code:    code,
		Message: message,
	}
}

func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("jsonrpc: error %d: %s", e.Code, e.Message)
}

const (
	ErrCodeParseError       = -32700
	ErrCodeInvalidRequest   = -32600
	ErrCodeMethodNotFound   = -32601
	ErrCodeInvalidParams    = -32602
	ErrCodeInternalError    = -32603
)
