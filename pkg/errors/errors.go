package errors

import (
	"encoding/json"
	"fmt"
)

type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("jsonrpc: error %d: %s", e.Code, e.Message)
}

func NewJSONRPCError(code int, message string) *JSONRPCError {
	return &JSONRPCError{
		Code:    code,
		Message: message,
	}
}

const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError = -32603
	ErrCodeServerError   = -32000
	ErrCodeTimeout       = -32001
	ErrCodeUnauthorized  = -32604
)