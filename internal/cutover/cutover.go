// Package cutover orchestrates the swap from valet-on-standard-ports
// to hostr-on-standard-ports. The destructive system mutations are
// emitted as a single shell block for the user to run with sudo;
// the user-side parts (Caddyfile swap, hostr-caddy restart) we do.
package cutover

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/scottzirkel/hostr/internal/caddyconf"
	"github.com/scottzirkel/hostr/internal/systemd"
)

type Phase int

const (
	PhaseOne     Phase = 1 // running alongside valet on alt ports
	PhaseTwo     Phase = 2 // hostr owns 80/443 and *.test resolution
	PhasePartial Phase = -1
)

func Detect() Phase {
	resolvOK := resolvIsSystemdStub()
	perLink := perLinkDropinExists()
	caddyOnStd := portBound("127.0.0.1:443") || portBound(":443")
	caddyOnAlt := portBound("127.0.0.1:8443")

	if !resolvOK && !perLink && caddyOnAlt {
		return PhaseOne
	}
	if resolvOK && perLink && caddyOnStd {
		return PhaseTwo
	}
	return PhasePartial
}

func perLinkDropinExists() bool {
	matches, _ := filepath.Glob("/etc/systemd/network/*.network.d/hostr.conf")
	return len(matches) > 0
}

func resolvIsSystemdStub() bool {
	target, err := os.Readlink("/etc/resolv.conf")
	if err != nil {
		return false
	}
	return strings.Contains(target, "systemd/resolve")
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func portBound(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

type Check struct {
	Name   string
	OK     bool
	Detail string
}

func Preflight() []Check {
	checks := []Check{
		unitActive("hostr-dns.service", "user"),
		unitActive("hostr-caddy.service", "user"),
		hasSites(),
	}
	checks = append(checks, unitState("valet-dns.service", "valet-dns (will be disabled)"))
	checks = append(checks, unitState("nginx.service", "nginx (will be disabled)"))
	return checks
}

func unitActive(unit, scope string) Check {
	args := []string{}
	if scope == "user" {
		args = append(args, "--user")
	}
	args = append(args, "is-active", unit)
	out, _ := exec.Command("systemctl", args...).Output()
	state := strings.TrimSpace(string(out))
	return Check{
		Name:   unit,
		OK:     state == "active",
		Detail: state,
	}
}

func unitState(unit, label string) Check {
	out, _ := exec.Command("systemctl", "is-active", unit).Output()
	state := strings.TrimSpace(string(out))
	// "active" is OK for our purposes (means there's something to stop).
	return Check{
		Name:   label,
		OK:     true,
		Detail: state,
	}
}

func hasSites() Check {
	// imported lazily to avoid cycle; just count fragment files.
	entries, _ := os.ReadDir(os.ExpandEnv("$HOME/.local/share/hostr/sites"))
	return Check{
		Name:   "configured sites",
		OK:     len(entries) > 0,
		Detail: fmt.Sprintf("%d", len(entries)),
	}
}

// systemd-resolved's *global* Domains= only adds a search/routing domain
// to the global server pool — it does NOT pin queries to a specific server.
// For per-domain server selection we need PER-LINK config, which means a
// drop-in in /etc/systemd/network/<file>.d/ for each existing .network file.
const sudoBlock = `# === hostr cutover — run as root, single block ===
set -e

# 1) Allow user processes (hostr-caddy under systemd --user) to bind low ports.
echo 'net.ipv4.ip_unprivileged_port_start=80' > /etc/sysctl.d/50-hostr.conf
sysctl --system >/dev/null

# 2) Stop and disable valet's services + dnsmasq (valet's local resolver).
systemctl disable --now valet-dns.service nginx.service dnsmasq.service 2>/dev/null || true

# 3) Restore /etc/resolv.conf to systemd-resolved's stub (replaces valet's hijack).
rm -f /etc/resolv.conf
ln -sf /run/systemd/resolve/stub-resolv.conf /etc/resolv.conf

# 4) Make sure systemd-resolved is up.
systemctl enable --now systemd-resolved.service

# 5) Per-link routing: each managed interface gets *.test → 127.0.0.1:1053.
for nf in /etc/systemd/network/*.network; do
    [ -f "$nf" ] || continue
    base=$(basename "$nf" .network)
    mkdir -p "/etc/systemd/network/${base}.network.d"
    cat > "/etc/systemd/network/${base}.network.d/hostr.conf" <<'CONF'
[Network]
DNS=127.0.0.1:1053
Domains=~test
CONF
done

# 6) Reload networkd + restart resolved to apply.
networkctl reload
systemctl restart systemd-resolved.service
`

const sudoRollbackBlock = `# === hostr cutover rollback — run as root ===
set -e

# 1) Re-enable valet's services.
systemctl enable --now valet-dns.service nginx.service dnsmasq.service 2>/dev/null || true

# 2) Remove per-link routing drop-ins.
for d in /etc/systemd/network/*.network.d; do
    rm -f "$d/hostr.conf"
    rmdir --ignore-fail-on-non-empty "$d" 2>/dev/null || true
done
networkctl reload 2>/dev/null || true

# 3) Remove any stale global drop-in (from earlier hostr versions).
rm -f /etc/systemd/resolved.conf.d/hostr.conf
systemctl restart systemd-resolved.service 2>/dev/null || true

# 4) Restore valet's resolv.conf hijack.
rm -f /etc/resolv.conf
ln -sf /opt/valet-linux/resolv.conf /etc/resolv.conf

# 5) Restore default unprivileged port range.
rm -f /etc/sysctl.d/50-hostr.conf
sysctl --system >/dev/null
`

func SudoBlock() string         { return sudoBlock }
func SudoRollbackBlock() string { return sudoRollbackBlock }

// SwapToPhaseTwo restarts hostr-caddy on standard ports.
func SwapToPhaseTwo() error {
	if err := systemd.Stop("hostr-caddy.service"); err != nil {
		return fmt.Errorf("stop hostr-caddy: %w", err)
	}
	if err := caddyconf.Write(caddyconf.PhaseTwo()); err != nil {
		return fmt.Errorf("write Caddyfile (Phase 2): %w", err)
	}
	if err := systemd.EnableNow("hostr-caddy.service"); err != nil {
		return fmt.Errorf("start hostr-caddy: %w", err)
	}
	// Wait for it to actually bind.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if portBound("127.0.0.1:443") || portBound(":443") {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("hostr-caddy didn't bind :443 within 10s — check `systemctl --user status hostr-caddy`")
}

// SwapToPhaseOne restarts hostr-caddy on the alt ports.
func SwapToPhaseOne() error {
	if err := systemd.Stop("hostr-caddy.service"); err != nil {
		return fmt.Errorf("stop hostr-caddy: %w", err)
	}
	if err := caddyconf.Write(caddyconf.PhaseOne()); err != nil {
		return fmt.Errorf("write Caddyfile (Phase 1): %w", err)
	}
	if err := systemd.EnableNow("hostr-caddy.service"); err != nil {
		return fmt.Errorf("start hostr-caddy: %w", err)
	}
	return nil
}
