// Package systemd writes hostr's user units and drives systemctl --user.
package systemd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"github.com/scottzirkel/hostr/internal/caddyconf"
	"github.com/scottzirkel/hostr/internal/paths"
)

const dnsUnit = `[Unit]
Description=hostr DNS responder for *.test
After=network.target

[Service]
Type=simple
ExecStart={{.HostrBin}} serve-dns --addr 127.0.0.1:{{.DNSPort}}
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`

const caddyUnit = `[Unit]
Description=hostr Caddy reverse proxy
After=network.target hostr-dns.service

[Service]
Type=notify
ExecStart=/usr/bin/caddy run --config {{.Caddyfile}} --adapter caddyfile
ExecReload=/usr/bin/caddy reload --config {{.Caddyfile}} --adapter caddyfile --force
Restart=on-failure
RestartSec=2
TimeoutStopSec=5
LimitNOFILE=1048576

[Install]
WantedBy=default.target
`

type unitData struct {
	HostrBin  string
	Caddyfile string
	DNSPort   int
}

func WriteUserUnits(dnsPort int) error {
	dir := paths.SystemdUserDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	data := unitData{
		HostrBin:  bin,
		Caddyfile: caddyconf.Path(),
		DNSPort:   dnsPort,
	}
	if err := writeUnit(dir, "hostr-dns.service", dnsUnit, data); err != nil {
		return err
	}
	if err := writeUnit(dir, "hostr-caddy.service", caddyUnit, data); err != nil {
		return err
	}
	return DaemonReload()
}

func writeUnit(dir, name, tmpl string, data any) error {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := t.Execute(f, data); err != nil {
		return fmt.Errorf("render %s: %w", name, err)
	}
	return nil
}

func DaemonReload() error {
	return run("systemctl", "--user", "daemon-reload")
}

func EnableNow(unit string) error {
	return run("systemctl", "--user", "enable", "--now", unit)
}

func DisableNow(unit string) error {
	return run("systemctl", "--user", "disable", "--now", unit)
}

func Stop(unit string) error {
	return run("systemctl", "--user", "stop", unit)
}

func IsActive(unit string) bool {
	out, _ := exec.Command("systemctl", "--user", "is-active", unit).Output()
	return string(out) == "active\n"
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunSystemctl is a convenience wrapper used by other internal packages.
func RunSystemctl(args ...string) error { return run("systemctl", args...) }
