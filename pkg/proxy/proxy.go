package proxy

import (
	"context"
	"encoding/json"
	"io"
)

type Proxy interface {
	Stream(ctx context.Context, upstream string, w io.Writer) error
	HandleRequest(ctx context.Context, req json.RawMessage) (json.RawMessage, error)
}

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	ID     interface{}     `json:"id"`
}

type Response struct {
	Result interface{} `json:"result,omitempty"`
	Error  interface{} `json:"error,omitempty"`
	ID     interface{} `json:"id"`
}
