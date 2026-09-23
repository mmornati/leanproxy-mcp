package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
)

// Tool pinning CLI (issue #310): `leanproxy-mcp tools pins list|diff|
// approve|reset`. The commands edit the pin file the running proxies use;
// a running proxy picks the change up within a second (it re-reads the
// file when it changes), no restart needed.

var toolsCmd = &cobra.Command{
	Use:   "tools",
	Short: "Inspect upstream tools (tool pinning)",
}

var toolsPinsCmd = &cobra.Command{
	Use:   "pins",
	Short: "List, diff, approve or reset the pinned tool definitions (rug-pull detection)",
	Long: `Tool pinning records a SHA-256 of every upstream tool definition (name, title,
description, inputSchema, outputSchema, annotations) in the pin file
(~/.config/leanproxy/pins.json by default). A server seen for the first time is
trusted on first use; afterwards a new or changed tool stays pending until it is
approved here. See security.tool_pinning in docs/configuration.md.`,
}

var toolsPinsFlags struct {
	file    string
	jsonOut bool
	all     bool
}

var toolsPinsListCmd = &cobra.Command{
	Use:          "list [server]",
	Short:        "List pinned tools and their status",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, mode, err := openPinStore()
		if err != nil {
			return err
		}
		server := ""
		if len(args) == 1 {
			server = args[0]
		}
		return printPinList(cmd.OutOrStdout(), store, mode, server, toolsPinsFlags.jsonOut)
	},
}

var toolsPinsDiffCmd = &cobra.Command{
	Use:          "diff [server] [tool]",
	Short:        "Show the unified diff of every tool awaiting approval",
	Args:         cobra.MaximumNArgs(2),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, _, err := openPinStore()
		if err != nil {
			return err
		}
		var server, tool string
		if len(args) > 0 {
			server = args[0]
		}
		if len(args) > 1 {
			tool = args[1]
		}
		return printPinDiff(cmd.OutOrStdout(), store.Current(), server, tool)
	},
}

var toolsPinsApproveCmd = &cobra.Command{
	Use:   "approve <server> [tool...|--all]",
	Short: "Approve new or changed tools (or a server identity change)",
	Long: `Approve pending tool definitions of a server: the named tools, or with --all
every pending tool, the server's identity change and its removed tools. With
only a server name it approves a pending server identity (serverInfo name)
change.`,
	Args:         cobra.MinimumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if toolsPinsFlags.all && len(args) > 1 {
			return errors.New("pass tool names or --all, not both")
		}
		store, _, err := openPinStore()
		if err != nil {
			return err
		}
		var done []string
		err = store.Update(func(f *toolpin.File) error {
			var aerr error
			done, aerr = toolpin.Approve(f, args[0], args[1:], toolsPinsFlags.all, time.Now())
			return aerr
		})
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if len(done) == 0 {
			fmt.Fprintf(out, "Nothing to approve for %s.\n", args[0])
			return nil
		}
		fmt.Fprintf(out, "Approved for %s: %s\n", args[0], strings.Join(done, ", "))
		fmt.Fprintf(out, "Pins written to %s; running proxies pick the change up within a second.\n", store.Path())
		return nil
	},
}

var toolsPinsResetCmd = &cobra.Command{
	Use:          "reset <server>",
	Short:        "Forget a server's pins (it is pinned again, trusted on first use, at the next refresh)",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		store, _, err := openPinStore()
		if err != nil {
			return err
		}
		if err := store.Update(func(f *toolpin.File) error { return toolpin.Reset(f, args[0]) }); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Pins of %s removed from %s. They are recreated (trust on first use) the next time a proxy lists its tools.\n", args[0], store.Path())
		return nil
	},
}

