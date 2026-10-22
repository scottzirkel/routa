package services

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/scottzirkel/routa/internal/paths"
)

func setupRuntimeDirs(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(paths.RunDir(), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeRuntimeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixedLookup(cmdline string, state processState) processLookup {
	return func(int) (string, processState) { return cmdline, state }
}

func TestInspectDatabaseRuntimeFindsLockHeldByRecycledPID(t *testing.T) {
	setupRuntimeDirs(t)
	socket := MySQLSocketPathForInstance("8.0", "affiliate")
	writeRuntimeFile(t, socket+".lock", "1519\n")
	writeRuntimeFile(t, MySQLPIDFileForInstance("8.0", "affiliate"), "1519\n")
	writeRuntimeFile(t, socket, "")

	state, err := inspectDatabaseRuntime("mysql", "8.0", "affiliate", fixedLookup("/usr/bin/php-fpm8.5: master process", processAlive))
	if err != nil {
		t.Fatal(err)
	}

	if state.OwnerPID != 0 {
		t.Fatalf("owner = %d, want none", state.OwnerPID)
	}
	if !state.Blocked() {
		t.Fatal("state should be blocked by stale files")
	}
	if len(state.Stale) != 3 {
		t.Fatalf("stale = %v, want lock, pid, and socket", state.Stale)
	}
	if !strings.Contains(state.Stale[0].String(), "reused by php-fpm8.5") {
		t.Fatalf("stale reason = %q", state.Stale[0].String())
	}
}

func TestInspectDatabaseRuntimeKeepsFilesOfLiveServer(t *testing.T) {
	setupRuntimeDirs(t)
	socket := MySQLSocketPathForInstance("8.0", "")
	writeRuntimeFile(t, socket+".lock", "4242\n")
	writeRuntimeFile(t, socket, "")

	cmdline := "/opt/mysqld --defaults-file=" + MySQLConfigPath("8.0")
	state, err := inspectDatabaseRuntime("mysql", "8.0", "", fixedLookup(cmdline, processAlive))
	if err != nil {
		t.Fatal(err)
	}

	if state.OwnerPID != 4242 {
		t.Fatalf("owner = %d, want 4242", state.OwnerPID)
	}
	if state.Blocked() || len(state.Stale) != 0 {
		t.Fatalf("stale = %v, want none", state.Stale)
	}
}

func TestInspectDatabaseRuntimeReportsDeadProcess(t *testing.T) {
	setupRuntimeDirs(t)
	writeRuntimeFile(t, MySQLSocketPathForInstance("8.0", "")+".lock", "4242\n")

	state, err := inspectDatabaseRuntime("mysql", "8.0", "", fixedLookup("", processGone))
	if err != nil {
		t.Fatal(err)
	}

	if len(state.Stale) != 1 || !strings.Contains(state.Stale[0].String(), "pid 4242 is gone") {
		t.Fatalf("stale = %v", state.Stale)
	}
}

func TestInspectDatabaseRuntimeLeavesUnidentifiablePIDAlone(t *testing.T) {
	setupRuntimeDirs(t)
	writeRuntimeFile(t, MySQLSocketPathForInstance("8.0", "")+".lock", "4242\n")

	state, err := inspectDatabaseRuntime("mysql", "8.0", "", fixedLookup("", processUnknown))
	if err != nil {
		t.Fatal(err)
	}

	if len(state.Unverified) != 1 || len(state.Stale) != 0 {
		t.Fatalf("unverified = %v, stale = %v", state.Unverified, state.Stale)
	}
	removed, err := state.Clear()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want nothing while a pid is unidentified", removed)
	}
}

func TestClearStaleDatabaseRuntimeRemovesStaleFiles(t *testing.T) {
	setupRuntimeDirs(t)
	socket := MariaDBSocketPathForInstance("11.4", "")
	writeRuntimeFile(t, socket+".lock", strconv.Itoa(unallocatablePID(t))+"\n")
	writeRuntimeFile(t, socket, "")

	removed, err := ClearStaleDatabaseRuntime("mariadb", "11.4", "")
	if err != nil {
		t.Fatal(err)
	}

	if len(removed) != 2 {
		t.Fatalf("removed = %v, want the lock and the socket", removed)
	}
	for _, path := range []string{socket, socket + ".lock"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists", path)
		}
	}
}

func TestClearStaleDatabaseRuntimeIgnoresMissingFiles(t *testing.T) {
	setupRuntimeDirs(t)

	removed, err := ClearStaleDatabaseRuntime("postgres", "16", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none", removed)
	}
}

func TestInspectDatabaseRuntimeRejectsUnknownEngine(t *testing.T) {
	setupRuntimeDirs(t)

	if _, err := InspectDatabaseRuntime("sqlite", "3", ""); err == nil {
		t.Fatal("expected an error for an unsupported engine")
	}
}

func TestDatabaseUnitsGuardAgainstStaleLocksAndRestartLoops(t *testing.T) {
	setupRuntimeDirs(t)
	routa := filepath.Join(t.TempDir(), "routa")
	writeRuntimeFile(t, routa, "")
	routaExecutable = func() (string, error) { return routa, nil }
	t.Cleanup(func() { routaExecutable = os.Executable })

	units := map[string]func() (string, error){
		"mysql":    func() (string, error) { return RenderMySQLUnitForInstance("8.0", "affiliate", "/opt/mysqld") },
		"mariadb":  func() (string, error) { return RenderMariaDBUnitForInstance("11.4", "", "/opt/mariadbd") },
		"postgres": func() (string, error) { return RenderPostgresUnitForInstance("16", "", "/opt/postgres") },
	}
	wantRepair := map[string]string{
		"mysql":    "ExecStartPre=-" + routa + " db repair mysql 8.0 affiliate",
		"mariadb":  "ExecStartPre=-" + routa + " db repair mariadb 11.4",
		"postgres": "ExecStartPre=-" + routa + " db repair postgres 16",
	}

	for engine, render := range units {
		unit, err := render()
		if err != nil {
			t.Fatalf("%s: %v", engine, err)
		}
		for _, want := range []string{wantRepair[engine], "StartLimitIntervalSec=60", "StartLimitBurst=5"} {
			if !strings.Contains(unit, want) {
				t.Fatalf("%s unit missing %q:\n%s", engine, want, unit)
			}
		}
	}
}

func TestDatabaseRepairExecOmittedWithoutAbsoluteRoutaPath(t *testing.T) {
	routaExecutable = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { routaExecutable = os.Executable })
	t.Setenv("PATH", t.TempDir())

	if got := databaseRepairExec("mysql", "8.0", ""); got != "" {
		t.Fatalf("repair exec = %q, want empty when routa cannot be located", got)
	}
}

// unallocatablePID returns a pid the kernel will never hand out, so the real
// /proc lookup reports it as gone.
func unallocatablePID(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile("/proc/sys/kernel/pid_max")
	if err != nil {
		t.Skipf("cannot read pid_max: %v", err)
	}
	max, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Skipf("unexpected pid_max %q", data)
	}
	return max
}
