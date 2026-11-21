package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/services"
	"github.com/scottzirkel/routa/internal/site"
	"github.com/scottzirkel/routa/internal/workspace"
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

	plan, err := planWorkspaceEnv(main, wt, "x", "REDIS_HOST=127.0.0.1\n", false)
	if err != nil || len(plan.redis.App)+len(plan.redis.Tests) != 0 {
		t.Fatalf("Redis on 6379 got numbers: %#v, %v", plan.redis, err)
	}
	if plan.tests.Set["DB_DATABASE"] != "testing_x" {
		t.Fatalf("test databases still get the suffix: %#v", plan.tests)
	}
	plan, err = planWorkspaceEnv(main, wt, "x", "REDIS_HOST=127.0.0.1\nREDIS_PORT=6380\n", false)
	if err != nil || plan.redis.App["REDIS_DB"] == "" || plan.redis.Tests["REDIS_DB"] == "" {
		t.Fatalf("Redis on 6380 got no numbers: %#v, %v", plan.redis, err)
	}
	if _, ok := plan.redis.App["REDIS_PREFIX"]; ok {
		t.Fatalf("an app that is not Laravel got a prefix: %#v", plan.redis)
	}
}

// makeLaravel turns dir into a Laravel app as DetectLaravel sees it.
func makeLaravel(t *testing.T, dir string, horizon bool) {
	t.Helper()
	require := `"laravel/framework": "^13.0"`
	if horizon {
		require += `, "laravel/horizon": "^5.0"`
	}
	writeTestFile(t, filepath.Join(dir, "artisan"), "#!/usr/bin/env php\n")
	writeTestFile(t, filepath.Join(dir, "composer.json"), `{"require": {`+require+`}}`)
}

