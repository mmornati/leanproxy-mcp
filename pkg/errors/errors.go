package errors

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

var (
	ErrCodeContextNil     = -32010
	ErrCodeContextTimeout = -32011
	ErrCodeContextCancel  = -32012

	DefaultMinTimeout = 100 * time.Millisecond
	DefaultMaxTimeout = 5 * time.Minute
)

type ContextError struct {
	Code    int
	Message string
	Cause   error
}

func (e *ContextError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("context error %d: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("context error %d: %s", e.Code, e.Message)
}

func (e *ContextError) Unwrap() error {
	return e.Cause
}

func NewContextError(code int, message string) *ContextError {
	return &ContextError{
		Code:    code,
		Message: message,
	}
}

func ValidateContext(ctx context.Context) error {
	if ctx == nil {
		return NewContextError(ErrCodeContextNil, "context is nil")
	}

	select {
	case <-ctx.Done():
		return NewContextError(ErrCodeContextCancel, "context already done").WithCause(ctx.Err())
	default:
	}

	deadline, ok := ctx.Deadline()
	if ok {
		now := time.Now()
		timeout := deadline.Sub(now)

		if timeout > DefaultMaxTimeout {
			return NewContextError(ErrCodeContextTimeout, "context timeout exceeds maximum").WithCause(fmt.Errorf("timeout %v exceeds maximum %v", timeout, DefaultMaxTimeout))
		}
		if timeout < DefaultMinTimeout {
			return NewContextError(ErrCodeContextTimeout, "context timeout below minimum").WithCause(fmt.Errorf("timeout %v below minimum %v", timeout, DefaultMinTimeout))
		}
	}

	return nil
}

func (e *ContextError) WithCause(cause error) *ContextError {
	e.Cause = cause
	return e
}

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
	ErrCodeInternalError  = -32603
	ErrCodeServerError    = -32000
	ErrCodeTimeout        = -32001
	ErrCodeUnauthorized   = -32604
)
