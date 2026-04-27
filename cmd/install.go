package cmd

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/hostr/internal/ca"
	"github.com/scottzirkel/hostr/internal/caddyconf"
	"github.com/scottzirkel/hostr/internal/paths"
	"github.com/scottzirkel/hostr/internal/systemd"
)

const phaseOneDNSPort = 1053

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Provision hostr alongside valet on alternate ports (DNS :1053, Caddy :8080/:8443)",
	Long: `install is non-destructive and idempotent. It:
  - creates hostr's XDG directories
  - generates Caddyfile and systemd user units
  - installs Caddy's local root CA into the system trust store (sudo)
  - starts hostr-dns and hostr-caddy on alternate ports

Sites can be added with park/link. Verify with:
  dig @127.0.0.1 -p 1053 example.test
  curl -k https://example.test:8443

When you're satisfied, run "hostr cutover" to swap to standard ports and DNS.`,
	RunE: runInstall,
}

func init() {
	rootCmd.AddCommand(installCmd)
}

func runInstall(_ *cobra.Command, _ []string) error {
	steps := []struct {
		name string
		fn   func() error
	}{
		{"create directories", ensureDirs},
		{"render Caddyfile", func() error { return caddyconf.Write(caddyconf.PhaseOne()) }},
		{"write systemd user units", func() error { return systemd.WriteUserUnits(phaseOneDNSPort) }},
		{"enable hostr-caddy", func() error { return systemd.EnableNow("hostr-caddy.service") }},
		{"wait for Caddy admin API", waitForCaddyAdmin},
		{"trust Caddy local CA (will sudo)", ca.Install},
		{"enable hostr-dns", func() error { return systemd.EnableNow("hostr-dns.service") }},
	}
	for _, s := range steps {
		fmt.Printf("→ %s\n", s.name)
		if err := s.fn(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	fmt.Println()
	fmt.Println("Done. Verify:")
	fmt.Println("  dig @127.0.0.1 -p 1053 example.test")
	fmt.Println("  systemctl --user status hostr-dns hostr-caddy")
	fmt.Println()
	fmt.Println("Note: *.test still resolves through valet system-wide. To test hostr's DNS")
	fmt.Println("specifically, query 127.0.0.1:1053 directly. Cutover swaps the system resolver.")
	return nil
}

func waitForCaddyAdmin() error {
	deadline := time.Now().Add(8 * time.Second)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://127.0.0.1:2019/config/")
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("Caddy admin API at 127.0.0.1:2019 didn't come up within 8s — check `systemctl --user status hostr-caddy`")
}

func ensureDirs() error {
	for _, d := range []string{
		paths.DataDir(), paths.StateDir(), paths.ConfigDir(),
		paths.RunDir(), paths.LogDir(), paths.PHPDir(),
		paths.CADir(), paths.SitesDir(),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
