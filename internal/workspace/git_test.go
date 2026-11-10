package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCopyPlanCopiesOnlyWhatAWorkingCopyNeeds(t *testing.T) {
	ignored := []string{
		".env",
		".env.testing",
		".env.bak",
		"vendor/",
		"node_modules/",
		"nova-components/MerchantManager/node_modules/",
		"nova-components/MerchantManager/vendor/",
		"public/build/",
		"public/vendor/nova/",
		"public/hot",
		"public/storage",
		"bootstrap/cache/config.php",
		"storage/framework/views/abc.php",
		"storage/logs/laravel.log",
		"storage/debugbar/",
		"storage/app/fix-home-urls.json",
		"storage/oauth-private.key",
		".ai/AGENTS.local.md",
		".ai/triage/",
		"CLAUDE.local.md",
		".claude/worktrees/",
		".claude/scheduled_tasks.lock",
		"notebooks/.venv/",
		"docs/",
		"config/ray.php",
		"database/database.sqlite",
	}
	got := CopyPlan(ignored, []string{".claude/worktrees/login"}, []string{"database/database.sqlite"}, []string{"config/ray.php"})
	want := []string{
		".env", ".env.testing", "vendor/", "node_modules/",
		"nova-components/MerchantManager/node_modules/", "nova-components/MerchantManager/vendor/",
		"public/build/", "public/vendor/nova/", "public/storage", "storage/oauth-private.key",
		".ai/AGENTS.local.md", ".ai/triage/", "CLAUDE.local.md", "config/ray.php",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %#v\nwant %#v", got, want)
	}
}

func TestParseWorktreeListSkipsMain(t *testing.T) {
	out := strings.Join([]string{
		"worktree /code/shop",
		"HEAD aaa",
		"branch refs/heads/main",
		"",
		"worktree /code/shop-5194",
		"HEAD bbb",
		"branch refs/heads/feature/5194",
		"",
		"worktree /code/shop-detached",
		"HEAD ccc",
		"detached",
		"",
	}, "\n")
	got := parseWorktreeList(out, "/code/shop")
	want := []Worktree{
		{Path: "/code/shop-5194", Branch: "feature/5194", Head: "bbb"},
		{Path: "/code/shop-detached", Head: "ccc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("worktrees = %#v", got)
	}
}

// TestWorktreeRoundTrip runs real git: create a worktree, copy what the
// plan picks, and remove it again with ignored files present.
func TestWorktreeRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	base := t.TempDir()
	main := filepath.Join(base, "shop")
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(main, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "/vendor\n.env\n/public/hot\n/bootstrap/cache/*.php\n/data\n")
	write("public/index.php", "<?php")
	write("bootstrap/cache/.gitignore", "")
	gitIn(t, main, "init", "-q", "-b", "main")
	gitIn(t, main, "add", ".")
	gitIn(t, main, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init")
	write(".env", "APP_URL=https://shop.test\n")
	write("vendor/autoload.php", "<?php")
	write("public/hot", "http://[::1]:5173")
	// data/ holds only ignored files, so git reports it as one directory.
	write("data/app.sqlite", "db")
	write("data/keep.txt", "keep")
	write("bootstrap/cache/config.php", "<?php return ['path' => '"+main+"'];")
	// php artisan storage:link makes this absolute link.
	write("storage/app/public/.gitignore", "*\n!.gitignore\n")
	if err := os.Symlink(filepath.Join(main, "storage", "app", "public"), filepath.Join(main, "public", "storage")); err != nil {
		t.Fatal(err)
	}
	write(".gitignore", "/vendor\n.env\n/public/hot\n/bootstrap/cache/*.php\n/data\n/public/storage\n")
	gitIn(t, main, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qam", "ignore link")

	if got, err := MainRepo(filepath.Join(main, "public")); err != nil || got != main {
		t.Fatalf("MainRepo = %q, %v", got, err)
	}
	wt := filepath.Join(base, "shop-5194")
	if err := AddWorktree(main, wt, "5194", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := MainRepo(wt); err != nil || got != main {
		t.Fatalf("MainRepo from worktree = %q, %v", got, err)
	}
	ignored, err := IgnoredPaths(main)
	if err != nil {
		t.Fatal(err)
	}
	skip := []string{"data/app.sqlite"}
	if err := CopyPaths(main, wt, CopyPlan(ignored, nil, skip, []string{"data/"}), skip, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".env", "vendor/autoload.php", "data/keep.txt"} {
		if _, err := os.Stat(filepath.Join(wt, rel)); err != nil {
			t.Errorf("%s not copied: %v", rel, err)
		}
	}
	for _, rel := range []string{"public/hot", "bootstrap/cache/config.php", "data/app.sqlite"} {
		if _, err := os.Stat(filepath.Join(wt, rel)); !os.IsNotExist(err) {
			t.Errorf("%s copied, want skipped", rel)
		}
	}
	if target, err := os.Readlink(filepath.Join(wt, "public", "storage")); err != nil || target != filepath.Join(wt, "storage", "app", "public") {
		t.Errorf("public/storage -> %q, %v; want it re-pointed into the worktree", target, err)
	}

	wts, err := ListWorktrees(main)
	if err != nil || len(wts) != 1 || wts[0].Path != wt || wts[0].Branch != "5194" {
		t.Fatalf("worktrees = %#v, %v", wts, err)
	}
	if err := RemoveWorktree(main, wt, false); err != nil {
		t.Fatalf("remove with only ignored files: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
