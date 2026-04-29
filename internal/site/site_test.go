package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLoadMissingStateUsesCurrentVersion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	state, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != CurrentStateVersion {
		t.Fatalf("version = %d, want %d", state.Version, CurrentStateVersion)
	}
}

func TestLoadLegacyStateWithoutVersionMigratesToCurrentVersion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeStateFile(t, `{
  "parked": ["/code"],
  "links": [{"name": "app", "path": "/code/app", "secure": true}],
  "default_php": "8.4"
}`)

	state, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != CurrentStateVersion {
		t.Fatalf("version = %d, want %d", state.Version, CurrentStateVersion)
	}
	if len(state.Parked) != 1 || state.Parked[0] != "/code" {
		t.Fatalf("parked dirs not preserved: %#v", state.Parked)
	}
	if len(state.Links) != 1 || state.Links[0].Name != "app" {
		t.Fatalf("links not preserved: %#v", state.Links)
	}
	if state.DefaultPHP != "8.4" {
		t.Fatalf("default PHP = %q, want 8.4", state.DefaultPHP)
	}
}

func TestLoadRejectsFutureStateVersion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeStateFile(t, `{"version": 999, "parked": [], "links": []}`)

	_, err := Load()
	if err == nil {
		t.Fatal("expected future version error")
	}
	if !strings.Contains(err.Error(), "unsupported version 999") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveWritesCurrentStateVersion(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := Save(&State{
		Parked: []string{"/code"},
		Links:  []Link{{Name: "app", Path: "/code/app", Secure: true}},
	}); err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	data, err := os.ReadFile(statePath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["version"] != float64(CurrentStateVersion) {
		t.Fatalf("version = %#v, want %d in %s", raw["version"], CurrentStateVersion, data)
	}
}

func TestResolveCombinesParkedDirsLinksProxyAndDefaultPHP(t *testing.T) {
	root := t.TempDir()
	parked := filepath.Join(root, "parked")
	blog := filepath.Join(parked, "blog")
	app := filepath.Join(parked, "app")
	custom := filepath.Join(root, "custom")
	for _, dir := range []string{
		filepath.Join(blog, "public"),
		filepath.Join(app, "public"),
		filepath.Join(custom, "web"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{
		filepath.Join(blog, "composer.json"),
		filepath.Join(blog, "public", "index.php"),
		filepath.Join(app, "composer.json"),
		filepath.Join(app, "public", "index.php"),
		filepath.Join(custom, "web", "index.html"),
	} {
		if err := os.WriteFile(file, []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	state := &State{
		Parked:     []string{parked},
		DefaultPHP: "8.4",
		Links: []Link{
			{Name: "app", Path: custom, Root: "web", Secure: false},
			{Name: "vite", Target: "127.0.0.1:5173", Secure: true},
		},
	}

	resolved := state.Resolve()
	if len(resolved) != 3 {
		t.Fatalf("got %d resolved sites, want 3: %#v", len(resolved), resolved)
	}
	byName := resolvedByName(resolved)

	if got := byName["blog"]; got.Kind != KindPHP || got.Docroot != filepath.Join(blog, "public") || got.PHP != "8.4" || !got.Secure {
		t.Fatalf("blog = %#v", got)
	}
	if got := byName["app"]; got.Kind != KindStatic || got.Path != custom || got.Docroot != filepath.Join(custom, "web") || got.PHP != "" || got.Secure {
		t.Fatalf("app link override = %#v", got)
	}
	if got := byName["vite"]; got.Kind != KindProxy || got.Target != "127.0.0.1:5173" || !got.Secure || got.Path != "" {
		t.Fatalf("vite proxy = %#v", got)
	}
}

func TestResolveSkipsInvalidAndHiddenParkedDirs(t *testing.T) {
	parked := t.TempDir()
	for _, dir := range []string{"valid", ".hidden", "Bad_Name"} {
		if err := os.MkdirAll(filepath.Join(parked, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	resolved := (&State{Parked: []string{parked}}).Resolve()
	if len(resolved) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if resolved[0].Name != "valid" {
		t.Fatalf("resolved site = %#v", resolved[0])
	}
}

func TestDetectSiteHeuristics(t *testing.T) {
	tests := []struct {
		name        string
		files       []string
		wantKind    Kind
		wantDocroot string
	}{
		{
			name:        "laravel public index wins",
			files:       []string{"composer.json", "public/index.php", "index.php"},
			wantKind:    KindPHP,
			wantDocroot: "public",
		},
		{
			name:        "plain php at root",
			files:       []string{"index.php"},
			wantKind:    KindPHP,
			wantDocroot: ".",
		},
		{
			name:        "dist static build",
			files:       []string{"dist/index.html"},
			wantKind:    KindStatic,
			wantDocroot: "dist",
		},
		{
			name:        "root static",
			files:       []string{"index.html"},
			wantKind:    KindStatic,
			wantDocroot: ".",
		},
		{
			name:        "missing docroot falls back to site path",
			wantKind:    KindStatic,
			wantDocroot: ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, file := range tt.files {
				path := filepath.Join(dir, file)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			gotKind, gotDocroot := detect(dir)
			wantDocroot := dir
			if tt.wantDocroot != "." {
				wantDocroot = filepath.Join(dir, tt.wantDocroot)
			}
			if gotKind != tt.wantKind || gotDocroot != wantDocroot {
				t.Fatalf("detect() = (%s, %q), want (%s, %q)", gotKind, gotDocroot, tt.wantKind, wantDocroot)
			}
		})
	}
}

func TestWriteFragmentsQuotesPathsAndUsesHTTPForInsecureSites(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	docroot := filepath.Join(t.TempDir(), "my project", "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteFragments([]Resolved{{
		Name:    "foo",
		Docroot: docroot,
		Kind:    KindStatic,
		Secure:  false,
	}}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_DATA_HOME"), "hostr", "sites", "foo.caddy"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"http://foo.test {",
		"root * " + strconv.Quote(docroot),
		"output file " + strconv.Quote(filepath.Join(os.Getenv("XDG_STATE_HOME"), "hostr", "log", "foo.log")),
		"roll_size 10MiB",
		"roll_keep 5",
		"roll_keep_for 720h",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered fragment missing %q:\n%s", want, content)
		}
	}
}

func TestWriteFragmentsRendersPHPSiteWithSocket(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	docroot := filepath.Join(t.TempDir(), "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteFragments([]Resolved{{
		Name:    "app",
		Docroot: docroot,
		Kind:    KindPHP,
		PHP:     "8.4",
		Secure:  true,
	}}); err != nil {
		t.Fatal(err)
	}

	content := readFragment(t, "app")
	for _, want := range []string{
		"app.test {",
		"tls internal",
		"root * " + strconv.Quote(docroot),
		"php_fastcgi " + strconv.Quote("unix/"+filepath.Join(os.Getenv("XDG_STATE_HOME"), "hostr", "run", "php-fpm-8.4.sock")),
		"file_server",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered fragment missing %q:\n%s", want, content)
		}
	}
}

func TestWriteFragmentsRendersMissingPHPFallback(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := WriteFragments([]Resolved{{
		Name:    "app",
		Docroot: t.TempDir(),
		Kind:    KindPHP,
		Secure:  true,
	}}); err != nil {
		t.Fatal(err)
	}

	content := readFragment(t, "app")
	for _, want := range []string{
		"respond \"hostr: app is a PHP site but no PHP version is installed. Run 'hostr php install <ver>'.\" 503",
		"file_server",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered fragment missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "php_fastcgi") {
		t.Fatalf("missing-PHP fragment should not render php_fastcgi:\n%s", content)
	}
}

func TestWriteFragmentsRendersProxySite(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := WriteFragments([]Resolved{{
		Name:   "vite",
		Target: "127.0.0.1:5173",
		Kind:   KindProxy,
		Secure: true,
	}}); err != nil {
		t.Fatal(err)
	}

	content := readFragment(t, "vite")
	for _, want := range []string{
		"vite.test {",
		"tls internal",
		"reverse_proxy " + strconv.Quote("127.0.0.1:5173"),
		"output file " + strconv.Quote(filepath.Join(os.Getenv("XDG_STATE_HOME"), "hostr", "log", "vite.log")),
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered fragment missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{"root *", "file_server", "php_fastcgi"} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("proxy fragment should not include %q:\n%s", unwanted, content)
		}
	}
}

func TestWriteFragmentsRejectsInvalidProxyTarget(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	err := WriteFragments([]Resolved{{
		Name:   "vite",
		Target: "127.0.0.1:nope",
		Kind:   KindProxy,
		Secure: true,
	}})
	if err == nil {
		t.Fatal("expected invalid proxy target error")
	}
	if !strings.Contains(err.Error(), "port must be 1-65535") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteFragmentsRemovesStaleFragments(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := WriteFragments([]Resolved{
		{Name: "old", Docroot: t.TempDir(), Kind: KindStatic, Secure: true},
		{Name: "new", Docroot: t.TempDir(), Kind: KindStatic, Secure: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := WriteFragments([]Resolved{
		{Name: "new", Docroot: t.TempDir(), Kind: KindStatic, Secure: true},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(fragmentPath("old")); !os.IsNotExist(err) {
		t.Fatalf("old fragment should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(fragmentPath("new")); err != nil {
		t.Fatalf("new fragment should remain: %v", err)
	}
}

func TestWriteFragmentsRejectsInvalidSiteNames(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	err := WriteFragments([]Resolved{{
		Name:    "bad/name",
		Docroot: t.TempDir(),
		Kind:    KindStatic,
		Secure:  true,
	}})
	if err == nil {
		t.Fatal("expected invalid site name error")
	}
}

func TestResolvePathReturnsLongestMatchingSitePath(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	state := &State{
		Links: []Link{
			{Name: "parent", Path: parent, Secure: true},
			{Name: "child", Path: child, Secure: true},
			{Name: "child-api", Path: child, Secure: true},
		},
	}

	matches := state.ResolvePath(filepath.Join(child, "app"))
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2: %#v", len(matches), matches)
	}
	if matches[0].Name != "child" || matches[1].Name != "child-api" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
}

func readFragment(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(fragmentPath(name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func fragmentPath(name string) string {
	return filepath.Join(os.Getenv("XDG_DATA_HOME"), "hostr", "sites", name+".caddy")
}

func writeStateFile(t *testing.T, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(statePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func resolvedByName(resolved []Resolved) map[string]Resolved {
	out := make(map[string]Resolved, len(resolved))
	for _, r := range resolved {
		out[r.Name] = r
	}
	return out
}
