package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottzirkel/hostr/internal/site"
)

func TestDoctorReportJSONShape(t *testing.T) {
	report := doctorReport{
		Services: []doctorService{
			{Name: "hostr-dns.service", OK: true, Status: "active"},
		},
		Network: doctorNetwork{
			CaddyAdmin: doctorEndpoint{Name: "caddy admin", OK: true, Detail: "127.0.0.1:2019 (up)"},
			CaddyHTTPS: doctorEndpoint{Name: "caddy https", OK: true, Detail: "127.0.0.1:443 (Phase 2)"},
			HostrDNS:   doctorEndpoint{Name: "hostr-dns", OK: true, Detail: "127.0.0.1:1053 (up)"},
		},
		DNS: doctorDNS{
			OK:       true,
			Name:     "doctor.hostr.test",
			Answer:   "127.0.0.1",
			Expected: "127.0.0.1",
		},
		Cutover: doctorCutover{
			Phase: "phase_two",
			Label: "Phase 2",
		},
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{
		`"services"`,
		`"network"`,
		`"dns"`,
		`"cutover"`,
		`"phase":"phase_two"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("JSON missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "site_probes") {
		t.Fatalf("empty site probes should be omitted: %s", body)
	}
}

func TestDoctorProbeJSONShape(t *testing.T) {
	report := doctorReport{
		SiteProbes: []doctorProbeResult{
			{Name: "app", URL: "https://app.test", OK: false, Status: "error", Error: "connection refused"},
			{Name: "api", URL: "https://api.test", OK: true, Status: "HTTP 204", StatusCode: 204},
		},
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{
		`"site_probes"`,
		`"error":"connection refused"`,
		`"status_code":204`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("JSON missing %s: %s", want, body)
		}
	}
}

func TestDoctorServiceStatusUsesSystemctlOutput(t *testing.T) {
	service := doctorServiceStatus("hostr-caddy.service", func(string) ([]byte, error) {
		return []byte("inactive\n"), errors.New("exit status 3")
	})

	if service.OK {
		t.Fatal("inactive service should not be OK")
	}
	if service.Status != "inactive" {
		t.Fatalf("status = %q, want inactive", service.Status)
	}
}

func TestDoctorServiceStatusFallsBackToError(t *testing.T) {
	service := doctorServiceStatus("hostr-caddy.service", func(string) ([]byte, error) {
		return nil, errors.New("systemctl unavailable")
	})

	if service.OK {
		t.Fatal("errored service should not be OK")
	}
	if service.Status != "systemctl unavailable" {
		t.Fatalf("status = %q", service.Status)
	}
}

func TestNormalizeProxyTarget(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "bare port", input: "5173", want: "127.0.0.1:5173"},
		{name: "leading colon", input: ":5173", want: "127.0.0.1:5173"},
		{name: "host and port", input: "localhost:5173", want: "localhost:5173"},
		{name: "invalid port", input: "nope", wantErr: "port must be 1-65535"},
		{name: "invalid zero port", input: ":0", wantErr: "port must be 1-65535"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeProxyTarget(tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("target = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatusReportsMissingCustomDocroot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	root := t.TempDir()
	missingDocroot := filepath.Join(root, "missing")
	if err := site.Save(&site.State{
		Links: []site.Link{{Name: "app", Path: root, Root: "missing", Secure: false}},
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	statusCmd.SetOut(&out)
	statusCmd.SetErr(&bytes.Buffer{})
	t.Cleanup(func() {
		statusCmd.SetOut(os.Stdout)
		statusCmd.SetErr(os.Stderr)
	})

	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatal(err)
	}

	body := out.String()
	for _, want := range []string{
		"NAME",
		"app.test",
		"static",
		"no",
		missingDocroot,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("status output missing %q:\n%s", want, body)
		}
	}
}
