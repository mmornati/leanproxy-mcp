package githubtools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/go-github/v62/github"
	"github.com/mmornati/leanproxy-mcp/pkg/ratelimit"
	"golang.org/x/oauth2"
)

const (
	toolListRepos = "list_repos"
	toolGetIssue  = "get_issue"
	toolCreatePR  = "create_pr"

	rateLimitCapacity = 5000
	rateLimitInterval = 1 * time.Hour
)

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ToolHandler func(ctx context.Context, client *github.Client, args json.RawMessage) (interface{}, error)

var githubTools = make(map[string]ToolHandler)

func init() {
	githubTools[toolListRepos] = handleListRepos
	githubTools[toolGetIssue] = handleGetIssue
	githubTools[toolCreatePR] = handleCreatePR
}

type GitHubClient struct {
	client      *github.Client
	logger      *slog.Logger
	rateLimiter *ratelimit.TokenBucket
	readOnly    bool
}

func NewGitHubClient(logger *slog.Logger) *GitHubClient {
	token := os.Getenv("GITHUB_TOKEN")
	readOnly := token == ""
	rl := ratelimit.NewTokenBucket(rateLimitCapacity, rateLimitInterval)

	if readOnly {
		logger.Warn("GITHUB_TOKEN not set — operating in read-only public mode", "rate_limit", rateLimitCapacity)
		client := github.NewClient(nil)
		return &GitHubClient{
			client:      client,
			logger:      logger,
			rateLimiter: rl,
			readOnly:    true,
		}
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)
	client := github.NewClient(tc)

	logger.Info("GitHub client initialized with token", "rate_limit", rateLimitCapacity)
	return &GitHubClient{
		client:      client,
		logger:      logger,
		rateLimiter: rl,
		readOnly:    false,
	}
}

func (c *GitHubClient) GetTools() []ToolDefinition {
	tools := []ToolDefinition{
		{
			Name:        toolListRepos,
			Description: "List repositories for a user or organization. Requires 'owner' argument. When GITHUB_TOKEN is not set, only public repositories are returned.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"owner": {"type": "string", "description": "GitHub username or organization name"},
					"type": {"type": "string", "description": "Repository type: all, owner, public, private, forks, sources, member (default: owner)"},
					"per_page": {"type": "integer", "description": "Results per page (max 100, default 30)"}
				},
				"required": ["owner"]
			}`),
		},
		{
			Name:        toolGetIssue,
			Description: "Get details of a specific GitHub issue. Requires 'owner', 'repo', and 'issue_number'.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"owner": {"type": "string", "description": "Repository owner (user or organization)"},
					"repo": {"type": "string", "description": "Repository name"},
					"issue_number": {"type": "integer", "description": "Issue number"}
				},
				"required": ["owner", "repo", "issue_number"]
			}`),
		},
	}

	if !c.readOnly {
		tools = append(tools, ToolDefinition{
			Name:        toolCreatePR,
			Description: "Create a pull request on a GitHub repository. Requires 'owner', 'repo', 'title', 'head', and 'base'.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"owner": {"type": "string", "description": "Repository owner (user or organization)"},
					"repo": {"type": "string", "description": "Repository name"},
					"title": {"type": "string", "description": "Pull request title"},
					"head": {"type": "string", "description": "The name of the branch where changes are implemented"},
					"base": {"type": "string", "description": "The name of the branch you want the changes pulled into"},
					"body": {"type": "string", "description": "Pull request body/description"}
				},
				"required": ["owner", "repo", "title", "head", "base"]
			}`),
		})
	}

	return tools
}

func (c *GitHubClient) IsReadOnly() bool {
	return c.readOnly
}

func (c *GitHubClient) GetRateLimiter() *ratelimit.TokenBucket {
	return c.rateLimiter
}

func (c *GitHubClient) CallTool(ctx context.Context, name string, args json.RawMessage) (interface{}, error) {
	info := c.rateLimiter.AllowWithInfo()
	if !info.Allowed {
		fmt.Fprintf(os.Stderr, "WARN: GitHub API rate limit exhausted — resets at %s\n", info.ResetAt.Format(time.RFC3339))
		return nil, &ratelimit.RateLimitError{
			Remaining: info.Remaining,
			ResetAt:   info.ResetAt,
		}
	}

	handler, ok := githubTools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}

	if name == toolCreatePR && c.readOnly {
		return nil, fmt.Errorf("tool %q is not available in read-only mode (GITHUB_TOKEN not set)", name)
	}

	return handler(ctx, c.client, args)
}

type RepoResult struct {
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Private     bool   `json:"private"`
	Fork        bool   `json:"fork"`
	Stars       int    `json:"stars"`
	Language    string `json:"language"`
}

type IssueResult struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Body      string `json:"body"`
	User      string `json:"user"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	URL       string `json:"url"`
}

