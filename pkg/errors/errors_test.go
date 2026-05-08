package errors

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestValidateContext(t *testing.T) {
	t.Run("nil context returns error", func(t *testing.T) {
		err := ValidateContext(nil)
		if err == nil {
			t.Fatal("expected error for nil context")
		}
		var ctxErr *ContextError
		if !errors.As(err, &ctxErr) {
			t.Fatalf("expected ContextError, got %T", err)
		}
		if ctxErr.Code != ErrCodeContextNil {
			t.Errorf("expected code %d, got %d", ErrCodeContextNil, ctxErr.Code)
		}
	})

	t.Run("context already done returns error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := ValidateContext(ctx)
		if err == nil {
			t.Fatal("expected error for done context")
		}
		var ctxErr *ContextError
		if !errors.As(err, &ctxErr) {
			t.Fatalf("expected ContextError, got %T", err)
		}
		if ctxErr.Code != ErrCodeContextCancel {
			t.Errorf("expected code %d, got %d", ErrCodeContextCancel, ctxErr.Code)
		}
	})

	t.Run("context with timeout too long returns error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		err := ValidateContext(ctx)
		if err == nil {
			t.Fatal("expected error for timeout exceeding maximum")
		}
		var ctxErr *ContextError
		if !errors.As(err, &ctxErr) {
			t.Fatalf("expected ContextError, got %T", err)
		}
		if ctxErr.Code != ErrCodeContextTimeout {
			t.Errorf("expected code %d, got %d", ErrCodeContextTimeout, ctxErr.Code)
		}
	})

	t.Run("context with timeout too short returns error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		err := ValidateContext(ctx)
		if err == nil {
			t.Fatal("expected error for timeout below minimum")
		}
		var ctxErr *ContextError
		if !errors.As(err, &ctxErr) {
			t.Fatalf("expected ContextError, got %T", err)
		}
		if ctxErr.Code != ErrCodeContextTimeout {
			t.Errorf("expected code %d, got %d", ErrCodeContextTimeout, ctxErr.Code)
		}
	})

	t.Run("valid context returns nil", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := ValidateContext(ctx)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("context without deadline returns nil", func(t *testing.T) {
		ctx := context.Background()

		err := ValidateContext(ctx)
		if err != nil {
			t.Fatalf("expected nil error for context without deadline, got %v", err)
		}
	})
}

func TestContextError(t *testing.T) {
	t.Run("error with cause", func(t *testing.T) {
		cause := errors.New("original error")
		err := NewContextError(ErrCodeContextNil, "context is nil").WithCause(cause)

		if err.Cause != cause {
			t.Errorf("expected cause, got nil")
		}
		if err.Error() == "" {
			t.Error("expected non-empty error message")
		}
	})

	t.Run("unwrap", func(t *testing.T) {
		cause := errors.New("original error")
		err := NewContextError(ErrCodeContextNil, "context is nil").WithCause(cause)

		if errors.Unwrap(err) != cause {
			t.Error("expected unwrap to return cause")
		}
	})
}
