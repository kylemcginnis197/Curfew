package config

import (
	"os"
	"path/filepath"
	"strings"
)

// appDir is the subdirectory used under the OS config/cache roots.
const appDir = "curfew"

// ConfigDir returns the directory holding config.toml, creating it if needed.
// Uses os.UserConfigDir which resolves correctly per-OS:
//   - Linux:   $XDG_CONFIG_HOME/curfew or ~/.config/curfew
//   - macOS:   ~/Library/Application Support/curfew
//   - Windows: %AppData%\curfew
func ConfigDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, appDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// CacheDir returns the directory holding the history DB and the daemon endpoint
// file, creating it if needed.
func CacheDir() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, appDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ConfigPath returns the absolute path to config.toml.
func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// DBPath returns the absolute path to the SQLite history database.
func DBPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "history.db"), nil
}

// EndpointPath returns the path to the daemon endpoint file (port + pid), which
// the TUI reads to locate the running daemon's localhost API.
func EndpointPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.json"), nil
}

// ExpandTilde replaces a leading ~ with the user's home directory. Paths in the
// config (e.g. provider log_glob) are written with ~ for portability.
func ExpandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
