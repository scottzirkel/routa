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

func TestPlanTestEnvSuffixesDatabasesAndPicksFreeRedis(t *testing.T) {
	used := map[int]bool{0: true, 1: true, 14: true, 15: true}
	plan, err := PlanTestEnv(affiliateTestEnv, nil, "5194", used)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"DB_DATABASE":             "testing_5194",
		"DB_METRICS_DATABASE":     "testing_5194",
		"DB_SINGLESTORE_DATABASE": "affiliate_testing_5194",
		"REDIS_DB":                "12",
		"REDIS_CACHE_DB":          "13",
	}
	if !reflect.DeepEqual(plan.Set, want) {
		t.Fatalf("set = %#v\nwant %#v", plan.Set, want)
	}
	if !reflect.DeepEqual(plan.Databases, []string{"affiliate_testing_5194", "testing_5194"}) {
		t.Fatalf("databases = %#v", plan.Databases)
	}
	if !reflect.DeepEqual(plan.Redis, []string{"REDIS_DB=12", "REDIS_CACHE_DB=13"}) {
		t.Fatalf("redis = %#v", plan.Redis)
	}
}

func TestPlanTestEnvKeepsNamesThatAlreadyHaveTheSuffix(t *testing.T) {
	plan, err := PlanTestEnv(map[string]string{"DB_DATABASE": "testing_fix_login"}, nil, "fix-login", nil)
	if err != nil || len(plan.Set) != 0 {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
}

func TestPlanTestEnvSkipsRedisForAppsWithoutIt(t *testing.T) {
	plan, err := PlanTestEnv(map[string]string{"DB_DATABASE": "testing"}, map[string]string{"CACHE_STORE": "file"}, "x", nil)
	if err != nil || len(plan.Redis) != 0 {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
	// A REDIS_ key in the parent's .env is enough to plan Redis.
	plan, err = PlanTestEnv(map[string]string{}, map[string]string{"REDIS_HOST": "127.0.0.1", "REDIS_QUEUE_DB": "3"}, "x", map[int]bool{0: true, 1: true})
	if err != nil || !reflect.DeepEqual(plan.Redis, []string{"REDIS_DB=13", "REDIS_CACHE_DB=14", "REDIS_QUEUE_DB=15"}) {
		t.Fatalf("plan = %#v, %v", plan, err)
	}
}

func TestFreeRedisDatabasesRunsOut(t *testing.T) {
	used := map[int]bool{}
	for i := 0; i < redisDatabases-1; i++ {
		used[i] = true
	}
	if _, err := freeRedisDatabases(used, 2); err == nil || !strings.Contains(err.Error(), "no free Redis") {
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
