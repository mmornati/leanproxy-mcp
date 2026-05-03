#!/usr/bin/env sh
set -euo pipefail

INSTALL_SCRIPT_VERSION="1.0.0"
LOG_FILE="/tmp/leanproxy-install.log"

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${VERSION:-latest}"
REPO_owner="leanproxy"
REPO_name="leanproxy-mcp"

log() {
    echo "[$(date +'%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

log_info() {
    log "INFO: $1"
}

log_error() {
    log "ERROR: $1" >&2
}

cleanup() {
    if [ -n "${TMP_DIR:-}" ] && [ -d "$TMP_DIR" ]; then
        rm -rf "$TMP_DIR"
    fi
}

trap cleanup EXIT INT TERM

detect_os() {
    case "$(uname -s)" in
        Linux*)     echo "linux";;
        Darwin*)    echo "darwin";;
        *)          echo " unsupported";;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64)     echo "amd64";;
        aarch64)    echo "arm64";;
        armv7l)     echo "arm";;
        *)          echo "amd64";;
    esac
}

get_latest_version() {
    version=$(curl -sSL "https://api.github.com/repos/${REPO_owner}/${REPO_name}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"v?([^"]+)".*/\1/')
    if [ -z "$version" ]; then
        log_error "Failed to determine latest version"
        return 1
    fi
    echo "$version"
}

download_file() {
    url="$1"
    dest="$2"
    log_info "Downloading $url"
    if command -v curl >/dev/null 2>&1; then
        curl -sSLf "$url" -o "$dest"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$url" -O "$dest"
    else
        log_error "Neither curl nor wget found"
        return 1
    fi
}

verify_checksum() {
    file="$1"
    expected="$2"
    actual=$(sha256sum "$file" 2>/dev/null | cut -d' ' -f1 || shasum -a 256 "$file" 2>/dev/null | cut -d' ' -f1)
    if [ "$actual" != "$expected" ]; then
        log_error "Checksum mismatch for $file"
        log_error "Expected: $expected"
        log_error "Actual:   $actual"
        return 1
    fi
    log_info "Checksum verified for $file"
}

create_config_dir() {
    config_dir="$HOME/.leanproxy"
    if [ ! -d "$config_dir" ]; then
        mkdir -p "$config_dir"
        chmod 0700 "$config_dir"
        log_info "Created config directory: $config_dir"
    fi
}

create_default_config() {
    config_file="$HOME/.leanproxy/config.yaml"
    if [ ! -f "$config_file" ]; then
        cat > "$config_file" << 'EOF'
# LeanProxy Configuration
# This is the default configuration file

# Server configuration directory
server_config_dir: "${HOME}/.config/leanproxy"

# Log level (debug, info, warn, error)
log_level: info

# Enable verbose logging
verbose: false

# Default listen address
listen: "127.0.0.1:8080"

# Default upstream URL
upstream: "http://localhost:8081"
EOF
        chmod 0600 "$config_file"
        log_info "Created default config: $config_file"
    fi
}

install_shell_completion() {
    shell="$1"
    compat_dir="$HOME/.leanproxy/completions"
    mkdir -p "$compat_dir"

    case "$shell" in
        bash)
            completion_file="$compat_dir/leanproxy.bash"
            cat > "$completion_file" << 'EOFBASH'
#!/bin/bash
# LeanProxy Bash Completion

_leanproxy() {
    local cur prev
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        serve)
            COMPREPLY+=(--listen --upstream --config --dry-run --help)
            ;;
        version)
            COMPREPLY=()
            ;;
        completion)
            COMPREPLY+=(--help bash zsh)
            ;;
        *)
            COMPREPLY+=($(compgen -W "serve version completion config init" -- "$cur"))
            ;;
    esac
}

complete -F _leanproxy leanproxy
EOFBASH
            if [ -d /etc/bash_completion.d ]; then
                cp "$completion_file" /etc/bash_completion.d/leanproxy
            fi
            ;;
        zsh)
            completion_file="$compat_dir/_leanproxy"
            cat > "$completion_file" << 'EOFZSH'
#compdef leanproxy

_leanproxy() {
    local -a commands
    commands=(
        'serve:Start the JSON-RPC streaming proxy server'
        'version:Print version information'
        'completion:Generate shell completion scripts'
        'config:Configuration management'
        'init:Initialize new configuration'
    )

    if (( CURRENT == 2 )); then
        _describe 'command' commands
    else
        case "${words[2]}" in
            serve)
                _arguments \
                    '--listen[Address to listen on]' \
                    '--upstream[Upstream JSON-RPC server URL]' \
                    '--config[Path to config file]' \
                    '--dry-run[Preview actions without making changes]'
                ;;
            completion)
                _arguments 'bash:Generate bash completion' 'zsh:Generate zsh completion'
                ;;
        esac
    fi
}

