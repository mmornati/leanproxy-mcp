# Installation

LeanProxy-MCP is a single Go binary. Releases are published for macOS and
Linux, on amd64 (x86_64) and arm64.

## Prerequisites

- **macOS or Linux**, amd64 or arm64. There is no Windows release.
- **An MCP client**: Claude Code, Claude Desktop, Cursor, VS Code (GitHub
  Copilot), OpenCode, or any other client that can start a stdio MCP server
  or connect to a Streamable HTTP one.
- **Go 1.25.5 or later**, only if you build from source.

## Supported platforms

| OS | Architecture | Release asset |
|---|---|---|
| macOS | arm64 (Apple Silicon) | `leanproxy-mcp_<version>_darwin_arm64.tar.gz` |
| macOS | amd64 (Intel) | `leanproxy-mcp_<version>_darwin_amd64.tar.gz` |
| Linux | amd64 (x86_64) | `leanproxy-mcp_<version>_linux_amd64.tar.gz` |
| Linux | arm64 (aarch64) | `leanproxy-mcp_<version>_linux_arm64.tar.gz` |

`<version>` is the release tag without the leading `v` (tag `v0.11` gives
`leanproxy-mcp_0.11_linux_amd64.tar.gz`). Each release also has a
`checksums.txt` file and an SBOM (`.sbom.json`) per archive.

!!! note "Windows"
    No Windows binary is released. Windows is not tested.

## Download the binary

### Automatic (macOS and Linux)

This script picks the latest release, detects your OS and architecture, and
installs the binary to `/usr/local/bin`:

```bash
VERSION=${VERSION:-$(curl -fsSL https://api.github.com/repos/mmornati/leanproxy-mcp/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')}
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH=amd64 ;;   # Intel/AMD
  aarch64) ARCH=arm64 ;;   # Linux on ARM reports aarch64
esac
curl -fsSL "https://github.com/mmornati/leanproxy-mcp/releases/download/${VERSION}/leanproxy-mcp_${VERSION#v}_${OS}_${ARCH}.tar.gz" -o leanproxy-mcp.tar.gz
tar -xzf leanproxy-mcp.tar.gz leanproxy-mcp
chmod +x leanproxy-mcp
sudo mv leanproxy-mcp /usr/local/bin/
rm leanproxy-mcp.tar.gz
```

To install a specific version, set `VERSION` first, for example
`VERSION=v0.11`, then run the same commands.

!!! tip "Verify the download"
    To check the archive, run these two commands after the first `curl`
    line and before `tar`. The two hashes must be the same:

    ```bash
    curl -fsSL "https://github.com/mmornati/leanproxy-mcp/releases/download/${VERSION}/checksums.txt" | grep "_${OS}_${ARCH}.tar.gz$"
    shasum -a 256 leanproxy-mcp.tar.gz
    ```

### Manual download