func init() {
	toolsPinsCmd.PersistentFlags().StringVar(&toolsPinsFlags.file, "file", "", "Pin file (default: security.tool_pinning.path, $LEANPROXY_PINS_FILE or ~/.config/leanproxy/pins.json)")
	toolsPinsListCmd.Flags().BoolVar(&toolsPinsFlags.jsonOut, "json", false, "Print the pins as JSON")
	toolsPinsApproveCmd.Flags().BoolVar(&toolsPinsFlags.all, "all", false, "Approve every pending change of the server")
	toolsPinsCmd.AddCommand(toolsPinsListCmd, toolsPinsDiffCmd, toolsPinsApproveCmd, toolsPinsResetCmd)
	toolsCmd.AddCommand(toolsPinsCmd)
	RootCmd.AddCommand(toolsCmd)
}

// pinningConfig loads security.tool_pinning from the proxy config (nil
// when there is no config or no block).
func pinningConfig() *toolpin.Config {
	path := GlobalConfigPath
	if path == "" {
		path = userConfigPath()
	}
	cfg, err := migrate.LoadConfig(context.Background(), path)
	if err != nil {
		if !errors.Is(err, migrate.ErrConfigNotFound) {
			slog.Debug("tool pinning: cannot load config, using defaults", "path", path, "error", err)
		}
		return nil
	}
	return cfg.ToolPinningConfig()
}

// openPinStore opens the pin file (--file wins over the config).
func openPinStore() (*toolpin.Store, toolpin.Mode, error) {
	pcfg := pinningConfig()
	path := toolsPinsFlags.file
	if path == "" {
		var err error
		if path, err = pcfg.ResolvePath(); err != nil {
			return nil, "", err
		}
	}
	store, err := toolpin.OpenStore(path)
	if err != nil {
		return nil, "", err
	}
	return store, pcfg.EffectiveMode(), nil
}

func printPinList(w io.Writer, store *toolpin.Store, mode toolpin.Mode, server string, jsonOut bool) error {
	f := store.Current()
	if server != "" {
		if _, ok := f.Servers[server]; !ok {
			return fmt.Errorf("server %q has no pins in %s", server, store.Path())
		}
	}
	if jsonOut {
		out := f
		if server != "" {
			out = &toolpin.File{Version: f.Version, Servers: map[string]*toolpin.ServerPins{server: f.Servers[server]}}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Fprintf(w, "Pin file: %s\nMode: %s\n", store.Path(), mode)
	if !store.Existed() {
		fmt.Fprintln(w, "\nNo pins yet: servers are pinned (trust on first use) the first time a proxy lists their tools.")
		return nil
	}
	pending := 0
	for _, name := range f.SortedServerNames() {
		if server != "" && name != server {
			continue
		}
		sp := f.Servers[name]
		fmt.Fprintf(w, "\n%s", name)
		if sp.ServerInfo != nil {
			fmt.Fprintf(w, " [%s %s]", sp.ServerInfo.Name, sp.ServerInfo.Version)
		}
		fmt.Fprintf(w, ", first seen %s\n", sp.FirstSeen.Format(time.RFC3339))
		if sp.PendingServerInfo != nil {
			pending++
			fmt.Fprintf(w, "  ! server identity changed: serverInfo name is now %q (approve: %s)\n", sp.PendingServerInfo.Name, toolpin.ApproveCommand(name, ""))
		}
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "  TOOL\tSTATUS\tFINDINGS\tAPPROVAL\tFIRST SEEN")
		for _, tname := range sp.SortedToolNames() {
			tp := sp.Tools[tname]
			if tp.NeedsApproval() {
				pending++
			}
			findings := tp.Findings
			if tp.Pending != nil {
				findings = tp.Pending.Findings
			}
			approval := tp.Approval
			if approval == "" {
				approval = "-"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", tname, tp.Status(), findingsSummary(findings), approval, tp.FirstSeen.Format("2006-01-02"))
		}
		_ = tw.Flush()
	}
	if pending > 0 {
		fmt.Fprintf(w, "\n%d change(s) awaiting approval: review with `leanproxy-mcp tools pins diff`, approve with `leanproxy-mcp tools pins approve <server> <tool>|--all`.\n", pending)
	}
	return nil
}

func findingsSummary(fs []toolpin.Finding) string {
	if len(fs) == 0 {
		return "-"
	}
	counts := map[toolpin.Severity]int{}
	for _, f := range fs {
		counts[f.Severity]++
	}
	var parts []string
	for _, sev := range []toolpin.Severity{toolpin.SeverityHigh, toolpin.SeverityMedium, toolpin.SeverityLow} {
		if counts[sev] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[sev], sev))
		}
	}
	return strings.Join(parts, ", ")
}

