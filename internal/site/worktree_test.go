package site

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeWorktree records wt as a linked worktree of repo the way git does,
// and gives both a PHP front controller.
func fakeWorktree(t *testing.T, repo, wt, gitdir string) {
	t.Helper()
	id := filepath.Base(wt)
	admin := filepath.Join(repo, ".git", "worktrees", id)
	mustWrite(t, filepath.Join(admin, "gitdir"), gitdir+"\n")
	mustWrite(t, filepath.Join(wt, ".git"), "gitdir: "+admin+"\n")
	mustWrite(t, filepath.Join(wt, "public", "index.php"), "<?php")
}

func fakeRepo(t *testing.T, repo string) {
	t.Helper()
	mustWrite(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	mustWrite(t, filepath.Join(repo, "public", "index.php"), "<?php")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func worktreeSites(t *testing.T, s *State) map[string]Resolved {
	t.Helper()
	out := map[string]Resolved{}
	for _, r := range s.Resolve() {
		out[r.Name] = r
	}
	return out
}

func TestWorktreeOfLinkedSiteGetsItsOwnSiteWithParentConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "shop")
	wt := filepath.Join(repo, ".claude", "worktrees", "login")
	fakeRepo(t, repo)
	fakeWorktree(t, repo, wt, filepath.Join(wt, ".git"))

	s := &State{Links: []Link{{Name: "shop", Path: repo, PHP: "8.3", Secure: true}}, DefaultPHP: "8.4"}
	sites := worktreeSites(t, s)

	r, ok := sites["shop-login"]
	if !ok {
		t.Fatalf("worktree site missing: %#v", sites)
	}
	if r.Path != wt || r.Docroot != filepath.Join(wt, "public") || r.Kind != KindPHP {
		t.Fatalf("worktree site = %#v", r)
	}
	if r.PHP != "8.3" || !r.Secure || r.WorktreeOf != "shop" {
		t.Fatalf("worktree site did not inherit parent config: %#v", r)
	}
}

func TestSiblingWorktreeInTrackedDirKeepsItsTrackedSite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	code := t.TempDir()
	repo := filepath.Join(code, "shop")
	wt := filepath.Join(code, "shop-5194")
	fakeRepo(t, repo)
	fakeWorktree(t, repo, wt, filepath.Join(wt, ".git"))

	s := &State{Parked: []string{code}}
	sites := worktreeSites(t, s)

	if len(sites) != 2 {
		t.Fatalf("sites = %#v, want shop and shop-5194 only", sites)
	}
	if r := sites["shop-5194"]; r.Path != wt || r.WorktreeOf != "" {
		t.Fatalf("tracked worktree site = %#v", r)
	}
}

func TestWorktreeSitesUseTheParentWithFewestLabels(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "shop")
	wt := filepath.Join(base, "elsewhere", "shop-login")
	fakeRepo(t, repo)
	fakeWorktree(t, repo, wt, filepath.Join(wt, ".git"))

	s := &State{Links: []Link{
		{Name: "app.shop", Path: repo, Secure: true},
		{Name: "shop", Path: repo, Secure: true},
		{Name: "api.shop", Path: repo, Secure: true},
	}}
	sites := worktreeSites(t, s)

	if _, ok := sites["shop-login"]; !ok {
		t.Fatalf("shop-login missing: %#v", sites)
	}
	for _, name := range []string{"app.shop-login", "api.shop-login", "shop-shop-login"} {
		if _, ok := sites[name]; ok {
			t.Fatalf("unexpected %s; wildcard hosts cover subdomains", name)
		}
	}
}

func TestWorktreeSitesSkipMissingFoldersAndIgnoredNames(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "shop")
	gone := filepath.Join(base, "shop-gone")
	ignored := filepath.Join(base, "shop-hidden")
	fakeRepo(t, repo)
	fakeWorktree(t, repo, gone, filepath.Join(gone, ".git"))
	fakeWorktree(t, repo, ignored, filepath.Join(ignored, ".git"))
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	s := &State{Links: []Link{{Name: "shop", Path: repo, Secure: true}}, Ignored: []string{"shop-hidden"}}
	sites := worktreeSites(t, s)

	if len(sites) != 1 {
		t.Fatalf("sites = %#v, want only shop", sites)
	}
}

func TestWorktreeSitesReadRelativeGitdirPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "shop")
	wt := filepath.Join(base, "shop-login")
	fakeRepo(t, repo)
	// git worktree.useRelativePaths records the path from the admin dir.
	fakeWorktree(t, repo, wt, filepath.Join("..", "..", "..", "..", "shop-login", ".git"))

	s := &State{Links: []Link{{Name: "shop", Path: repo, Secure: true}}}
	if r, ok := worktreeSites(t, s)["shop-login"]; !ok || r.Path != wt {
		t.Fatalf("shop-login = %#v, %v", r, ok)
	}
}

func TestWorktreeSitesRebaseAbsoluteRootsInsideTheRepo(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "shop")
	wt := filepath.Join(base, "shop-login")
	fakeRepo(t, repo)
	fakeWorktree(t, repo, wt, filepath.Join(wt, ".git"))

	s := &State{Links: []Link{{Name: "shop", Path: repo, Root: filepath.Join(repo, "public"), Secure: true}}}
	if r := worktreeSites(t, s)["shop-login"]; r.Docroot != filepath.Join(wt, "public") {
		t.Fatalf("docroot = %q, want the worktree's public dir", r.Docroot)
	}
}

func TestResolvePathInsideWorktreePicksTheWorktreeSite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := t.TempDir()
	repo := filepath.Join(base, "shop")
	wt := filepath.Join(repo, ".claude", "worktrees", "login")
	fakeRepo(t, repo)
	fakeWorktree(t, repo, wt, filepath.Join(wt, ".git"))

	s := &State{Links: []Link{{Name: "shop", Path: repo, PHP: "8.3", Secure: true}}}
	matches := s.ResolvePath(filepath.Join(wt, "public"))
	if len(matches) != 1 || matches[0].Name != "shop-login" || matches[0].PHP != "8.3" {
		t.Fatalf("matches = %#v", matches)
	}
}

func TestWorktreeSuffixStripsTheRepoFolderName(t *testing.T) {
	cases := map[string]string{
		"/code/shop-5194":                    "5194",
		"/code/shop/.claude/worktrees/login": "login",
		"/tmp/Fix_Login Page":                "fix-login-page",
		"/code/shopping":                     "shopping",
	}
	for wt, want := range cases {
		if got := WorktreeSuffix("/code/shop", wt); got != want {
			t.Errorf("WorktreeSuffix(%q) = %q, want %q", wt, got, want)
		}
	}
}
