package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A server that cannot start — a stale socket lock, a port taken, a corrupt
// data directory — otherwise restarts every RestartSec forever. These limits
// stop the unit after a few quick failures so it lands in "failed", where
// `routa doctor` can see it, instead of looping unnoticed.
const unitStartLimit = `StartLimitIntervalSec=60
StartLimitBurst=5`

// routaExecutable is swapped in tests.
var routaExecutable = os.Executable

// databaseRepairExec renders the ExecStartPre line that clears stale runtime
// files before the server starts, so a restart recovers on its own instead of
// failing against a lock its own crashed process left behind. It returns an
// empty string when routa's binary path cannot be resolved to an absolute path,
// since systemd rejects a unit whose Exec line is not absolute.
func databaseRepairExec(engine, version, instance string) string {
	bin := routaBinaryPath()
	if bin == "" {
		return ""
	}
	args := []string{"db", "repair", engine, version}
	if instance != "" {
		args = append(args, instance)
	}
	// The leading "-" keeps a failed or missing repair from blocking the start.
	return "ExecStartPre=-" + bin + " " + strings.Join(args, " ")
}

func routaBinaryPath() string {
	bin, err := routaExecutable()
	if err != nil || bin == "" {
		bin, err = exec.LookPath("routa")
		if err != nil {
			return ""
		}
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	if !filepath.IsAbs(bin) {
		return ""
	}
	return bin
}
