package redistools

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	toolGet    = "redis_get"
	toolSet    = "redis_set"
	toolDelete = "redis_delete"
	toolKeys   = "redis_keys"
	toolExists = "redis_exists"

	DefaultPoolSize = 10
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ToolHandler func(ctx context.Context, args json.RawMessage) (interface{}, error)

type Config struct {
	Address  string `json:"address"`
	Password string `json:"password"`
	PoolSize int    `json:"pool_size"`
	DB       int    `json:"db"`
	UseTLS   bool   `json:"use_tls"`
}

func DefaultConfig() Config {
	return Config{
		Address:  "127.0.0.1:6379",
		PoolSize: DefaultPoolSize,
		DB:       0,
	}
}

type pooledConn struct {
	conn net.Conn
	mu   sync.Mutex
}

type RedisClient struct {
	config Config
	logger *slog.Logger
	pool   chan *pooledConn
	tools  map[string]ToolHandler
	mu     sync.Mutex
	closed atomic.Bool
}

func NewRedisClient(logger *slog.Logger, cfg Config) (*RedisClient, error) {
	if cfg.Address == "" {
		cfg.Address = "127.0.0.1:6379"
	}
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = DefaultPoolSize
	}

	client := &RedisClient{
		config: cfg,
		logger: logger,
		pool:   make(chan *pooledConn, cfg.PoolSize),
	}

	for i := 0; i < cfg.PoolSize; i++ {
		conn, err := client.dial()
		if err != nil {
			client.Close()
			return nil, fmt.Errorf("dial redis (conn %d/%d): %w", i+1, cfg.PoolSize, err)
		}
		client.pool <- &pooledConn{conn: conn}
	}

	client.tools = map[string]ToolHandler{
		toolGet:    client.handleGet,
		toolSet:    client.handleSet,
		toolDelete: client.handleDelete,
		toolKeys:   client.handleKeys,
		toolExists: client.handleExists,
	}

	logger.Info("redis client initialized",
		"address", cfg.Address,
		"pool_size", cfg.PoolSize,
		"db", cfg.DB,
	)
	return client, nil
}

func (c *RedisClient) dial() (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	var conn net.Conn
	var err error
	if c.config.UseTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", c.config.Address, &tls.Config{
			InsecureSkipVerify: false,
			MinVersion:         tls.VersionTLS12,
		})
	} else {
		conn, err = dialer.Dial("tcp", c.config.Address)
	}
	if err != nil {
		return nil, err
	}

	if c.config.Password != "" {
		if err := c.writeCommand(conn, "AUTH", c.config.Password); err != nil {
			conn.Close()
			return nil, fmt.Errorf("auth: %w", err)
		}
		if _, err := c.readResponse(conn); err != nil {
			conn.Close()
			return nil, fmt.Errorf("auth response: %w", err)
		}
	}

	if c.config.DB != 0 {
		if err := c.writeCommand(conn, "SELECT", strconv.Itoa(c.config.DB)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("select db: %w", err)
		}
		if _, err := c.readResponse(conn); err != nil {
			conn.Close()
			return nil, fmt.Errorf("select db response: %w", err)
		}
	}

	return conn, nil
}

func (c *RedisClient) Close() {
	c.mu.Lock()
	if !c.closed.CompareAndSwap(false, true) {
		c.mu.Unlock()
		return
	}
	close(c.pool)
	c.mu.Unlock()

	for pc := range c.pool {
		pc.conn.Close()
	}
}

