package pool

import (
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
)

// childEnvAllowlistNames are the exact environment variable names passed
// through to every stdio child by default (#311), regardless of server
// config. They cover the base interpreter/OS needs (PATH, HOME, ...),
// locale, TLS trust and outbound proxy settings, and the runtime managers
// npx/uvx/node commonly rely on (nvm, volta, pnpm, uv, pyenv, asdf, go).
var childEnvAllowlistNames = map[string]bool{
	// POSIX / general
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"TMPDIR": true, "TEMP": true, "TMP": true, "LANG": true, "TZ": true, "TERM": true,
	// Windows
	"SYSTEMROOT": true, "COMSPEC": true, "PATHEXT": true, "APPDATA": true,
	"LOCALAPPDATA": true, "USERPROFILE": true, "PROGRAMDATA": true,
	// Outbound proxy (both cases, as many tools only check one)
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true,
	// TLS trust
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "NODE_EXTRA_CA_CERTS": true,
	"REQUESTS_CA_BUNDLE": true,
	// Runtime managers (npx/uvx/node)
	"VOLTA_HOME": true, "PNPM_HOME": true, "GOPATH": true, "GOROOT": true,
}

// childEnvAllowlistPrefixes are name prefixes passed through by default in
// addition to childEnvAllowlistNames (#311).
var childEnvAllowlistPrefixes = []string{
	"LC_",    // locale
	"XDG_",   // XDG base dirs
	"NVM_",   // nvm (Node)
	"UV_",    // uv (Python)
	"PYENV_", // pyenv (Python)
	"ASDF_",  // asdf (multi-runtime)
}