Download the archive for your platform from the
[GitHub Releases page](https://github.com/mmornati/leanproxy-mcp/releases),
extract it, and put `leanproxy-mcp` somewhere on your `PATH`.

## Install with Homebrew (macOS and Linux)

```bash
brew tap mmornati/leanproxy-mcp https://github.com/mmornati/leanproxy-mcp
brew install leanproxy-mcp
```

!!! note
    The formula is updated by a pull request after each release, so it can
    lag behind the latest release. Run `leanproxy-mcp version` to check what
    you got, and use the download script above if you need the newest
    version.

## Build from source

```bash
git clone https://github.com/mmornati/leanproxy-mcp.git
cd leanproxy-mcp
go build -o leanproxy-mcp .
sudo mv leanproxy-mcp /usr/local/bin/
```

The Makefile has two related targets:

| Target | What it does |
|---|---|
| `make build-local` | Builds for your platform into `dist/leanproxy-mcp` |
| `make install` | Runs `go install`, which puts the binary in `$(go env GOPATH)/bin`. Make sure that directory is on your `PATH`. It does not install to `/usr/local/bin` and does not need `sudo` |

## Verify the installation

```bash
leanproxy-mcp version
```

Example output:

```
leanproxy-mcp version 0.11
build date: 2026-09-24T04:56:34Z
platform: darwin/arm64
go: go1.25.14
```

A binary built with plain `go build` reports `version dev` and
`build date: unknown`.

!!! note
    The v0.11 release binary prints a few
    `completion: failed to register completion for --...` lines on stderr
    for every command. They are harmless and are fixed on `main`.

## Connect your IDE

LeanProxy reads its upstream MCP servers from one file,
`~/.config/leanproxy_servers.yaml`. Your IDE then starts a single MCP
server, `leanproxy-mcp server run --stdio`, instead of each server
separately.

### Step 1: import your existing MCP servers

```bash
# Preview what would be imported
leanproxy-mcp migrate --dry-run

# Import
leanproxy-mcp migrate
```

`migrate` reads these files and writes the servers it finds to
`~/.config/leanproxy_servers.yaml` (or to `--target`, or to
`$LEANPROXY_CONFIG`):

| Source | File | Key read |
|---|---|---|
| OpenCode | `~/.config/opencode/opencode.json` | `mcp` (entries with a `command`) |
| Claude Code | `~/.claude.json`, `~/.config/claude/mcp_config.json` | top-level `mcpServers` |
| Cursor | `~/.cursor/mcp.json` | `mcp_servers` |
| VS Code | user `settings.json` (`~/.config/Code/User/`, `~/Library/Application Support/Code/User/`, and the VSCodium equivalents) | `mcpExtensions` |
| Generic | `~/.config/mcp.json` | `mcp_servers` |

Example output:

```
Found 4 MCP server(s) from 1 source(s):

  OpenCode: 4 server(s)
  Claude:   0 server(s)
  VS Code:  0 server(s)
  Cursor:   0 server(s)
  Generic:  0 server(s)

  [1] nexus-dev (opencode) - /usr/bin/env
  [2] nexus-dev-test (opencode) - /usr/bin/env
  [3] garmin (opencode) - uvx
  [4] Intervals.icu (opencode) - /usr/bin/env

Import to ~/.config/leanproxy_servers.yaml? [y/N]:
```

!!! warning "What `migrate` does not find"
    - It imports **stdio servers only**. Remote (`url`) entries are not
      imported; add them to the YAML file by hand
      ([Quick Start](quickstart.md#1-configure-mcp-servers)).
    - It does not read Claude Desktop's `claude_desktop_config.json`, VS
      Code's `mcp.json` files, or Claude Code project-scoped servers.
    - Cursor's own `mcp.json` uses the key `mcpServers`, which `migrate` does
      not read, so Cursor servers are usually not found.
    - In the Claude Code file, an `env` written as an object
      (`"env": {"KEY": "value"}`) makes the whole file unreadable to
      `migrate`, and it is skipped without an error.
    - If LeanProxy itself is already configured in one of these files, it is
      imported too. Remove that entry from `leanproxy_servers.yaml`, or LeanProxy
      will try to start itself as an upstream server.

    Check the preview (`--dry-run`) and add anything missing with
    [`server add`](quickstart.md#1-configure-mcp-servers) or by editing the
    YAML file.

### Step 2: point your IDE at LeanProxy

Every client starts the same command: `leanproxy-mcp server run --stdio`.
The [Quick Start](quickstart.md#connect-your-client) has the snippet for
Claude Code, Claude Desktop, Cursor, VS Code and OpenCode.

For example, Cursor (`~/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "leanproxy": {
      "command": "leanproxy-mcp",
      "args": ["server", "run", "--stdio"]
    }
  }
}
```

The client starts LeanProxy when it connects and stops it when it
disconnects. You do not run anything by hand. LeanProxy then starts the
upstream servers from `leanproxy_servers.yaml`.

!!! warning "Remove the original entries"
    After you import your servers, remove them from the IDE's own MCP
    configuration. Otherwise the IDE starts them twice (once directly, once
    through LeanProxy) and sees every tool twice.

!!! warning "Do not use `serve` for an IDE"
    `leanproxy-mcp serve` speaks a line-based TCP protocol that no MCP client
    understands, and it is deprecated. Use `server run --stdio`, or
    `server run --http` for one shared gateway
    ([Quick Start](quickstart.md#2b-or-run-one-shared-gateway-over-http)).

## Shell completions

```bash
# Bash
leanproxy-mcp completion bash | sudo tee /etc/bash_completion.d/leanproxy-mcp > /dev/null

# Zsh (the directory must be in your $fpath)
mkdir -p ~/.zsh/completions
leanproxy-mcp completion zsh > ~/.zsh/completions/_leanproxy-mcp

# Fish
leanproxy-mcp completion fish > ~/.config/fish/completions/leanproxy-mcp.fish
```

Generate completions with the binary. The files in the repository's
`completions/` directory are outdated.

## Next steps

- [Quick Start](quickstart.md)
- [Configuration](configuration.md)
- [Commands Reference](commands.md)
