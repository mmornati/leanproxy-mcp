package cmd

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/httpsec"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
	"github.com/mmornati/leanproxy-mcp/pkg/streamhttp"
	"github.com/spf13/cobra"
)

// The daemon is one `server run --http` gateway kept running by the user's
// service manager (launchd on macOS, systemd --user on Linux). Every MCP
// client connects to it by URL, so there is one proxy process and one set
// of upstream servers however many clients, sessions and agents run,
// instead of one `server run --stdio` (and one copy of every upstream
// server) per client session.

const (
	daemonLabel       = "io.leanproxy.mcp"
	daemonSystemdUnit = "leanproxy-mcp.service"
	defaultDaemonHTTP = "127.0.0.1:8765"
	defaultDaemonDash = "127.0.0.1:9090"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run one shared LeanProxy gateway as a background service",
	Long: `Run LeanProxy as one background service that every MCP client shares.

Without it, each MCP client session starts its own "server run --stdio"
process, and each of those starts its own copy of every upstream server.
The daemon is a single "server run --http" gateway, started at login by
launchd (macOS) or systemd --user (Linux): one process, one set of
upstream servers, one log, and a dashboard to follow all of it.

  leanproxy-mcp daemon install     install and start the service
  leanproxy-mcp daemon status      is it running, what is connected
  leanproxy-mcp daemon clients     how to point each MCP client at it
  leanproxy-mcp daemon open        open the dashboard, already signed in
  leanproxy-mcp daemon uninstall   stop and remove the service`,
}

var daemonFlags struct {
	http      string
	dashboard string
	logLevel  string
	config    string
	showToken bool
}

func init() {
	install := &cobra.Command{
		Use:   "install",
		Short: "Install and start the shared gateway service",
		Args:  cobra.NoArgs,
		RunE:  runDaemonInstall,
	}
	install.Flags().StringVar(&daemonFlags.http, "http", defaultDaemonHTTP, "Address the MCP endpoint listens on (loopback only)")
	install.Flags().StringVar(&daemonFlags.dashboard, "dashboard", defaultDaemonDash, "Address the dashboard listens on (loopback only), or 'off'")
	install.Flags().StringVar(&daemonFlags.logLevel, "log-level", "info", "Log level of the service (debug, info, warn, error)")
	install.Flags().StringVar(&daemonFlags.config, "config", "", "Path to leanproxy_servers.yaml (default: the usual location)")

	clients := &cobra.Command{
		Use:   "clients",
		Short: "Show how to connect each MCP client to the gateway",
		Args:  cobra.NoArgs,
		RunE:  runDaemonClients,
	}
	clients.Flags().BoolVar(&daemonFlags.showToken, "show-token", false, "Print the bearer token in the snippets instead of a placeholder")

	daemonCmd.AddCommand(install, clients,
		&cobra.Command{Use: "uninstall", Short: "Stop and remove the service", Args: cobra.NoArgs, RunE: runDaemonUninstall},
		&cobra.Command{Use: "status", Short: "Show whether the gateway is running and what is connected", Args: cobra.NoArgs, RunE: runDaemonStatus},
		&cobra.Command{Use: "restart", Short: "Restart the service", Args: cobra.NoArgs, RunE: runDaemonRestart},
		&cobra.Command{Use: "open", Short: "Open the dashboard in the browser, signed in", Args: cobra.NoArgs, RunE: runDaemonOpen},
	)
	RootCmd.AddCommand(daemonCmd)
}

// daemonSpec is everything the service definition is built from.
type daemonSpec struct {
	Executable string
	HTTPAddr   string
	DashAddr   string // "" when the dashboard is off
	TokenFile  string
	// DashTokenFile holds the dashboard's own token: the dashboard can
	// add servers, so its credential is not the one MCP clients carry in
	// their configuration.
	DashTokenFile string
	LogFile       string
	LogLevel      string
	ConfigPath    string // "" for the default
	// Path is the PATH the service runs with: the installer's own, so the
	// commands of stdio servers (npx, uvx, docker, ...) resolve the same
	// way they do in the user's shell. Service managers start with a
	// minimal PATH otherwise.
	Path string
	Home string
}

