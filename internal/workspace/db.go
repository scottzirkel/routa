package workspace

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/scottzirkel/routa/internal/services"
)

// Database is the connection a Laravel-style .env describes.
type Database struct {
	Connection string // mysql, mariadb, pgsql, sqlite
	Host       string
	Port       string
	Name       string
	User       string
	Password   string
}

func DatabaseFromEnv(env map[string]string) Database {
	d := Database{
		Connection: env["DB_CONNECTION"],
		Host:       env["DB_HOST"],
		Port:       env["DB_PORT"],
		Name:       env["DB_DATABASE"],
		User:       env["DB_USERNAME"],
		Password:   env["DB_PASSWORD"],
	}
	// "localhost" makes MySQL clients use a socket; routa's servers listen on TCP.
	if d.Host == "" || d.Host == "localhost" {
		d.Host = "127.0.0.1"
	}
	if d.Port == "" {
		switch d.Connection {
		case "pgsql":
			d.Port = "5432"
		case "mysql", "mariadb":
			d.Port = "3306"
		}
	}
	return d
}

// Engine names the server family: mysql, mariadb, postgres, sqlite, or "".
func (d Database) Engine() string {
	switch d.Connection {
	case "mysql", "mariadb", "sqlite":
		return d.Connection
	case "pgsql":
		return "postgres"
	}
	return ""
}

// CopyName is the database a workspace gets when it clones or empties the
// parent's database: the parent's name plus the workspace suffix.
func CopyName(parent, suffix string) (string, error) {
	name := parent + "_" + strings.ReplaceAll(suffix, "-", "_")
	if len(name) > 63 {
		return "", fmt.Errorf("database name %s is longer than 63 characters; use a shorter workspace name", name)
	}
	return name, nil
}

// CreateDatabase creates an empty database next to d's.
func CreateDatabase(d Database, tools services.DatabaseTools, name string) error {
	switch d.Engine() {
	case "mysql", "mariadb":
		return withMySQLDefaults(d, func(defaults string) error {
			return run(exec.Command(tools.Client, defaults, "-e", "CREATE DATABASE "+mysqlIdent(name)))
		})
	case "postgres":
		return run(postgresCommand(d, tools.Client, "-v", "ON_ERROR_STOP=1", "-c", "CREATE DATABASE "+postgresIdent(name)))
	}
	return fmt.Errorf("creating %s databases is not supported", d.Connection)
}

// CloneDatabase creates name and fills it with a dump of d's database.
func CloneDatabase(d Database, tools services.DatabaseTools, name string) error {
	if err := CreateDatabase(d, tools, name); err != nil {
		return err
	}
	switch d.Engine() {
	case "mysql", "mariadb":
		return withMySQLDefaults(d, func(defaults string) error {
			dump := exec.Command(tools.Dump, defaults, "--single-transaction", "--routines", "--triggers", "--events", "--no-tablespaces", d.Name)
			restore := exec.Command(tools.Client, defaults, name)
			return pipe(dump, restore)
		})
	case "postgres":
		dump := postgresCommand(d, tools.Dump, "--no-owner", "--no-privileges")
		restore := postgresCommand(Database{Host: d.Host, Port: d.Port, User: d.User, Password: d.Password, Name: name}, tools.Client, "-q", "-v", "ON_ERROR_STOP=1")
		return pipe(dump, restore)
	}
	return fmt.Errorf("cloning %s databases is not supported", d.Connection)
}

// DropDatabase drops name, connecting through d's database.
func DropDatabase(d Database, tools services.DatabaseTools, name string) error {
	switch d.Engine() {
	case "mysql", "mariadb":
		return withMySQLDefaults(d, func(defaults string) error {
			return run(exec.Command(tools.Client, defaults, "-e", "DROP DATABASE IF EXISTS "+mysqlIdent(name)))
		})
	case "postgres":
		return run(postgresCommand(d, tools.Client, "-v", "ON_ERROR_STOP=1", "-c", "DROP DATABASE IF EXISTS "+postgresIdent(name)))
	}
	return fmt.Errorf("dropping %s databases is not supported", d.Connection)
}

