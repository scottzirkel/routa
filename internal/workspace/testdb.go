package workspace

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/scottzirkel/routa/internal/site"
)

// testDatabaseKey matches the .env keys that name a database: DB_DATABASE
// and per-connection ones such as DB_METRICS_DATABASE.
var testDatabaseKey = regexp.MustCompile(`^DB_(?:[A-Z0-9]+_)*DATABASE$`)

// TestDatabases is what a linked worktree's test runs should use instead of
// the test database names it shares with every other checkout.
type TestDatabases struct {
	Env    map[string]string // variable -> database name with the worktree suffix
	Forced []string          // phpunit.xml variables with force="true", which routa cannot override
}

// TestDatabasesFor returns the test database names for the linked worktree
// that contains dir. Test databases are created on the fly and thrown away,
// so they are not cloned; they only need the worktree's suffix so two
// checkouts never run tests against the same database. Names come from
// phpunit.xml (or phpunit.xml.dist) and .env.testing. Values that are SQLite
// paths, :memory:, or ${...} references are left alone. The main checkout,
// and any directory outside a linked worktree, gets nothing.
func TestDatabasesFor(dir string) (TestDatabases, bool) {
	root, main, ok := linkedWorktree(dir)
	if !ok {
		return TestDatabases{}, false
	}
	suffix := strings.ReplaceAll(site.WorktreeSuffix(main, root), "-", "_")
	if suffix == "" {
		return TestDatabases{}, false
	}
	out := TestDatabases{Env: map[string]string{}}
	add := func(key, value string) {
		if _, seen := out.Env[key]; seen || !testDatabaseKey.MatchString(key) || !plainDatabaseName(value) {
			return
		}
		out.Env[key] = value + "_" + suffix
	}
	vars := phpunitEnv(root)
	for _, v := range vars {
		add(v.Name, v.Value)
		if v.Force == "true" && testDatabaseKey.MatchString(v.Name) && plainDatabaseName(v.Value) {
			out.Forced = append(out.Forced, v.Name)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, ".env.testing")); err == nil {
		env := ParseEnv(string(data))
		keys := make([]string, 0, len(env))
		for k := range env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			add(k, env[k])
		}
	}
	return out, len(out.Env) > 0
}

func plainDatabaseName(v string) bool {
	return v != "" && v != ":memory:" && !strings.ContainsAny(v, "/\\$") && !strings.HasSuffix(v, ".sqlite")
}

type phpunitVar struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
	Force string `xml:"force,attr"`
}

// phpunitEnv reads the <env> and <server> variables PHPUnit sets, from the
// config file PHPUnit itself would pick.
func phpunitEnv(root string) []phpunitVar {
	for _, name := range []string{"phpunit.xml", "phpunit.xml.dist"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		var cfg struct {
			PHP struct {
				Env    []phpunitVar `xml:"env"`
				Server []phpunitVar `xml:"server"`
			} `xml:"php"`
		}
		if xml.Unmarshal(data, &cfg) != nil {
			return nil
		}
		return append(cfg.PHP.Env, cfg.PHP.Server...)
	}
	return nil
}

// linkedWorktree finds the linked worktree containing dir by walking up to
// the nearest .git entry. A linked worktree has a .git file pointing into
// the main repository's .git/worktrees; the main checkout has a directory.
func linkedWorktree(dir string) (root, main string, ok bool) {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		info, err := os.Lstat(filepath.Join(d, ".git"))
		if err == nil {
			if info.IsDir() {
				return "", "", false
			}
			data, err := os.ReadFile(filepath.Join(d, ".git"))
			if err != nil {
				return "", "", false
			}
			gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
			if !filepath.IsAbs(gitdir) {
				gitdir = filepath.Join(d, gitdir)
			}
			// <main>/.git/worktrees/<id>
			worktrees := filepath.Dir(filepath.Clean(gitdir))
			common := filepath.Dir(worktrees)
			if filepath.Base(worktrees) != "worktrees" || filepath.Base(common) != ".git" {
				return "", "", false
			}
			return d, filepath.Dir(common), true
		}
		if parent := filepath.Dir(d); parent == d {
			return "", "", false
		}
	}
}
