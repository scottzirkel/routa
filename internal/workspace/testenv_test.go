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

func TestSharedRedisDatabases(t *testing.T) {
	parent := map[string]string{"REDIS_HOST": "127.0.0.1", "REDIS_PREFIX": "{af}"}
	prefixOnly := map[string]string{"REDIS_HOST": "127.0.0.1", "REDIS_PREFIX": "{af-5194}"}
	if got := SharedRedisDatabases(parent, prefixOnly); !reflect.DeepEqual(got, []string{"REDIS_DB", "REDIS_CACHE_DB"}) {
		t.Fatalf("a prefix alone still shares the databases; got %#v", got)
	}
	own := map[string]string{"REDIS_DB": "59", "REDIS_CACHE_DB": "60"}
	if got := SharedRedisDatabases(parent, own); len(got) != 0 {
		t.Fatalf("own numbers reported as shared: %#v", got)
	}
	if got := SharedRedisDatabases(map[string]string{"CACHE_STORE": "file"}, map[string]string{}); len(got) != 0 {
		t.Fatalf("an app without Redis reported sharing: %#v", got)
	}
}
