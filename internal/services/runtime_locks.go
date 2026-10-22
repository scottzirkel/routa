package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/scottzirkel/routa/internal/paths"
)

// Database servers guard their unix socket with a lock file holding the pid of
// the server that owns it. mysqld and mariadbd refuse to start when that pid is
// alive, so a server killed without cleanup can lock itself out of its own
// runtime directory: the pid gets recycled by an unrelated process, the lock
// looks live forever, and systemd restarts the unit until it hits its start
// limit. Checking the pid's identity — not just its existence — is what tells a
// stale lock apart from a real conflict.

// StaleRuntimeFile is a socket, lock, or pid file left behind by a database
// server that is no longer running.
type StaleRuntimeFile struct {
	Path   string
	PID    int
	Reason string
}

func (f StaleRuntimeFile) String() string {
	if f.PID > 0 {
		return fmt.Sprintf("%s (pid %d %s)", f.Path, f.PID, f.Reason)
	}
	return fmt.Sprintf("%s (%s)", f.Path, f.Reason)
}

// DatabaseRuntimeState reports who owns an instance's runtime files.
type DatabaseRuntimeState struct {
	// OwnerPID is the live server holding these files, or 0 when none does.
	OwnerPID int
	// Stale lists files whose recorded pid is dead or belongs to some other
	// process. They are safe to remove.
	Stale []StaleRuntimeFile
	// Unverified lists files whose pid is alive but could not be identified.
	// Removing those could pull the socket out from under a running server.
	Unverified []StaleRuntimeFile
}

// Blocked reports whether stale files would keep a server from starting.
func (s DatabaseRuntimeState) Blocked() bool {
	return s.OwnerPID == 0 && len(s.Stale) > 0
}

type processState int

const (
	processGone processState = iota
	processUnknown
	processAlive
)

type processLookup func(pid int) (cmdline string, state processState)

type runtimeFile struct {
	path   string
	socket bool
}

// InspectDatabaseRuntime classifies the runtime files of one database instance.
func InspectDatabaseRuntime(engine, version, instance string) (DatabaseRuntimeState, error) {
	return inspectDatabaseRuntime(engine, version, instance, procCmdline)
}

// ClearStaleDatabaseRuntime removes the stale runtime files of one database
// instance and returns what it removed.
func ClearStaleDatabaseRuntime(engine, version, instance string) ([]StaleRuntimeFile, error) {
	state, err := InspectDatabaseRuntime(engine, version, instance)
	if err != nil {
		return nil, err
	}
	return state.Clear()
}

// Clear removes the stale files. It removes nothing when a live server owns
// them, or when any pid could not be identified.
func (s DatabaseRuntimeState) Clear() ([]StaleRuntimeFile, error) {
	if !s.Blocked() || len(s.Unverified) > 0 {
		return nil, nil
	}
	var removed []StaleRuntimeFile
	for _, file := range s.Stale {
		if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("remove %s: %w", file.Path, err)
		}
		removed = append(removed, file)
	}
	return removed, nil
}

func inspectDatabaseRuntime(engine, version, instance string, lookup processLookup) (DatabaseRuntimeState, error) {
	files, owner, err := databaseRuntimeFiles(engine, version, instance)
	if err != nil {
		return DatabaseRuntimeState{}, err
	}
	state := DatabaseRuntimeState{}
	var sockets []runtimeFile
	for _, file := range files {
		if file.socket {
			sockets = append(sockets, file)
			continue
		}
		pid, ok, err := readPIDFile(file.path)
		if err != nil {
			return DatabaseRuntimeState{}, err
		}
		if !ok {
			continue
		}
		if pid <= 0 {
			state.Stale = append(state.Stale, StaleRuntimeFile{Path: file.path, Reason: "holds no usable pid"})
			continue
		}
		cmdline, process := lookup(pid)
		switch {
		case process == processGone:
			state.Stale = append(state.Stale, StaleRuntimeFile{Path: file.path, PID: pid, Reason: "is gone"})
		case process == processUnknown:
			state.Unverified = append(state.Unverified, StaleRuntimeFile{Path: file.path, PID: pid, Reason: "could not be identified"})
		case owner(cmdline):
			state.OwnerPID = pid
		default:
			state.Stale = append(state.Stale, StaleRuntimeFile{Path: file.path, PID: pid, Reason: "was reused by " + processLabel(cmdline)})
		}
	}
	// A socket carries no pid of its own, so it is only stale once a lock or
	// pid file has proven the server behind it is gone.
	if state.OwnerPID == 0 && len(state.Unverified) == 0 && len(state.Stale) > 0 {
		for _, socket := range sockets {
			if _, err := os.Lstat(socket.path); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return DatabaseRuntimeState{}, err
			}
			state.Stale = append(state.Stale, StaleRuntimeFile{Path: socket.path, Reason: "belongs to a server that is gone"})
		}
	}
	return state, nil
}