func TestPlanWorkspaceEnvGivesALaravelAppPrefixesNotNumbers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-x")
	writeTestFile(t, filepath.Join(wt, ".env.testing"), "REDIS_HOST=127.0.0.1\nREDIS_PORT=6380\nDB_DATABASE=testing\n")
	parentEnv := "APP_NAME=Shop\nREDIS_HOST=127.0.0.1\nREDIS_PORT=6380\nREDIS_PREFIX=\"{shop}\"\n"

	makeLaravel(t, main, false)
	plan, err := planWorkspaceEnv(main, wt, "x", parentEnv, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"REDIS_PREFIX": "{shop-x}"}; !reflect.DeepEqual(plan.redis.App, want) {
		t.Fatalf("app = %#v, want %#v: no numbers, and no Horizon prefix without Horizon", plan.redis.App, want)
	}
	if want := map[string]string{"REDIS_PREFIX": "x_laravel-database-"}; !reflect.DeepEqual(plan.redis.Tests, want) {
		t.Fatalf("tests = %#v, want %#v", plan.redis.Tests, want)
	}

	makeLaravel(t, main, true)
	plan, err = planWorkspaceEnv(main, wt, "x", parentEnv, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.redis.App["HORIZON_PREFIX"] != "x_shop_horizon:" || plan.redis.Tests["HORIZON_PREFIX"] != "x_laravel_horizon:" {
		t.Fatalf("an app with Horizon got %#v", plan.redis)
	}

	// A prefix touches no data, so a Redis on the standard port gets one too.
	plan, err = planWorkspaceEnv(main, wt, "x", "APP_NAME=Shop\nREDIS_HOST=127.0.0.1\n", false)
	if err != nil || plan.redis.App["REDIS_PREFIX"] != "x_shop-database-" {
		t.Fatalf("Redis on 6379 got %#v, %v", plan.redis, err)
	}
	// An app that never mentions Redis gets nothing.
	writeTestFile(t, filepath.Join(wt, ".env.testing"), "DB_DATABASE=testing\n")
	plan, err = planWorkspaceEnv(main, wt, "x", "APP_NAME=Shop\nCACHE_STORE=file\n", false)
	if err != nil || len(plan.redis.App)+len(plan.redis.Tests) != 0 {
		t.Fatalf("an app without Redis got %#v, %v", plan.redis, err)
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

func TestWorkspaceRedisDatabasesClearsALaravelWorkspacesOwnPrefixes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	main, wt, other := filepath.Join(base, "shop"), filepath.Join(base, "shop-x"), filepath.Join(base, "shop-y")
	makeLaravel(t, main, true)
	writeTestFile(t, filepath.Join(main, ".env"), "REDIS_PORT=6380\nREDIS_PREFIX=\"{af}\"\nHORIZON_PREFIX=\"{af-horizon}\"\n")
	writeTestFile(t, filepath.Join(main, ".env.testing"), "REDIS_PORT=6380\n")
	writeTestFile(t, filepath.Join(wt, ".env"), "REDIS_PORT=6380\nREDIS_PASSWORD=null\nREDIS_PREFIX=\"{af-x}\"\nHORIZON_PREFIX=\"{af-horizon-x}\"\n")
	writeTestFile(t, filepath.Join(wt, ".env.testing"), "REDIS_PORT=6380\nREDIS_PREFIX=x_laravel-database-\nHORIZON_PREFIX=x_laravel_horizon:\n")

	describe := func(plan workspaceRedis) string {
		var parts []string
		for _, p := range plan.prefixes {
			parts = append(parts, p.addr+" "+p.prefix+" "+redisNumbers(p.databases))
		}
		return strings.Join(parts, "\n")
	}
	plan := workspaceRedisDatabases(main, wt)
	want := strings.Join([]string{
		"127.0.0.1:6380 {af-x} databases 0, 1",
		"127.0.0.1:6380 {af-horizon-x} databases 0, 1",
		"127.0.0.1:6380 x_laravel-database- databases 0, 1",
		"127.0.0.1:6380 x_laravel_horizon: databases 0, 1",
	}, "\n")
	if got := describe(plan); got != want || len(plan.clear) != 0 || len(plan.kept) != 0 {
		t.Fatalf("prefixes =\n%s\nwant\n%s\nclear %#v, kept %q", got, want, plan.clear, plan.kept)
	}
	for _, p := range plan.prefixes {
		if p.password != "" {
			t.Errorf("Laravel's null became a password: %#v", p)
		}
	}

	// A prefix another checkout uses, or one that overlaps another's, is kept.
	writeTestFile(t, filepath.Join(other, ".env"), "REDIS_PORT=6380\nREDIS_PREFIX=\"{af-x}\"\n")
	writeTestFile(t, filepath.Join(other, ".env.testing"), "REDIS_PORT=6380\nREDIS_PREFIX=x_laravel-database-old\n")
	if err := site.Save(&site.State{Links: []site.Link{{Name: "shop-y", Path: other, Secure: true}}}); err != nil {
		t.Fatal(err)
	}
	plan = workspaceRedisDatabases(main, wt)
	kept := strings.Join(plan.kept, "\n")
	for _, line := range []string{
		"Redis prefix {af-x} in .env: " + other + " uses it too",
		"Redis prefix x_laravel-database- in .env.testing: " + other + " uses x_laravel-database-old, which overlaps it",
	} {
		if !strings.Contains(kept, line) {
			t.Errorf("kept = %q, want a line %q", kept, line)
		}
	}
	for _, p := range plan.prefixes {
		if p.prefix == "{af-x}" || p.prefix == "x_laravel-database-" {
			t.Fatalf("would clear %s, which %s uses", p.prefix, other)
		}
	}

	// A worktree still on the parent's prefix clears nothing.
	writeTestFile(t, filepath.Join(wt, ".env"), "REDIS_PORT=6380\nREDIS_PREFIX=\"{af}\"\nHORIZON_PREFIX=\"{af-horizon}\"\n")
	plan = workspaceRedisDatabases(main, wt)
	if !strings.Contains(strings.Join(plan.kept, "\n"), "Redis prefix {af} in .env: "+main+" uses it too") {
		t.Fatalf("kept = %q", plan.kept)
	}
	for _, p := range plan.prefixes {
		if strings.HasPrefix(p.prefix, "{af") {
			t.Fatalf("would clear the parent's prefix %s", p.prefix)
		}
	}
}

func TestWorkspaceRedisDatabasesEmptiesAnOlderLaravelWorkspacesOwnNumbers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-x")
	makeLaravel(t, main, true)
	writeTestFile(t, filepath.Join(main, ".env"), "REDIS_PORT=6380\nREDIS_PREFIX=\"{af}\"\n")
	writeTestFile(t, filepath.Join(wt, ".env"), "REDIS_PORT=6380\nREDIS_PREFIX=\"{af}\"\nREDIS_DB=32\nREDIS_CACHE_DB=33\n")
	plan := workspaceRedisDatabases(main, wt)
	if len(plan.clear) != 2 || len(plan.prefixes) != 0 || len(plan.kept) != 0 {
		t.Fatalf("plan = %#v, want databases 32 and 33 emptied and nothing else", plan)
	}

	// Never on the standard port, even by prefix.
	writeTestFile(t, filepath.Join(wt, ".env"), "REDIS_PREFIX=\"{af-x}\"\n")
	plan = workspaceRedisDatabases(main, wt)
	if len(plan.prefixes) != 0 || !strings.Contains(strings.Join(plan.kept, "\n"), "Redis prefixes {af-x}, laravel_horizon: in .env: port 6379 is the standard port") {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestClearWorkspaceRedisClearsPrefixesOnlyWithDropDatabase(t *testing.T) {
	var deleted []string
	t.Cleanup(func() { deleteRedisPrefix = services.DeleteRedisPrefix })
	deleteRedisPrefix = func(addr, username, password string, db int, prefix string) (int, error) {
		deleted = append(deleted, addr+"/"+strconv.Itoa(db)+" "+prefix)
		if prefix == "{b}" && db == 1 {
			return 0, errors.New("connection refused")
		}
		return 3, nil
	}
	plan := workspaceRedis{
		prefixes: []redisPrefix{
			{addr: "127.0.0.1:6380", prefix: "{a}", databases: []int{0, 1}},
			{addr: "127.0.0.1:6380", prefix: "{b}", databases: []int{0, 1}},
		},
		kept: []string{"Redis prefix {af} in .env: /main uses it too"},
	}

	var out, errs strings.Builder
	clearWorkspaceRedis(&out, &errs, plan, false)
	if len(deleted) != 0 || !strings.Contains(out.String(), "→ kept Redis prefixes {a}, {b}; pass --drop-database to clear them") {
		t.Fatalf("without --drop-database: deleted %v, out %q", deleted, out.String())
	}

	out.Reset()
	clearWorkspaceRedis(&out, &errs, plan, true)
	want := "→ kept Redis prefix {af} in .env: /main uses it too\n" +
		"→ cleared Redis prefix {a} in databases 0, 1 on 127.0.0.1:6380 (6 keys)\n" +
		"→ cleared Redis prefix {b} in database 0 on 127.0.0.1:6380 (3 keys)\n"
	if out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
	if !strings.Contains(errs.String(), "could not clear Redis prefix {b} in database 1 on 127.0.0.1:6380: connection refused") {
		t.Fatalf("errs = %q", errs.String())
	}
}

// workspaceGitRepo makes a main checkout served as shop, with worktrees in
// shop-worktrees named names, and moves into it. .env files are ignored.
func workspaceGitRepo(t *testing.T, names ...string) (main string, wts []string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	base := t.TempDir()
	main = filepath.Join(base, "shop")
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", main}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeTestFile(t, filepath.Join(main, ".gitignore"), ".env\n.env.testing\n")
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "init")
	for _, name := range names {
		wt := filepath.Join(base, "shop-worktrees", name)
		git("worktree", "add", "-q", "-b", name, wt)
		wts = append(wts, wt)
	}
	if err := site.Save(&site.State{Links: []site.Link{{Name: "shop", Path: main, Secure: true}}}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(main)
	return main, wts
}

func TestWorkspaceListFlagsSharedRedisByPrefixForLaravel(t *testing.T) {
	main, wts := workspaceGitRepo(t, "copied", "prefixed", "numbered")
	parentEnv := "REDIS_PORT=6380\nREDIS_PREFIX=\"{af}\"\n"
	writeTestFile(t, filepath.Join(main, ".env"), parentEnv)
	writeTestFile(t, filepath.Join(wts[0], ".env"), parentEnv)
	writeTestFile(t, filepath.Join(wts[1], ".env"), "REDIS_PORT=6380\nREDIS_PREFIX=\"{af-prefixed}\"\n")
	writeTestFile(t, filepath.Join(wts[2], ".env"), parentEnv+"REDIS_DB=40\nREDIS_CACHE_DB=41\n")
	list := func() string {
		var out strings.Builder
		c := &cobra.Command{}
		c.SetOut(&out)
		if err := runWorkspaceList(c, nil); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	makeLaravel(t, main, false)
	out := list()
	if !strings.Contains(out, "! copied uses the parent's Redis databases and key prefix") || !strings.Contains(out, "routa workspace fix copied") {
		t.Fatalf("a copy of the parent's .env was not flagged:\n%s", out)
	}
	if strings.Contains(out, "! prefixed") || strings.Contains(out, "! numbered") {
		t.Fatalf("a worktree with its own prefix or numbers was flagged:\n%s", out)
	}

	// An app that is not Laravel ignores REDIS_PREFIX: only numbers count.
	if err := os.Remove(filepath.Join(main, "artisan")); err != nil {
		t.Fatal(err)
	}
	out = list()
	for _, name := range []string{"copied", "prefixed"} {
		if !strings.Contains(out, "! "+name+" uses the parent's Redis databases. Run 'routa workspace fix "+name+"'") {
			t.Errorf("%s was not flagged by number:\n%s", name, out)
		}
	}
	if strings.Contains(out, "! numbered") {
		t.Fatalf("a worktree with its own numbers was flagged:\n%s", out)
	}
}

func TestWorkspaceFixGivesALaravelWorktreeItsOwnPrefixes(t *testing.T) {
	main, wts := workspaceGitRepo(t, "copied", "numbered")
	makeLaravel(t, main, true)
	parentEnv := "APP_NAME=Affiliate.com\nREDIS_PORT=6380\nREDIS_PREFIX=\"{af}\"\nHORIZON_PREFIX=\"{af-horizon}\"\n"
	parentTest := "REDIS_HOST=127.0.0.1\nREDIS_PORT=6380\n"
	writeTestFile(t, filepath.Join(main, ".env"), parentEnv)
	writeTestFile(t, filepath.Join(main, ".env.testing"), parentTest)
	writeTestFile(t, filepath.Join(wts[0], ".env"), parentEnv)
	writeTestFile(t, filepath.Join(wts[0], ".env.testing"), parentTest)
	numbered := parentEnv + "REDIS_DB=40\nREDIS_CACHE_DB=41\n"
	writeTestFile(t, filepath.Join(wts[1], ".env"), numbered)
	writeTestFile(t, filepath.Join(wts[1], ".env.testing"), parentTest+"REDIS_DB=42\nREDIS_CACHE_DB=43\n")
	oldDryRun := workspaceFixDryRun
	t.Cleanup(func() { workspaceFixDryRun = oldDryRun })
	workspaceFixDryRun = false

	var err error
	out := captureStdout(t, func() { err = runWorkspaceFix(nil, []string{"copied"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "→ .env: Redis keys share a database and prefix with another checkout, use REDIS_PREFIX={af-copied}, HORIZON_PREFIX={af-horizon-copied}") {
		t.Fatalf("out =\n%s", out)
	}
	env, _ := os.ReadFile(filepath.Join(wts[0], ".env"))
	if want := strings.ReplaceAll(strings.ReplaceAll(parentEnv, "{af}", "{af-copied}"), "{af-horizon}", "{af-horizon-copied}"); string(env) != want {
		t.Fatalf(".env =\n%s\nwant\n%s", env, want)
	}
	testEnv, _ := os.ReadFile(filepath.Join(wts[0], ".env.testing"))
	if want := parentTest + "HORIZON_PREFIX=copied_laravel_horizon:\nREDIS_PREFIX=copied_laravel-database-\n"; string(testEnv) != want {
		t.Fatalf(".env.testing =\n%s\nwant\n%s", testEnv, want)
	}
	for _, f := range []string{string(env), string(testEnv)} {
		if strings.Contains(f, "REDIS_DB") || strings.Contains(f, "REDIS_CACHE_DB") {
			t.Fatalf("fix wrote database numbers:\n%s", f)
		}
	}

	for _, name := range []string{"copied", "numbered"} {
		out = captureStdout(t, func() { err = runWorkspaceFix(nil, []string{name}) })
		if err != nil || !strings.Contains(out, "already matches") {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
	if env, _ := os.ReadFile(filepath.Join(wts[1], ".env")); string(env) != numbered {
		t.Fatalf("a worktree with its own numbers was changed:\n%s", env)
	}
}

func TestWorkspaceFixGivesOtherAppsFreeNumbers(t *testing.T) {
	main, wts := workspaceGitRepo(t, "copied")
	parentEnv := "REDIS_PORT=6380\nREDIS_PREFIX=shop_\n"
	writeTestFile(t, filepath.Join(main, ".env"), parentEnv)
	writeTestFile(t, filepath.Join(wts[0], ".env"), parentEnv)
	oldDryRun := workspaceFixDryRun
	t.Cleanup(func() { workspaceFixDryRun = oldDryRun })
	workspaceFixDryRun = false

	var err error
	out := captureStdout(t, func() { err = runWorkspaceFix(nil, []string{"copied"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "→ .env: Redis numbers are shared, use REDIS_DB=14, REDIS_CACHE_DB=15") {
		t.Fatalf("out =\n%s", out)
	}
	env, _ := os.ReadFile(filepath.Join(wts[0], ".env"))
	if want := parentEnv + "REDIS_CACHE_DB=15\nREDIS_DB=14\n"; string(env) != want {
		t.Fatalf(".env =\n%s\nwant\n%s", env, want)
	}
}

func TestPlanWorkspaceEnvKeepsSharedTestDatabaseNames(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-x")
	makeLaravel(t, main, false)
	writeTestFile(t, filepath.Join(wt, ".env.testing"), "REDIS_PORT=6380\nDB_DATABASE=testing\nDB_METRICS_DATABASE=metrics_testing\n")
	parentEnv := "APP_NAME=Shop\nREDIS_PORT=6380\n"

	plan, err := planWorkspaceEnv(main, wt, "x", parentEnv, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.tests.Set) != 0 || len(plan.tests.Databases) != 0 || !plan.sharedTests {
		t.Fatalf("shared test databases were renamed: %#v", plan.tests)
	}
	if plan.redis.Tests["REDIS_PREFIX"] != "x_laravel-database-" {
		t.Fatalf("the tests lost their Redis prefix: %#v", plan.redis)
	}

	plan, err = planWorkspaceEnv(main, wt, "x", parentEnv, false)
	if err != nil || plan.tests.Set["DB_DATABASE"] != "testing_x" || plan.sharedTests {
		t.Fatalf("own test databases: %#v, %v", plan.tests, err)
	}
}

func TestWorkspaceServerDatabaseKeepsATestDatabase(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-x")
	writeTestFile(t, filepath.Join(main, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE=shop\n")
	writeTestFile(t, filepath.Join(main, ".env.testing"), "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE=testing\n")
	for _, name := range []string{"testing", "testing_test_2"} {
		writeTestFile(t, filepath.Join(wt, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE="+name+"\n")
		if got := workspaceServerDatabase(main, wt); got.testsOf != main {
			t.Fatalf("%s: got %#v, want it kept because the parent's tests use it", name, got)
		}
	}
	writeTestFile(t, filepath.Join(wt, ".env"), "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE=shop_x\n")
	if got := workspaceServerDatabase(main, wt); got.name != "shop_x" || got.testsOf != "" {
		t.Fatalf("the workspace's own database: %#v", got)
	}
}

func TestSharedTestDatabasesAreLeftAloneByListAndFix(t *testing.T) {
	main, wts := workspaceGitRepo(t, "copied")
	test := "DB_CONNECTION=mysql\nDB_PORT=3310\nDB_DATABASE=testing\n"
	writeTestFile(t, filepath.Join(main, ".env"), "APP_NAME=Shop\n")
	writeTestFile(t, filepath.Join(wts[0], ".env"), "APP_NAME=Shop\n")
	writeTestFile(t, filepath.Join(main, ".env.testing"), test)
	writeTestFile(t, filepath.Join(wts[0], ".env.testing"), test)
	oldDryRun := workspaceFixDryRun
	t.Cleanup(func() { workspaceFixDryRun = oldDryRun })
	workspaceFixDryRun = true
	list := func() string {
		var out strings.Builder
		c := &cobra.Command{}
		c.SetOut(&out)
		if err := runWorkspaceList(c, nil); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	fix := func() string {
		var err error
		out := captureStdout(t, func() { err = runWorkspaceFix(nil, []string{"copied"}) })
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	// "own", the default: list warns and fix renames.
	if out := list(); !strings.Contains(out, "! copied runs tests against the parent's test databases") {
		t.Fatalf("own: list =\n%s", out)
	}
	if out := fix(); !strings.Contains(out, ".env.testing: DB_DATABASE=testing is shared, use testing_copied") {
		t.Fatalf("own: fix =\n%s", out)
	}

	writeTestFile(t, filepath.Join(main, workspace.ProjectFile), `{"workspace": {"testDatabases": "shared"}}`)
	if out := list(); strings.Contains(out, "test databases") {
		t.Fatalf("shared: list warned:\n%s", out)
	}
	if out := fix(); !strings.Contains(out, "already matches") {
		t.Fatalf("shared: fix =\n%s", out)
	}
	if data, _ := os.ReadFile(filepath.Join(wts[0], ".env.testing")); string(data) != test {
		t.Fatalf(".env.testing changed:\n%s", data)
	}
}
