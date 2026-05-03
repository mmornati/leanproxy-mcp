//go:build windows
// +build windows

package socket

import (
	"fmt"
	"net"
)

const WindowsPipeName = "tokengate"

type WindowsTransport struct {
	pipeName string
}

func NewWindowsTransport(pipeName string) *WindowsTransport {
	if pipeName == "" {
		pipeName = WindowsPipeName
	}
	return &WindowsTransport{
		pipeName: pipeName,
	}
}

func (t *WindowsTransport) Listen() (net.Listener, error) {
	return net.Listen("unix", t.pipePath())
}

func (t *WindowsTransport) pipePath() string {
	return fmt.Sprintf("\\.\\pipe\\%s", t.pipeName)
}

func (t *WindowsTransport) Path() string {
	return t.pipePath()
}

func IsWindowsNamedPipe(path string) bool {
	return len(path) > 9 && path[:9] == "\\.\\pipe\\"
}