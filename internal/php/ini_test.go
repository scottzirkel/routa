package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestINISettingsRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := SetINISetting("8.4", "memory_limit", "512M"); err != nil {
		t.Fatal(err)
	}
	if err := SetINISetting("8.4", "upload_max_filesize", "128M"); err != nil {
		t.Fatal(err)
	}
	if err := SetINISetting("8.4", "memory_limit", "1G"); err != nil {
		t.Fatal(err)
	}

	settings, err := LoadINISettings("8.4")
	if err != nil {
		t.Fatal(err)
	}
	want := []INISetting{
		{Key: "memory_limit", Value: "1G"},
		{Key: "upload_max_filesize", Value: "128M"},
	}
	if len(settings) != len(want) {
		t.Fatalf("got %d settings, want %d: %#v", len(settings), len(want), settings)
	}
	for i := range want {
		if settings[i] != want[i] {
			t.Fatalf("setting %d = %#v, want %#v", i, settings[i], want[i])
		}
	}

	if err := UnsetINISetting("8.4", "memory_limit"); err != nil {
		t.Fatal(err)
	}
	settings, err = LoadINISettings("8.4")
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 1 || settings[0].Key != "upload_max_filesize" {
		t.Fatalf("unexpected settings after unset: %#v", settings)
	}
}

func TestWriteFPMConfigIncludesINISettings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := SetINISetting("8.4", "memory_limit", "512M"); err != nil {
		t.Fatal(err)
	}
	if err := SetINISetting("8.4", "post_max_size", "128M"); err != nil {
		t.Fatal(err)
	}
	if err := WriteFPMConfig("8.4"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "hostr", "run", "php-fpm-8.4.conf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"decorate_workers_output = no\nphp_admin_value[memory_limit] = 512M",
		"php_admin_value[post_max_size] = 128M",
		"php_admin_value[opcache.memory_consumption] = 256",
		"php_admin_value[opcache.revalidate_freq] = 0",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, content)
		}
	}
}

func TestEffectiveINISettingsUserOverridesLaravelDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := SetINISetting("8.4", "memory_limit", "-1"); err != nil {
		t.Fatal(err)
	}
	settings, err := EffectiveINISettings("8.4")
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, setting := range settings {
		got[setting.Key] = setting.Value
	}
	if got["memory_limit"] != "-1" {
		t.Fatalf("memory_limit = %q, want -1", got["memory_limit"])
	}
	if got["opcache.max_accelerated_files"] != "20000" {
		t.Fatalf("missing Laravel opcache default: %#v", got)
	}
}