func printPinDiff(w io.Writer, f *toolpin.File, server, tool string) error {
	if server != "" {
		if _, ok := f.Servers[server]; !ok {
			return fmt.Errorf("server %q has no pins", server)
		}
	}
	shown := 0
	for _, name := range f.SortedServerNames() {
		if server != "" && name != server {
			continue
		}
		sp := f.Servers[name]
		if sp.PendingServerInfo != nil && tool == "" {
			shown++
			old := ""
			if sp.ServerInfo != nil {
				old = sp.ServerInfo.Name
			}
			fmt.Fprintf(w, "=== %s: server identity changed\n--- serverInfo.name (approved)\n+++ serverInfo.name (now served)\n-%s\n+%s\n\n", name, old, sp.PendingServerInfo.Name)
		}
		for _, tname := range sp.SortedToolNames() {
			if tool != "" && tname != tool {
				continue
			}
			tp := sp.Tools[tname]
			switch tp.Status() {
			case toolpin.StatusChanged, toolpin.StatusNew:
			case toolpin.StatusRemoved:
				if tool == "" {
					shown++
					fmt.Fprintf(w, "=== %s/%s: removed (no longer listed by the server; `approve --all` forgets it)\n\n", name, tname)
				}
				continue
			default:
				continue
			}
			shown++
			fmt.Fprintf(w, "=== %s/%s: %s\n", name, tname, tp.Status())
			for _, fd := range tp.Pending.Findings {
				fmt.Fprintf(w, "    scanner: %s — %s\n", fd, fd.Detail)
			}
			fmt.Fprint(w, tp.Diff(name, tname))
			fmt.Fprintf(w, "Approve with: %s\n\n", toolpin.ApproveCommand(name, tname))
		}
	}
	if shown == 0 {
		fmt.Fprintln(w, "No pending changes.")
	}
	return nil
}

// newToolPinner builds the pinner of a front end from
// security.tool_pinning (warn mode when absent; nil when off). A pin file
// that cannot be opened disables pinning with a warning, except in block
// mode, where the proxy refuses to start rather than run unprotected.
func newToolPinner(cfg *migrate.Config) *toolpin.Pinner {
	pcfg := cfg.ToolPinningConfig()
	p, err := toolpin.New(pcfg, serverDomains(cfg), slog.Default())
	if err != nil {
		if pcfg.EffectiveMode() == toolpin.ModeBlock {
			logError("tool pinning (block mode): %v", err)
		}
		slog.Warn("tool pinning disabled: cannot open the pin file", "error", err)
		return nil
	}
	return p
}

// serverDomains maps each HTTP/SSE server to its own URL host, which the
// description scanner never reports.
func serverDomains(cfg *migrate.Config) map[string][]string {
	if cfg == nil {
		return nil
	}
	out := map[string][]string{}
	for _, s := range cfg.Servers {
		if s == nil || s.HTTP == nil || s.HTTP.URL == "" {
			continue
		}
		if u, err := url.Parse(s.HTTP.URL); err == nil && u.Hostname() != "" {
			out[s.Name] = []string{u.Hostname()}
		}
	}
	return out
}