// args is the gateway's command line.
func (d daemonSpec) args() []string {
	args := []string{"server", "run", "--http", d.HTTPAddr,
		"--log-file", d.LogFile, "--log-level", d.LogLevel}
	if d.DashAddr != "" {
		// A token file: the write actions are available, and the token
		// never appears in `ps`.
		args = append(args, "--dashboard-bind", d.DashAddr, "--dashboard-token-file", d.DashTokenFile)
	}
	if d.ConfigPath != "" {
		args = append(args, "--config", d.ConfigPath)
	}
	return args
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// launchdPlist renders the LaunchAgent of the gateway.
func launchdPlist(d daemonSpec) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + daemonLabel + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range append([]string{d.Executable}, d.args()...) {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString(`	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>` + xmlEscape(d.Path) + `</string>
		<key>HOME</key>
		<string>` + xmlEscape(d.Home) + `</string>
	</dict>
	<key>WorkingDirectory</key>
	<string>` + xmlEscape(d.Home) + `</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(d.LogFile+".stdout") + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(d.LogFile+".stderr") + `</string>
</dict>
</plist>
`)
	return b.String()
}

// systemdQuote quotes one word of an ExecStart line.
func systemdQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$%") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`, `%`, `%%`)
	return `"` + r.Replace(s) + `"`
}

// systemdUnit renders the user unit of the gateway.
func systemdUnit(d daemonSpec) string {
	words := []string{systemdQuote(d.Executable)}
	for _, a := range d.args() {
		words = append(words, systemdQuote(a))
	}
	return `[Unit]
Description=LeanProxy MCP gateway
After=network-online.target

[Service]
ExecStart=` + strings.Join(words, " ") + `
Environment=` + systemdQuote("PATH="+d.Path) + `
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`
}

// dashboardTokenPath is the token file of the daemon's dashboard.
func dashboardTokenPath(home string) string {
	return filepath.Join(home, ".config", "leanproxy", "dashboard.token")
}

func daemonLogFile(home string) string {
	return filepath.Join(home, ".leanproxy", "logs", "daemon.log")
}

func launchdPlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", daemonLabel+".plist")
}

func systemdUnitPath(home string) string {
	return filepath.Join(home, ".config", "systemd", "user", daemonSystemdUnit)
}

// checkLoopbackAddr validates a daemon listen address: host:port on a
// loopback host. The service is a local gateway; exposing it on the
// network is a deliberate `server run --http` setup, not an install
// default.
func checkLoopbackAddr(flag, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fmt.Errorf("--%s %q is not host:port", flag, addr)
	}
	if !httpsec.IsLoopbackHost(host) {
		return fmt.Errorf("--%s %q is not a loopback address: the service only listens locally (run `server run --http` yourself to expose it)", flag, addr)
	}
	return nil
}

// stableExecutable picks the path the service should run. exe is where
// the running binary really is; invoked is how it was called.
//
// A package manager installs the binary in a versioned directory and
// exposes it through a symlink that survives upgrades (Homebrew:
// /opt/homebrew/bin/leanproxy-mcp -> ../Cellar/leanproxy-mcp/1.1/bin/...).
// The service must run the symlink: the versioned path disappears at the
// next upgrade and the service would no longer start. So when the command
// was found through a symlink that leads to this same binary, that
// symlink is what is recorded.
func stableExecutable(exe, invoked string) string {
	candidate := invoked
	if !filepath.IsAbs(candidate) {
		found, err := exec.LookPath(candidate)
		if err != nil {
			return exe
		}
		candidate = found
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return exe
	}
	info, err := os.Lstat(candidate)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return exe
	}
	target, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return exe
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil || target != real {
		return exe
	}
	return candidate
}

