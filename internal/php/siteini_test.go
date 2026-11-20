package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottzirkel/routa/internal/site"
)

func renderedPool(t *testing.T, conf, pool string) string {
	t.Helper()
	start := strings.Index(conf, "["+pool+"]")
	if start < 0 {
		t.Fatalf("pool %s missing:\n%s", pool, conf)
	}
	rest := conf[start+len(pool)+2:]
	if end := strings.Index(rest, "\n["); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func TestSiteSettingsReachEveryNameServedFromTheProject(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	app := filepath.Join(t.TempDir(), "app")
	other := filepath.Join(t.TempDir(), "other")
	for _, dir := range []string{app, other} {
		if err := os.MkdirAll(filepath.Join(dir, "public"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "public", "index.php"), []byte("<?php"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := site.Save(&site.State{
		DefaultPHP: "8.5",
		Links: []site.Link{
			{Name: "app", Path: app, Root: "public"},
			{Name: "api.app", Path: app, Root: "public"},
			{Name: "other", Path: other, Root: "public"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := SetINISetting("8.5", "upload_max_filesize", "20M"); err != nil {
		t.Fatal(err)
	}
	if err := SetSiteINISetting(app, "upload_max_filesize", "200M"); err != nil {
		t.Fatal(err)
	}
	if err := SetSiteINISetting(app, "max_execution_time", "120"); err != nil {
		t.Fatal(err)
	}

	if err := WriteFPMConfig("8.5"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "php-fpm-8.5.conf"))
	if err != nil {
		t.Fatal(err)
	}
	conf := string(data)
	for _, pool := range []string{"routa-app", "routa-api.app"} {
		body := renderedPool(t, conf, pool)
		if !strings.Contains(body, "php_admin_value[upload_max_filesize] = 200M") || !strings.Contains(body, "php_admin_value[max_execution_time] = 120") {
			t.Fatalf("%s did not get the project's settings:\n%s", pool, body)
		}
		if strings.Count(body, "php_admin_value[upload_max_filesize]") != 1 {
			t.Fatalf("%s sets upload_max_filesize twice:\n%s", pool, body)
		}
	}
	if body := renderedPool(t, conf, "routa-other"); !strings.Contains(body, "php_admin_value[upload_max_filesize] = 20M") || strings.Contains(body, "max_execution_time") {
		t.Fatalf("another project picked up app's settings:\n%s", body)
	}

	st, _ := site.Load()
	if got := strings.Join(SitesUsingProject(app, st.Resolve()), ","); got != "api.app,app" {
		t.Fatalf("SitesUsingProject = %s", got)
	}
}

func TestWorktreeInheritsItsParentProjectsSettings(t *testing.T) {
	sites := []site.Resolved{
		{Name: "app", Path: "/code/app", Kind: site.KindPHP},
		{Name: "app-42", Path: "/code/app-worktrees/42", Kind: site.KindPHP, WorktreeOf: "app"},
	}
	got := SiteProjectPaths(sites[1], sites)
	if strings.Join(got, ",") != "/code/app,/code/app-worktrees/42" {
		t.Fatalf("SiteProjectPaths = %v", got)
	}
	if names := strings.Join(SitesUsingProject("/code/app", sites), ","); names != "app,app-42" {
		t.Fatalf("SitesUsingProject = %s", names)
	}
}

func TestSiteSettingsRejectExtensions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	for _, key := range []string{ExtensionKey, ZendExtensionKey} {
		if err := SetSiteINISetting("/code/app", key, "/opt/x.so"); err == nil || !strings.Contains(err.Error(), "conf.d") {
			t.Fatalf("%s: err = %v, want a pointer to conf.d", key, err)
		}
	}
}

func TestSiteINIPathKeepsSameNamedProjectsApart(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	a, b := SiteINIPath("/code/app"), SiteINIPath("/clients/app")
	if a == b {
		t.Fatalf("two projects named app share %s", a)
	}
	if !strings.HasPrefix(filepath.Base(a), "app-") || SiteINIPath("/code/app/") != a {
		t.Fatalf("SiteINIPath = %s", a)
	}
}
