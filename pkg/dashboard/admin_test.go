package dashboard

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeAdmin struct {
	servers  []ServerStatus
	sessions []Session
	calls    []Call
	actions  []string
	added    []ServerSpec
	fail     error
}

func (f *fakeAdmin) Overview() Overview {
	return Overview{Version: "test", PID: 4242, Frontend: "http://127.0.0.1:8765/mcp", ConfigPath: "/cfg.yaml", LastReloadAt: time.Now()}
}
func (f *fakeAdmin) Servers() []ServerStatus { return f.servers }
func (f *fakeAdmin) Sessions() []Session     { return f.sessions }
func (f *fakeAdmin) Calls(limit int, server string) []Call {
	var out []Call
	for _, c := range f.calls {
		if server == "" || c.Server == server {
			out = append(out, c)
		}
	}
	return out
}
func (f *fakeAdmin) act(name string) error {
	if f.fail != nil {
		return f.fail
	}
	f.actions = append(f.actions, name)
	return nil
}
func (f *fakeAdmin) RestartServer(_ context.Context, name string) error {
	return f.act("restart " + name)
}
func (f *fakeAdmin) SetServerEnabled(_ context.Context, name string, enabled bool) error {
	if enabled {
		return f.act("enable " + name)
	}
	return f.act("disable " + name)
}
func (f *fakeAdmin) AddServer(_ context.Context, spec ServerSpec) error {
	if err := f.act("add " + spec.Name); err != nil {
		return err
	}
	f.added = append(f.added, spec)
	return nil
}
func (f *fakeAdmin) RemoveServer(_ context.Context, name string) error {
	return f.act("remove " + name)
}
func (f *fakeAdmin) Reload(context.Context) error { return f.act("reload") }

func sampleAdmin() *fakeAdmin {
	return &fakeAdmin{
		servers: []ServerStatus{
			{Name: "garmin", Transport: "stdio", Enabled: true, State: "idle", Health: HealthOK, Summary: "Up", Tools: 28, ToolsKnown: true, Requests: 12, PID: 77},
			{Name: "github", Transport: "http", Enabled: true, State: "error", Health: HealthDown,
				Summary: "Authentication refused: check this server's token", LastError: "authorization required <script>x</script>", LastErrorAt: time.Now().Add(-time.Minute)},
			{Name: "old", Transport: "stdio", Enabled: false, State: "disabled", Health: HealthDisabled, Summary: "Disabled in the config"},
		},
		sessions: []Session{{ID: "ab12cd34", Client: "claude-code", ClientVersion: "2.1", Exposure: "passthrough", Initialized: true, OpenedAt: time.Now().Add(-time.Hour), Calls: 3}},
		calls: []Call{
			{Time: time.Now(), Client: "claude-code", Server: "garmin", Tool: "get_activities", DurationMs: 42, Status: "ok", ResponseBytes: 2048},
			{Time: time.Now(), Client: "codex", Server: "github", Tool: "list_issues", DurationMs: 3, Status: "error", Error: "authorization required"},
		},
	}
}

func adminHandler(admin Admin, token string) http.Handler {
	globalLogger = slog.New(slog.DiscardHandler)
	return newServer(Config{Admin: admin, Token: token}).routes()
}

func doRequest(h http.Handler, method, path string, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var adminHeader = map[string]string{"X-LeanProxy-Admin": "1"}

func TestAdminIndexShowsServersAndEscapesErrors(t *testing.T) {
	body := doRequest(adminHandler(sampleAdmin(), "tok"), "GET", "/", nil, nil).Body.String()
	for _, want := range []string{
		"garmin", "github", "Authentication refused", "h-down", "h-ok", "h-disabled",
		"pid 4242", "/cfg.yaml", `href="/static/app.css"`, "Add a server", "1 down",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index lacks %q", want)
		}
	}
	// Upstream-controlled text is escaped, and the page has no inline
	// script or style (the CSP forbids both).
	if strings.Contains(body, "<script>x</script>") {
		t.Error("an upstream error message reached the page unescaped")
	}
	if strings.Contains(body, "<style") || strings.Contains(body, "<script>") {
		t.Error("the page has an inline <style> or <script>")
	}
}

