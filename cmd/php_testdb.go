package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/scottzirkel/routa/internal/workspace"
)

// isTestRun reports whether a `routa php` command line starts a test
// runner: phpunit, pest, paratest, `artisan test`, or `composer test`.
func isTestRun(args []string) bool {
	script, rest := phpScript(args)
	switch filepath.Base(script) {
	case "phpunit", "pest", "paratest":
		return true
	case "artisan":
		return firstCommand(rest) == "test"
	case "composer", "composer.phar":
		return composerRunsTests(rest)
	}
	return false
}

// phpScript finds the script php would run and the arguments after it,
// skipping php's own options. Code from stdin has no script.
func phpScript(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			return "", nil
		case a == "-f":
			if i+1 < len(args) {
				return args[i+1], args[i+2:]
			}
			return "", nil
		case a == "-c" || a == "-d" || a == "-z":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

// composerRunsTests reports whether composer arguments run the "test"
// script, as `composer test` or `composer run[-script] test`.
func composerRunsTests(args []string) bool {
	var words []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-d":
			i++
		case strings.HasPrefix(args[i], "-"):
		default:
			words = append(words, args[i])
		}
	}
	if len(words) == 0 {
		return false
	}
	if words[0] == "test" {
		return true
	}
	return (words[0] == "run" || words[0] == "run-script") && len(words) > 1 && words[1] == "test"
}

func firstCommand(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// worktreeTestEnv gives a test run inside a linked worktree its own test
// databases, such as DB_DATABASE=testing_5194. PHPUnit and Laravel's
// .env.testing never overwrite a variable that is already set, so no tracked
// file changes; Laravel's parallel databases then become testing_5194_test_1.
// Variables the caller already set win.
func worktreeTestEnv() []string {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	dbs, ok := workspace.TestDatabasesFor(cwd)
	if !ok {
		return nil
	}
	for _, key := range dbs.Forced {
		fmt.Fprintf(os.Stderr, "routa: phpunit.xml sets %s with force=\"true\", so this worktree's tests use the same database as other checkouts\n", key)
	}
	keys := make([]string, 0, len(dbs.Env))
	for k := range dbs.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var env []string
	for _, k := range keys {
		if _, set := os.LookupEnv(k); !set {
			env = append(env, k+"="+dbs.Env[k])
		}
	}
	return env
}
