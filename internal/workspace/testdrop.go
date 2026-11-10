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

// TestServer is a database server and the workspace test databases on it.
type TestServer struct {
	Via   Database // connection details, without a database name
	Bases []string // base test database names with the workspace suffix
}

// WorkspaceTestServers picks the connections whose database ends with the
// workspace suffix, the names PlanTestEnv gave them, grouped by server. Only
// MySQL-protocol servers on this machine count, so cleanup can never reach a
// remote database or one that is not the workspace's.
func WorkspaceTestServers(conns []Connection, suffix string) []TestServer {
	suffix = "_" + strings.ReplaceAll(suffix, "-", "_")
	byServer := map[string]*TestServer{}
	var order []string
	for _, c := range conns {
		switch c.Driver {
		case "mysql", "mariadb", "singlestore":
		default:
			continue
		}
		if !localHost(c.Host) || !strings.HasSuffix(c.Database, suffix) || c.Database == suffix {
			continue
		}
		host := c.Host
		if host == "" || host == "localhost" {
			host = "127.0.0.1"
		}
		key := host + ":" + c.Port
		s, ok := byServer[key]
		if !ok {
			s = &TestServer{Via: Database{Connection: "mysql", Host: host, Port: c.Port, User: c.Username, Password: c.Password}}
			byServer[key] = s
			order = append(order, key)
		}
		if !contains(s.Bases, c.Database) {
			s.Bases = append(s.Bases, c.Database)
		}
	}
	out := make([]TestServer, 0, len(order))
	for _, key := range order {
		sort.Strings(byServer[key].Bases)
		out = append(out, *byServer[key])
	}
	return out
}

// TestDatabasesToDrop picks each base and its parallel copies (base_test_N)
// from the databases that exist, matching names exactly.
func TestDatabasesToDrop(bases, existing []string) []string {
	var drop []string
	for _, db := range existing {
		for _, base := range bases {
			if db == base || regexp.MustCompile(`^`+regexp.QuoteMeta(base)+`_test_\d+$`).MatchString(db) {
				drop = append(drop, db)
				break
			}
		}
	}
	sort.Strings(drop)
	return drop
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
