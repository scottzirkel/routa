package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottzirkel/routa/internal/php"
	"github.com/scottzirkel/routa/internal/site"
)

func withPHPINISite(t *testing.T, name string) {
	t.Helper()
	old := phpINISite
	phpINISite = name
	t.Cleanup(func() { phpINISite = old })
}

func TestPHPINISetAndUnsetTakeOneArgumentFewerWithSite(t *testing.T) {
	withPHPINISite(t, "")
	if err := phpINISetCmd.Args(phpINISetCmd, []string{"memory_limit", "1G"}); err == nil {
		t.Fatal("set without --site accepted no version")
	}
	if err := phpINIUnsetCmd.Args(phpINIUnsetCmd, []string{"memory_limit"}); err == nil {
		t.Fatal("unset without --site accepted no version")
	}

	withPHPINISite(t, "app")
	if err := phpINISetCmd.Args(phpINISetCmd, []string{"memory_limit", "1G"}); err != nil {
		t.Fatalf("set --site: %v", err)
	}
	if err := phpINIUnsetCmd.Args(phpINIUnsetCmd, []string{"memory_limit"}); err != nil {
		t.Fatalf("unset --site: %v", err)
	}
	for _, c := range []string{"show", "path", "set", "unset", "edit"} {
		sub, _, err := phpINICmd.Find([]string{c})
		if err != nil || sub.Flags().Lookup("site") == nil {
			t.Fatalf("routa php ini %s has no --site flag", c)
		}
	}
}

func siteINIFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	app := filepath.Join(t.TempDir(), "app")
	docs := filepath.Join(t.TempDir(), "docs")
	if err := os.MkdirAll(filepath.Join(app, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "public", "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "index.html"), []byte("<p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := site.Save(&site.State{
		DefaultPHP: "8.5",
		Links: []site.Link{
			{Name: "app", Path: app, Root: "public"},
			{Name: "api.app", Path: app, Root: "public"},
			{Name: "docs", Path: docs},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestResolveSiteProjectFindsPHPSitesOnly(t *testing.T) {
	app := siteINIFixture(t)

	r, _, err := resolveSiteProject("api.app.test")
	if err != nil {
		t.Fatal(err)
	}
	if r.Path != app {
		t.Fatalf("api.app resolved to %s, want %s", r.Path, app)
	}
	if _, _, err := resolveSiteProject("docs"); err == nil || !strings.Contains(err.Error(), "only apply to PHP sites") {
		t.Fatalf("static site: err = %v", err)
	}
	if _, _, err := resolveSiteProject("nope"); err == nil {
		t.Fatal("unknown site accepted")
	}
}

func TestShowSiteINIListsSettingsAndTheSitesTheyReach(t *testing.T) {
	app := siteINIFixture(t)
	if err := php.SetSiteINISetting(app, "upload_max_filesize", "200M"); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := showSiteINI(&out, "app"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{php.SiteINIPath(app), "applies to api.app, app", "upload_max_filesize", "200M"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("show --site output missing %q:\n%s", want, out.String())
		}
	}
}

func TestPrintCopyReportNamesTurnedOffExtensions(t *testing.T) {
	var out bytes.Buffer
	changed := printCopyReport(&out, "8.5", "8.6", php.CopyReport{
		Settings:  []string{"memory_limit"},
		TurnedOff: []string{"mongo.ini: extension = /opt/mongo.so"},
		ConfFiles: []string{"mongo.ini"},
	})
	if !changed {
		t.Fatal("report with changes said nothing changed")
	}
	for _, want := range []string{"carried PHP 8.5 settings over to 8.6: memory_limit", "copied conf.d/mongo.ini", "rebuild them for PHP 8.6", "/opt/mongo.so"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q:\n%s", want, out.String())
		}
	}
}
