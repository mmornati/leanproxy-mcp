#!/usr/bin/env sh
set -euo pipefail

REPO_owner="${REPO_owner:-leanproxy}"
REPO_name="${REPO_name:-leanproxy-mcp}"
VERSION="${VERSION:-}"
OUTPUT_DIR="${OUTPUT_DIR:-./dist}"

log() {
    echo "[$(date +'%Y-%m-%d %H:%M:%S')] $1"
}

log_info() {
    log "INFO: $1"
}

log_error() {
    log "ERROR: $1" >&2
}

detect_os() {
    case "$(uname -s)" in
        Linux*)     echo "linux";;
        Darwin*)    echo "darwin";;
        MINGW*)     echo "windows";;
        *)          echo "";;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64)     echo "amd64";;
        aarch64)    echo "arm64";;
        armv7l)     echo "arm";;
        *)          echo "";;
    esac
}

build_binary() {
    os="$1"
    arch="$2"
    output_dir="${3:-${OUTPUT_DIR}}"
    ext=""
    if [ "$os" = "windows" ]; then
        ext=".exe"
    fi

    bin_name="leanproxy-${VERSION}-${os}-${arch}${ext}"
    output_path="${output_dir}/${bin_name}"

    log_info "Building $bin_name..."

    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
        -ldflags="-s -w -X github.com/mmornati/leanproxy-mcp/cmd.versionString=${VERSION}" \
        -o "$output_path" \
        .

    if [ ! -f "$output_path" ]; then
        log_error "Build failed for $bin_name"
        return 1
    fi

    log_info "Built: $output_path"
}

create_archive() {
    os="$1"
    arch="$2"
    archive_dir="${OUTPUT_DIR}/leanproxy-${VERSION}-${os}-${arch}"
    archive_name="leanproxy-${VERSION}-${os}-${arch}.tar.gz"
    archive_path="${OUTPUT_DIR}/${archive_name}"

    mkdir -p "$archive_dir"
    cp "${OUTPUT_DIR}/leanproxy-${VERSION}-${os}-${arch}/leanproxy" "$archive_dir/" 2>/dev/null || \
    cp "${OUTPUT_DIR}/leanproxy-${VERSION}-${os}-${arch}" "$archive_dir/" 2>/dev/null || true

    if [ -f "LICENSE" ]; then
        cp "LICENSE" "$archive_dir/"
    fi
    if [ -f "README.md" ]; then
        cp "README.md" "$archive_dir/"
    fi

    completions_dir="$archive_dir/completions"
    mkdir -p "$completions_dir"

    cat > "$completions_dir/leanproxy.bash" << 'EOFBASH'
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

    cat > "$completions_dir/_leanproxy" << 'EOFZSH'
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
    fi
}
_leanproxy "$@"
EOFZSH

    tar -czf "$archive_path" -C "$archive_dir" .
    log_info "Created archive: $archive_path"

    rm -rf "$archive_dir"
}

compute_checksums() {
    checksums_file="${OUTPUT_DIR}/checksums.txt"
    : > "$checksums_file"
    for archive in "${OUTPUT_DIR}"/*.tar.gz; do
        if [ -f "$archive" ]; then
            checksum=$(sha256sum "$archive" | cut -d' ' -f1)
            basename=$(basename "$archive")
            echo "$checksum  $basename" >> "$checksums_file"
            log_info "Checksum for $basename: $checksum"
        fi
    done
}

main() {
    if [ -z "$VERSION" ]; then
        if [ -f "cmd/version.go" ]; then
            VERSION=$(grep 'var versionString' cmd/version.go | sed 's/.*= "//' | tr -d '"')
        else
            VERSION="0.1.0"
        fi
    fi

    log_info "=== LeanProxy Release Builder v1.0.0 ==="
    log_info "Version: $VERSION"
    log_info "Repo: $REPO_owner/$REPO_name"

    current_os=$(detect_os)
    current_arch=$(detect_arch)

    log_info "Current platform: $current_os/$current_arch"

    mkdir -p "$OUTPUT_DIR"

    if [ -n "$1" ] && [ "$1" = "--all" ]; then
        platforms="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64"
    else
        platforms="${current_os}/${current_arch}"
    fi

    for platform in $platforms; do
        os="${platform%/*}"
        arch="${platform#*/}"
        build_binary "$os" "$arch"
        create_archive "$os" "$arch"
    done

    compute_checksums

    log_info "=== Build complete ==="
    log_info "Output directory: $OUTPUT_DIR"
    ls -la "$OUTPUT_DIR"
}

main "$@"