func TestAdminIndexWithoutAdminShowsUsageOnly(t *testing.T) {
	body := doRequest(adminHandler(nil, ""), "GET", "/", nil, nil).Body.String()
	if strings.Contains(body, `id="servers"`) || !strings.Contains(body, "Token usage") {
		t.Fatalf("a dashboard without admin data must show usage only")
	}
	if rec := doRequest(adminHandler(nil, ""), "GET", "/api/admin/servers", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("admin JSON without admin = %d, want 503", rec.Code)
	}
}

func TestAdminFragmentsAndJSON(t *testing.T) {
	h := adminHandler(sampleAdmin(), "")
	if body := doRequest(h, "GET", "/admin/sessions", nil, nil).Body.String(); !strings.Contains(body, "claude-code") || !strings.Contains(body, "passthrough") {
		t.Errorf("sessions fragment: %s", body)
	}
	calls := doRequest(h, "GET", "/admin/calls", nil, nil).Body.String()
	if !strings.Contains(calls, "get_activities") || !strings.Contains(calls, "c-error") || !strings.Contains(calls, "42 ms") {
		t.Errorf("calls fragment: %s", calls)
	}
	if filtered := doRequest(h, "GET", "/admin/calls?server=github", nil, nil).Body.String(); strings.Contains(filtered, "get_activities") {
		t.Error("?server= did not filter the calls")
	}
	rec := doRequest(h, "GET", "/api/admin/servers", nil, nil)
	if rec.Header().Get("Content-Type") != "application/json" || !strings.Contains(rec.Body.String(), `"health":"down"`) {
		t.Errorf("servers JSON: %s", rec.Body.String())
	}
}

func TestAdminWritesNeedATokenAndTheHeader(t *testing.T) {
	// No token: read-only, whatever the request looks like.
	admin := sampleAdmin()
	h := adminHandler(admin, "")
	if rec := doRequest(h, "POST", "/admin/servers/garmin/restart", url.Values{}, adminHeader); rec.Code != http.StatusForbidden {
		t.Fatalf("write on a token-less dashboard = %d, want 403", rec.Code)
	}
	if body := doRequest(h, "GET", "/", nil, nil).Body.String(); strings.Contains(body, "Add a server") || !strings.Contains(body, "Read-only") {
		t.Error("a token-less dashboard must not offer write actions")
	}

	// Token configured, but no X-LeanProxy-Admin header (what a
	// cross-site form would send).
	h = adminHandler(admin, "tok")
	if rec := doRequest(h, "POST", "/admin/servers/garmin/restart", url.Values{}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("write without the admin header = %d, want 403", rec.Code)
	}
	if len(admin.actions) != 0 {
		t.Fatalf("refused requests ran actions: %v", admin.actions)
	}
}