func (c *RedisClient) GetTools() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        toolGet,
			Description: "Get the value of a Redis key.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"key": {"type": "string", "description": "Redis key name"}
				},
				"required": ["key"]
			}`),
		},
		{
			Name:        toolSet,
			Description: "Set a Redis key to a value with optional TTL.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"key": {"type": "string", "description": "Redis key name"},
					"value": {"type": "string", "description": "Value to set"},
					"ttl_seconds": {"type": "integer", "description": "Optional TTL in seconds"}
				},
				"required": ["key", "value"]
			}`),
		},
		{
			Name:        toolDelete,
			Description: "Delete one or more Redis keys.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"keys": {
						"type": "array",
						"items": {"type": "string"},
						"description": "Keys to delete"
					}
				},
				"required": ["keys"]
			}`),
		},
		{
			Name:        toolKeys,
			Description: "Find all keys matching a glob pattern.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"pattern": {"type": "string", "description": "Glob pattern (e.g. user:*)"}
				},
				"required": ["pattern"]
			}`),
		},
		{
			Name:        toolExists,
			Description: "Check if one or more keys exist in Redis.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"keys": {
						"type": "array",
						"items": {"type": "string"},
						"description": "Keys to check"
					}
				},
				"required": ["keys"]
			}`),
		},
	}
}

func (c *RedisClient) CallTool(ctx context.Context, name string, args json.RawMessage) (interface{}, error) {
	handler, ok := c.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return handler(ctx, args)
}

func (c *RedisClient) withConn(fn func(conn net.Conn) error) error {
	c.mu.Lock()
	if c.closed.Load() {
		c.mu.Unlock()
		return fmt.Errorf("client is closed")
	}

	pc, ok := <-c.pool
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("client is closed")
	}
	c.mu.Unlock()

	pc.mu.Lock()
	defer pc.mu.Unlock()

	err := fn(pc.conn)
	if err != nil {
		pc.conn.Close()
		newConn, dialErr := c.dial()
		if dialErr != nil {
			return fmt.Errorf("operation failed and re-dial failed: %w (dial: %v)", err, dialErr)
		}
		pc.conn = newConn
	}

	c.mu.Lock()
	if !c.closed.Load() {
		c.pool <- pc
	} else {
		pc.conn.Close()
	}
	c.mu.Unlock()
	return err
}

func (c *RedisClient) writeCommand(conn net.Conn, args ...string) error {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("*%d\r\n", len(args)))
	for _, arg := range args {
		buf.WriteString(fmt.Sprintf("$%d\r\n%s\r\n", len(arg), arg))
	}
	_, err := conn.Write([]byte(buf.String()))
	return err
}

func (c *RedisClient) readResponse(conn net.Conn) (interface{}, error) {
	return c.readResponseInternal(conn, 0)
}

func (c *RedisClient) readResponseInternal(conn net.Conn, depth int) (interface{}, error) {
	if depth > 10 {
		return nil, fmt.Errorf("response nesting too deep")
	}

	line, err := readLine(conn)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, fmt.Errorf("empty response from server")
	}

	switch line[0] {
	case '+':
		return string(line[1:]), nil
	case '-':
		return nil, fmt.Errorf("redis error: %s", string(line[1:]))
	case ':':
		n, err := strconv.ParseInt(string(line[1:]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse integer: %w", err)
		}
		return n, nil
	case '$':
		n, err := strconv.Atoi(string(line[1:]))
		if err != nil {
			return nil, fmt.Errorf("parse bulk string length: %w", err)
		}
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := readFull(conn, buf); err != nil {
			return nil, fmt.Errorf("read bulk string: %w", err)
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(string(line[1:]))
		if err != nil {
			return nil, fmt.Errorf("parse array length: %w", err)
		}
		if n < 0 {
			return nil, nil
		}
		result := make([]interface{}, n)
		for i := 0; i < n; i++ {
			result[i], err = c.readResponseInternal(conn, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unknown response type: %c", line[0])
	}
}

func readLine(conn net.Conn) ([]byte, error) {
	buf := make([]byte, 0, 128)
	tmp := make([]byte, 1)
	for {
		_, err := conn.Read(tmp)
		if err != nil {
			return nil, err
		}
		if tmp[0] == '\n' {
			if len(buf) > 0 && buf[len(buf)-1] == '\r' {
				return buf[:len(buf)-1], nil
			}
			return buf, nil
		}
		buf = append(buf, tmp[0])
	}
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	return io.ReadFull(conn, buf)
}

type GetResult struct {
	Key   string  `json:"key"`
	Value *string `json:"value,omitempty"`
	Found bool    `json:"found"`
}

func (c *RedisClient) handleGet(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var params struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Key == "" {
		return nil, fmt.Errorf("'key' is required")
	}

	var result GetResult
	result.Key = params.Key

	err := c.withConn(func(conn net.Conn) error {
		if err := c.writeCommand(conn, "GET", params.Key); err != nil {
			return err
		}
		val, err := c.readResponse(conn)
		if err != nil {
			return err
		}
		if val == nil {
			result.Found = false
		} else {
			s, ok := val.(string)
			if !ok {
				return fmt.Errorf("unexpected type for GET result: %T", val)
			}
			result.Found = true
			result.Value = &s
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type SetResult struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}

func (c *RedisClient) handleSet(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var params struct {
		Key        string `json:"key"`
		Value      string `json:"value"`
		TTLSeconds *int   `json:"ttl_seconds,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Key == "" {
		return nil, fmt.Errorf("'key' is required")
	}

	var result SetResult
	result.Key = params.Key

	err := c.withConn(func(conn net.Conn) error {
		var cmdArgs []string
		if params.TTLSeconds != nil && *params.TTLSeconds > 0 {
			cmdArgs = []string{"SET", params.Key, params.Value, "EX", strconv.Itoa(*params.TTLSeconds)}
		} else {
			cmdArgs = []string{"SET", params.Key, params.Value}
		}
		if err := c.writeCommand(conn, cmdArgs...); err != nil {
			return err
		}
		val, err := c.readResponse(conn)
		if err != nil {
			return err
		}
		result.Status = fmt.Sprintf("%v", val)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type DeleteResult struct {
	Deleted int64    `json:"deleted"`
	Keys    []string `json:"keys"`
}

func (c *RedisClient) handleDelete(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var params struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(params.Keys) == 0 {
		return nil, fmt.Errorf("'keys' is required and must be a non-empty array")
	}

	var result DeleteResult
	result.Keys = params.Keys

	err := c.withConn(func(conn net.Conn) error {
		cmdArgs := append([]string{"DEL"}, params.Keys...)
		if err := c.writeCommand(conn, cmdArgs...); err != nil {
			return err
		}
		val, err := c.readResponse(conn)
		if err != nil {
			return err
		}
		n, ok := val.(int64)
		if !ok {
			return fmt.Errorf("unexpected type for DEL result: %T", val)
		}
		result.Deleted = n
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type KeysResult struct {
	Pattern string   `json:"pattern"`
	Keys    []string `json:"keys"`
	Count   int      `json:"count"`
}

func (c *RedisClient) handleKeys(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var params struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Pattern == "" {
		return nil, fmt.Errorf("'pattern' is required")
	}

	var result KeysResult
	result.Pattern = params.Pattern

	err := c.withConn(func(conn net.Conn) error {
		if err := c.writeCommand(conn, "KEYS", params.Pattern); err != nil {
			return err
		}
		val, err := c.readResponse(conn)
		if err != nil {
			return err
		}
		switch v := val.(type) {
		case []interface{}:
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					return fmt.Errorf("unexpected type in KEYS result: %T", item)
				}
				result.Keys = append(result.Keys, s)
			}
		case nil:
			result.Keys = []string{}
		}
		result.Count = len(result.Keys)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result.Keys == nil {
		result.Keys = []string{}
	}
	return result, nil
}

type ExistsResult struct {
	Keys   []string `json:"keys"`
	Exists int64    `json:"exists"`
}

func (c *RedisClient) handleExists(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var params struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(params.Keys) == 0 {
		return nil, fmt.Errorf("'keys' is required and must be a non-empty array")
	}

	var result ExistsResult
	result.Keys = params.Keys

	err := c.withConn(func(conn net.Conn) error {
		cmdArgs := append([]string{"EXISTS"}, params.Keys...)
		if err := c.writeCommand(conn, cmdArgs...); err != nil {
			return err
		}
		val, err := c.readResponse(conn)
		if err != nil {
			return err
		}
		n, ok := val.(int64)
		if !ok {
			return fmt.Errorf("unexpected type for EXISTS result: %T", val)
		}
		result.Exists = n
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
