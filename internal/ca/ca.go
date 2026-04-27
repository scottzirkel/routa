// Package ca installs Caddy's auto-generated root CA into the Arch system
// trust store via p11-kit's `trust anchor`.
//
// We deliberately don't use `caddy trust` here — its embedded sudo invocation
// was unreliable in our environment (silent exit 1, no prompt visible).
// Browser NSS trust is handled by Caddy itself the first time `caddy trust`
// or HTTPS issuance touches a profile; this package only owns the system store.
package ca

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func Install() error {
	src, err := rootPath()
	if err != nil {
		return err
	}
	if err := preAuth("install the local root CA into /etc/ca-certificates/"); err != nil {
		return err
	}
	return runSudo("trust", "anchor", "--store", src)
}

func Uninstall() error {
	src, err := rootPath()
	if err != nil {
		return err
	}
	if err := preAuth("remove the local root CA from /etc/ca-certificates/"); err != nil {
		return err
	}
	return runSudo("trust", "anchor", "--remove", src)
}

func rootPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(home, ".local/share/caddy/pki/authorities/local/root.crt")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("Caddy root not found at %s — is hostr-caddy running? (`systemctl --user status hostr-caddy`): %w", p, err)
	}
	return p, nil
}

func preAuth(reason string) error {
	fmt.Fprintf(os.Stderr, "  hostr needs sudo to %s.\n", reason)
	cmd := exec.Command("sudo", "-v")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo authentication failed: %w", err)
	}
	return nil
}

func runSudo(args ...string) error {
	cmd := exec.Command("sudo", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
