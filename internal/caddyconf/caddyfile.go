// Package caddyconf renders the top-level Caddyfile that imports per-site fragments.
package caddyconf

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/scottzirkel/hostr/internal/paths"
)

const rootTmpl = `{
	http_port  {{.HTTPPort}}
	https_port {{.HTTPSPort}}
	admin      127.0.0.1:{{.AdminPort}}
	log {
		output file {{.LogDir}}/caddy.log
		level INFO
	}
}

import {{.SitesDir}}/*.caddy
`

type RootConfig struct {
	HTTPPort  int
	HTTPSPort int
	AdminPort int
}

// PhaseOne is the alongside-valet config: alternate ports, isolated.
func PhaseOne() RootConfig {
	return RootConfig{HTTPPort: 8080, HTTPSPort: 8443, AdminPort: 2019}
}

// PhaseTwo is the post-cutover config: standard ports.
func PhaseTwo() RootConfig {
	return RootConfig{HTTPPort: 80, HTTPSPort: 443, AdminPort: 2019}
}

// Path returns the Caddyfile path used by the systemd unit.
func Path() string { return filepath.Join(paths.DataDir(), "Caddyfile") }

func Write(cfg RootConfig) error {
	t, err := template.New("Caddyfile").Parse(rootTmpl)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.DataDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.SitesDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.LogDir(), 0o755); err != nil {
		return err
	}
	f, err := os.Create(Path())
	if err != nil {
		return err
	}
	defer f.Close()
	data := struct {
		RootConfig
		LogDir   string
		SitesDir string
	}{
		RootConfig: cfg,
		LogDir:     paths.LogDir(),
		SitesDir:   paths.SitesDir(),
	}
	if err := t.Execute(f, data); err != nil {
		return fmt.Errorf("render Caddyfile: %w", err)
	}
	return nil
}
