package paths

import (
	"os"
	"path/filepath"
)

func xdg(envVar, fallback string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func DataDir() string   { return filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "hostr") }
func StateDir() string  { return filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "hostr") }
func ConfigDir() string { return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "hostr") }
func RunDir() string    { return filepath.Join(StateDir(), "run") }
func LogDir() string    { return filepath.Join(StateDir(), "log") }
func PHPDir() string    { return filepath.Join(DataDir(), "php") }
func CADir() string     { return filepath.Join(DataDir(), "ca") }
func SitesDir() string  { return filepath.Join(DataDir(), "sites") }

func SystemdUserDir() string {
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "systemd", "user")
}

func ConfigFile() string { return filepath.Join(ConfigDir(), "config.toml") }
