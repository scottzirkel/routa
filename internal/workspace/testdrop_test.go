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

func TestWorkspaceTestServersOnlyPicksLocalSuffixedDatabases(t *testing.T) {
	conns := []Connection{
		{Name: "mysql", Driver: "mysql", Host: "127.0.0.1", Port: "3310", Database: "testing_5194", Username: "u"},
		{Name: "mysql-metrics", Driver: "mysql", Host: "localhost", Port: "3310", Database: "testing_5194", Username: "u"},
		{Name: "singlestore", Driver: "singlestore", Host: "127.0.0.1", Port: "3307", Database: "affiliate_testing_5194", Username: "root"},
		{Name: "staging", Driver: "mysql", Host: "staging.example.com", Port: "3306", Database: "staging_5194"},
		{Name: "main", Driver: "mysql", Host: "127.0.0.1", Port: "3310", Database: "testing"},
		{Name: "pgsql", Driver: "pgsql", Host: "127.0.0.1", Port: "5432", Database: "testing_5194"},
		{Name: "sqlite", Driver: "sqlite", Database: "database/testing_5194"},
	}
	got := WorkspaceTestServers(conns, "5194")
	if len(got) != 2 {
		t.Fatalf("servers = %#v", got)
	}
	if got[0].Via.Port != "3310" || !reflect.DeepEqual(got[0].Bases, []string{"testing_5194"}) {
		t.Fatalf("mysql server = %#v", got[0])
	}
	if got[1].Via.Port != "3307" || !reflect.DeepEqual(got[1].Bases, []string{"affiliate_testing_5194"}) || got[1].Via.User != "root" {
		t.Fatalf("singlestore server = %#v", got[1])
	}
}

func TestTestDatabasesToDropMatchesExactly(t *testing.T) {
	existing := []string{
		"testing", "testing_test_1",
		"testing_5194", "testing_5194_test_1", "testing_5194_test_12",
		"testing_51945", "testing_51945_test_1", "testing_5194_test_x", "testing_5194_copy",
		"aff_5194",
	}
	got := TestDatabasesToDrop([]string{"testing_5194"}, existing)
	want := []string{"testing_5194", "testing_5194_test_1", "testing_5194_test_12"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("drop = %#v\nwant %#v", got, want)
	}
}
