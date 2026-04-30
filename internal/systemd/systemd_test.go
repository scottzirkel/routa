package systemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderUserUnitFiles(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	hostrBin := filepath.Join(t.TempDir(), "hostr")
	units, err := RenderUserUnitFiles(1053, hostrBin)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("got %d units, want 2: %#v", len(units), units)
	}

	byName := map[string]string{}
	for _, unit := range units {
		byName[unit.Name] = unit.Content
	}

	dns := byName["hostr-dns.service"]
	for _, want := range []string{
		"Description=hostr DNS responder for *.test",
		"ExecStart=" + hostrBin + " serve-dns --addr 127.0.0.1:1053",
		"WantedBy=default.target",
	} {
		if !strings.Contains(dns, want) {
			t.Fatalf("DNS unit missing %q:\n%s", want, dns)
		}
	}

	caddy := byName["hostr-caddy.service"]
	for _, want := range []string{
		"Description=hostr Caddy reverse proxy",
		"After=network.target hostr-dns.service",
		"ExecStart=/usr/bin/caddy run --config " + filepath.Join(os.Getenv("XDG_DATA_HOME"), "hostr", "Caddyfile") + " --adapter caddyfile",
		"ExecReload=/usr/bin/caddy reload --config " + filepath.Join(os.Getenv("XDG_DATA_HOME"), "hostr", "Caddyfile") + " --adapter caddyfile --force",
		"LimitNOFILE=1048576",
	} {
		if !strings.Contains(caddy, want) {
			t.Fatalf("Caddy unit missing %q:\n%s", want, caddy)
		}
	}
}
