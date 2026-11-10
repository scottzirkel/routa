package services

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// DatabaseTools are the command-line client and dump tool for one engine.
type DatabaseTools struct {
	Client string
	Dump   string
}

// FindDatabaseTools returns the client and dump tool to use against a
// database server. Tools shipped with a routa-managed version come first so
// the dump format matches the server; otherwise the ones on PATH are used.
// engine is mysql, mariadb, or postgres. version may be empty when the
// server is not one routa manages.
func FindDatabaseTools(engine, version string) (DatabaseTools, error) {
	var dirs []string
	var clients, dumps []string
	switch engine {
	case "mysql":
		if version != "" {
			for _, v := range []string{version, majorVersionLabel(version)} {
				dirs = append(dirs, filepath.Dir(managedBinaryPath("mysql", v, "mysql")))
			}
		}
		clients, dumps = []string{"mysql", "mariadb"}, []string{"mysqldump", "mariadb-dump"}
	case "mariadb":
		clients, dumps = []string{"mariadb", "mysql"}, []string{"mariadb-dump", "mysqldump"}
	case "postgres":
		if version != "" {
			if server, err := FindPostgresBinary(version); err == nil {
				dirs = append(dirs, filepath.Dir(server))
			}
		}
		clients, dumps = []string{"psql"}, []string{"pg_dump"}
	default:
		return DatabaseTools{}, fmt.Errorf("unsupported database engine %q", engine)
	}

	// Pick each tool on its own: a managed client can fail to start (the
	// MySQL 8.0 client needs libncurses.so.5) while its dump tool works.
	client, err := firstRunnable(dirs, clients)
	if err != nil {
		return DatabaseTools{}, err
	}
	dump, err := firstRunnable(dirs, dumps)
	if err != nil {
		return DatabaseTools{}, err
	}
	return DatabaseTools{Client: client, Dump: dump}, nil
}

// firstRunnable returns the first candidate that runs `--version`: the
// preferred name in each managed dir, then each name on PATH.
func firstRunnable(dirs, names []string) (string, error) {
	var candidates []string
	for _, dir := range dirs {
		candidates = append(candidates, filepath.Join(dir, names[0]))
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, path)
		}
	}
	for _, c := range candidates {
		if executable(c) && exec.Command(c, "--version").Run() == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("none of %v found or able to run", names)
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}
