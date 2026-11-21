package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/scottzirkel/routa/internal/services"
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

func TestWorkspaceRedisDatabasesClearsOnlyTheWorkspacesOwn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	main, wt, other := filepath.Join(base, "shop"), filepath.Join(base, "shop-x"), filepath.Join(base, "shop-y")
	writeTestFile(t, filepath.Join(main, ".env"), "REDIS_HOST=127.0.0.1\nREDIS_PORT=6380\nREDIS_PASSWORD=null\n")
	writeTestFile(t, filepath.Join(wt, ".env"), "REDIS_HOST=127.0.0.1\nREDIS_PORT=6380\nREDIS_PASSWORD=null\nREDIS_DB=32\nREDIS_CACHE_DB=33\n")
	writeTestFile(t, filepath.Join(wt, ".env.testing"), "REDIS_HOST=127.0.0.1\nREDIS_PORT=6380\nREDIS_DB=34\nREDIS_CACHE_DB=35\nREDIS_QUEUE_DB=${REDIS_DB}\n")
	writeTestFile(t, filepath.Join(other, ".env.testing"), "REDIS_DB=35\n")
	if err := site.Save(&site.State{Links: []site.Link{{Name: "shop-y", Path: other, Secure: true}}}); err != nil {
		t.Fatal(err)
	}

	plan := workspaceRedisDatabases(main, wt)
	var cleared []string
	for _, db := range plan.clear {
		cleared = append(cleared, db.addr+"/"+strconv.Itoa(db.number))
		if db.password != "" || db.username != "" {
			t.Errorf("Laravel's null became a credential: %#v", db)
		}
	}
	if want := []string{"127.0.0.1:6380/32", "127.0.0.1:6380/33", "127.0.0.1:6380/34"}; strings.Join(cleared, " ") != strings.Join(want, " ") {
		t.Fatalf("clear = %v, want %v", cleared, want)
	}
	kept := strings.Join(plan.kept, "\n")
	for _, want := range []string{"Redis database 35: " + other + " uses it too", "REDIS_QUEUE_DB names in .env.testing: its value is not a plain number"} {
		if !strings.Contains(kept, want) {
			t.Errorf("kept = %q, want a line with %q", kept, want)
		}
	}
}

func TestWorkspaceRedisDatabasesKeepsWhatItCannotBeSureOf(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-x")
	writeTestFile(t, filepath.Join(main, ".env"), "REDIS_PORT=6380\n")
	for env, why := range map[string]string{
		"REDIS_DB=32\n": "standard port for Redis", // no port means 6379
		"REDIS_PORT=6380\nREDIS_HOST=10.0.0.5\nREDIS_DB=32\n": "REDIS_HOST is not this machine",
		"REDIS_PORT=6380\nREDIS_URL=redis://x\nREDIS_DB=32\n": "REDIS_URL",
		"REDIS_PORT=6380\nREDIS_DB=0\nREDIS_CACHE_DB=1\n":     "Laravel's default",
	} {
		writeTestFile(t, filepath.Join(wt, ".env"), env)
		plan := workspaceRedisDatabases(main, wt)
		if len(plan.clear) != 0 || !strings.Contains(strings.Join(plan.kept, "\n"), why) {
			t.Errorf("%q: clear %#v, kept %q; want everything kept because %s", env, plan.clear, plan.kept, why)
		}
	}
}

func TestClearWorkspaceRedisOnlyWithDropDatabase(t *testing.T) {
	var flushed []string
	t.Cleanup(func() { flushRedis = services.FlushRedisDatabase })
	flushRedis = func(addr, username, password string, db int) error {
		flushed = append(flushed, addr+"/"+strconv.Itoa(db))
		if db == 33 {
			return errors.New("connection refused")
		}
		return nil
	}
	plan := workspaceRedis{
		clear: []redisDatabase{{addr: "127.0.0.1:6380", number: 32}, {addr: "127.0.0.1:6380", number: 33}},
		kept:  []string{"Redis database 35: /x uses it too"},
	}

	var out, errs strings.Builder
	clearWorkspaceRedis(&out, &errs, plan, false)
	if len(flushed) != 0 || !strings.Contains(out.String(), "→ kept Redis databases 32, 33; pass --drop-database") {
		t.Fatalf("without --drop-database: flushed %v, out %q", flushed, out.String())
	}

	out.Reset()
	clearWorkspaceRedis(&out, &errs, plan, true)
	want := "→ kept Redis database 35: /x uses it too\n→ cleared Redis database 32 on 127.0.0.1:6380\n"
	if out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
	if !strings.Contains(errs.String(), "could not empty Redis database 33 on 127.0.0.1:6380: connection refused") {
		t.Fatalf("errs = %q", errs.String())
	}
}
