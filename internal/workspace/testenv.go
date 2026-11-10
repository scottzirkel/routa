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

// TestEnvPlan is what a new workspace changes in its copied .env.testing to
// get its own test databases.
type TestEnvPlan struct {
	Set       map[string]string // keys to set
	Databases []string          // test database names, sorted and unique
}

// PlanTestDatabases gives a workspace its own test databases. They are
// created on the fly and thrown away, so they are only renamed: every
// database name in .env.testing gets the workspace suffix, testing becoming
// testing_5194. Values that already end with the suffix, SQLite paths,
// :memory:, and ${...} references are left alone.
func PlanTestDatabases(testEnv map[string]string, suffix string) TestEnvPlan {
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
	return plan
}

// RedisPlan is the Redis database numbers a new workspace gets, for its app
// (.env) and for its tests (.env.testing).
type RedisPlan struct {
	App, Tests map[string]string
}

// Summary lists one file's assignments, REDIS_DB first.
func (p RedisPlan) Summary(set map[string]string) string {
	keys := redisKeysOf(set)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + set[k]
	}
	return strings.Join(parts, ", ")
}

// PlanRedis gives a workspace Redis databases of its own. Every checkout
// shares one Redis server, and a flush, such as cache:clear with a Redis
// cache or a test that clears the cache, empties a whole database. So the
// workspace's app and its tests each get REDIS_DB and REDIS_CACHE_DB (plus any
// other REDIS_*_DB the parent sets) with numbers no other checkout uses,
// picked from the top down. Nothing is planned when the parent's .env and
// .env.testing never mention REDIS_. withTests is false when the workspace
// has no .env.testing to edit.
func PlanRedis(parentEnv, testEnv map[string]string, withTests bool, used map[int]bool, databases int) (RedisPlan, error) {
	plan := RedisPlan{App: map[string]string{}, Tests: map[string]string{}}
	if !mentionsRedis(parentEnv) && !mentionsRedis(testEnv) {
		return plan, nil
	}
	appKeys := redisKeys(parentEnv)
	var testKeys []string
	if withTests {
		testKeys = redisKeys(testEnv, parentEnv)
	}
	numbers, err := freeRedisDatabases(used, len(appKeys)+len(testKeys), databases)
	if err != nil {
		return plan, err
	}
	for i, key := range appKeys {
		plan.App[key] = strconv.Itoa(numbers[i])
	}
	for i, key := range testKeys {
		plan.Tests[key] = strconv.Itoa(numbers[len(appKeys)+i])
	}
	return plan, nil
}

func mentionsRedis(env map[string]string) bool {
	for key := range env {
		if strings.HasPrefix(key, "REDIS_") {
			return true
		}
	}
	return false
}

// redisKeys lists the Redis database keys to assign: REDIS_DB and
// REDIS_CACHE_DB, plus any other REDIS_*_DB the given files set.
func redisKeys(envs ...map[string]string) []string {
	set := map[string]string{"REDIS_DB": "", "REDIS_CACHE_DB": ""}
	for _, env := range envs {
		for key := range env {
			if redisDBKey.MatchString(key) {
				set[key] = ""
			}
		}
	}
	return redisKeysOf(set)
}

// redisKeysOf sorts keys with REDIS_DB first, so it gets the lowest number.
func redisKeysOf(set map[string]string) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sort.SliceStable(keys, func(i, j int) bool { return keys[i] == "REDIS_DB" && keys[j] != "REDIS_DB" })
	return keys
}

// freeRedisDatabases picks n unused database numbers below databases, from
// the top down the way they were picked by hand, in ascending order.
func freeRedisDatabases(used map[int]bool, n, databases int) ([]int, error) {
	var free []int
	for i := databases - 1; i >= 0 && len(free) < n; i-- {
		if !used[i] {
			free = append(free, i)
		}
	}
	if len(free) < n {
		return nil, fmt.Errorf("no free Redis databases: the checkouts' .env and .env.testing files already use the server's %d; routa's own Redis has 64 after 'routa redis restart'", databases)
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

// SharedRedisDatabases lists the Redis database keys a worktree's .env sets
// to the same number as the parent's, counting Laravel's defaults (REDIS_DB 0,
// REDIS_CACHE_DB 1) for keys a file leaves out. A key prefix does not count:
// a flush empties the whole database. Nothing is shared when the parent does
// not use Redis.
func SharedRedisDatabases(parent, worktree map[string]string) []string {
	if !mentionsRedis(parent) {
		return nil
	}
	defaults := map[string]string{"REDIS_DB": "0", "REDIS_CACHE_DB": "1"}
	value := func(env map[string]string, key string) string {
		if v, ok := env[key]; ok && v != "" {
			return v
		}
		return defaults[key]
	}
	var shared []string
	for _, key := range redisKeys(parent, worktree) {
		if p := value(parent, key); p != "" && p == value(worktree, key) {
			shared = append(shared, key)
		}
	}
	return shared
}

// IsTestDatabaseKey reports whether a .env key names a database, such as
// DB_DATABASE or DB_SINGLESTORE_DATABASE.
func IsTestDatabaseKey(key string) bool { return testDatabaseKey.MatchString(key) }

// PlainDatabaseName reports whether a value is a database name rather than
// a SQLite path, :memory:, or a ${...} reference.
func PlainDatabaseName(v string) bool { return plainDatabaseName(v) }

// RedisKeys lists the Redis database keys a set of .env files uses: REDIS_DB
// and REDIS_CACHE_DB, plus any other REDIS_*_DB they set, REDIS_DB first.
func RedisKeys(envs ...map[string]string) []string { return redisKeys(envs...) }

// FreeRedisDatabases picks n database numbers that are not in used.
func FreeRedisDatabases(used map[int]bool, n, databases int) ([]int, error) {
	return freeRedisDatabases(used, n, databases)
}

// EffectiveRedisDatabase is the number a key resolves to, counting Laravel's
// defaults (REDIS_DB 0, REDIS_CACHE_DB 1) when the file leaves it out.
func EffectiveRedisDatabase(env map[string]string, key string) (int, bool) {
	v := env[key]
	if v == "" {
		v = map[string]string{"REDIS_DB": "0", "REDIS_CACHE_DB": "1"}[key]
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}
