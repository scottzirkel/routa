package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceAddClonesTheDatabaseByDefault(t *testing.T) {
	if got := workspaceAddCmd.Flags().Lookup("database").DefValue; got != "clone" {
		t.Fatalf("--database default = %q, want clone", got)
	}
}

func TestPlanWorkspaceDatabaseRefusesWhatItCannotIsolate(t *testing.T) {
	main := t.TempDir()
	cases := map[string]string{
		"DB_URL":        "DB_CONNECTION=mysql\nDB_URL=mysql://root@127.0.0.1/shop\n",
		"no connection": "DB_DATABASE=shop\n",
		"unsupported":   "DB_CONNECTION=sqlsrv\nDB_DATABASE=shop\n",
		"no database":   "DB_CONNECTION=mysql\n",
	}
	for name, env := range cases {
		for _, mode := range []string{"clone", "empty"} {
			if _, err := planWorkspaceDatabase(main, main+"-x", "x", mode, env); err == nil || !strings.Contains(err.Error(), "never share") {
				t.Errorf("%s/%s: err = %v, want a refusal", name, mode, err)
			}
		}
	}
}

func TestPlanWorkspaceDatabaseWithoutADatabase(t *testing.T) {
	main := t.TempDir()
	plan, err := planWorkspaceDatabase(main, main+"-x", "x", "clone", "APP_NAME=shop\n")
	if err != nil || plan.summary != "" || len(plan.envSet) != 0 {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
}

func TestPlanWorkspaceDatabaseClonesSQLiteInsideTheWorktree(t *testing.T) {
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-x")
	parentFile := filepath.Join(main, "database", "database.sqlite")
	writeTestFile(t, parentFile, "data")

	plan, err := planWorkspaceDatabase(main, wt, "x", "clone", "DB_CONNECTION=sqlite\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.envSet) != 0 {
		t.Fatalf("a relative SQLite path already points inside the worktree: %#v", plan.envSet)
	}
	if len(plan.skip) == 0 || plan.skip[0] != "database/database.sqlite" {
		t.Fatalf("skip = %#v", plan.skip)
	}
	if err := plan.apply(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(wt, "database", "database.sqlite")); err != nil || string(data) != "data" {
		t.Fatalf("worktree copy = %q, %v", data, err)
	}
}

func TestPlanWorkspaceDatabaseMovesAnOutsideSQLiteFileIntoTheWorktree(t *testing.T) {
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-x")
	outside := filepath.Join(base, "data", "shop.sqlite")
	writeTestFile(t, outside, "data")
	writeTestFile(t, filepath.Join(main, ".env"), "DB_CONNECTION=sqlite\nDB_DATABASE="+outside+"\n")

	plan, err := planWorkspaceDatabase(main, wt, "x", "empty", "DB_CONNECTION=sqlite\nDB_DATABASE="+outside+"\n")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wt, "database", "shop.sqlite")
	if plan.envSet["DB_DATABASE"] != want {
		t.Fatalf("DB_DATABASE = %q, want %q", plan.envSet["DB_DATABASE"], want)
	}
	if err := plan.apply(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(want); err != nil || len(data) != 0 {
		t.Fatalf("empty database = %q, %v", data, err)
	}
	if data, _ := os.ReadFile(outside); string(data) != "data" {
		t.Fatal("the parent's SQLite file changed")
	}
}

func TestWorkspaceServerDatabaseNeverReturnsTheParents(t *testing.T) {
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-x")
	writeTestFile(t, filepath.Join(main, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE=shop\n")

	writeTestFile(t, filepath.Join(wt, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE=shop\n")
	if got := workspaceServerDatabase(main, wt); got.name != "" {
		t.Fatalf("returned the parent's database %q", got.name)
	}
	writeTestFile(t, filepath.Join(wt, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE=shop_x\n")
	if got := workspaceServerDatabase(main, wt); got.name != "shop_x" || got.via.Name != "shop" {
		t.Fatalf("got %#v, want shop_x through shop", got)
	}
	writeTestFile(t, filepath.Join(wt, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3311\nDB_DATABASE=shop_x\n")
	if got := workspaceServerDatabase(main, wt); got.name != "" {
		t.Fatalf("returned a database on another server: %#v", got)
	}
}
