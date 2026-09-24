package registry

import (
	"context"
	"fmt"

	"github.com/mmornati/leanproxy-mcp/pkg/registry/mcpregistry"
)

// SourceOfficial tags entries that came from the official MCP Registry.
const SourceOfficial = "official"

// SyncSources refreshes the local cache from the official MCP Registry (the
// default marketplace source) plus every configured custom NDJSON source
// (registry.sources — opt-in only, issue #313). A failure syncing one
// custom source is logged and skipped rather than failing the whole sync,
// so one broken opt-in feed cannot take down the default source; a failure
// reaching the official registry is returned, since it is the primary
// source operators expect `marketplace sync` to refresh.
func (f *FeedFetcher) SyncSources(ctx context.Context) error {
	var all []RegistryFeedEntry

	if f.official != nil {
		servers, err := f.official.ListAll(ctx)
		if err != nil {
			return fmt.Errorf("registry feed: sync official MCP Registry: %w\nHint: check your connection or try again later with `leanproxy marketplace sync`", err)
		}
		for _, s := range servers {
			all = append(all, mapOfficialServer(s))
		}
		f.logger.Info("official MCP Registry synced", "entries", len(servers))
	}

	for _, src := range f.customSources {
		entries, err := f.fetchNDJSON(ctx, src.URL, src.Name)
		if err != nil {
			f.logger.Warn("registry feed: custom source sync failed, skipping",
				"source", src.Name, "url", src.URL, "error", err)
			continue
		}
		all = append(all, entries...)
		f.logger.Info("custom registry source synced", "source", src.Name, "entries", len(entries))
	}

	return f.finishSync(all)
}

// mapOfficialServer converts one official-registry server.json record into
// a RegistryFeedEntry. It prefers the first package it can build a stdio
// command for; failing that, the first remote (http/sse) transport. A
// record with neither installable package nor remote is still mapped (for
// display in `marketplace search`) but cannot be installed as-is.
func mapOfficialServer(s mcpregistry.Server) RegistryFeedEntry {
	entry := RegistryFeedEntry{
		Name:              s.Name,
		Description:       s.Description,
		Version:           s.Version,
		Source:            SourceOfficial,
		License:           s.License,
		NamespaceVerified: s.Meta.Official != nil && s.Meta.Official.IsVerified,
	}
	if s.Repository.URL != "" {
		entry.URL = s.Repository.URL
	}

	if len(s.Packages) > 0 {
		p := s.Packages[0]
		entry.Version = firstNonEmpty(p.Version, s.Version)
		entry.PackageRegistry = p.RegistryType
		entry.PackageIdentifier = p.Identifier
		entry.Transport = "stdio"
		entry.Command, entry.Args = commandForPackage(p)
		entry.Env = envForPackage(p)
		return entry
	}

	if len(s.Remotes) > 0 {
		r := s.Remotes[0]
		entry.URL = r.URL
		switch r.TransportType {
		case "sse":
			entry.Transport = "sse"
		default:
			entry.Transport = "http"
		}
	}

	return entry
}

// commandForPackage builds the exact, version-pinned command line for a
// package record, per registry type. Unrecognized registry types fall back
// to the runtime hint (or the identifier) unpinned; buildServerConfig in
// pkg/migrate applies a best-effort pin on top for those.
func commandForPackage(p mcpregistry.Package) (string, []string) {
	spec := p.Identifier
	if p.Version != "" {
		switch p.RegistryType {
		case "pypi":
			spec = p.Identifier + "==" + p.Version
		case "oci":
			if len(p.Version) > 7 && p.Version[:7] == "sha256:" {
				spec = p.Identifier + "@" + p.Version
			} else {
				spec = p.Identifier + ":" + p.Version
			}
		default: // npm and anything else that uses an npm-style "@version" suffix
			spec = p.Identifier + "@" + p.Version
		}
	}

	switch p.RegistryType {
	case "npm":
		args := append([]string{"-y"}, p.RuntimeArguments...)
		args = append(args, spec)
		args = append(args, p.PackageArguments...)
		return "npx", args
	case "pypi":
		args := append([]string{}, p.RuntimeArguments...)
		args = append(args, spec)
		args = append(args, p.PackageArguments...)
		return "uvx", args
	case "oci":
		args := append([]string{"run", "--rm", "-i"}, p.RuntimeArguments...)
		args = append(args, spec)
		args = append(args, p.PackageArguments...)
		return "docker", args
	default:
		cmd := p.RuntimeHint
		if cmd == "" {
			cmd = p.Identifier
		}
		args := append([]string{}, p.RuntimeArguments...)
		args = append(args, p.PackageArguments...)
		return cmd, args
	}
}

// envForPackage converts a package's declared environment variables into a
// name->placeholder map. Values are never fetched from anywhere here: the
// registry only publishes variable *names*, not secrets, and the installer
// (pkg/migrate) leaves them for the operator to fill in.
func envForPackage(p mcpregistry.Package) map[string]string {
	if len(p.EnvironmentVariables) == 0 {
		return nil
	}
	out := make(map[string]string, len(p.EnvironmentVariables))
	for _, e := range p.EnvironmentVariables {
		out[e.Name] = ""
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
