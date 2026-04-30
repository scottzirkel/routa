package cmd

import (
	"errors"
	"strings"
	"testing"
)

func TestMissingInstallDependenciesReportsAbsentCommands(t *testing.T) {
	missing := missingInstallDependencies(func(name string) (string, error) {
		if name == "trust" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	})

	if len(missing) != 1 || missing[0] != "trust" {
		t.Fatalf("missing dependencies = %#v, want trust", missing)
	}
}

func TestInstallDependencyErrorIncludesPackages(t *testing.T) {
	missing := missingInstallDependencies(func(string) (string, error) {
		return "", errors.New("not found")
	})
	if strings.Join(missing, ", ") != "caddy, trust, systemctl" {
		t.Fatalf("missing dependencies = %#v", missing)
	}

	err := installDependencyError(missing)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"missing required command(s): caddy, trust, systemctl",
		"Arch example: sudo pacman -S caddy p11-kit systemd",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}