func TestAdminWriteActions(t *testing.T) {
	admin := sampleAdmin()
	h := adminHandler(admin, "tok")
	for _, path := range []string{
		"/admin/servers/garmin/restart", "/admin/servers/garmin/disable",
		"/admin/servers/old/enable", "/admin/servers/old/remove", "/admin/reload",
	} {
		rec := doRequest(h, "POST", path, url.Values{}, adminHeader)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `class="flash good"`) {
			t.Errorf("%s = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	want := []string{"restart garmin", "disable garmin", "enable old", "remove old", "reload"}
	if !reflect.DeepEqual(admin.actions, want) {
		t.Fatalf("actions = %v, want %v", admin.actions, want)
	}

	// A failing action answers 200 with the reason, so htmx shows it.
	admin.fail = errors.New("server is busy")
	rec := doRequest(h, "POST", "/admin/servers/garmin/restart", url.Values{}, adminHeader)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "server is busy") || !strings.Contains(rec.Body.String(), `class="flash bad"`) {
		t.Fatalf("failed action = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminAddServerParsesTheForm(t *testing.T) {
	admin := sampleAdmin()
	h := adminHandler(admin, "tok")
	form := url.Values{
		"name": {"files"}, "transport": {"stdio"}, "command": {"npx"},
		"args": {`-y server-filesystem "/path with spaces" a\ b`},
		"env":  {"A=1\n\n B=two words \n"},
	}
	if rec := doRequest(h, "POST", "/admin/servers", form, adminHeader); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "files added.") {
		t.Fatalf("add = %d %s", rec.Code, rec.Body.String())
	}
	got := admin.added[0]
	wantArgs := []string{"-y", "server-filesystem", "/path with spaces", "a b"}
	if got.Name != "files" || got.Command != "npx" || !reflect.DeepEqual(got.Args, wantArgs) || !reflect.DeepEqual(got.Env, []string{"A=1", "B=two words"}) {
		t.Fatalf("spec = %+v", got)
	}

	form = url.Values{"name": {"gh"}, "transport": {"http"}, "url": {"https://example.com/mcp"}, "headers": {"Authorization: Bearer abc\nX-Tenant: 7"}}
	doRequest(h, "POST", "/admin/servers", form, adminHeader)
	got = admin.added[1]
	if got.URL != "https://example.com/mcp" || got.Headers["Authorization"] != "Bearer abc" || got.Headers["X-Tenant"] != "7" {
		t.Fatalf("spec = %+v", got)
	}

	for name, bad := range map[string]url.Values{
		"no name":         {"transport": {"stdio"}, "command": {"npx"}},
		"bad env":         {"name": {"x"}, "env": {"NOEQUALS"}},
		"bad header":      {"name": {"x"}, "headers": {"no colon"}},
		"unbalanced args": {"name": {"x"}, "args": {`"open`}},
	} {
		before := len(admin.added)
		rec := doRequest(h, "POST", "/admin/servers", bad, adminHeader)
		if !strings.Contains(rec.Body.String(), `class="flash bad"`) || len(admin.added) != before {
			t.Errorf("%s: accepted (%s)", name, rec.Body.String())
		}
	}
}

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		"":                nil,
		"  a   b  ":       {"a", "b"},
		`a "b c" d`:       {"a", "b c", "d"},
		`--x="" y`:        {"--x=", "y"},
		`a\ b "c \" d"`:   {"a b", `c " d`},
		"one\ttwo\nthree": {"one", "two", "three"},
		`""`:              {""},
	}
	for in, want := range cases {
		got, err := splitArgs(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("splitArgs(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{`"open`, `trailing\`} {
		if _, err := splitArgs(in); err == nil {
			t.Errorf("splitArgs(%q) accepted", in)
		}
	}
}

func TestTemplateHelpers(t *testing.T) {
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	spans := map[time.Duration]string{0: "0s", 12 * time.Second: "12s", 4 * time.Minute: "4m", 3 * time.Hour: "3h", 72 * time.Hour: "3d"}
	for d, want := range spans {
		if got := span(now, now.Add(-d)); got != want {
			t.Errorf("span(%v) = %q, want %q", d, got, want)
		}
	}
	if ago(now, now) != "just now" || ago(now, now.Add(-4*time.Minute)) != "4m ago" || ago(now, time.Time{}) != "-" || span(now, time.Time{}) != "-" {
		t.Error("ago")
	}
	if until(now, now.Add(-time.Second)) != "" || until(now, now.Add(90*time.Second)) != "2m" {
		t.Error("until")
	}
	if millis(0) != "-" || millis(0.84) != "0.8 ms" || millis(12.4) != "12 ms" || millis(1400) != "1.4 s" {
		t.Error("millis")
	}
	if bytesShort(812) != "812 B" || bytesShort(4200) != "4.2 kB" || bytesShort(1_300_000) != "1.3 MB" {
		t.Error("bytesShort")
	}
	if domID("Intervals.icu") != "Intervals_2eicu" || domID("a b") != "a_20b" {
		t.Errorf("domID = %q, %q", domID("Intervals.icu"), domID("a b"))
	}
}

func TestLoginRedirectsABrowser(t *testing.T) {
	h := handleLogin("tok")
	req := httptest.NewRequest("GET", "/login?token=tok", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" || len(rec.Result().Cookies()) != 1 {
		t.Fatalf("browser login = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// The page is nothing without its assets: each must be served, with a
// type the browser accepts under X-Content-Type-Options: nosniff.
func TestStaticAssetsAreServed(t *testing.T) {
	h := adminHandler(sampleAdmin(), "")
	for path, wantType := range map[string]string{
		"/static/app.css":     "text/css",
		"/static/app.js":      "javascript",
		"/static/htmx.min.js": "javascript",
	} {
		rec := doRequest(h, "GET", path, nil, nil)
		if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("%s = %d (%d bytes), want 200", path, rec.Code, rec.Body.Len())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, wantType) {
			t.Errorf("%s Content-Type = %q, want %s", path, ct, wantType)
		}
	}
	if rec := doRequest(h, "GET", "/static/nope.css", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown asset = %d, want 404", rec.Code)
	}
}
