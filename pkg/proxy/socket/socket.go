package socket

import (
	"context"
	"net"
)

type SocketServer interface {
	Serve(ctx context.Context) error
	Shutdown(ctx context.Context) error
	Addr() net.Addr
}

type ServerConfig struct {
	Path       string
	Perm       uint32
	MaxMsgSize int64
	RateLimit  int
}

func DefaultConfig() ServerConfig {
	return ServerConfig{
		Path:       "~/.leanproxy/leanproxy.sock",
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
		RateLimit:  100,
	}
}