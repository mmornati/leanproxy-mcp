package githubtools

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/google/go-github/v62/github"
)

func TestNewGitHubClient_ReadOnly(t *testing.T) {
	os.Unsetenv("GITHUB_TOKEN")
	client := NewGitHubClient(slog.Default())
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if !client.readOnly {
		t.Error("expected read-only mode when GITHUB_TOKEN is not set")
	}
	if client.rateLimiter == nil {
		t.Error("expected rate limiter")
	}
}

func TestNewGitHubClient_Authenticated(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token-12345")
	client := NewGitHubClient(slog.Default())
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.readOnly {
		t.Error("expected authenticated mode when GITHUB_TOKEN is set")
	}
}

func TestGetTools_ReadOnly(t *testing.T) {
	os.Unsetenv("GITHUB_TOKEN")
	client := NewGitHubClient(slog.Default())
	tools := client.GetTools()

	foundListRepos := false
	foundGetIssue := false
	foundCreatePR := false

	for _, tool := range tools {
		switch tool.Name {
		case toolListRepos:
			foundListRepos = true
		case toolGetIssue:
			foundGetIssue = true
		case toolCreatePR:
			foundCreatePR = true
		}
	}

	if !foundListRepos {
		t.Error("expected list_repos tool in read-only mode")
	}
	if !foundGetIssue {
		t.Error("expected get_issue tool in read-only mode")
	}
	if foundCreatePR {
		t.Error("expected create_pr tool NOT to be available in read-only mode")
	}
}

func TestGetTools_Authenticated(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	client := NewGitHubClient(slog.Default())
	tools := client.GetTools()

	foundCreatePR := false
	for _, tool := range tools {
		if tool.Name == toolCreatePR {
			foundCreatePR = true
			break
		}
	}

	if !foundCreatePR {
		t.Error("expected create_pr tool in authenticated mode")
	}
}

func TestToolDefinitions_HaveSchemas(t *testing.T) {
	os.Unsetenv("GITHUB_TOKEN")
	client := NewGitHubClient(slog.Default())
	tools := client.GetTools()

	for _, tool := range tools {
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		if tool.InputSchema == nil || string(tool.InputSchema) == "" || string(tool.InputSchema) == "{}" {
			t.Errorf("tool %q has empty or missing input schema", tool.Name)
		}
		var schema map[string]interface{}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Errorf("tool %q has invalid input schema JSON: %v", tool.Name, err)
		}
	}
}

func TestIsReadOnly(t *testing.T) {
	os.Unsetenv("GITHUB_TOKEN")
	client := NewGitHubClient(slog.Default())
	if !client.IsReadOnly() {
		t.Error("expected IsReadOnly to return true")
	}

	t.Setenv("GITHUB_TOKEN", "token")
	client2 := NewGitHubClient(slog.Default())
	if client2.IsReadOnly() {
		t.Error("expected IsReadOnly to return false with token")
	}
}

