package workspace

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/scottzirkel/routa/internal/services"
)

// Connection is one database connection as a Laravel app resolves it.
type Connection struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
}

const connectionsMarker = "@@routa-connections@@"

// LaravelConnectionsScript boots a Laravel app and prints its resolved
// database connections after a marker. Run it with `php -r` from the app's
// directory and APP_ENV=testing to get what its tests connect to, including
// defaults from config/database.php that no .env file states.
const LaravelConnectionsScript = `error_reporting(0);
require 'vendor/autoload.php';
$app = require 'bootstrap/app.php';
$app->make(Illuminate\Contracts\Console\Kernel::class)->bootstrap();
$out = [];
foreach ((array) config('database.connections') as $name => $c) {
    if (! is_array($c)) { continue; }
    $row = ['name' => (string) $name];
    foreach (['driver', 'host', 'port', 'database', 'username', 'password'] as $k) {
        $v = $c[$k] ?? '';
        $row[$k] = is_scalar($v) ? (string) $v : '';
    }
    $out[] = $row;
}
echo "\n` + connectionsMarker + `", json_encode($out), "\n";`

// ParseConnections reads LaravelConnectionsScript's output.
func ParseConnections(output string) ([]Connection, error) {
	i := strings.LastIndex(output, connectionsMarker)
	if i < 0 {
		return nil, fmt.Errorf("the app did not report its database connections")
	}
	var conns []Connection
	if err := json.Unmarshal([]byte(strings.TrimSpace(output[i+len(connectionsMarker):])), &conns); err != nil {
		return nil, err
	}
	return conns, nil
}

// TestServer is a database server the app's tests use, with the main
// checkout's test database names on it, such as testing.
type TestServer struct {
	Via       Database // connection details, without a database name
	Templates []string // the main checkout's test database names
}

// TestServers picks the servers the app's tests actually use: connections
// whose database is one of the main checkout's test database names, from its
// .env.testing. Every other connection, such as a read-only or production
// one, is ignored. So are servers that are not on this machine, a host left
// empty, and servers on a service's standard port, which belong to a system
// service rather than routa. Only MySQL-protocol servers count.
func TestServers(conns []Connection, templates []string) []TestServer {
	byServer := map[string]*TestServer{}
	var order []string
	for _, c := range conns {
		switch c.Driver {
		case "mysql", "mariadb", "singlestore":
		default:
			continue
		}
		if c.Host == "" || !localHost(c.Host) || services.StandardPortOwner(c.Port) != "" || !contains(templates, c.Database) {
			continue
		}
		host := c.Host
		if host == "localhost" {
			host = "127.0.0.1"
		}
		key := host + ":" + c.Port
		s, ok := byServer[key]
		if !ok {
			s = &TestServer{Via: Database{Connection: "mysql", Host: host, Port: c.Port, User: c.Username, Password: c.Password}}
			byServer[key] = s
			order = append(order, key)
		}
		if !contains(s.Templates, c.Database) {
			s.Templates = append(s.Templates, c.Database)
		}
	}
	out := make([]TestServer, 0, len(order))
	for _, key := range order {
		sort.Strings(byServer[key].Templates)
		out = append(out, *byServer[key])
	}
	return out
}

// HostsTestSet reports whether a server already holds template or one of its
// _test_N copies: evidence that it is the server the tests run on.
func HostsTestSet(existing []string, template string) bool {
	for _, db := range existing {
		if testCopyRE.ReplaceAllString(db, "") == template {
			return true
		}
	}
	return false
}

// BusyDatabases lists the databases that have an open connection right now,
// such as a test run in progress, so cleanup can leave them alone.
func BusyDatabases(d Database, tools services.DatabaseTools) (map[string]bool, error) {
	busy := map[string]bool{}
	err := withMySQLDefaults(d, func(defaults string) error {
		rows, err := mysqlQuery(tools, defaults, "SELECT DISTINCT DB FROM information_schema.PROCESSLIST WHERE DB IS NOT NULL")
		for _, row := range rows {
			busy[row[0]] = true
		}
		return err
	})
	return busy, err
}

var (
	testCopyRE = regexp.MustCompile(`_test_\d+$`)
	suffixRE   = regexp.MustCompile(`^[a-z0-9_]+$`)
)

// StaleTestDatabases picks the test databases that worktrees left behind:
// <template>_<suffix>, where template is one of the main checkout's test
// database names, and its _test_N parallel copies, when no current worktree
// has that suffix and no checkout's .env files name the database. The main
// checkout's own databases are never picked.
func StaleTestDatabases(existing, templates []string, liveSuffixes, liveNames map[string]bool) []string {
	var stale []string
	for _, db := range existing {
		base := testCopyRE.ReplaceAllString(db, "")
		if liveNames[base] || contains(templates, base) {
			continue
		}
		matched, live := false, false
		for _, t := range templates {
			suffix, ok := strings.CutPrefix(base, t+"_")
			if !ok || !suffixRE.MatchString(suffix) {
				continue
			}
			matched = true
			if liveSuffixes[suffix] {
				live = true
			}
		}
		if matched && !live {
			stale = append(stale, db)
		}
	}
	sort.Strings(stale)
	return stale
}

// GroupTestSets counts databases per base name, folding _test_N copies into
// their base, for reporting.
func GroupTestSets(dbs []string) map[string]int {
	sets := map[string]int{}
	for _, db := range dbs {
		sets[testCopyRE.ReplaceAllString(db, "")]++
	}
	return sets
}

// ListDatabases returns the databases on a MySQL-protocol server.
func ListDatabases(d Database, tools services.DatabaseTools) ([]string, error) {
	var names []string
	err := withMySQLDefaults(d, func(defaults string) error {
		rows, err := mysqlQuery(tools, defaults, "SHOW DATABASES")
		for _, row := range rows {
			names = append(names, row[0])
		}
		return err
	})
	return names, err
}

// DropDatabases drops each named database on a MySQL-protocol server.
func DropDatabases(d Database, tools services.DatabaseTools, names []string) error {
	if len(names) == 0 {
		return nil
	}
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "DROP DATABASE IF EXISTS %s;\n", mysqlIdent(name))
	}
	return withMySQLDefaults(d, func(defaults string) error {
		return runMySQLScript(tools, defaults, b.String())
	})
}

func localHost(host string) bool {
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
