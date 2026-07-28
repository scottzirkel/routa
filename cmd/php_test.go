package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/site"
)

func TestRunWithRoutaPHPPreservesChildExitCode(t *testing.T) {
	err := runWithRoutaPHP(&phpCLIContext{Bin: "/tmp/php"}, "sh", []string{"-c", "exit 7"})
	if err == nil {
		t.Fatal("expected child exit error")
	}

	var exit interface{ ExitCode() int }
	if !errors.As(err, &exit) {
		t.Fatalf("error does not expose ExitCode: %T %[1]v", err)
	}
	if exit.ExitCode() != 7 {
		t.Fatalf("exit code = %d, want 7", exit.ExitCode())
	}
}

func TestExplicitOrCurrentPHPSpecAcceptsExplicitVersion(t *testing.T) {
	got, err := explicitOrCurrentPHPSpec([]string{"8.4"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "8.4" {
		t.Fatalf("spec = %q, want 8.4", got)
	}
}

func TestExplicitOrCurrentPHPSpecDefaultsToCurrentPHP(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	exact := filepath.Join(paths.PHPDir(), "8.4.20")
	binDir := filepath.Join(exact, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "php"), []byte("#!/bin/sh\nprintf 'Core\\nXdebug\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("8.4.20", filepath.Join(paths.PHPDir(), "8.4")); err != nil {
		t.Fatal(err)
	}
	if err := site.Save(&site.State{DefaultPHP: "8.4"}); err != nil {
		t.Fatal(err)
	}

	got, err := explicitOrCurrentPHPSpec(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "8.4" {
		t.Fatalf("spec = %q, want current default 8.4", got)
	}
}

func TestPHPConfigSpecsIncludesAliasWhenPresent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	exact := filepath.Join(paths.PHPDir(), "8.4.20")
	if err := os.MkdirAll(filepath.Join(exact, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("8.4.20", filepath.Join(paths.PHPDir(), "8.4")); err != nil {
		t.Fatal(err)
	}

	got := phpConfigSpecs("8.4.20")
	want := []string{"8.4.20", "8.4"}
	if len(got) != len(want) {
		t.Fatalf("specs = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("specs = %#v, want %#v", got, want)
		}
	}
}