// withMySQLDefaults passes credentials through a private option file, so the
// password never appears in the process list.
func withMySQLDefaults(d Database, fn func(defaultsFlag string) error) error {
	f, err := os.CreateTemp("", "routa-mysql-*.cnf")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	content := "[client]\n" +
		"host=" + mysqlOption(d.Host) + "\n" +
		"port=" + mysqlOption(d.Port) + "\n" +
		"protocol=tcp\n"
	if d.User != "" {
		content += "user=" + mysqlOption(d.User) + "\n"
	}
	if d.Password != "" {
		content += "password=" + mysqlOption(d.Password) + "\n"
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return fn("--defaults-extra-file=" + f.Name())
}

func mysqlOption(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

func mysqlIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func postgresIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func postgresCommand(d Database, bin string, args ...string) *exec.Cmd {
	full := []string{"-h", d.Host, "-p", d.Port}
	if d.User != "" {
		full = append(full, "-U", d.User)
	}
	full = append(full, args...)
	full = append(full, "--dbname="+d.Name)
	cmd := exec.Command(bin, full...)
	cmd.Env = os.Environ()
	if d.Password != "" {
		cmd.Env = append(cmd.Env, "PGPASSWORD="+d.Password)
	}
	return cmd
}

func run(cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", cmd.Args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// pipe streams dump's output into restore.
func pipe(dump, restore *exec.Cmd) error {
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	var dumpErr, restoreErr bytes.Buffer
	dump.Stdout, dump.Stderr = w, &dumpErr
	restore.Stdin, restore.Stderr = r, &restoreErr
	if err := restore.Start(); err != nil {
		r.Close()
		w.Close()
		return err
	}
	if err := dump.Start(); err != nil {
		r.Close()
		w.Close()
		_ = restore.Wait()
		return err
	}
	r.Close()
	w.Close()
	dErr := dump.Wait()
	rErr := restore.Wait()
	if dErr != nil {
		return fmt.Errorf("%s: %w: %s", dump.Args[0], dErr, strings.TrimSpace(dumpErr.String()))
	}
	if rErr != nil {
		return fmt.Errorf("%s: %w: %s", restore.Args[0], rErr, strings.TrimSpace(restoreErr.String()))
	}
	return nil
}

// ProjectDatabase is the database a checkout's .env points at, resolved far
// enough to tell whether two checkouts use the same one.
type ProjectDatabase struct {
	Database
	Kind    string // none, sqlite, server, url, or unknown
	File    string // absolute SQLite path
	URL     string // DB_URL or DATABASE_URL
	Problem string // why routa cannot tell, for Kind unknown
}

// ProjectDatabaseFor reads the database settings of the checkout at dir.
// With no DB_CONNECTION and no other DB_ settings, a Laravel 11+ app uses
// database/database.sqlite when it exists.
func ProjectDatabaseFor(dir string, env map[string]string) ProjectDatabase {
	p := ProjectDatabase{Database: DatabaseFromEnv(env)}
	if url := firstNonEmpty(env["DB_URL"], env["DATABASE_URL"]); url != "" {
		p.Kind, p.URL = "url", url
		return p
	}
	switch p.Connection {
	case "":
		for _, key := range []string{"DB_DATABASE", "DB_HOST", "DB_PORT", "DB_USERNAME"} {
			if env[key] != "" {
				p.Kind, p.Problem = "unknown", "DB_CONNECTION is not set in .env"
				return p
			}
		}
		file := filepath.Join(dir, "database", "database.sqlite")
		if _, err := os.Stat(file); err != nil {
			p.Kind = "none"
			return p
		}
		p.Connection, p.Kind, p.File = "sqlite", "sqlite", file
	case "sqlite":
		switch {
		case p.Name == ":memory:":
			p.Kind = "none"
		case p.Name == "":
			p.Kind, p.File = "sqlite", filepath.Join(dir, "database", "database.sqlite")
		case filepath.IsAbs(p.Name):
			p.Kind, p.File = "sqlite", filepath.Clean(p.Name)
		default:
			p.Kind, p.File = "sqlite", filepath.Join(dir, p.Name)
		}
	case "mysql", "mariadb", "pgsql":
		p.Kind = "server"
	default:
		p.Kind, p.Problem = "unknown", "DB_CONNECTION="+p.Connection+" is not supported"
	}
	return p
}

// SameServer reports whether both use the same database server.
func (p ProjectDatabase) SameServer(o ProjectDatabase) bool {
	return p.Kind == "server" && o.Kind == "server" && p.Engine() == o.Engine() && p.Host == o.Host && p.Port == o.Port
}

// Same reports whether both checkouts use the same database.
func (p ProjectDatabase) Same(o ProjectDatabase) bool {
	switch {
	case p.Kind == "url" && o.Kind == "url":
		return p.URL == o.URL
	case p.Kind == "sqlite" && o.Kind == "sqlite":
		return p.File == o.File
	default:
		return p.SameServer(o) && p.Name == o.Name
	}
}

// Label names the database for people: a database name, file, or URL.
func (p ProjectDatabase) Label() string {
	switch p.Kind {
	case "server":
		return p.Name
	case "sqlite":
		return p.File
	case "url":
		return "DB_URL"
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
