package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// testDatabaseKey matches the .env keys that name a database: DB_DATABASE
// and per-connection ones such as DB_SINGLESTORE_DATABASE.
var testDatabaseKey = regexp.MustCompile(`^DB_(?:[A-Z0-9]+_)*DATABASE$`)

// redisDBKey matches the .env keys that pick a Redis database number, such
// as REDIS_DB and REDIS_CACHE_DB.
var redisDBKey = regexp.MustCompile(`^REDIS_(?:[A-Z0-9]+_)*DB$`)

// redisPrefixKey matches the .env keys that set a Redis key prefix, such as
// REDIS_PREFIX and REDIS_CACHE_PREFIX.
var redisPrefixKey = regexp.MustCompile(`^REDIS_(?:[A-Z0-9]+_)*PREFIX$`)

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

// RedisPlan is the Redis settings a new workspace gets, for its app (.env)
// and for its tests (.env.testing): key prefixes for a Laravel app, database
// numbers for anything else.
type RedisPlan struct {
	App, Tests map[string]string
}

// Summary lists one file's assignments, REDIS_DB and REDIS_PREFIX first.
func (p RedisPlan) Summary(set map[string]string) string {
	keys := redisKeysOf(set)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + set[k]
	}
	return strings.Join(parts, ", ")
}

// PlanRedis gives a workspace that is not a Laravel app Redis databases of
// its own, since such an app does not read REDIS_PREFIX. Laravel apps get
// prefixes instead; see PlanRedisPrefixes. The workspace's app and its tests
// each get REDIS_DB and REDIS_CACHE_DB (plus any other REDIS_*_DB the parent
// sets) with numbers no other checkout uses, picked from the top down.
// Nothing is planned when the parent's .env and .env.testing never mention
// REDIS_. withTests is false when the workspace has no .env.testing to edit.
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

// redisKeysOf sorts keys with REDIS_DB first, so it gets the lowest number,
// and REDIS_PREFIX first among prefixes.
func redisKeysOf(set map[string]string) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	first := func(k string) bool { return k == "REDIS_DB" || k == "REDIS_PREFIX" }
	sort.SliceStable(keys, func(i, j int) bool { return first(keys[i]) && !first(keys[j]) })
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
	for n := range RedisDatabaseOwners(checkouts) {
		used[n] = true
	}
	return used
}

// RedisDatabaseOwners maps each Redis database number set in the .env and
// .env.testing of the checkouts to the first checkout that sets it.
// Laravel's defaults, which a checkout uses without setting them, are not
// included; UsedRedisDatabases adds them.
func RedisDatabaseOwners(checkouts []string) map[int]string {
	owners := map[int]string{}
	for _, dir := range checkouts {
		for _, name := range []string{".env", ".env.testing"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			for key, value := range ParseEnv(string(data)) {
				if n, err := strconv.Atoi(value); err == nil && redisDBKey.MatchString(key) {
					if _, seen := owners[n]; !seen {
						owners[n] = dir
					}
				}
			}
		}
	}
	return owners
}

