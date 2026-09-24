#!/bin/sh
# Installs the leanproxy-mcp binary from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/mmornati/leanproxy-mcp/main/install/install.sh | sh
#
# Environment:
#   VERSION      release to install, e.g. v0.11 or 0.11 (default: latest)
#   INSTALL_DIR  destination directory (default: /usr/local/bin; sudo is
#                used when it is not writable)
#
# The release archive is verified against the release's checksums.txt
# before anything is installed. Only the binary is installed: no config
# files are written and no shell completions are installed (generate them
# with `leanproxy-mcp completion bash|zsh|fish`).
set -eu

REPO_OWNER="mmornati"
REPO_NAME="leanproxy-mcp"
BINARY="leanproxy-mcp"

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${VERSION:-latest}"

info() {
    echo "==> $*"
}

fail() {
    echo "error: $*" >&2
    exit 1
}

detect_os() {
    case "$(uname -s)" in
        Linux) echo "linux" ;;
        Darwin) echo "darwin" ;;
        *) fail "unsupported operating system: $(uname -s) (supported: Linux, macOS)" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64 | amd64) echo "amd64" ;;
        aarch64 | arm64) echo "arm64" ;;
        *) fail "unsupported architecture: $(uname -m) (supported: amd64, arm64)" ;;
    esac
}

# fetch URL DEST: download URL to DEST (DEST "-" writes to stdout).
fetch() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$1" -O "$2"
    else
        fail "neither curl nor wget is installed"
    fi
}

latest_tag() {
    fetch "https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest" - |
        sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1
}

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        fail "neither sha256sum nor shasum is installed; cannot verify the download"
    fi
}

main() {
    os=$(detect_os)
    arch=$(detect_arch)

    if [ "$VERSION" = "latest" ]; then
        tag=$(latest_tag)
        [ -n "$tag" ] || fail "could not determine the latest release of ${REPO_OWNER}/${REPO_NAME}"
    else
        tag="v${VERSION#v}"
    fi
    version="${tag#v}"

    archive="${BINARY}_${version}_${os}_${arch}.tar.gz"
    base_url="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${tag}"

    tmp_dir=$(mktemp -d)
    trap 'rm -rf "$tmp_dir"' EXIT
    trap 'exit 1' INT TERM

    info "Downloading ${archive} (${tag})"
    fetch "${base_url}/${archive}" "${tmp_dir}/${archive}" ||
        fail "could not download ${base_url}/${archive}"
    fetch "${base_url}/checksums.txt" "${tmp_dir}/checksums.txt" ||
        fail "could not download ${base_url}/checksums.txt"

    # Match the file name exactly: checksums.txt also lists
    # "<archive>.sbom.json", which a substring match would pick up too.
    expected=$(awk -v f="$archive" '$2 == f {print $1}' "${tmp_dir}/checksums.txt")
    [ -n "$expected" ] || fail "${archive} is not listed in checksums.txt"
    actual=$(sha256_of "${tmp_dir}/${archive}")
    [ "$actual" = "$expected" ] ||
        fail "checksum mismatch for ${archive} (expected ${expected}, got ${actual})"
    info "Checksum verified"

    tar -xzf "${tmp_dir}/${archive}" -C "$tmp_dir" "$BINARY" ||
        fail "${archive} does not contain ${BINARY}"

    sudo=""
    if [ ! -d "$INSTALL_DIR" ]; then
        mkdir -p "$INSTALL_DIR" 2>/dev/null || sudo="sudo"
    elif [ ! -w "$INSTALL_DIR" ]; then
        sudo="sudo"
    fi
    if [ -n "$sudo" ]; then
        command -v sudo >/dev/null 2>&1 ||
            fail "${INSTALL_DIR} is not writable and sudo is unavailable; set INSTALL_DIR to a writable directory"
        info "${INSTALL_DIR} is not writable; using sudo"
        sudo mkdir -p "$INSTALL_DIR"
    fi
    $sudo install -m 0755 "${tmp_dir}/${BINARY}" "${INSTALL_DIR}/${BINARY}"

    info "Installed ${BINARY} ${tag} to ${INSTALL_DIR}/${BINARY}"
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) ;;
        *) info "Note: ${INSTALL_DIR} is not on your PATH" ;;
    esac
    info "Shell completions: run '${BINARY} completion --help'"
}

main "$@"
