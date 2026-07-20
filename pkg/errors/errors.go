package errors

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

var (
	// ErrCodeContextNil indicates a nil context was provided.
	ErrCodeContextNil = -32010
	// ErrCodeContextTimeout indicates the context deadline exceeded limits.
	ErrCodeContextTimeout = -32011
	// ErrCodeContextCancel indicates the context was canceled.
	ErrCodeContextCancel = -32012

	// DefaultMinTimeout is the minimum allowed context timeout duration.
	DefaultMinTimeout = 100 * time.Millisecond
	// DefaultMaxTimeout is the maximum allowed context timeout duration.
	DefaultMaxTimeout = 5 * time.Minute
)

// ContextError represents an error related to context validation failures.
type ContextError struct {
	Code    int
	Message string
	Cause   error
}

// Error returns a formatted string of the context error including its code, message, and cause.
func (e *ContextError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("context error %d: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("context error %d: %s", e.Code, e.Message)
}

// Unwrap returns the underlying cause of the context error.
func (e *ContextError) Unwrap() error {
	return e.Cause
}

// NewContextError creates a new ContextError with the given error code and message.
func NewContextError(code int, message string) *ContextError {
	return &ContextError{
		Code:    code,
		Message: message,
	}
}

// ValidateContext checks that the context is not nil, not already done, and its deadline
// falls within the allowed timeout range.
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

// WithCause attaches an underlying cause to the context error and returns the receiver.
func (e *ContextError) WithCause(cause error) *ContextError {
	e.Cause = cause
	return e
}

// JSONRPCError represents a JSON-RPC error response with code, message, and optional data.
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error returns a formatted string describing the JSON-RPC error.
func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("jsonrpc: error %d: %s", e.Code, e.Message)
}

// NewJSONRPCError creates a new JSONRPCError with the given error code and message.
func NewJSONRPCError(code int, message string) *JSONRPCError {
	return &JSONRPCError{
		Code:    code,
		Message: message,
	}
}

const (
	// ErrCodeParseError indicates a JSON parse error (-32700).
	ErrCodeParseError = -32700
	// ErrCodeInvalidRequest indicates an invalid JSON-RPC request (-32600).
	ErrCodeInvalidRequest = -32600
	// ErrCodeMethodNotFound indicates the requested method was not found (-32601).
	ErrCodeMethodNotFound = -32601
	// ErrCodeInvalidParams indicates invalid method parameters (-32602).
	ErrCodeInvalidParams = -32602
	// ErrCodeInternalError indicates an internal server error (-32603).
	ErrCodeInternalError = -32603
	// ErrCodeServerError indicates a generic server error (-32000).
	ErrCodeServerError = -32000
	// ErrCodeTimeout indicates the request timed out (-32001).
	ErrCodeTimeout = -32001
	// ErrCodeUnauthorized indicates the request was unauthorized (-32604).
	ErrCodeUnauthorized = -32604
	// ErrCodeBudgetExceeded indicates the budget has been exceeded (-32050).
	ErrCodeBudgetExceeded = -32050
)