// IsRedisDatabaseKey reports whether a .env key picks a Redis database
// number, such as REDIS_DB or REDIS_CACHE_DB.
func IsRedisDatabaseKey(key string) bool { return redisDBKey.MatchString(key) }

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
// REDIS_CACHE_DB 1) for keys a file leaves out. With byPrefix, for a Laravel
// app, the worktree only shares them when its REDIS_PREFIX, or Laravel's
// default for it, is the parent's too; its own prefix keeps its keys apart.
// Nothing is shared when the parent does not use Redis.
func SharedRedisDatabases(parent, worktree map[string]string, byPrefix bool) []string {
	if !mentionsRedis(parent) {
		return nil
	}
	if byPrefix {
		p, _ := EffectiveRedisPrefix(parent, "REDIS_PREFIX")
		w, _ := EffectiveRedisPrefix(worktree, "REDIS_PREFIX")
		if p != w {
			return nil
		}
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

// Laravel is what a checkout's composer.json says about Laravel.
type Laravel struct {
	// App is true for a Laravel app: an artisan file and laravel/framework.
	// Such an app reads REDIS_PREFIX, so workspaces can share its Redis
	// databases and stay apart by key prefix.
	App bool
	// Horizon is true when composer.json requires laravel/horizon, which
	// keeps its own HORIZON_PREFIX.
	Horizon bool
}

// DetectLaravel reads dir's artisan file and composer.json, counting both
// require and require-dev.
func DetectLaravel(dir string) Laravel {
	data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return Laravel{}
	}
	var composer struct {
		Require    map[string]any `json:"require"`
		RequireDev map[string]any `json:"require-dev"`
	}
	if json.Unmarshal(data, &composer) != nil {
		return Laravel{}
	}
	requires := func(pkg string) bool {
		_, a := composer.Require[pkg]
		_, b := composer.RequireDev[pkg]
		return a || b
	}
	_, err = os.Stat(filepath.Join(dir, "artisan"))
	return Laravel{App: err == nil && requires("laravel/framework"), Horizon: requires("laravel/horizon")}
}

// PlanRedisPrefixes gives a Laravel workspace key prefixes of its own, in
// .env for its app and in .env.testing for its tests. The workspace keeps the
// parent's Redis database numbers. Each file gets REDIS_PREFIX, HORIZON_PREFIX
// when the app uses Horizon or the parent's file sets it, and any other
// REDIS_*_PREFIX the parent's file sets, each made from the parent's value in
// that file (see WorkspaceRedisPrefix). Nothing is planned when the parent's
// .env and .env.testing never mention REDIS_. withTests is false when the
// workspace has no .env.testing to edit.
func PlanRedisPrefixes(parentEnv, testEnv map[string]string, withTests, horizon bool, name string) RedisPlan {
	plan := RedisPlan{App: map[string]string{}, Tests: map[string]string{}}
	if !mentionsRedis(parentEnv) && !mentionsRedis(testEnv) {
		return plan
	}
	plan.App = WorkspaceRedisPrefixes(parentEnv, horizon, name)
	if withTests {
		plan.Tests = WorkspaceRedisPrefixes(testEnv, horizon, name)
	}
	return plan
}

// WorkspaceRedisPrefixes is the prefix keys a workspace named name sets in
// one file, made from the parent's copy of that file.
func WorkspaceRedisPrefixes(parent map[string]string, horizon bool, name string) map[string]string {
	set := map[string]string{}
	for _, key := range RedisPrefixKeys(parent, horizon) {
		value, _ := EffectiveRedisPrefix(parent, key)
		set[key] = WorkspaceRedisPrefix(value, name)
	}
	return set
}

// RedisPrefixKeys lists the prefix keys a file uses: REDIS_PREFIX,
// HORIZON_PREFIX when the app uses Horizon or the file sets it, and any other
// REDIS_*_PREFIX the file sets, REDIS_PREFIX first.
func RedisPrefixKeys(env map[string]string, horizon bool) []string {
	set := map[string]string{"REDIS_PREFIX": ""}
	if _, ok := env["HORIZON_PREFIX"]; ok || horizon {
		set["HORIZON_PREFIX"] = ""
	}
	for key := range env {
		if redisPrefixKey.MatchString(key) {
			set[key] = ""
		}
	}
	return redisKeysOf(set)
}

// WorkspaceRedisPrefix makes a workspace's prefix from the parent's. A Redis
// cluster hash tag, a value wrapped in braces, gets the name inside them, so
// {af} becomes {af-5382}. Any other value gets the name in front, so
// laravel-database- becomes 5382_laravel-database-: no other checkout's keys
// can then start with the workspace's prefix.
func WorkspaceRedisPrefix(parent, name string) string {
	if len(parent) >= 2 && parent[0] == '{' && parent[len(parent)-1] == '}' {
		return parent[:len(parent)-1] + "-" + name + "}"
	}
	return name + "_" + parent
}

// EffectiveRedisPrefix is the prefix a Laravel app uses for key: the file's
// value, where null and (null) mean no prefix, or else Laravel's default made
// from the file's APP_NAME (REDIS_PREFIX and HORIZON_PREFIX only). ok is
// false for another key the file does not set.
func EffectiveRedisPrefix(env map[string]string, key string) (prefix string, ok bool) {
	if v, set := env[key]; set {
		switch strings.ToLower(v) {
		case "null", "(null)", "empty", "(empty)":
			return "", true
		}
		return v, true
	}
	app := env["APP_NAME"]
	if app == "" {
		app = "laravel"
	}
	switch key {
	case "REDIS_PREFIX":
		return laravelSlug(app, '-') + "-database-", true
	case "HORIZON_PREFIX":
		return laravelSlug(app, '_') + "_horizon:", true
	}
	return "", false
}

// laravelSlug is close to Laravel's Str::slug, enough to name the default
// prefixes: lower case, @ as "at", anything but letters, digits and the
// separator dropped, runs of separators and spaces as one separator.
func laravelSlug(s string, sep rune) string {
	flip := '_'
	if sep == '_' {
		flip = '-'
	}
	s = strings.ReplaceAll(strings.ToLower(s), "@", string(sep)+"at"+string(sep))
	var b strings.Builder
	pending := false
	for _, r := range s {
		switch {
		case r == sep || r == flip || unicode.IsSpace(r):
			pending = b.Len() > 0
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if pending {
				b.WriteRune(sep)
				pending = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// RedisSpace is one Redis database and key prefix an app's keys go under.
type RedisSpace struct {
	DB     int
	Prefix string
}

// RedisSpaces lists where a Laravel app's keys go for one file: every
// database its REDIS_*_DB keys resolve to, counting Laravel's defaults,
// paired with the file's value, or Laravel's default, for each prefix key.
// Numbers that are not plain numbers are left out.
func RedisSpaces(env map[string]string, prefixKeys []string) []RedisSpace {
	var out []RedisSpace
	for _, db := range RedisDatabasesOf(env) {
		for _, key := range prefixKeys {
			if prefix, ok := EffectiveRedisPrefix(env, key); ok {
				out = append(out, RedisSpace{DB: db, Prefix: prefix})
			}
		}
	}
	return out
}

// RedisDatabasesOf lists the database numbers a file's REDIS_*_DB keys
// resolve to, counting Laravel's defaults 0 and 1, sorted and unique.
func RedisDatabasesOf(env map[string]string) []int {
	seen := map[int]bool{}
	var out []int
	for _, key := range redisKeys(env) {
		if n, ok := EffectiveRedisDatabase(env, key); ok && n >= 0 && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// MentionsRedis reports whether a .env file sets any REDIS_ key.
func MentionsRedis(env map[string]string) bool { return mentionsRedis(env) }