// databaseRuntimeFiles lists the runtime files an instance claims along with a
// predicate that recognizes the server process those files belong to.
func databaseRuntimeFiles(engine, version, instance string) ([]runtimeFile, func(string) bool, error) {
	switch engine {
	case "mysql":
		if err := validateDatabaseInstanceTarget(ValidateMySQLVersion, ValidateMySQLInstance, version, instance); err != nil {
			return nil, nil, err
		}
		socket := MySQLSocketPathForInstance(version, instance)
		return socketLockRuntimeFiles(socket, MySQLPIDFileForInstance(version, instance)),
			commandOwner(MySQLConfigPathForInstance(version, instance), MySQLDataDirForInstance(version, instance)), nil
	case "mariadb":
		if err := validateDatabaseInstanceTarget(ValidateMariaDBVersion, ValidateMariaDBInstance, version, instance); err != nil {
			return nil, nil, err
		}
		socket := MariaDBSocketPathForInstance(version, instance)
		return socketLockRuntimeFiles(socket, MariaDBPIDFileForInstance(version, instance)),
			commandOwner(MariaDBConfigPathForInstance(version, instance), MariaDBDataDirForInstance(version, instance)), nil
	case "postgres":
		if err := validateDatabaseInstanceTarget(ValidatePostgresVersion, ValidatePostgresInstance, version, instance); err != nil {
			return nil, nil, err
		}
		dataDir := PostgresDataDirForInstance(version, instance)
		port, err := databaseConfiguredPort(PostgresConfigPathForInstance(version, instance), "Postgres", PostgresDefaultPort)
		if err != nil {
			return nil, nil, err
		}
		socket := filepath.Join(paths.RunDir(), ".s.PGSQL."+port)
		files := []runtimeFile{
			{path: PostgresPIDFileForInstance(version, instance)},
			{path: filepath.Join(dataDir, "postmaster.pid")},
			{path: socket + ".lock"},
			{path: socket, socket: true},
		}
		return files, commandOwner(PostgresConfigPathForInstance(version, instance), dataDir), nil
	}
	return nil, nil, fmt.Errorf("unsupported database %q (supported: mariadb, mysql, postgres)", engine)
}

func socketLockRuntimeFiles(socket, pidFile string) []runtimeFile {
	return []runtimeFile{
		{path: pidFile},
		{path: socket + ".lock"},
		{path: socket, socket: true},
	}
}

func validateDatabaseInstanceTarget(validateVersion, validateInstance func(string) error, version, instance string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	return validateInstance(instance)
}

// commandOwner recognizes a server started for this instance by the config file
// or data directory on its command line — the same paths routa bakes into the
// unit, so a recycled pid running anything else never matches.
func commandOwner(markers ...string) func(string) bool {
	return func(cmdline string) bool {
		for _, path := range markers {
			if path != "" && strings.Contains(cmdline, path) {
				return true
			}
		}
		return false
	}
}

func readPIDFile(path string) (int, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	field := strings.TrimSpace(string(data))
	if idx := strings.IndexAny(field, " \t\n\r"); idx >= 0 {
		field = field[:idx]
	}
	pid, err := strconv.Atoi(field)
	if err != nil {
		return 0, true, nil
	}
	return pid, true, nil
}

func procCmdline(pid int) (string, processState) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	data, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", processGone
		}
		return "", processUnknown
	}
	cmdline := strings.TrimSpace(strings.ReplaceAll(string(data), "\x00", " "))
	if cmdline == "" {
		// Kernel threads and zombies have an empty cmdline; fall back to the
		// process name rather than calling an unidentified live pid stale.
		comm, err := os.ReadFile(filepath.Join(dir, "comm"))
		if err != nil {
			if os.IsNotExist(err) {
				return "", processGone
			}
			return "", processUnknown
		}
		cmdline = strings.TrimSpace(string(comm))
		if cmdline == "" {
			return "", processUnknown
		}
	}
	return cmdline, processAlive
}

func processLabel(cmdline string) string {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return "another process"
	}
	return filepath.Base(fields[0])
}
