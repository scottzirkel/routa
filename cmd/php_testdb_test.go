package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestIsTestRun(t *testing.T) {
	cases := map[bool][][]string{
		true: {
			{"artisan", "test"},
			{"artisan", "test", "--parallel"},
			{"-d", "memory_limit=1G", "artisan", "test"},
			{"vendor/bin/pest", "--filter", "Login"},
			{"vendor/bin/phpunit"},
			{"/code/shop/vendor/phpunit/phpunit/phpunit", "--configuration", "phpunit.xml"},
			{"vendor/bin/paratest"},
			{"-f", "vendor/bin/pest"},
			{"/usr/bin/composer", "test"},
			{"/usr/bin/composer", "run-script", "test"},
		},
		false: {
			{"artisan", "migrate"},
			{"artisan", "tinker"},
			{"artisan"},
			{"-r", "echo 1;"},
			{"--", "--install-dir=/tmp"},
			{"/usr/bin/composer", "install"},
			{"/usr/bin/composer", "run", "lint"},
			{"-v"},
		},
	}
	for want, list := range cases {
		for _, args := range list {
			if got := isTestRun(args); got != want {
				t.Errorf("isTestRun(%q) = %v, want %v", args, got, want)
			}
		}
	}
}

func TestWorktreeTestEnvKeepsVariablesTheCallerSet(t *testing.T) {
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-5194")
	admin := filepath.Join(main, ".git", "worktrees", "shop-5194")
	writeTestFile(t, filepath.Join(admin, "gitdir"), filepath.Join(wt, ".git"))
	writeTestFile(t, filepath.Join(wt, ".git"), "gitdir: "+admin+"\n")
	writeTestFile(t, filepath.Join(wt, "phpunit.xml"), `<phpunit><php><env name="DB_DATABASE" value="testing"/><env name="DB_METRICS_DATABASE" value="metrics"/></php></phpunit>`)

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(wt); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DB_METRICS_DATABASE", "mine")
	env := worktreeTestEnv()
	if !slices.Equal(env, []string{"DB_DATABASE=testing_5194"}) {
		t.Fatalf("env = %#v", env)
	}
}
