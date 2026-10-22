package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottzirkel/routa/internal/cutover"
	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/services"
	"github.com/scottzirkel/routa/internal/site"
)

func TestDoctorReportJSONShape(t *testing.T) {
	report := doctorReport{
		Services: []doctorService{
			{Name: "routa-dns.service", OK: true, Status: "active"},
		},
		Network: doctorNetwork{
			CaddyAdmin: doctorEndpoint{Name: "caddy admin", OK: true, Detail: "127.0.0.1:2019 (up)"},
			CaddyHTTPS: doctorEndpoint{Name: "caddy https", OK: true, Detail: "127.0.0.1:443 (standard HTTPS)"},
			RoutaDNS:   doctorEndpoint{Name: "routa-dns", OK: true, Detail: "127.0.0.1:1053 (up)"},
		},
		DNS: doctorDNS{
			OK:       true,
			Name:     "doctor.routa.test",
			Answer:   "127.0.0.1",
			Expected: "127.0.0.1",
		},
		Cutover: doctorCutover{
			Phase: "phase_two",
			Label: "Cut over",
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
	service := doctorServiceStatus("routa-caddy.service", func(string) ([]byte, error) {
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
	service := doctorServiceStatus("routa-caddy.service", func(string) ([]byte, error) {
		return nil, errors.New("systemctl unavailable")
	})

	if service.OK {
		t.Fatal("errored service should not be OK")
	}
	if service.Status != "systemctl unavailable" {
		t.Fatalf("status = %q", service.Status)
	}
}

func TestRestartUnitsAcceptsPHPVersion(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	phpDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "php")
	if err := os.MkdirAll(filepath.Join(phpDir, "8.4.20", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("8.4.20", filepath.Join(phpDir, "8.4")); err != nil {
		t.Fatal(err)
	}

	units, err := restartUnits([]string{"php", "8.4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 || units[0] != "routa-php@8.4.service" {
		t.Fatalf("units = %#v", units)
	}
}

func TestRestartUnitsAcceptsPHPUnitAlias(t *testing.T) {
	units, err := restartUnits([]string{"php@8.4"})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 1 || units[0] != "routa-php@8.4.service" {
		t.Fatalf("units = %#v", units)
	}
}

func TestRunningPHPUnitsIgnoresSitePoolSockets(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	runDir := paths.RunDir()
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"php-fpm-8.4.sock",
		"php-fpm-8.4-app.sock",
		"php-fpm-8.4.20.sock",
		"php-fpm-8.4.20-api.sock",
	} {
		f, err := os.Create(filepath.Join(runDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	got := runningPHPUnits()
	want := []string{"routa-php@8.4.20.service", "routa-php@8.4.service"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("runningPHPUnits() = %#v, want %#v", got, want)
	}
}

func TestOptionalServiceDoctorDetailReportsMissingBinaryAndPortConflict(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, services.RedisConfigPath(), "port "+port+"\n")
	writeFile(t, filepath.Join(paths.SystemdUserDir(), services.RedisUnitName), "[Service]\nExecStart=/tmp/routa-missing-redis "+services.RedisConfigPath()+"\n")

	got := optionalServiceDoctorDetail(services.RedisUnitName, false, "inactive")

	for _, want := range []string{
		"missing binary /tmp/routa-missing-redis",
		"Redis port 127.0.0.1:" + port + " is already bound",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("detail missing %q: %s", want, got)
		}
	}
}

func TestOptionalServiceDoctorDetailReportsMySQLMariaDBMismatch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	bin := filepath.Join(t.TempDir(), "mysqld")
	writeFile(t, bin, "#!/bin/sh\necho 'mysqld  Ver 10.11.6-MariaDB for Linux on x86_64'\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, services.MySQLConfigPath("8.0"), "port=3309\n")
	writeFile(t, filepath.Join(paths.SystemdUserDir(), services.MySQLUnitName("8.0")), "[Service]\nExecStart="+bin+" --defaults-file="+services.MySQLConfigPath("8.0")+"\n")

	got := optionalServiceDoctorDetail(services.MySQLUnitName("8.0"), true, "active")

	if !strings.Contains(got, "MySQL unit points at MariaDB binary "+bin) {
		t.Fatalf("detail = %q", got)
	}
}

func TestOptionalServiceDoctorDetailReportsStaleSocketLock(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	pidMax, err := os.ReadFile("/proc/sys/kernel/pid_max")
	if err != nil {
		t.Skipf("cannot read pid_max: %v", err)
	}
	deadPID := strings.TrimSpace(string(pidMax))

	unit := services.MySQLUnitNameForInstance("8.0", "affiliate")
	writeFile(t, services.MySQLConfigPathForInstance("8.0", "affiliate"), "port=3309\n")
	writeFile(t, filepath.Join(paths.SystemdUserDir(), unit), "[Service]\nExecStart=/tmp/routa-missing-mysqld\n")
	writeFile(t, services.MySQLSocketPathForInstance("8.0", "affiliate")+".lock", deadPID+"\n")

	got := optionalServiceDoctorDetail(unit, false, "failed")

	for _, want := range []string{
		"stale runtime files block startup",
		"pid " + deadPID + " is gone",
		"routa db repair mysql 8.0 affiliate",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("detail missing %q: %s", want, got)
		}
	}
}

func TestServiceRestartLoopDetailReportsRepeatedRestarts(t *testing.T) {
	show := func(unit, property string) ([]byte, error) {
		if property != "NRestarts" {
			t.Fatalf("property = %q", property)
		}
		return []byte("363\n"), nil
	}

	got := serviceRestartLoopDetail("routa-mysql@8.0_affiliate.service", false, show)

	if !strings.Contains(got, "restarted this unit 363 times; it is failing on every start") {
		t.Fatalf("detail = %q", got)
	}
	if recovered := serviceRestartLoopDetail("routa-mysql@8.0_affiliate.service", true, show); !strings.Contains(recovered, "since it was last started by hand") {
		t.Fatalf("active detail = %q", recovered)
	}
}

func TestServiceRestartLoopDetailStaysQuietForOccasionalRestarts(t *testing.T) {
	show := func(string, string) ([]byte, error) { return []byte("1\n"), nil }

	if got := serviceRestartLoopDetail("routa-mysql@8.0.service", false, show); got != "" {
		t.Fatalf("detail = %q, want empty", got)
	}
}

func TestCaddyAddrLabelReportsLikelyPortOwnerConflict(t *testing.T) {
	got := caddyAddrLabel(true, false, false)

	if !strings.Contains(got, "another process may own standard HTTPS") {
		t.Fatalf("label = %q", got)
	}
}

func TestDoctorCutoverLabelsAvoidInternalPhaseWording(t *testing.T) {
	for _, label := range []string{
		caddyAddrLabel(true, false, true),
		caddyAddrLabel(false, true, true),
		phaseLabel(cutover.PhaseOne),
		phaseLabel(cutover.PhaseTwo),
	} {
		if strings.Contains(label, "Phase") {
			t.Fatalf("doctor label should not mention internal phase wording: %q", label)
		}
	}
}

func TestParseRoutaDNSOutputFindsARecord(t *testing.T) {
	got := parseRoutaDNSOutput("doctor.routa.test.\t60\tIN\tA\t127.0.0.1\n")

	if got.Answer != "127.0.0.1" {
		t.Fatalf("answer = %q", got.Answer)
	}
	if got.Detail != "" {
		t.Fatalf("detail = %q, want empty", got.Detail)
	}
}

func TestParseRoutaDNSOutputPreservesNoAnswerDetail(t *testing.T) {
	got := parseRoutaDNSOutput("doctor.routa.test.\t60\tIN\tNXDOMAIN\n")

	if got.Answer != "(no answer)" {
		t.Fatalf("answer = %q", got.Answer)
	}
	if !strings.Contains(got.Detail, "NXDOMAIN") {
		t.Fatalf("detail = %q", got.Detail)
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
		{name: "trimmed bare port", input: " 5173 ", want: "127.0.0.1:5173"},
		{name: "leading colon", input: ":5173", want: "127.0.0.1:5173"},
		{name: "host and port", input: "localhost:5173", want: "localhost:5173"},
		{name: "trimmed host and port", input: " localhost:5173 ", want: "localhost:5173"},
		{name: "bracketed ipv6 host and port", input: "[::1]:5173", want: "[::1]:5173"},
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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
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

func TestStatusCommandHasListAlias(t *testing.T) {
	for _, alias := range statusCmd.Aliases {
		if alias == "list" {
			return
		}
	}
	t.Fatalf("status aliases = %#v, want list", statusCmd.Aliases)
}

func TestStatusJSONReportsResolvedSites(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	root := t.TempDir()
	parked := filepath.Join(root, "parked")
	parkedApp := filepath.Join(parked, "parked-app")
	linkedApp := filepath.Join(root, "linked-app")
	writeFile(t, filepath.Join(parkedApp, "index.html"), "ok")
	writeFile(t, filepath.Join(linkedApp, "public", "index.html"), "ok")

	if err := site.Save(&site.State{
		Parked: []string{parked},
		Links: []site.Link{
			{Name: "linked", Path: linkedApp, Root: "public", Secure: false, PHP: "8.4"},
		},
		Aliases: []site.Alias{{Name: "linked-alias", Target: "linked"}},
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	statusJSON = true
	statusCmd.SetOut(&out)
	statusCmd.SetErr(&bytes.Buffer{})
	t.Cleanup(func() {
		statusJSON = false
		statusCmd.SetOut(os.Stdout)
		statusCmd.SetErr(os.Stderr)
	})

	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatal(err)
	}

	var got []statusSite
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	byName := map[string]statusSite{}
	for _, s := range got {
		byName[s.Name] = s
	}

	if got := byName["parked-app"]; got.Source != "parked" || got.URL != "https://parked-app.test" || got.Kind != site.KindStatic {
		t.Fatalf("parked-app = %#v", got)
	}
	if got := byName["linked"]; got.Source != "linked" || got.URL != "http://linked.test" || got.Docroot != filepath.Join(linkedApp, "public") || !got.PHPMissing {
		t.Fatalf("linked = %#v", got)
	}
	if got := byName["linked-alias"]; got.Source != "alias" || got.AliasOf != "linked" || got.URL != "http://linked-alias.test" {
		t.Fatalf("linked-alias = %#v", got)
	}
}
