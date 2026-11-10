package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// testDatabaseKey matches the .env keys that name a database: DB_DATABASE
// and per-connection ones such as DB_SINGLESTORE_DATABASE.
var testDatabaseKey = regexp.MustCompile(`^DB_(?:[A-Z0-9]+_)*DATABASE$`)

// redisDBKey matches the .env keys that pick a Redis database number, such
// as REDIS_DB and REDIS_CACHE_DB.
var redisDBKey = regexp.MustCompile(`^REDIS_(?:[A-Z0-9]+_)*DB$`)

// redisDatabases is how many databases a default Redis server has (0–15).
const redisDatabases = 16

// TestEnvPlan is what a new workspace changes in its copied .env.testing.
type TestEnvPlan struct {
	Set       map[string]string // keys to set
	Databases []string          // test database names, sorted and unique
	Redis     []string          // "KEY=n" assignments, sorted
}

// PlanTestEnv gives a workspace its own test databases and Redis databases.
// Test databases are created on the fly and thrown away, so they are only
// renamed: every database name in .env.testing gets the workspace suffix,
// testing becoming testing_5194. Values that already end with the suffix,
// SQLite paths, :memory:, and ${...} references are left alone.
//
// Redis is shared by every checkout, and a test that flushes it would clear
// another checkout's data, so the workspace gets Redis database numbers that
// no other checkout's .env or .env.testing uses. Laravel's defaults (REDIS_DB
// 0, REDIS_CACHE_DB 1) count as used. Redis is only planned when the parent's
// .env or .env.testing mentions REDIS_.
func PlanTestEnv(testEnv, parentEnv map[string]string, suffix string, used map[int]bool) (TestEnvPlan, error) {
	suffix = strings.ReplaceAll(suffix, "-", "_")
	plan := TestEnvPlan{Set: map[string]string{}}
	seen := map[string]bool{}
	for _, key := range sortedKeys(testEnv) {
		value := testEnv[key]
		if !testDatabaseKey.MatchString(key) || !plainDatabaseName(value) || strings.HasSuffix(value, "_"+suffix) {
			continue
		}
		plan.Set[key] = value + "_" + suffix
		if !seen[plan.Set[key]] {
			seen[plan.Set[key]] = true
			plan.Databases = append(plan.Databases, plan.Set[key])
		}
	}
	sort.Strings(plan.Databases)

	keys := redisKeys(testEnv, parentEnv)
	if len(keys) == 0 {
		return plan, nil
	}
	numbers, err := freeRedisDatabases(used, len(keys))
	if err != nil {
		return plan, err
	}
	for i, key := range keys {
		plan.Set[key] = strconv.Itoa(numbers[i])
		plan.Redis = append(plan.Redis, fmt.Sprintf("%s=%d", key, numbers[i]))
	}
	return plan, nil
}

// redisKeys lists the Redis database keys to assign: REDIS_DB and
// REDIS_CACHE_DB, plus any other REDIS_*_DB the app sets. None when the app
// does not use Redis.
func redisKeys(envs ...map[string]string) []string {
	usesRedis := false
	set := map[string]bool{}
	for _, env := range envs {
		for key := range env {
			if strings.HasPrefix(key, "REDIS_") {
				usesRedis = true
			}
			if redisDBKey.MatchString(key) {
				set[key] = true
			}
		}
	}
	if !usesRedis {
		return nil
	}
	set["REDIS_DB"], set["REDIS_CACHE_DB"] = true, true
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// REDIS_DB first, so it gets the lower number of the pair.
	sort.SliceStable(keys, func(i, j int) bool { return keys[i] == "REDIS_DB" && keys[j] != "REDIS_DB" })
	return keys
}

// freeRedisDatabases picks n unused database numbers from the top down, the
// way they have been picked by hand (12 and 13, then 14 and 15), and returns
// them in ascending order.
func freeRedisDatabases(used map[int]bool, n int) ([]int, error) {
	var free []int
	for i := redisDatabases - 1; i >= 0 && len(free) < n; i-- {
		if !used[i] {
			free = append(free, i)
		}
	}
	if len(free) < n {
		return nil, fmt.Errorf("no free Redis databases: every checkout's .env and .env.testing together use all %d", redisDatabases)
	}
	sort.Ints(free)
	return free, nil
}

// UsedRedisDatabases collects the Redis database numbers set in the .env and
// .env.testing of each checkout. Laravel's defaults always count as used.
func UsedRedisDatabases(checkouts []string) map[int]bool {
	used := map[int]bool{0: true, 1: true}
	for _, dir := range checkouts {
		for _, name := range []string{".env", ".env.testing"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			for key, value := range ParseEnv(string(data)) {
				if n, err := strconv.Atoi(value); err == nil && redisDBKey.MatchString(key) {
					used[n] = true
				}
			}
		}
	}
	return used
}

func plainDatabaseName(v string) bool {
	return v != "" && v != ":memory:" && !strings.ContainsAny(v, "/\\$") && !strings.HasSuffix(v, ".sqlite")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SharedTestDatabases lists the test database keys a worktree's .env.testing
// sets to the same name as the parent's, as a copy made by hand would.
func SharedTestDatabases(parent, worktree map[string]string) []string {
	var shared []string
	for _, key := range sortedKeys(worktree) {
		value := worktree[key]
		if testDatabaseKey.MatchString(key) && plainDatabaseName(value) && parent[key] == value {
			shared = append(shared, key)
		}
	}
	return shared
}
