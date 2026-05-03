package compactor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIClient_Distill_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Authorization header, got %s", r.Header.Get("Authorization"))
		}

		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": `{"server_name":"test-server","tools":[{"name":"test_tool","description":"Test tool","parameters":{}}]}`,
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewOpenAIClient(OpenAIClientConfig{
		Endpoint: server.URL,
		APIKey:   "test-key",
		Model:    "gpt-4o-mini",
	}, nil)

	manifest := RawManifest{
		Name:        "test-server",
		Description: "A test server",
		Tools: []RawTool{
			{Name: "test_tool", Description: "A test tool", Parameters: json.RawMessage("{}")},
		},
	}

	result, err := client.Distill(context.Background(), manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ServerName != "test-server" {
		t.Errorf("expected server_name 'test-server', got %s", result.ServerName)
	}

	if len(result.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(result.Tools))
	}

	if result.Tools[0].Name != "test_tool" {
		t.Errorf("expected tool name 'test_tool', got %s", result.Tools[0].Name)
	}
}

func TestOpenAIClient_Distill_MissingAPIKey(t *testing.T) {
	client := NewOpenAIClient(OpenAIClientConfig{
		Endpoint: "https://api.openai.com/v1/chat/completions",
		APIKey:   "",
		Model:    "gpt-4o-mini",
	}, nil)

	manifest := RawManifest{Name: "test"}

	_, err := client.Distill(context.Background(), manifest)
	if err == nil {
		t.Error("expected error for missing API key")
	}
}

func TestOpenAIClient_Distill_MissingEndpoint(t *testing.T) {
	client := NewOpenAIClient(OpenAIClientConfig{
		Endpoint: "",
		APIKey:   "test-key",
		Model:    "gpt-4o-mini",
	}, nil)

	manifest := RawManifest{Name: "test"}

	_, err := client.Distill(context.Background(), manifest)
	if err == nil {
		t.Error("expected error for missing endpoint")
	}
}

func TestOpenAIClient_Distill_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewOpenAIClient(OpenAIClientConfig{
		Endpoint: server.URL,
		APIKey:   "test-key",
		Model:    "gpt-4o-mini",
	}, nil)

	manifest := RawManifest{Name: "test"}

	_, err := client.Distill(context.Background(), manifest)
	if err == nil {
		t.Error("expected error for API failure")
	}
}

func TestOpenAIClient_Distill_RetrySucceeds(t *testing.T) {
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": `{"server_name":"test","tools":[]}`,
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewOpenAIClient(OpenAIClientConfig{
		Endpoint: server.URL,
		APIKey:   "test-key",
		Model:    "gpt-4o-mini",
	}, nil)

	manifest := RawManifest{Name: "test"}

	result, err := client.Distill(context.Background(), manifest)
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}

	if result.ServerName != "test" {
		t.Errorf("expected server_name 'test', got %s", result.ServerName)
	}
}

func TestBuildDistillationPrompt(t *testing.T) {
	manifest := RawManifest{
		Name:        "test-server",
		Description: "A test server",
		Tools: []RawTool{
			{Name: "tool1", Description: "First tool", Parameters: json.RawMessage("{}")},
			{Name: "tool2", Description: "Second tool", Parameters: json.RawMessage("{}")},
		},
	}

	prompt := BuildDistillationPrompt(manifest)

	if !strings.Contains(prompt, "Optimize this MCP tool manifest") {
		t.Error("prompt should contain instruction")
	}

	if !strings.Contains(prompt, "test-server") {
		t.Error("prompt should contain server name")
	}
}