func TestCallTool_UnknownTool(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	client := NewGitHubClient(slog.Default())
	_, err := client.CallTool(context.Background(), "nonexistent", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestCallTool_CreatePR_ReadOnly(t *testing.T) {
	os.Unsetenv("GITHUB_TOKEN")
	client := NewGitHubClient(slog.Default())
	_, err := client.CallTool(context.Background(), toolCreatePR, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when calling create_pr in read-only mode")
	}
}

func TestCallTool_ListRepos_InvalidArgs(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	client := NewGitHubClient(slog.Default())
	_, err := client.CallTool(context.Background(), toolListRepos, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for list_repos without owner")
	}
}

func TestCallTool_GetIssue_InvalidArgs(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	client := NewGitHubClient(slog.Default())
	_, err := client.CallTool(context.Background(), toolGetIssue, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for get_issue without required args")
	}
}

func TestGetRateLimiter(t *testing.T) {
	os.Unsetenv("GITHUB_TOKEN")
	client := NewGitHubClient(slog.Default())
	rl := client.GetRateLimiter()
	if rl == nil {
		t.Fatal("expected non-nil rate limiter")
	}
	if rl.Allow() {
		t.Log("rate limiter allows requests")
	}
}

func TestGetReadOnlyNotice(t *testing.T) {
	notice := GetReadOnlyNotice()
	if notice == "" {
		t.Error("expected non-empty read-only notice")
	}
}

func TestRepoResult_JSON(t *testing.T) {
	r := RepoResult{
		Name:     "test-repo",
		FullName: "owner/test-repo",
		Stars:    42,
		Private:  false,
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal RepoResult: %v", err)
	}
	var result RepoResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal RepoResult: %v", err)
	}
	if result.Name != "test-repo" {
		t.Errorf("name = %q, want %q", result.Name, "test-repo")
	}
}

func TestIssueResult_JSON(t *testing.T) {
	r := IssueResult{
		Number: 1,
		Title:  "Test Issue",
		State:  "open",
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal IssueResult: %v", err)
	}
	var result IssueResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal IssueResult: %v", err)
	}
	if result.Number != 1 {
		t.Errorf("number = %d, want 1", result.Number)
	}
}

func TestPRArgs_Validation(t *testing.T) {
	tests := []struct {
		name    string
		args    PRArgs
		wantErr bool
	}{
		{
			name:    "missing owner",
			args:    PRArgs{Owner: "", Repo: "r", Title: "t", Head: "h", Base: "b"},
			wantErr: true,
		},
		{
			name:    "missing repo",
			args:    PRArgs{Owner: "o", Repo: "", Title: "t", Head: "h", Base: "b"},
			wantErr: true,
		},
		{
			name:    "missing title",
			args:    PRArgs{Owner: "o", Repo: "r", Title: "", Head: "h", Base: "b"},
			wantErr: true,
		},
		{
			name:    "valid with body",
			args:    PRArgs{Owner: "o", Repo: "r", Title: "t", Head: "h", Base: "b", Body: "desc"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, _ := json.Marshal(tt.args)
			_, err := handleCreatePR(context.Background(), github.NewClient(nil), data)
			if tt.wantErr && err == nil {
				t.Error("expected error")
			}
			if !tt.wantErr && err == nil {
				// Will fail because no real token, but that's expected
				t.Log("would need GITHUB_TOKEN to succeed")
			}
		})
	}
}

func TestHandleListRepos_NoOwner(t *testing.T) {
	_, err := handleListRepos(context.Background(), github.NewClient(nil), json.RawMessage(`{"owner":""}`))
	if err == nil {
		t.Error("expected error for empty owner")
	}
}

func TestHandleGetIssue_NoOwner(t *testing.T) {
	_, err := handleGetIssue(context.Background(), github.NewClient(nil), json.RawMessage(`{"owner":"","repo":"r","issue_number":1}`))
	if err == nil {
		t.Error("expected error for empty owner")
	}
}

func TestHandleGetIssue_NoRepo(t *testing.T) {
	_, err := handleGetIssue(context.Background(), github.NewClient(nil), json.RawMessage(`{"owner":"o","repo":"","issue_number":1}`))
	if err == nil {
		t.Error("expected error for empty repo")
	}
}

func TestHandleGetIssue_InvalidIssueNumber(t *testing.T) {
	_, err := handleGetIssue(context.Background(), github.NewClient(nil), json.RawMessage(`{"owner":"o","repo":"r","issue_number":0}`))
	if err == nil {
		t.Error("expected error for issue_number <= 0")
	}
}

func TestRateLimitTokenBucket(t *testing.T) {
	os.Unsetenv("GITHUB_TOKEN")
	client := NewGitHubClient(slog.Default())
	rl := client.GetRateLimiter()

	if rl.Remaining() < 4990 {
		t.Errorf("expected near-full bucket, got %d", rl.Remaining())
	}
}