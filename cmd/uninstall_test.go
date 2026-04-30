package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPHPUnitsForUninstallDiscoversEnabledAndRuntimeInstances(t *testing.T) {
	systemdDir := t.TempDir()
	runDir := t.TempDir()

	wantsDir := filepath.Join(systemdDir, "default.target.wants")
	if err := os.MkdirAll(wantsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(systemdDir, "hostr-php@.service"),
		filepath.Join(wantsDir, "hostr-php@8.4.service"),
		filepath.Join(runDir, "php-fpm-8.3.conf"),
		filepath.Join(runDir, "php-fpm-8.3.sock"),
	} {
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := phpUnitsForUninstall(systemdDir, runDir)
	want := []string{"hostr-php@8.3.service", "hostr-php@8.4.service"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("phpUnitsForUninstall() = %#v, want %#v", got, want)
	}
}

func TestPHPUnitsForUninstallIgnoresTemplatesAndMalformedRuntimeFiles(t *testing.T) {
	systemdDir := t.TempDir()
	runDir := t.TempDir()

	for _, path := range []string{
		filepath.Join(systemdDir, "hostr-php@.service"),
		filepath.Join(systemdDir, "hostr-php@.service.bak"),
		filepath.Join(runDir, "php-fpm-.conf"),
		filepath.Join(runDir, "php-fpm-8.4.log"),
		filepath.Join(runDir, "php-fpm-8.4.conf"),
	} {
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := phpUnitsForUninstall(systemdDir, runDir)
	want := []string{"hostr-php@8.4.service"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("phpUnitsForUninstall() = %#v, want %#v", got, want)
	}
}

func TestHostrUnitsForUninstallIncludesDiscoveredPHPUnits(t *testing.T) {
	configHome := t.TempDir()
	stateHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", stateHome)

	if err := os.MkdirAll(filepath.Join(configHome, "systemd", "user", "default.target.wants"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateHome, "hostr", "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(configHome, "systemd", "user", "default.target.wants", "hostr-php@8.3.service"),
		filepath.Join(stateHome, "hostr", "run", "php-fpm-8.4.sock"),
	} {
		if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := hostrUnitsForUninstall()
	want := []string{"hostr-caddy.service", "hostr-dns.service", "hostr-php@8.3.service", "hostr-php@8.4.service"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hostrUnitsForUninstall() = %#v, want %#v", got, want)
	}
}

func TestHostrDirsForPurgeUsesXDGHostrDirs(t *testing.T) {
	configHome := t.TempDir()
	dataHome := t.TempDir()
	stateHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("XDG_STATE_HOME", stateHome)

	got := hostrDirsForPurge()
	want := []string{
		filepath.Join(dataHome, "hostr"),
		filepath.Join(stateHome, "hostr"),
		filepath.Join(configHome, "hostr"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hostrDirsForPurge() = %#v, want %#v", got, want)
	}
}

func TestPurgeHostrDirRemovesOnlyHostrNamedDirectory(t *testing.T) {
	root := t.TempDir()
	hostrDir := filepath.Join(root, "hostr")
	keepDir := filepath.Join(root, "not-hostr")
	if err := os.MkdirAll(filepath.Join(hostrDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(keepDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := purgeHostrDir(hostrDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hostrDir); !os.IsNotExist(err) {
		t.Fatalf("hostr dir should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(keepDir); err != nil {
		t.Fatalf("unrelated dir should remain: %v", err)
	}

	err := purgeHostrDir(keepDir)
	if err == nil {
		t.Fatal("expected refusal for non-hostr directory")
	}
	if !strings.Contains(err.Error(), "refusing to purge non-hostr directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}
