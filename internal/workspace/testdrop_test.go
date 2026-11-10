package workspace

import (
	"reflect"
	"strings"
	"testing"
)

func TestTableColumnsAndCopyScript(t *testing.T) {
	tables := tableColumns([][]string{{"a", "id"}, {"a", "name"}, {"b`x", "id"}})
	script := copyRowsScript("aff_main", "aff_5194", tables)
	for _, want := range []string{
		"SET SESSION foreign_key_checks = 0;",
		"SET SESSION sql_mode = 'NO_AUTO_VALUE_ON_ZERO';",
		"INSERT INTO `aff_5194`.`a` (`id`, `name`) SELECT `id`, `name` FROM `aff_main`.`a`;",
		"INSERT INTO `aff_5194`.`b``x` (`id`) SELECT `id` FROM `aff_main`.`b``x`;",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script is missing %q:\n%s", want, script)
		}
	}
	if got := countMismatches([][]string{{"a", "3", "3"}, {"b", "5", "4"}}); !reflect.DeepEqual(got, []string{"b (5 → 4)"}) {
		t.Fatalf("mismatches = %#v", got)
	}
}

func TestParseConnectionsIgnoresOutputBeforeTheMarker(t *testing.T) {
	out := "Deprecated: something\n" + connectionsMarker + `[{"name":"mysql","driver":"mysql","host":"127.0.0.1","port":"3310","database":"testing_5194","username":"u","password":"p"}]` + "\n"
	conns, err := ParseConnections(out)
	if err != nil || len(conns) != 1 || conns[0].Database != "testing_5194" || conns[0].Password != "p" {
		t.Fatalf("conns = %#v, %v", conns, err)
	}
	if _, err := ParseConnections("Fatal error"); err == nil {
		t.Fatal("expected an error without the marker")
	}
}

// affiliateTestConns is what the affiliate app reports with APP_ENV=testing,
// including its read-only and production connections on port 3306.
var affiliateTestConns = []Connection{
	{Name: "mysql", Driver: "mysql", Host: "127.0.0.1", Port: "3310", Database: "testing", Username: "u"},
	{Name: "mariadb", Driver: "mariadb", Host: "127.0.0.1", Port: "3310", Database: "testing"},
	{Name: "mysql-metrics", Driver: "mysql", Host: "localhost", Port: "3310", Database: "testing"},
	{Name: "mysql-app-readonly", Driver: "mysql", Host: "", Port: "3306", Database: ""},
	{Name: "mysql-staging-readonly", Driver: "mysql", Host: "", Port: "3306", Database: "staging"},
	{Name: "singlestore", Driver: "singlestore", Host: "127.0.0.1", Port: "3307", Database: "affiliate_testing", Username: "root"},
	{Name: "singlestore-prod", Driver: "mysql", Host: "127.0.0.1", Port: "3306", Database: "affiliate"},
	{Name: "singlestore-readonly", Driver: "mysql", Host: "", Port: "3306", Database: "affiliate"},
	{Name: "laravel-brain", Driver: "mysql", Host: "127.0.0.1", Port: "3306", Database: ""},
	{Name: "remote", Driver: "mysql", Host: "staging.example.com", Port: "3310", Database: "testing"},
	{Name: "pgsql", Driver: "pgsql", Host: "127.0.0.1", Port: "5433", Database: "testing"},
}

func TestTestServersOnlyPicksTheServersTestsUse(t *testing.T) {
	got := TestServers(affiliateTestConns, []string{"testing", "affiliate_testing"})
	if len(got) != 2 {
		t.Fatalf("servers = %#v", got)
	}
	if got[0].Via.Port != "3310" || !reflect.DeepEqual(got[0].Templates, []string{"testing"}) {
		t.Fatalf("mysql server = %#v", got[0])
	}
	if got[1].Via.Port != "3307" || !reflect.DeepEqual(got[1].Templates, []string{"affiliate_testing"}) || got[1].Via.User != "root" {
		t.Fatalf("singlestore server = %#v", got[1])
	}
}

func TestTestServersNeverPicksAStandardPort(t *testing.T) {
	// Even when a test database name matches, port 3306 belongs to a system
	// server routa does not run.
	conns := []Connection{{Name: "mysql", Driver: "mysql", Host: "127.0.0.1", Port: "3306", Database: "testing"}}
	if got := TestServers(conns, []string{"testing"}); len(got) != 0 {
		t.Fatalf("servers = %#v", got)
	}
}

func TestHostsTestSet(t *testing.T) {
	if !HostsTestSet([]string{"aff_main", "testing"}, "testing") || !HostsTestSet([]string{"testing_test_3"}, "testing") {
		t.Fatal("a server with the test database or its copies hosts the set")
	}
	if HostsTestSet([]string{"testing_5194", "testing_5194_test_1", "affiliate"}, "testing") {
		t.Fatal("a server with only worktree sets does not prove it is the main test server")
	}
}

func TestStaleTestDatabases(t *testing.T) {
	existing := []string{
		"testing", "testing_test_1", // main's own
		"testing_5194", "testing_5194_test_1", // live worktree 5194
		"testing_4764", "testing_4764_test_1", "testing_4764_test_12", // worktree gone
		"testing_51945",                     // gone too: 51945 is not 5194
		"testing_ops", "testing_ops_test_1", // gone by suffix, but a checkout's .env.testing names it
		"testing_x_test_3",        // a copy whose base is already gone
		"ops_testing", "aff_main", // other naming, never touched
		"testing_Bad-Name", // not a worktree suffix
	}
	live := map[string]bool{"5194": true}
	names := map[string]bool{"testing_ops": true}
	got := StaleTestDatabases(existing, []string{"testing"}, live, names)
	want := []string{"testing_4764", "testing_4764_test_1", "testing_4764_test_12", "testing_51945", "testing_x_test_3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stale = %#v\nwant %#v", got, want)
	}
	if sets := GroupTestSets(got); sets["testing_4764"] != 3 || sets["testing_x"] != 1 {
		t.Fatalf("sets = %#v", sets)
	}
}

func TestStaleTestDatabasesWithOverlappingTemplates(t *testing.T) {
	// testing_metrics_5194 belongs to template testing_metrics and live
	// worktree 5194, even though it also starts with testing_.
	got := StaleTestDatabases([]string{"testing_metrics", "testing_metrics_5194", "testing_metrics_old"},
		[]string{"testing", "testing_metrics"}, map[string]bool{"5194": true}, nil)
	if !reflect.DeepEqual(got, []string{"testing_metrics_old"}) {
		t.Fatalf("stale = %#v", got)
	}
}
