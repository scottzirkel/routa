package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottzirkel/routa/internal/site"
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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

func TestPlanWorkspaceDatabaseRefusesStandardPorts(t *testing.T) {
	main := t.TempDir()
	for _, env := range []string{
		"DB_CONNECTION=mysql\nDB_PORT=3306\nDB_DATABASE=shop\n",
		"DB_CONNECTION=mysql\nDB_DATABASE=shop\n", // no port means 3306
		"DB_CONNECTION=pgsql\nDB_PORT=5432\nDB_DATABASE=shop\n",
	} {
		if _, err := planWorkspaceDatabase(main, main+"-x", "x", "clone", env); err == nil || !strings.Contains(err.Error(), "standard port") {
			t.Errorf("%q: err = %v, want a standard-port refusal", env, err)
		}
	}
}

func TestPlanWorkspaceEnvLeavesStandardPortRedisAlone(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-x")
	writeTestFile(t, filepath.Join(wt, ".env.testing"), "DB_DATABASE=testing\n")

	plan, err := planWorkspaceEnv(main, wt, "x", "REDIS_HOST=127.0.0.1\n")
	if err != nil || len(plan.redis.App)+len(plan.redis.Tests) != 0 {
		t.Fatalf("Redis on 6379 got numbers: %#v, %v", plan.redis, err)
	}
	if plan.tests.Set["DB_DATABASE"] != "testing_x" {
		t.Fatalf("test databases still get the suffix: %#v", plan.tests)
	}
	plan, err = planWorkspaceEnv(main, wt, "x", "REDIS_HOST=127.0.0.1\nREDIS_PORT=6380\n")
	if err != nil || plan.redis.App["REDIS_DB"] == "" || plan.redis.Tests["REDIS_DB"] == "" {
		t.Fatalf("Redis on 6380 got no numbers: %#v, %v", plan.redis, err)
	}
}

func TestWorkspaceServerDatabaseKeepsStandardPorts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-x")
	writeTestFile(t, filepath.Join(main, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3306\nDB_DATABASE=shop\n")
	writeTestFile(t, filepath.Join(wt, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3306\nDB_DATABASE=shop_x\n")
	if got := workspaceServerDatabase(main, wt); got.standardPort == "" {
		t.Fatalf("got %#v, want it marked as on a standard port", got)
	}
}

func TestWorkspaceServerDatabaseKeepsADatabaseAnotherCheckoutUses(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	main, wt, other := filepath.Join(base, "shop"), filepath.Join(base, "shop-x"), filepath.Join(base, "shop-y")
	writeTestFile(t, filepath.Join(main, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE=shop_main\n")
	writeTestFile(t, filepath.Join(wt, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE=shop_old\n")
	writeTestFile(t, filepath.Join(other, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE=shop_old\n")
	if err := site.Save(&site.State{Links: []site.Link{{Name: "shop-y", Path: other, Secure: true}}}); err != nil {
		t.Fatal(err)
	}
	if got := workspaceServerDatabase(main, wt); got.sharedWith != other {
		t.Fatalf("got %#v, want it kept because %s uses it", got, other)
	}
}

func TestWorkspaceRepoExplainsHowToPickASite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	t.Chdir(repo)
	defer func() { workspaceSite = "" }()

	if _, _, _, err := workspaceRepo(); err == nil || !strings.Contains(err.Error(), "--site") {
		t.Fatalf("err = %v, want a hint about --site", err)
	}
	workspaceSite = "missing"
	if _, _, _, err := workspaceRepo(); err == nil || !strings.Contains(err.Error(), "no routa site named missing") {
		t.Fatalf("err = %v", err)
	}
	if err := site.Save(&site.State{Links: []site.Link{{Name: "shop", Path: repo, Secure: true}}}); err != nil {
		t.Fatal(err)
	}
	workspaceSite = "shop.test"
	t.Chdir(t.TempDir()) // anywhere: --site picks the repository
	main, parent, _, err := workspaceRepo()
	if err != nil || parent != "shop" {
		t.Fatalf("main = %q, parent = %q, err = %v", main, parent, err)
	}
	if want, _ := filepath.EvalSymlinks(repo); main != repo && main != want {
		t.Fatalf("main = %q, want %q", main, repo)
	}
}