type PRArgs struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Title string `json:"title"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Body  string `json:"body,omitempty"`
}

type PRResult struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	State   string `json:"state"`
	URL     string `json:"url"`
	HTMLURL string `json:"html_url"`
}

func handleListRepos(ctx context.Context, client *github.Client, args json.RawMessage) (interface{}, error) {
	var params struct {
		Owner   string `json:"owner"`
		Type    string `json:"type"`
		PerPage int    `json:"per_page"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Owner == "" {
		return nil, fmt.Errorf("'owner' is required")
	}
	if params.PerPage <= 0 || params.PerPage > 100 {
		params.PerPage = 30
	}

	opt := &github.RepositoryListByUserOptions{
		Type: params.Type,
		ListOptions: github.ListOptions{
			PerPage: params.PerPage,
		},
	}

	repos, _, err := client.Repositories.ListByUser(ctx, params.Owner, opt)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}

	results := make([]RepoResult, 0, len(repos))
	for _, r := range repos {
		lang := ""
		if r.Language != nil {
			lang = *r.Language
		}
		desc := ""
		if r.Description != nil {
			desc = *r.Description
		}
		results = append(results, RepoResult{
			Name:        r.GetName(),
			FullName:    r.GetFullName(),
			Description: desc,
			URL:         r.GetHTMLURL(),
			Private:     r.GetPrivate(),
			Fork:        r.GetFork(),
			Stars:       r.GetStargazersCount(),
			Language:    lang,
		})
	}

	return results, nil
}

func handleGetIssue(ctx context.Context, client *github.Client, args json.RawMessage) (interface{}, error) {
	var params struct {
		Owner       string `json:"owner"`
		Repo        string `json:"repo"`
		IssueNumber int    `json:"issue_number"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Owner == "" || params.Repo == "" || params.IssueNumber <= 0 {
		return nil, fmt.Errorf("'owner', 'repo', and 'issue_number' are required")
	}

	issue, _, err := client.Issues.Get(ctx, params.Owner, params.Repo, params.IssueNumber)
	if err != nil {
		return nil, fmt.Errorf("get issue: %w", err)
	}

	user := ""
	if issue.User != nil {
		user = issue.User.GetLogin()
	}

	return IssueResult{
		Number:    issue.GetNumber(),
		Title:     issue.GetTitle(),
		State:     issue.GetState(),
		Body:      issue.GetBody(),
		User:      user,
		CreatedAt: issue.GetCreatedAt().Format(time.RFC3339),
		UpdatedAt: issue.GetUpdatedAt().Format(time.RFC3339),
		URL:       issue.GetHTMLURL(),
	}, nil
}

func handleCreatePR(ctx context.Context, client *github.Client, args json.RawMessage) (interface{}, error) {
	var params PRArgs
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Owner == "" || params.Repo == "" || params.Title == "" || params.Head == "" || params.Base == "" {
		return nil, fmt.Errorf("'owner', 'repo', 'title', 'head', and 'base' are required")
	}

	body := params.Body
	pr := &github.NewPullRequest{
		Title: &params.Title,
		Head:  &params.Head,
		Base:  &params.Base,
		Body:  &body,
	}

	created, _, err := client.PullRequests.Create(ctx, params.Owner, params.Repo, pr)
	if err != nil {
		return nil, fmt.Errorf("create PR: %w", err)
	}

	return PRResult{
		Number:  created.GetNumber(),
		Title:   created.GetTitle(),
		State:   created.GetState(),
		URL:     created.GetURL(),
		HTMLURL: created.GetHTMLURL(),
	}, nil
}

func GetReadOnlyNotice() string {
	return "GITHUB_TOKEN not set. Running in read-only public mode. Only public repository data is accessible. Set GITHUB_TOKEN environment variable for full access (including create_pr tool)."
}