// isAllowedEnvName reports whether name is in the least-privilege base
// environment passed to every stdio child by default (#311).
func isAllowedEnvName(name string) bool {
	if childEnvAllowlistNames[name] {
		return true
	}
	for _, prefix := range childEnvAllowlistPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// envVarRefPattern matches a "${VAR}" reference inside an explicit env
// value, for expansion from the parent (proxy) environment.
var envVarRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// knownServerEnvHint documents, for a handful of well-known first- and
// third-party MCP server packages, which environment variables they
// commonly need. Used only to warn operators at start when migrating from
// full env inheritance (#311); it is never authoritative and never blocks
// a spawn.
type knownServerEnvHint struct {
	match []string // substrings matched against "command args..." (case-insensitive)
	vars  []string
}

var knownServerEnvHints = []knownServerEnvHint{
	{match: []string{"@modelcontextprotocol/server-github", "github-mcp-server"}, vars: []string{"GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"}},
	{match: []string{"@modelcontextprotocol/server-gitlab"}, vars: []string{"GITLAB_TOKEN", "GITLAB_PERSONAL_ACCESS_TOKEN"}},
	{match: []string{"@modelcontextprotocol/server-slack"}, vars: []string{"SLACK_BOT_TOKEN", "SLACK_TEAM_ID"}},
	{match: []string{"@modelcontextprotocol/server-postgres"}, vars: []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING"}},
	{match: []string{"@modelcontextprotocol/server-brave-search"}, vars: []string{"BRAVE_API_KEY"}},
	{match: []string{"@modelcontextprotocol/server-google-maps"}, vars: []string{"GOOGLE_MAPS_API_KEY"}},
	{match: []string{"@modelcontextprotocol/server-aws-kb-retrieval"}, vars: []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION"}},
}

// envListToMap parses a "KEY=VALUE" list (as returned by os.Environ or held
// in a *.Env config field) into a name->value map. Entries without "=" are
// ignored.
func envListToMap(list []string) map[string]string {
	m := make(map[string]string, len(list))
	for _, kv := range list {
		name, value, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		m[name] = value
	}
	return m
}

// expandEnvValue expands every "${VAR}" reference in value using parent. It
// returns the expanded string, or ok=false and the name of the first
// referenced variable that parent does not have.
func expandEnvValue(value string, parent map[string]string) (expanded string, missing string, ok bool) {
	ok = true
	expanded = envVarRefPattern.ReplaceAllStringFunc(value, func(m string) string {
		if !ok {
			return m
		}
		name := envVarRefPattern.FindStringSubmatch(m)[1]
		v, present := parent[name]
		if !present {
			missing = name
			ok = false
			return m
		}
		return v
	})
	return expanded, missing, ok
}

// expandExplicitEnv expands "${VAR}" references (from parent) in each
// "KEY=VALUE" entry's value. An entry with no "=" is passed through
// unchanged. Returns an error naming serverName and the unset variable on
// the first unresolved reference.
func expandExplicitEnv(entries []string, parent map[string]string, serverName string) ([]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			out = append(out, entry)
			continue
		}
		expanded, missing, ok := expandEnvValue(value, parent)
		if !ok {
			return nil, fmt.Errorf("server %s: env %q references unset variable %q", serverName, key, missing)
		}
		out = append(out, key+"="+expanded)
	}
	return out, nil
}

// buildChildEnv builds the environment passed to a stdio server's child
// process (#311). By default it is a minimal, least-privilege environment:
// the allowlist above, plus cfg.EnvPassthrough (copied as-is from parent),
// plus cfg.Env (explicit "KEY=VALUE", with "${VAR}" expansion from parent),
// plus PYTHONUNBUFFERED=1. cfg.InheritEnv restores the pre-#311 behavior of
// passing all of parentEnv through, logging one warning.
//
// logger may be nil (no warnings logged, used by doctor's dry report).
func buildChildEnv(parentEnv []string, cfg StdioServerConfig, logger *slog.Logger) ([]string, error) {
	parentMap := envListToMap(parentEnv)

	var env []string
	if cfg.InheritEnv {
		if logger != nil {
			logger.Warn("server has inherit_env: true — the proxy's entire environment (including any secrets in it) is passed to this child process; prefer env_passthrough/env for least privilege",
				"name", cfg.Name)
		}
		env = append(env, parentEnv...)
	} else {
		for _, kv := range parentEnv {
			name, _, _ := strings.Cut(kv, "=")
			if isAllowedEnvName(name) {
				env = append(env, kv)
			}
		}
		for _, name := range cfg.EnvPassthrough {
			if v, ok := parentMap[name]; ok {
				env = append(env, name+"="+v)
			}
		}
	}

	expanded, err := expandExplicitEnv(cfg.Env, parentMap, cfg.Name)
	if err != nil {
		return nil, err
	}
	env = append(env, expanded...)
	env = append(env, "PYTHONUNBUFFERED=1")

	warnKnownMissingEnv(cfg, parentMap, env, logger)

	return env, nil
}

// warnKnownMissingEnv logs one warning per (server, variable) that a
// well-known MCP server package likely needs, is present in the parent
// (proxy) environment, but is not being passed to the child — almost
// always because a config written for the old full-inheritance behavior
// was not updated for #311. Never logs values.
func warnKnownMissingEnv(cfg StdioServerConfig, parentMap map[string]string, childEnv []string, logger *slog.Logger) {
	if logger == nil {
		return
	}
	childSet := make(map[string]bool, len(childEnv))
	for _, kv := range childEnv {
		name, _, _ := strings.Cut(kv, "=")
		childSet[name] = true
	}
	cmdline := strings.ToLower(cfg.Command + " " + strings.Join(cfg.Args, " "))
	for _, hint := range knownServerEnvHints {
		matched := false
		for _, s := range hint.match {
			if strings.Contains(cmdline, strings.ToLower(s)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for _, name := range hint.vars {
			if _, inParent := parentMap[name]; !inParent {
				continue
			}
			if childSet[name] {
				continue
			}
			logger.Warn("server likely needs an environment variable that the proxy has but is not passing to it (#311); add it to env_passthrough or env",
				"name", cfg.Name, "variable", name)
		}
	}
}

// ChildEnvReport returns, for `leanproxy-mcp doctor` (#311), the sorted
// names of the environment variables that would be passed to cfg's child
// process and the sorted names of parent variables that would be dropped.
// It never returns or logs values.
func ChildEnvReport(parentEnv []string, cfg StdioServerConfig) (passed, dropped []string, err error) {
	env, err := buildChildEnv(parentEnv, cfg, nil)
	if err != nil {
		return nil, nil, err
	}
	passedSet := make(map[string]bool, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if passedSet[name] {
			continue
		}
		passedSet[name] = true
		passed = append(passed, name)
	}
	for _, kv := range parentEnv {
		name, _, _ := strings.Cut(kv, "=")
		if !passedSet[name] {
			dropped = append(dropped, name)
		}
	}
	sort.Strings(passed)
	sort.Strings(dropped)
	return passed, dropped, nil
}