// printToolPinningStatus is the tool pinning section of `doctor security`
// (#310): mode, pin file, and every drift event still open (tools awaiting
// approval, identity changes, removed tools, name collisions, scanner
// findings of approved tools).
func printToolPinningStatus(w io.Writer) {
	fmt.Fprintln(w, "## Tool Pinning")
	fmt.Fprintln(w)
	pcfg := pinningConfig()
	mode := pcfg.EffectiveMode()
	fmt.Fprintf(w, "  Mode: %s\n", mode)
	if mode == toolpin.ModeOff {
		fmt.Fprintln(w, "  Tool pinning is off (security.tool_pinning.mode: off): tool definitions are not checked.")
		return
	}
	path, err := pcfg.ResolvePath()
	if err != nil {
		fmt.Fprintf(w, "  ERROR: %v\n", err)
		return
	}
	fmt.Fprintf(w, "  Pin file: %s\n", path)
	store, err := toolpin.OpenStore(path)
	if err != nil {
		fmt.Fprintf(w, "  ERROR: %v\n", err)
		return
	}
	if !store.Existed() {
		fmt.Fprintln(w, "  No pins yet: servers are pinned (trust on first use) the first time a proxy lists their tools.")
		return
	}
	f := store.Current()
	tools := 0
	var pending, identity, removed, flagged []string
	for _, name := range f.SortedServerNames() {
		sp := f.Servers[name]
		if sp.PendingServerInfo != nil {
			old := ""
			if sp.ServerInfo != nil {
				old = sp.ServerInfo.Name
			}
			identity = append(identity, fmt.Sprintf("%s: serverInfo name %q -> %q", name, old, sp.PendingServerInfo.Name))
		}
		for _, tname := range sp.SortedToolNames() {
			tp := sp.Tools[tname]
			tools++
			switch tp.Status() {
			case toolpin.StatusNew, toolpin.StatusChanged:
				line := fmt.Sprintf("%s/%s (%s", name, tname, tp.Status())
				if sev := toolpin.MaxSeverity(tp.Pending.Findings); sev != "" {
					line += ", scanner: " + findingsSummary(tp.Pending.Findings)
				}
				pending = append(pending, line+")")
			case toolpin.StatusRemoved:
				removed = append(removed, name+"/"+tname)
			}
			if tp.Status() == toolpin.StatusApproved && toolpin.MaxSeverity(tp.Findings).AtLeast(toolpin.SeverityMedium) {
				flagged = append(flagged, fmt.Sprintf("%s/%s (%s)", name, tname, findingsSummary(tp.Findings)))
			}
		}
	}
	fmt.Fprintf(w, "  Pinned: %d servers, %d tools\n", len(f.Servers), tools)
	section := func(title string, items []string, hint string) {
		fmt.Fprintf(w, "\n  %s: %d\n", title, len(items))
		for _, it := range items {
			fmt.Fprintf(w, "    - %s\n", it)
		}
		if len(items) > 0 && hint != "" {
			fmt.Fprintf(w, "    %s\n", hint)
		}
	}
	section("Awaiting approval (tool_added / tool_changed)", pending, "Review: leanproxy-mcp tools pins diff; approve: leanproxy-mcp tools pins approve <server> <tool>|--all")
	section("Server identity changes (server_identity_changed)", identity, "Approve: leanproxy-mcp tools pins approve <server>")
	section("Removed tools (tool_removed)", removed, "Forget them: leanproxy-mcp tools pins approve <server> --all")
	all := toolpin.Collisions(f, "")
	collisions := make([]string, 0, len(all))
	for _, c := range all {
		collisions = append(collisions, fmt.Sprintf("%s/%s <-> %s/%s", c.Server, c.Tool, c.OtherServer, c.OtherTool))
	}
	section("Tool name collisions across servers (shadowing)", collisions, "Discovery is namespaced (server_tool), so both stay reachable; check that each server is the one you expect.")
	section("Approved tools with medium/high scanner findings", flagged, "")
}
