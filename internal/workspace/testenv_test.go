package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// affiliateTestEnv is the shape of the affiliate app's .env.testing.
var affiliateTestEnv = map[string]string{
	"APP_ENV":                      "testing",
	"DB_CONNECTION":                "mysql",
	"DB_PORT":                      "3310",
	"DB_DATABASE":                  "testing",
	"DB_METRICS_DATABASE":          "testing",
	"DB_SINGLESTORE_DATABASE":      "affiliate_testing",
	"DB_SINGLESTORE_PROD_DATABASE": "${DB_SINGLESTORE_DATABASE}",
	"DB_SQLITE_DATABASE":           ":memory:",
	"REDIS_HOST":                   "127.0.0.1",
}

func TestPlanTestDatabasesSuffixesEveryTestDatabase(t *testing.T) {
	plan := PlanTestDatabases(affiliateTestEnv, "5194")
	want := map[string]string{
		"DB_DATABASE":             "testing_5194",
		"DB_METRICS_DATABASE":     "testing_5194",
		"DB_SINGLESTORE_DATABASE": "affiliate_testing_5194",
	}
	if !reflect.DeepEqual(plan.Set, want) {
		t.Fatalf("set = %#v\nwant %#v", plan.Set, want)
	}
	if !reflect.DeepEqual(plan.Databases, []string{"affiliate_testing_5194", "testing_5194"}) {
		t.Fatalf("databases = %#v", plan.Databases)
	}
}

