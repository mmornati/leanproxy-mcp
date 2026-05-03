package socket

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestHandlerTokenResolve(t *testing.T) {
	resolver := &mockTokenResolver{
		resolveFunc: func(ctx context.Context, uri string) (interface{}, error) {
			return map[string]interface{}{
				"resolved": true,
				"uri":      uri,
			}, nil
		},
	}

	handler := NewHandler(resolver, nil, nil, nil, nil)

	params := json.RawMessage(`{"uri":"api://example"}`)
	result, err := handler.handleTokenResolve(context.Background(), params)
	if err != nil {
		t.Fatalf("handleTokenResolve failed: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Expected result to be a map")
	}

	if resultMap["resolved"] != true {
		t.Error("Expected resolved to be true")
	}
}

func TestHandlerTokenResolveNoResolver(t *testing.T) {
	handler := NewHandler(nil, nil, nil, nil, nil)

	params := json.RawMessage(`{"uri":"api://example"}`)
	_, err := handler.handleTokenResolve(context.Background(), params)
	if err == nil {
		t.Fatal("Expected error when no resolver configured")
	}
}

func TestHandlerTokenResolveInvalidParams(t *testing.T) {
	resolver := &mockTokenResolver{}
	handler := NewHandler(resolver, nil, nil, nil, nil)

	params := json.RawMessage(`invalid json`)
	_, err := handler.handleTokenResolve(context.Background(), params)
	if err == nil {
		t.Fatal("Expected error for invalid params")
	}
}

func TestHandlerTokenValidate(t *testing.T) {
	handler := NewHandler(nil, nil, nil, nil, nil)

	params := json.RawMessage(`{"token":"secret","policy":"default"}`)
	result, err := handler.handleTokenValidate(context.Background(), params)
	if err != nil {
		t.Fatalf("handleTokenValidate failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if resultMap["valid"] != true {
		t.Error("Expected valid to be true")
	}
}

func TestHandlerProxyStatus(t *testing.T) {
	handler := NewHandler(nil, nil, nil, nil, nil)

	result, err := handler.handleProxyStatus(context.Background(), nil)
	if err != nil {
		t.Fatalf("handleProxyStatus failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if resultMap["status"] != "unknown" {
		t.Error("Expected status to be unknown")
	}
}

func TestHandlerConfigGet(t *testing.T) {
	configGetter := &mockConfigGetter{
		getFunc: func(ctx context.Context, key string) (interface{}, error) {
			return map[string]interface{}{"key": key, "value": "test-value"}, nil
		},
	}

	handler := NewHandler(nil, nil, configGetter, nil, nil)

	params := json.RawMessage(`{"key":"test.key"}`)
	result, err := handler.handleConfigGet(context.Background(), params)
	if err != nil {
		t.Fatalf("handleConfigGet failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if resultMap["value"] != "test-value" {
		t.Error("Expected value to be test-value")
	}
}

func TestHandlerConfigGetNoGetter(t *testing.T) {
	handler := NewHandler(nil, nil, nil, nil, nil)

	params := json.RawMessage(`{"key":"test.key"}`)
	_, err := handler.handleConfigGet(context.Background(), params)
	if err == nil {
		t.Fatal("Expected error when no config getter configured")
	}
}

func TestHandlerConfigSet(t *testing.T) {
	configSetter := &mockConfigSetter{
		setFunc: func(ctx context.Context, key string, value interface{}) error {
			return nil
		},
	}

	handler := NewHandler(nil, nil, nil, configSetter, nil)

	params := json.RawMessage(`{"key":"test.key","value":"new-value"}`)
	result, err := handler.handleConfigSet(context.Background(), params)
	if err != nil {
		t.Fatalf("handleConfigSet failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if resultMap["set"] != true {
		t.Error("Expected set to be true")
	}
}

func TestHandlerShutdown(t *testing.T) {
	shutdownCalled := false
	handler := NewHandler(nil, nil, nil, nil, func() {
		shutdownCalled = true
	})

	_, err := handler.handleShutdown(context.Background(), nil)
	if err != nil {
		t.Fatalf("handleShutdown failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if !shutdownCalled {
		t.Error("Expected shutdown function to be called")
	}
}

type mockConfigGetter struct {
	getFunc func(ctx context.Context, key string) (interface{}, error)
}

func (m *mockConfigGetter) Get(ctx context.Context, key string) (interface{}, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, key)
	}
	return nil, nil
}

type mockConfigSetter struct {
	setFunc func(ctx context.Context, key string, value interface{}) error
}

func (m *mockConfigSetter) Set(ctx context.Context, key string, value interface{}) error {
	if m.setFunc != nil {
		return m.setFunc(ctx, key, value)
	}
	return nil
}