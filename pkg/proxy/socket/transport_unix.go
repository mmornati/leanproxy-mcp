package socket

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

type UnixTransport struct {
	path string
	perm uint32
}

func NewUnixTransport(path string, perm uint32) *UnixTransport {
	return &UnixTransport{
		path: path,
		perm: perm,
	}
}

func (t *UnixTransport) Listen() (net.Listener, error) {
	if err := os.Remove(t.path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove existing socket: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(t.path), 0755); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}

listener, err := net.Listen("unix", t.path)
	if err != nil {
		return nil, fmt.Errorf("listen unix: %w", err)
	}

	if err := os.Chmod(t.path, os.FileMode(t.perm)); err != nil {
		listener.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	return listener, nil
}

func (t *UnixTransport) Path() string {
	return t.path
}

func ValidateUnixSocketPerms(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat socket: %w", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("not a unix socket")
	}

	if stat.Mode&0077 != 0 {
		return fmt.Errorf("socket has insecure permissions")
	}

	return nil
}