func TestPlanTestDatabasesKeepsNamesThatAlreadyHaveTheSuffix(t *testing.T) {
	if plan := PlanTestDatabases(map[string]string{"DB_DATABASE": "testing_fix_login"}, "fix-login"); len(plan.Set) != 0 {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestPlanRedisGivesTheAppAndItsTestsTheirOwnNumbers(t *testing.T) {
	parent := map[string]string{"REDIS_HOST": "127.0.0.1", "REDIS_PREFIX": "{af}"}
	used := map[int]bool{0: true, 1: true, 63: true}
	plan, err := PlanRedis(parent, affiliateTestEnv, true, used, 64)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"REDIS_DB": "59", "REDIS_CACHE_DB": "60"}; !reflect.DeepEqual(plan.App, want) {
		t.Fatalf("app = %#v, want %#v", plan.App, want)
	}
	if want := map[string]string{"REDIS_DB": "61", "REDIS_CACHE_DB": "62"}; !reflect.DeepEqual(plan.Tests, want) {
		t.Fatalf("tests = %#v, want %#v", plan.Tests, want)
	}
	if got := plan.Summary(plan.App); got != "REDIS_DB=59, REDIS_CACHE_DB=60" {
		t.Fatalf("summary = %q", got)
	}
}

func TestPlanRedisWithoutTestEnvOrRedis(t *testing.T) {
	plan, err := PlanRedis(map[string]string{"REDIS_HOST": "127.0.0.1", "REDIS_QUEUE_DB": "3"}, nil, false, map[int]bool{0: true, 1: true}, 16)
	if err != nil || len(plan.Tests) != 0 || !reflect.DeepEqual(plan.App, map[string]string{"REDIS_DB": "13", "REDIS_CACHE_DB": "14", "REDIS_QUEUE_DB": "15"}) {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
	plan, err = PlanRedis(map[string]string{"CACHE_STORE": "file"}, map[string]string{"DB_DATABASE": "testing"}, true, nil, 16)
	if err != nil || len(plan.App)+len(plan.Tests) != 0 {
		t.Fatalf("an app without Redis got %#v, %v", plan, err)
	}
}

func TestFreeRedisDatabasesRunsOut(t *testing.T) {
	used := map[int]bool{}
	for i := 0; i < 15; i++ {
		used[i] = true
	}
	if _, err := freeRedisDatabases(used, 2, 16); err == nil || !strings.Contains(err.Error(), "routa redis restart") {
		t.Fatalf("err = %v", err)
	}
}

func TestUsedRedisDatabasesReadsEveryCheckout(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(a, ".env"), []byte("REDIS_DB=3\nREDIS_HOST=127.0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, ".env.testing"), []byte("REDIS_DB=12\nREDIS_CACHE_DB=13\nDB_PORT=3310\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := UsedRedisDatabases([]string{a, b, filepath.Join(b, "missing")})
	want := map[int]bool{0: true, 1: true, 3: true, 12: true, 13: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("used = %#v", got)
	}
}

func TestSharedTestDatabases(t *testing.T) {
	parent := map[string]string{"DB_DATABASE": "testing", "DB_SINGLESTORE_DATABASE": "affiliate_testing"}
	copied := map[string]string{"DB_DATABASE": "testing", "DB_SINGLESTORE_DATABASE": "affiliate_testing_5194"}
	if got := SharedTestDatabases(parent, copied); !reflect.DeepEqual(got, []string{"DB_DATABASE"}) {
		t.Fatalf("shared = %#v", got)
	}
}

func TestSharedRedisDatabasesByNumber(t *testing.T) {
	// An app that is not Laravel does not read REDIS_PREFIX, so only the
	// numbers count.
	parent := map[string]string{"REDIS_HOST": "127.0.0.1", "REDIS_PREFIX": "{af}"}
	prefixOnly := map[string]string{"REDIS_HOST": "127.0.0.1", "REDIS_PREFIX": "{af-5194}"}
	if got := SharedRedisDatabases(parent, prefixOnly, false); !reflect.DeepEqual(got, []string{"REDIS_DB", "REDIS_CACHE_DB"}) {
		t.Fatalf("a prefix alone still shares the databases; got %#v", got)
	}
	own := map[string]string{"REDIS_DB": "59", "REDIS_CACHE_DB": "60"}
	if got := SharedRedisDatabases(parent, own, false); len(got) != 0 {
		t.Fatalf("own numbers reported as shared: %#v", got)
	}
	if got := SharedRedisDatabases(map[string]string{"CACHE_STORE": "file"}, map[string]string{}, false); len(got) != 0 {
		t.Fatalf("an app without Redis reported sharing: %#v", got)
	}
}

func TestSharedRedisDatabasesByPrefix(t *testing.T) {
	parent := map[string]string{"APP_NAME": "Shop", "REDIS_HOST": "127.0.0.1", "REDIS_PREFIX": "{af}"}
	copied := map[string]string{"APP_NAME": "Shop", "REDIS_HOST": "127.0.0.1", "REDIS_PREFIX": "{af}"}
	if got := SharedRedisDatabases(parent, copied, true); !reflect.DeepEqual(got, []string{"REDIS_DB", "REDIS_CACHE_DB"}) {
		t.Fatalf("the same numbers and prefix are shared; got %#v", got)
	}
	ownPrefix := map[string]string{"APP_NAME": "Shop", "REDIS_HOST": "127.0.0.1", "REDIS_PREFIX": "{af-5194}"}
	if got := SharedRedisDatabases(parent, ownPrefix, true); len(got) != 0 {
		t.Fatalf("its own prefix keeps it apart; got %#v", got)
	}
	ownNumbers := map[string]string{"REDIS_PREFIX": "{af}", "REDIS_DB": "59", "REDIS_CACHE_DB": "60"}
	if got := SharedRedisDatabases(parent, ownNumbers, true); len(got) != 0 {
		t.Fatalf("an older workspace's own numbers keep it apart; got %#v", got)
	}
	// Without REDIS_PREFIX both files use Laravel's default for APP_NAME.
	noPrefix := map[string]string{"APP_NAME": "Shop", "REDIS_HOST": "127.0.0.1"}
	if got := SharedRedisDatabases(noPrefix, map[string]string{"APP_NAME": "Shop"}, true); len(got) != 2 {
		t.Fatalf("the same default prefix is shared; got %#v", got)
	}
	if got := SharedRedisDatabases(noPrefix, map[string]string{"APP_NAME": "Shop", "REDIS_PREFIX": "5194_shop-database-"}, true); len(got) != 0 {
		t.Fatalf("a prefix set over the default keeps it apart; got %#v", got)
	}
}

func TestWorkspaceRedisPrefix(t *testing.T) {
	for parent, want := range map[string]string{
		"{af}":              "{af-5382}",
		"{af-horizon}":      "{af-horizon-5382}",
		"laravel-database-": "5382_laravel-database-",
		"shop_horizon:":     "5382_shop_horizon:",
		"{af}:":             "5382_{af}:", // not wrapped in braces
		"":                  "5382_",
	} {
		if got := WorkspaceRedisPrefix(parent, "5382"); got != want {
			t.Errorf("WorkspaceRedisPrefix(%q) = %q, want %q", parent, got, want)
		}
	}
}

func TestEffectiveRedisPrefixDefaults(t *testing.T) {
	cases := []struct {
		env       map[string]string
		key, want string
	}{
		{map[string]string{"APP_NAME": "Affiliate.com"}, "REDIS_PREFIX", "affiliatecom-database-"},
		{map[string]string{"APP_NAME": "My App"}, "REDIS_PREFIX", "my-app-database-"},
		{map[string]string{"APP_NAME": "My App"}, "HORIZON_PREFIX", "my_app_horizon:"},
		{map[string]string{"APP_NAME": "foo_bar-baz @ Co"}, "REDIS_PREFIX", "foo-bar-baz-at-co-database-"},
		{map[string]string{}, "REDIS_PREFIX", "laravel-database-"},
		{map[string]string{}, "HORIZON_PREFIX", "laravel_horizon:"},
		{map[string]string{"REDIS_PREFIX": "{af}"}, "REDIS_PREFIX", "{af}"},
		{map[string]string{"REDIS_PREFIX": "null"}, "REDIS_PREFIX", ""},
	}
	for _, c := range cases {
		if got, ok := EffectiveRedisPrefix(c.env, c.key); !ok || got != c.want {
			t.Errorf("EffectiveRedisPrefix(%v, %s) = %q, %v; want %q", c.env, c.key, got, ok, c.want)
		}
	}
	if _, ok := EffectiveRedisPrefix(map[string]string{}, "REDIS_CACHE_PREFIX"); ok {
		t.Error("a prefix key with no Laravel default and no value resolved")
	}
}

func TestPlanRedisPrefixesWritesPrefixesAndNoNumbers(t *testing.T) {
	parent := map[string]string{
		"APP_NAME":           "Affiliate.com",
		"REDIS_HOST":         "127.0.0.1",
		"REDIS_PORT":         "6380",
		"REDIS_PREFIX":       "{af}",
		"HORIZON_PREFIX":     "{af-horizon}",
		"REDIS_CACHE_PREFIX": "cache:",
	}
	plan := PlanRedisPrefixes(parent, affiliateTestEnv, true, true, "5382")
	wantApp := map[string]string{
		"REDIS_PREFIX":       "{af-5382}",
		"HORIZON_PREFIX":     "{af-horizon-5382}",
		"REDIS_CACHE_PREFIX": "5382_cache:",
	}
	if !reflect.DeepEqual(plan.App, wantApp) {
		t.Fatalf("app = %#v, want %#v", plan.App, wantApp)
	}
	// .env.testing sets no APP_NAME or prefixes, so Laravel's defaults for
	// "laravel" are the starting point.
	wantTests := map[string]string{
		"REDIS_PREFIX":   "5382_laravel-database-",
		"HORIZON_PREFIX": "5382_laravel_horizon:",
	}
	if !reflect.DeepEqual(plan.Tests, wantTests) {
		t.Fatalf("tests = %#v, want %#v", plan.Tests, wantTests)
	}
	for _, set := range []map[string]string{plan.App, plan.Tests} {
		for key := range set {
			if IsRedisDatabaseKey(key) {
				t.Fatalf("a prefix plan set a database number: %s", key)
			}
		}
	}
	if got := plan.Summary(plan.App); got != "REDIS_PREFIX={af-5382}, HORIZON_PREFIX={af-horizon-5382}, REDIS_CACHE_PREFIX=5382_cache:" {
		t.Fatalf("summary = %q", got)
	}
}

func TestPlanRedisPrefixesWithoutHorizon(t *testing.T) {
	parent := map[string]string{"APP_NAME": "Shop", "REDIS_HOST": "127.0.0.1"}
	plan := PlanRedisPrefixes(parent, nil, false, false, "x")
	if want := map[string]string{"REDIS_PREFIX": "x_shop-database-"}; !reflect.DeepEqual(plan.App, want) || len(plan.Tests) != 0 {
		t.Fatalf("plan = %#v, want only %#v", plan, want)
	}
	// A parent file that already sets HORIZON_PREFIX keeps one of its own.
	parent["HORIZON_PREFIX"] = "shop-horizon:"
	if plan := PlanRedisPrefixes(parent, nil, false, false, "x"); plan.App["HORIZON_PREFIX"] != "x_shop-horizon:" {
		t.Fatalf("plan = %#v", plan)
	}
	if plan := PlanRedisPrefixes(map[string]string{"CACHE_STORE": "file"}, map[string]string{"DB_DATABASE": "testing"}, true, true, "x"); len(plan.App)+len(plan.Tests) != 0 {
		t.Fatalf("an app without Redis got %#v", plan)
	}
}

func TestDetectLaravel(t *testing.T) {
	dir := t.TempDir()
	if got := DetectLaravel(dir); got.App || got.Horizon {
		t.Fatalf("an empty folder is Laravel: %#v", got)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("composer.json", `{"require": {"laravel/framework": "^13.0"}}`)
	if got := DetectLaravel(dir); got.App {
		t.Fatalf("composer.json without artisan counts as an app: %#v", got)
	}
	write("artisan", "#!/usr/bin/env php\n")
	if got := DetectLaravel(dir); !got.App || got.Horizon {
		t.Fatalf("got %#v, want an app without Horizon", got)
	}
	write("composer.json", `{"require": {"laravel/framework": "^13.0"}, "require-dev": {"laravel/horizon": "^5.0"}}`)
	if got := DetectLaravel(dir); !got.App || !got.Horizon {
		t.Fatalf("got %#v, want an app with Horizon", got)
	}
	write("composer.json", `{"require": {"symfony/console": "^7.0"}}`)
	if got := DetectLaravel(dir); got.App {
		t.Fatalf("a PHP app without laravel/framework counts as Laravel: %#v", got)
	}
}

func TestRedisSpacesCountsDefaultsAndEveryPrefix(t *testing.T) {
	env := map[string]string{"APP_NAME": "Shop", "REDIS_DB": "5", "REDIS_PREFIX": "{s}", "HORIZON_PREFIX": "{s-h}"}
	got := RedisSpaces(env, RedisPrefixKeys(env, false))
	want := []RedisSpace{{1, "{s}"}, {1, "{s-h}"}, {5, "{s}"}, {5, "{s-h}"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("spaces = %#v, want %#v", got, want)
	}
}

func TestRedisDatabaseOwnersNamesTheFirstCheckout(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(a, ".env"), []byte("REDIS_DB=32\nREDIS_CACHE_DB=33\nREDIS_PREFIX=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, ".env.testing"), []byte("REDIS_DB=33\nREDIS_QUEUE_DB=40\nREDIS_DB_X=7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := RedisDatabaseOwners([]string{a, b})
	want := map[int]string{32: a, 33: a, 40: b}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("owners = %#v, want %#v", got, want)
	}
}
