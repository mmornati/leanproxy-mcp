package router

import "errors"

var (
	ErrToolNotFound  = errors.New("tool not found in any registered server")
	ErrAmbiguousTool = errors.New("tool found in multiple servers")
	ErrServerOffline = errors.New("target server is offline")
	ErrRoutingFailed = errors.New("routing failed")
	ErrInvalidMethod = errors.New("invalid method name")
)

type RouterError struct {
	Code    int
	Message string
	Err     error
}

func (e *RouterError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *RouterError) Unwrap() error {
	return e.Err
}

func NewRouterError(code int, message string, err error) *RouterError {
	return &RouterError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

const (
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)
