package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// serveTokenEnv names the environment variable that supplies the `serve`
// auth token instead of the token file.
const serveTokenEnv = "LEANPROXY_SERVE_TOKEN" // #nosec G101 -- this is the env var NAME, not a credential

// serveTokenFileName is the token file created under ~/.config/leanproxy.
const serveTokenFileName = "serve.token"

// minServeTokenLen is the shortest token accepted from a flag, the
// environment or the token file. The generated token is 64 hex characters.
const minServeTokenLen = 16

// serveTokenBytes is the number of random bytes in a generated token.
const serveTokenBytes = 32

// serveTokenPath returns ~/.config/leanproxy/serve.token for home.
func serveTokenPath(home string) string {
	return filepath.Join(home, ".config", "leanproxy", serveTokenFileName)
}

// resolveServeToken picks the token the `serve` listener requires: the
// --auth-token flag, else $LEANPROXY_SERVE_TOKEN, else the token file under
// home (created with a fresh random token on first start). source says
// where the token came from, for the startup log; it never contains the
// token itself.
func resolveServeToken(flagToken, envToken, home string) (token, source string, err error) {
	if flagToken != "" {
		if err := checkServeToken(flagToken); err != nil {
			return "", "", fmt.Errorf("--auth-token: %w", err)
		}
		return flagToken, "--auth-token flag", nil
	}
	if envToken != "" {
		if err := checkServeToken(envToken); err != nil {
			return "", "", fmt.Errorf("%s: %w", serveTokenEnv, err)
		}
		return envToken, serveTokenEnv, nil
	}
	if home == "" {
		return "", "", errors.New("cannot locate the home directory for the serve token file; pass --auth-token or set " + serveTokenEnv)
	}
	path := serveTokenPath(home)
	token, err = loadOrCreateServeToken(path)
	if err != nil {
		return "", "", err
	}
	return token, path, nil
}

func checkServeToken(token string) error {
	if len(token) < minServeTokenLen {
		return fmt.Errorf("token must be at least %d characters", minServeTokenLen)
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return errors.New("token must not contain whitespace")
	}
	return nil
}

// loadOrCreateServeToken reads the token at path, or creates the file (mode
// 0600, directory 0700) with a new random token when it does not exist. A
// token file readable by group or others is tightened to 0600 with a
// warning.
func loadOrCreateServeToken(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create serve token directory: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, err := readServeToken(path)
		if err == nil {
			return token, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		token, err = generateServeToken()
		if err != nil {
			return "", err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			// Another serve process created it first: read theirs.
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create serve token file: %w", err)
		}
		_, werr := f.WriteString(token + "\n")
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("write serve token file: %w", errors.Join(werr, cerr))
		}
		slog.Info("generated serve auth token", "path", path)
		return token, nil
	}
	return "", fmt.Errorf("serve token file %s could not be read or created", path)
}

func readServeToken(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0o077 != 0 {
		slog.Warn("serve token file is readable by other users; tightening its mode to 0600", "path", path, "mode", info.Mode().Perm().String())
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("restrict serve token file mode: %w", err)
		}
	}
	data, err := os.ReadFile(path) // #nosec G304 -- fixed path under the user's config dir
	if err != nil {
		return "", fmt.Errorf("read serve token file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if err := checkServeToken(token); err != nil {
		return "", fmt.Errorf("serve token file %s: %w", path, err)
	}
	return token, nil
}

func generateServeToken() (string, error) {
	b := make([]byte, serveTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate serve token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// isLoopbackListenAddr reports whether a --listen address only binds a
// loopback interface. An empty host (":8080") binds every interface, and a
// host name other than "localhost" is not trusted to resolve to loopback.
func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// serveAuthSettings validates the auth flags against the listen address and
// returns the token to require ("" with noAuth). --no-auth is refused on a
// non-loopback address and together with an explicit token.
func serveAuthSettings(listenAddr, flagToken string, noAuth bool, envToken, home string) (token, source string, err error) {
	if noAuth {
		if flagToken != "" {
			return "", "", errors.New("--no-auth and --auth-token are mutually exclusive")
		}
		if !isLoopbackListenAddr(listenAddr) {
			return "", "", fmt.Errorf("refusing to start: --no-auth is only allowed on a loopback address, not %q", listenAddr)
		}
		return "", "", nil
	}
	return resolveServeToken(flagToken, envToken, home)
}