func buildDaemonSpec() (daemonSpec, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return daemonSpec{}, fmt.Errorf("cannot locate the home directory: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return daemonSpec{}, fmt.Errorf("cannot locate the leanproxy-mcp executable: %w", err)
	}
	exe = stableExecutable(exe, os.Args[0])
	if err := checkLoopbackAddr("http", daemonFlags.http); err != nil {
		return daemonSpec{}, err
	}
	dash := daemonFlags.dashboard
	if dash == "off" {
		dash = ""
	}
	if dash != "" {
		if err := checkLoopbackAddr("dashboard", dash); err != nil {
			return daemonSpec{}, err
		}
	}
	configPath := daemonFlags.config
	if configPath != "" {
		if configPath, err = filepath.Abs(configPath); err != nil {
			return daemonSpec{}, err
		}
	}
	return daemonSpec{
		Executable:    exe,
		HTTPAddr:      daemonFlags.http,
		DashAddr:      dash,
		TokenFile:     serveTokenPath(home),
		DashTokenFile: dashboardTokenPath(home),
		LogFile:       daemonLogFile(home),
		LogLevel:      daemonFlags.logLevel,
		ConfigPath:    configPath,
		Path:          os.Getenv("PATH"),
		Home:          home,
	}, nil
}

// run executes a service-manager command and returns its combined output
// on failure.
func runServiceCommand(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput() // #nosec G204 -- fixed launchctl/systemctl commands
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func launchdDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func runDaemonInstall(cmd *cobra.Command, args []string) error {
	spec, err := buildDaemonSpec()
	if err != nil {
		return err
	}
	if _, err := loadOrCreateServeToken(spec.TokenFile); err != nil {
		return err
	}
	if spec.DashAddr != "" {
		if _, err := loadOrCreateServeToken(spec.DashTokenFile); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(spec.LogFile), 0o700); err != nil {
		return err
	}

	switch runtime.GOOS {
	case "darwin":
		path := launchdPlistPath(spec.Home)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- ~/Library/LaunchAgents is a standard, world-readable directory
			return err
		}
		// Replace a previous install: bootout fails when none is loaded.
		_ = exec.Command("launchctl", "bootout", launchdDomain()+"/"+daemonLabel).Run() // #nosec G204 -- fixed command, the argument is the numeric uid and a constant label
		if err := os.WriteFile(path, []byte(launchdPlist(spec)), 0o600); err != nil {
			return err
		}
		if err := runServiceCommand("launchctl", "bootstrap", launchdDomain(), path); err != nil {
			return err
		}
		fmt.Printf("Installed %s\n", path)
	case "linux":
		path := systemdUnitPath(spec.Home)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- standard systemd user directory
			return err
		}
		if err := os.WriteFile(path, []byte(systemdUnit(spec)), 0o600); err != nil {
			return err
		}
		if err := runServiceCommand("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if err := runServiceCommand("systemctl", "--user", "enable", "--now", daemonSystemdUnit); err != nil {
			return err
		}
		// Apply a changed unit to a service that was already running.
		_ = runServiceCommand("systemctl", "--user", "restart", daemonSystemdUnit)
		fmt.Printf("Installed %s\n", path)
	default:
		return fmt.Errorf("daemon install supports macOS (launchd) and Linux (systemd --user); on %s run it yourself:\n  %s %s",
			runtime.GOOS, spec.Executable, strings.Join(spec.args(), " "))
	}

	if info := waitForGateway(10 * time.Second); info == nil {
		fmt.Printf("The service was installed but the gateway did not come up yet. Check %s\n", spec.LogFile)
	} else {
		fmt.Printf("Gateway running: pid %d, %s\n", info.PID, info.ListenAddr)
	}
	fmt.Printf("Log: %s (rotated at 20 MiB)\n\n", spec.LogFile)
	fmt.Println("Next: point your MCP clients at it and remove their old `server run --stdio` entry:")
	fmt.Println("  leanproxy-mcp daemon clients")
	if spec.DashAddr != "" {
		fmt.Println("Dashboard:")
		fmt.Println("  leanproxy-mcp daemon open")
	}
	return nil
}

