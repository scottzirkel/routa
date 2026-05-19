package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottzirkel/routa/internal/site"
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

	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "php-fpm-8.4.conf")
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

func TestWriteFPMConfigWritesZendExtensionToPHPIni(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	extPath := XdebugExtensionPath("8.4")
	if err := os.MkdirAll(filepath.Dir(extPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extPath, []byte("xdebug"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnableXdebug("8.4", XdebugOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := WriteFPMConfig("8.4"); err != nil {
		t.Fatal(err)
	}

	confPath := filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "php-fpm-8.4.conf")
	confData, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(confData), "php_admin_value[zend_extension]") {
		t.Fatalf("zend_extension should be written to php.ini, not pool config:\n%s", confData)
	}

	iniPath := filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "php-fpm-8.4.ini")
	iniData, err := os.ReadFile(iniPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(iniData), "zend_extension = "+extPath) {
		t.Fatalf("FPM php.ini missing zend_extension:\n%s", iniData)
	}
}

func TestWriteFPMConfigIncludesPerSitePoolWithoutCopyingEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	project := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(filepath.Join(project, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(project, "public", "index.php"): "<?php",
		filepath.Join(project, ".env"):                "APP_ENV=local\nDB_DATABASE='routa app'\nnot shell syntax\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := site.Save(&site.State{
		DefaultPHP: "8.4",
		Links:      []site.Link{{Name: "app", Path: project, Root: "public", Secure: true}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := WriteFPMConfig("8.4"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "php-fpm-8.4.conf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"[routa-app]",
		"listen = " + filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "php-fpm-8.4-app.sock"),
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "env[") {
		t.Fatalf("rendered config should not copy project .env values:\n%s", content)
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

func TestXdebugToggleSettings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := EnableXdebug("8.4", XdebugOptions{}); err != nil {
		t.Fatal(err)
	}
	status, err := XdebugINIStatus("8.4", []string{"Core", "Xdebug"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || !status.Enabled {
		t.Fatalf("status = %#v, want available and enabled", status)
	}
	if status.Mode != "debug,develop" || status.StartWithRequest != "yes" || status.ClientHost != "127.0.0.1" || status.ClientPort != "9003" {
		t.Fatalf("status = %#v", status)
	}

	if err := DisableXdebug("8.4"); err != nil {
		t.Fatal(err)
	}
	status, err = XdebugINIStatus("8.4", []string{"xdebug"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled {
		t.Fatalf("status = %#v, want disabled", status)
	}
	if status.Mode != "off" || status.StartWithRequest != "default" {
		t.Fatalf("status = %#v", status)
	}
}

func TestXdebugToggleSharedExtension(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	extPath := XdebugExtensionPath("8.4")
	if err := os.MkdirAll(filepath.Dir(extPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extPath, []byte("xdebug"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnableXdebug("8.4", XdebugOptions{}); err != nil {
		t.Fatal(err)
	}
	status, err := XdebugINIStatus("8.4", []string{"Core"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || !status.Enabled || status.ZendExtension != extPath {
		t.Fatalf("status = %#v, want shared extension enabled", status)
	}

	if err := DisableXdebug("8.4"); err != nil {
		t.Fatal(err)
	}
	status, err = XdebugINIStatus("8.4", []string{"Core"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.Enabled || status.ZendExtension != "" || status.Mode != "off" {
		t.Fatalf("status = %#v, want available but disabled", status)
	}
}

func TestXdebugStatusUnavailableWhenModuleMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := EnableXdebug("8.4", XdebugOptions{}); err != nil {
		t.Fatal(err)
	}
	status, err := XdebugINIStatus("8.4", []string{"Core"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Available || status.Enabled {
		t.Fatalf("status = %#v, want unavailable and disabled", status)
	}
}

func TestEnsureXdebugDisabledIfAvailableUsesSharedExtension(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	extPath := XdebugExtensionPath("8.4")
	if err := os.MkdirAll(filepath.Dir(extPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extPath, []byte("xdebug"), 0o644); err != nil {
		t.Fatal(err)
	}

	ok, err := EnsureXdebugDisabledIfAvailable("8.4")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected shared Xdebug extension to be detected")
	}
	status, err := XdebugINIStatus("8.4", []string{"Core"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.Enabled || status.Mode != "off" || status.StartWithRequest != "default" {
		t.Fatalf("status = %#v", status)
	}
}

func TestEnsureXdebugDisabledIfAvailableSkipsMissingModule(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	dir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "php", "8.4", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	phpBin := filepath.Join(dir, "php")
	if err := os.WriteFile(phpBin, []byte("#!/bin/sh\nprintf 'Core\\njson\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ok, err := EnsureXdebugDisabledIfAvailable("8.4")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected missing Xdebug module to be skipped")
	}
	settings, err := LoadINISettings("8.4")
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) != 0 {
		t.Fatalf("settings = %#v, want none", settings)
	}
}

func TestEnsureXdebugDisabledIfAvailableWritesOffDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	dir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "php", "8.4", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	phpBin := filepath.Join(dir, "php")
	if err := os.WriteFile(phpBin, []byte("#!/bin/sh\nprintf 'Core\\nXdebug\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ok, err := EnsureXdebugDisabledIfAvailable("8.4")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected Xdebug module to be detected")
	}
	status, err := XdebugINIStatus("8.4", []string{"xdebug"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || status.Enabled || status.Mode != "off" || status.StartWithRequest != "default" {
		t.Fatalf("status = %#v", status)
	}
}
