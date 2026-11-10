package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeLinkedWorktree lays out main/.git/worktrees/<id> and a worktree whose
// .git file points at it, the way git does.
func fakeLinkedWorktree(t *testing.T, main, wt string) {
	t.Helper()
	admin := filepath.Join(main, ".git", "worktrees", filepath.Base(wt))
	for _, dir := range []string{admin, wt} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+admin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const testPHPUnit = `<?xml version="1.0" encoding="UTF-8"?>
<phpunit>
    <php>
        <env name="APP_ENV" value="testing"/>
        <env name="DB_DATABASE" value="testing"/>
        <env name="DB_METRICS_DATABASE" value="testing"/>
        <!-- <env name="DB_OLD_DATABASE" value="old"/> -->
        <server name="DB_REPORTS_DATABASE" value="reports_testing"/>
    </php>
</phpunit>
`

func TestTestDatabasesForAddsTheWorktreeSuffix(t *testing.T) {
	base := t.TempDir()
	main, wt := filepath.Join(base, "affiliate"), filepath.Join(base, "affiliate-5194")
	fakeLinkedWorktree(t, main, wt)
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(wt, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("phpunit.xml", testPHPUnit)
	write(".env.testing", "DB_DATABASE=ignored_because_phpunit_wins\nDB_SINGLESTORE_DATABASE=affiliate_testing\nDB_SINGLESTORE_PROD_DATABASE=\"${DB_SINGLESTORE_DATABASE}\"\nDB_SQLITE_DATABASE=:memory:\n")

	sub := filepath.Join(wt, "tests", "Feature")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := TestDatabasesFor(sub)
	if !ok {
		t.Fatal("expected test databases")
	}
	want := map[string]string{
		"DB_DATABASE":             "testing_5194",
		"DB_METRICS_DATABASE":     "testing_5194",
		"DB_REPORTS_DATABASE":     "reports_testing_5194",
		"DB_SINGLESTORE_DATABASE": "affiliate_testing_5194",
	}
	if !reflect.DeepEqual(got.Env, want) {
		t.Fatalf("env = %#v\nwant %#v", got.Env, want)
	}
}

func TestTestDatabasesForReportsForcedVariables(t *testing.T) {
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "agent", "login")
	fakeLinkedWorktree(t, main, wt)
	if err := os.WriteFile(filepath.Join(wt, "phpunit.xml.dist"), []byte(`<phpunit><php><env name="DB_DATABASE" value="testing" force="true"/></php></phpunit>`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := TestDatabasesFor(wt)
	if got.Env["DB_DATABASE"] != "testing_login" || !reflect.DeepEqual(got.Forced, []string{"DB_DATABASE"}) {
		t.Fatalf("got %#v", got)
	}
}

func TestTestDatabasesForSkipsTheMainCheckout(t *testing.T) {
	main := t.TempDir()
	if err := os.MkdirAll(filepath.Join(main, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(main, "phpunit.xml"), []byte(testPHPUnit), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := TestDatabasesFor(main); ok {
		t.Fatalf("main checkout got %#v", got)
	}
}
