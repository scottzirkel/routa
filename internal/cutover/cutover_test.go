package cutover

import (
	"strings"
	"testing"
)

func TestDetectPhase(t *testing.T) {
	tests := []struct {
		name       string
		resolvOK   bool
		perLink    bool
		caddyOnStd bool
		caddyOnAlt bool
		want       Phase
	}{
		{
			name:       "phase one alongside valet",
			caddyOnAlt: true,
			want:       PhaseOne,
		},
		{
			name:       "phase two after cutover",
			resolvOK:   true,
			perLink:    true,
			caddyOnStd: true,
			want:       PhaseTwo,
		},
		{
			name:       "phase two tolerates stale alt listener",
			resolvOK:   true,
			perLink:    true,
			caddyOnStd: true,
			caddyOnAlt: true,
			want:       PhaseTwo,
		},
		{
			name:     "missing caddy listener is partial",
			resolvOK: true,
			perLink:  true,
			want:     PhasePartial,
		},
		{
			name:       "resolver changed without per-link routing is partial",
			resolvOK:   true,
			caddyOnStd: true,
			want:       PhasePartial,
		},
		{
			name:       "per-link routing without resolver stub is partial",
			perLink:    true,
			caddyOnStd: true,
			want:       PhasePartial,
		},
		{
			name: "nothing running is partial",
			want: PhasePartial,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectPhase(tt.resolvOK, tt.perLink, tt.caddyOnStd, tt.caddyOnAlt)
			if got != tt.want {
				t.Fatalf("detectPhase() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSudoBlockConfiguresPerLinkRouting(t *testing.T) {
	block := SudoBlock()
	for _, want := range []string{
		"/etc/systemd/network/*.network",
		"DNS=127.0.0.1:1053",
		"Domains=~test",
		"systemctl restart systemd-resolved.service",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sudo block missing %q:\n%s", want, block)
		}
	}
}

func TestRollbackBlockRemovesHostrRouting(t *testing.T) {
	block := SudoRollbackBlock()
	for _, want := range []string{
		`rm -f "$d/hostr.conf"`,
		"rm -f /etc/systemd/resolved.conf.d/hostr.conf",
		"ln -sf /opt/valet-linux/resolv.conf /etc/resolv.conf",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("rollback block missing %q:\n%s", want, block)
		}
	}
}
