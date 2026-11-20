package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasUserSettingsIgnoresWhatEveryInstallWrites(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	for _, s := range []INISetting{
		{Key: XdebugModeKey, Value: "off"},
		{Key: XdebugStartWithRequestKey, Value: "default"},
		{Key: ExtensionKey, Value: PcovExtension.Path("8.6")},
	} {
		if err := SetINISetting("8.6", s.Key, s.Value); err != nil {
			t.Fatal(err)
		}
	}
	has, err := HasUserSettings("8.6")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("install defaults counted as user settings")
	}

	if err := SetINISetting("8.6", "upload_max_filesize", "20M"); err != nil {
		t.Fatal(err)
	}
	if has, _ := HasUserSettings("8.6"); !has {
		t.Fatal("upload_max_filesize not counted as a user setting")
	}
}

func TestPreviousMinorWithSettingsPicksNewestOlderVersion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	for _, spec := range []string{"8.3", "8.4"} {
		if err := SetINISetting(spec, "memory_limit", "-1"); err != nil {
			t.Fatal(err)
		}
	}
	// Only install defaults, so it does not count.
	if err := SetINISetting("8.5", XdebugModeKey, "off"); err != nil {
		t.Fatal(err)
	}
	// Exact patches and newer versions never count.
	for _, spec := range []string{"8.4.20", "8.7"} {
		if err := SetINISetting(spec, "memory_limit", "1G"); err != nil {
			t.Fatal(err)
		}
	}

	got, err := PreviousMinorWithSettings("8.6")
	if err != nil {
		t.Fatal(err)
	}
	if got != "8.4" {
		t.Fatalf("PreviousMinorWithSettings(8.6) = %q, want 8.4", got)
	}
	if got, _ := PreviousMinorWithSettings("8.3"); got != "" {
		t.Fatalf("PreviousMinorWithSettings(8.3) = %q, want none", got)
	}
}

func TestCopySettingsCarriesSettingsAndTurnsOffExtensions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	for _, s := range []INISetting{
		{Key: "upload_max_filesize", Value: "20M"},
		{Key: "memory_limit", Value: "-1"},
		{Key: ExtensionKey, Value: PcovExtension.Path("8.5")},
		{Key: ZendExtensionKey, Value: "/opt/ioncube_8.5.so"},
	} {
		if err := SetINISetting("8.5", s.Key, s.Value); err != nil {
			t.Fatal(err)
		}
	}
	writeConfD(t, "8.5", "20-mongo.ini", "[mongodb]\nextension = /opt/mongodb_8.5.so\nmongodb.debug = 0\n")
	writeConfD(t, "8.5", "30-kept.ini", "memory_limit = 2G\n")
	writeConfD(t, "8.6", "30-kept.ini", "memory_limit = 4G\n")
	if err := SetINISetting("8.6", "memory_limit", "512M"); err != nil {
		t.Fatal(err)
	}

	report, err := CopySettings("8.5", "8.6")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(report.Settings, ",") != "upload_max_filesize,memory_limit" {
		t.Fatalf("Settings = %v", report.Settings)
	}
	if strings.Join(report.ConfFiles, ",") != "20-mongo.ini" || strings.Join(report.SkippedConfFiles, ",") != "30-kept.ini" {
		t.Fatalf("ConfFiles = %v, Skipped = %v", report.ConfFiles, report.SkippedConfFiles)
	}
	if len(report.TurnedOff) != 2 {
		t.Fatalf("TurnedOff = %v, want the ioncube and mongodb lines", report.TurnedOff)
	}

	settings, err := LoadINISettings("8.6")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range settings {
		got[s.Key] = s.Value
	}
	if got["upload_max_filesize"] != "20M" || got["memory_limit"] != "-1" {
		t.Fatalf("8.6 settings = %#v", got)
	}
	if _, ok := got[ExtensionKey]; ok {
		t.Fatalf("routa's pcov line for 8.5 was copied to 8.6: %#v", got)
	}
	if _, ok := got[ZendExtensionKey]; ok {
		t.Fatalf("an 8.5 extension was left on for 8.6: %#v", got)
	}
	raw, err := os.ReadFile(INIPath("8.6"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "; zend_extension = /opt/ioncube_8.5.so") {
		t.Fatalf("turned-off extension line missing:\n%s", raw)
	}

	mongo, err := os.ReadFile(filepath.Join(ConfDir("8.6"), "20-mongo.ini"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mongo), "; extension = /opt/mongodb_8.5.so") || !strings.Contains(string(mongo), "mongodb.debug = 0") {
		t.Fatalf("copied conf.d file:\n%s", mongo)
	}
	kept, _ := os.ReadFile(filepath.Join(ConfDir("8.6"), "30-kept.ini"))
	if string(kept) != "memory_limit = 4G\n" {
		t.Fatalf("8.6's own conf.d file was overwritten: %q", kept)
	}
	// The copied conf.d file must still parse.
	if _, err := EffectiveINISettings("8.6"); err != nil {
		t.Fatal(err)
	}
}
