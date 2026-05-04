package socket

import (
	"context"
	"net"
)

type SocketServer interface {
	Serve(ctx context.Context) error
	Shutdown(ctx context.Context) error
	Addr() net.Addr
	Authenticate(token string) bool
}

type ServerConfig struct {
	Path       string
	Perm       uint32
	MaxMsgSize int64
	RateLimit  int
	AuthToken  string
}

func DefaultConfig() ServerConfig {
	return ServerConfig{
		Path:       "~/.leanproxy/leanproxy.sock",
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
		RateLimit:  100,
	}
}