_leanproxy "$@"
EOFZSH
            local zsh_completion_dir
            zsh_completion_dir="$(brew --prefix)/share/zsh/site-functions" 2>/dev/null || \
                                 "${HOME}/.local/share/zsh/site-functions" 2>/dev/null || \
                                 "${HOME}/.zsh/completions" 2>/dev/null
            if [ -d "$zsh_completion_dir" ]; then
                cp "$completion_file" "$zsh_completion_dir/"
            fi
            ;;
    esac
    log_info "Installed $shell completion"
}

backup_existing() {
    if [ -f "$INSTALL_DIR/leanproxy" ]; then
        backup="${INSTALL_DIR}/leanproxy.bak"
        cp "$INSTALL_DIR/leanproxy" "$backup"
        log_info "Backed up existing binary to $backup"
    fi
}

main() {
    log_info "=== LeanProxy Installer v${INSTALL_SCRIPT_VERSION} ==="
    log_info "Install directory: $INSTALL_DIR"
    log_info "Version: $VERSION"

    os=$(detect_os)
    arch=$(detect_arch)

    if [ "$os" = " unsupported" ]; then
        log_error "Unsupported operating system: $(uname -s)"
        exit 1
    fi

    log_info "Detected OS: $os, Arch: $arch"

    if [ "$VERSION" = "latest" ]; then
        log_info "Fetching latest version..."
        VERSION=$(get_latest_version)
        log_info "Latest version: $VERSION"
    fi

    archive_name="leanproxy-${VERSION}-${os}-${arch}.tar.gz"
    download_url="https://github.com/${REPO_owner}/${REPO_name}/releases/download/v${VERSION}/${archive_name}"
    checksum_url="https://github.com/${REPO_owner}/${REPO_name}/releases/download/v${VERSION}/checksums.txt"

    TMP_DIR=$(mktemp -d)
    archive_path="${TMP_DIR}/${archive_name}"
    checksum_path="${TMP_DIR}/checksums.txt"

    log_info "Downloading release from $download_url"
    if ! download_file "$download_url" "$archive_path"; then
        log_error "Failed to download release"
        exit 1
    fi

    if ! download_file "$checksum_url" "$checksum_path"; then
        log_error "Failed to download checksums"
        exit 1
    fi

    expected_checksum=$(grep "$archive_name" "$checksum_path" | cut -d' ' -f1)
    if [ -z "$expected_checksum" ]; then
        log_error "Could not find checksum for $archive_name"
        exit 1
    fi

    if ! verify_checksum "$archive_path" "$expected_checksum"; then
        log_error "Archive checksum verification failed"
        exit 1
    fi

    mkdir -p "$INSTALL_DIR"
    chmod 755 "$INSTALL_DIR"

    tar -xzf "$archive_path" -C "$TMP_DIR"
    binary_path="${TMP_DIR}/leanproxy-${VERSION}-${os}-${arch}/leanproxy"
    if [ ! -f "$binary_path" ]; then
        binary_path="${TMP_DIR}/leanproxy"
    fi

    backup_existing

    cp "$binary_path" "$INSTALL_DIR/leanproxy"
    chmod 755 "$INSTALL_DIR/leanproxy"
    log_info "Installed leanproxy to $INSTALL_DIR/leanproxy"

    create_config_dir
    create_default_config

    current_shell=$(basename "$SHELL" 2>/dev/null || echo "bash")
    install_shell_completion "$current_shell"

    log_info "=== Installation complete ==="
    log_info "Run 'leanproxy version' to verify installation"

    if command -v leanproxy >/dev/null 2>&1; then
        log_info "leanproxy is now available in your PATH"
    else
        log_info "You may need to add $INSTALL_DIR to your PATH"
    fi
}

DRY_RUN=${DRY_RUN:-false}
if [ "$DRY_RUN" = "true" ]; then
    echo "[Dry Run] Would perform the following actions:"
    echo "  - Download leanproxy ${VERSION} for ${os}/${arch}"
    echo "  - Install to ${INSTALL_DIR}"
    echo "  - Create ~/.leanproxy configuration"
    echo "  - Install shell completion for ${current_shell:-bash}"
    exit 0
fi

main