// gatewayStatus returns the status of the running HTTP gateway, or nil.
func gatewayStatus() *statusfile.StatusInfo {
	all, err := statusfile.ReadAllStatus()
	if err != nil {
		return nil
	}
	for i := range all {
		if all[i].HTTP != nil {
			return &all[i]
		}
	}
	return nil
}

func waitForGateway(timeout time.Duration) *statusfile.StatusInfo {
	deadline := time.Now().Add(timeout)
	for {
		if info := gatewayStatus(); info != nil {
			return info
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func runDaemonUninstall(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var path string
	switch runtime.GOOS {
	case "darwin":
		path = launchdPlistPath(home)
		_ = exec.Command("launchctl", "bootout", launchdDomain()+"/"+daemonLabel).Run() // #nosec G204 -- fixed command, the argument is the numeric uid and a constant label
	case "linux":
		path = systemdUnitPath(home)
		_ = exec.Command("systemctl", "--user", "disable", "--now", daemonSystemdUnit).Run()
	default:
		return fmt.Errorf("daemon uninstall is not supported on %s", runtime.GOOS)
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("The service is not installed.")
			return nil
		}
		return err
	}
	if runtime.GOOS == "linux" {
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	fmt.Printf("Removed %s\n", path)
	fmt.Println("MCP clients still configured with the gateway URL will fail to connect until you change them back.")
	return nil
}

func runDaemonRestart(cmd *cobra.Command, args []string) error {
	switch runtime.GOOS {
	case "darwin":
		return runServiceCommand("launchctl", "kickstart", "-k", launchdDomain()+"/"+daemonLabel)
	case "linux":
		return runServiceCommand("systemctl", "--user", "restart", daemonSystemdUnit)
	default:
		return fmt.Errorf("daemon restart is not supported on %s", runtime.GOOS)
	}
}

func daemonInstalled(home string) bool {
	path := launchdPlistPath(home)
	if runtime.GOOS == "linux" {
		path = systemdUnitPath(home)
	}
	_, err := os.Stat(path)
	return err == nil
}

func runDaemonStatus(cmd *cobra.Command, args []string) error {
	home, _ := os.UserHomeDir()
	info := gatewayStatus()
	switch {
	case info != nil:
		fmt.Printf("Gateway running: pid %d, up %s\n", info.PID, time.Since(info.StartedAt).Round(time.Second))
		fmt.Printf("  MCP endpoint: %s\n", info.HTTP.URL)
		if info.Dashboard != "" {
			fmt.Printf("  dashboard:    %s  (leanproxy-mcp daemon open)\n", info.Dashboard)
		}
		fmt.Printf("  clients:      %d connected\n", info.Clients)
		up := 0
		for _, s := range info.Servers {
			if s.Status == "running" || s.Status == "stopped" {
				up++
			}
		}
		fmt.Printf("  servers:      %d of %d up  (leanproxy-mcp status)\n", up, len(info.Servers))
	case daemonInstalled(home):
		fmt.Printf("The service is installed but the gateway is not running. Check %s\n", daemonLogFile(home))
	default:
		fmt.Println("The service is not installed. Install it with: leanproxy-mcp daemon install")
	}

	// The reason the daemon exists: say so when it is being bypassed.
	if all, err := statusfile.ReadAllStatus(); err == nil {
		stdio := 0
		for _, s := range all {
			if s.HTTP == nil {
				stdio++
			}
		}
		if stdio > 0 {
			fmt.Printf("\n%d separate `server run --stdio` process(es) are also running, each with its own upstream servers.\n", stdio)
			fmt.Println("Move those clients to the gateway: leanproxy-mcp daemon clients")
		}
	}
	return nil
}

// dashboardLoginCode asks the dashboard at base for a one-time sign-in
// code, authenticating with its token.
func dashboardLoginCode(base, token string) (string, error) {
	req, err := http.NewRequest(http.MethodPost, base+"/login/code", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the dashboard answered %s", resp.Status)
	}
	return strings.TrimSpace(string(body)), nil
}

func runDaemonOpen(cmd *cobra.Command, args []string) error {
	info := gatewayStatus()
	if info == nil || info.Dashboard == "" {
		return errors.New("no running gateway with a dashboard: run `leanproxy-mcp daemon install`")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	token, err := loadOrCreateServeToken(dashboardTokenPath(home))
	if err != nil {
		return err
	}
	// The browser gets a code that works once, for one minute: the
	// dashboard token itself never reaches a command line, the browser's
	// history or its address bar.
	code, err := dashboardLoginCode(info.Dashboard, token)
	if err != nil {
		return fmt.Errorf("cannot sign in to the dashboard at %s: %w", info.Dashboard, err)
	}
	login := info.Dashboard + "/login?code=" + url.QueryEscape(code)
	var opener string
	switch runtime.GOOS {
	case "darwin":
		opener = "open"
	case "linux":
		opener = "xdg-open"
	}
	if opener != "" {
		if err := exec.Command(opener, login).Start(); err == nil { // #nosec G204 -- fixed opener; the URL is the local dashboard with a one-time code
			fmt.Printf("Opened %s\n", info.Dashboard)
			return nil
		}
	}
	fmt.Println("Open this link within a minute (it works once):")
	fmt.Println("  " + login)
	return nil
}

// clientSnippets renders, for each MCP client, how to connect it to the
// gateway at endpoint. token is the bearer value to show.
func clientSnippets(endpoint, token string) string {
	auth := "Bearer " + token
	return fmt.Sprintf(`Gateway: %[1]s

Remove the old leanproxy entry (the one running "server run --stdio") from
each client, then add the gateway:

Claude Code
  claude mcp remove leanproxy
  claude mcp add --transport http --scope user leanproxy %[1]s --header "Authorization: %[2]s"

Codex CLI (~/.codex/config.toml)
  [mcp_servers.leanproxy]
  url = "%[1]s"
  bearer_token_env_var = "LEANPROXY_SERVE_TOKEN"

VS Code / GitHub Copilot (.vscode/mcp.json or the user mcp.json)
  { "servers": { "leanproxy": { "type": "http", "url": "%[1]s",
      "headers": { "Authorization": "%[2]s" } } } }

Cursor (~/.cursor/mcp.json) and other JSON configs
  { "mcpServers": { "leanproxy": { "url": "%[1]s",
      "headers": { "Authorization": "%[2]s" } } } }

Claude Desktop (claude_desktop_config.json) and any client that can only
start a command: a thin stdio bridge to the gateway, no token to paste
  { "mcpServers": { "leanproxy": { "command": "leanproxy-mcp", "args": ["connect"] } } }

OpenCode (opencode.json)
  { "mcp": { "leanproxy": { "type": "remote", "url": "%[1]s",
      "headers": { "Authorization": "%[2]s" } } } }
`, endpoint, auth)
}

func runDaemonClients(cmd *cobra.Command, args []string) error {
	endpoint := "http://" + defaultDaemonHTTP + streamhttp.DefaultEndpoint
	if info := gatewayStatus(); info != nil {
		endpoint = info.HTTP.URL
	} else {
		fmt.Println("(The gateway is not running: showing the default address. Start it with `leanproxy-mcp daemon install`.)")
		fmt.Println()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	tokenPath := serveTokenPath(home)
	token := "<token>"
	if daemonFlags.showToken {
		if token, err = loadOrCreateServeToken(tokenPath); err != nil {
			return err
		}
	}
	fmt.Print(clientSnippets(endpoint, token))
	if !daemonFlags.showToken {
		fmt.Printf("\n<token> is the content of %s\n", tokenPath)
		fmt.Println("Print the snippets with the real token: leanproxy-mcp daemon clients --show-token")
	}
	return nil
}
