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

func TestResolveAppliesParkedRootToEachChild(t *testing.T) {
	parked := t.TempDir()
	api := filepath.Join(parked, "api")
	web := filepath.Join(parked, "web")
	for _, dir := range []string{
		filepath.Join(api, "dist"),
		filepath.Join(web, "dist"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resolved := (&State{
		Parked:      []string{parked},
		ParkedRoots: map[string]string{parked: "dist"},
	}).Resolve()

	byName := resolvedByName(resolved)
	if got := byName["api"]; got.Kind != KindStatic || got.Path != api || got.Docroot != filepath.Join(api, "dist") {
		t.Fatalf("api = %#v", got)
	}
	if got := byName["web"]; got.Kind != KindStatic || got.Path != web || got.Docroot != filepath.Join(web, "dist") {
		t.Fatalf("web = %#v", got)
	}
}

func TestResolveExplicitLinkOverridesParkedRoot(t *testing.T) {
	root := t.TempDir()
	parked := filepath.Join(root, "parked")
	child := filepath.Join(parked, "app")
	custom := filepath.Join(root, "custom")
	for _, dir := range []string{
		filepath.Join(child, "dist"),
		filepath.Join(custom, "public"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resolved := (&State{
		Parked:      []string{parked},
		ParkedRoots: map[string]string{parked: "dist"},
		Links:       []Link{{Name: "app", Path: custom, Root: "public", Secure: false}},
	}).Resolve()

	byName := resolvedByName(resolved)
	if got := byName["app"]; got.Path != custom || got.Docroot != filepath.Join(custom, "public") || got.Secure {
		t.Fatalf("explicit link should override tracked site: %#v", got)
	}
}

func TestResolveExplicitProxyOverridesTrackedSite(t *testing.T) {
	parked := t.TempDir()
	app := filepath.Join(parked, "app", "public")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved := (&State{
		Parked: []string{parked},
		Links:  []Link{{Name: "app", Target: "127.0.0.1:5173", Secure: true}},
	}).Resolve()

	if len(resolved) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if got := resolved[0]; got.Kind != KindProxy || got.Target != "127.0.0.1:5173" || got.Path != "" || got.Docroot != "" {
		t.Fatalf("explicit proxy should override tracked filesystem site: %#v", got)
	}
}

func TestResolveSkipsIgnoredParkedSitesButAllowsExplicitLinks(t *testing.T) {
	parked := t.TempDir()
	for _, dir := range []string{"ignored", "visible"} {
		if err := os.MkdirAll(filepath.Join(parked, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	resolved := (&State{
		Parked:  []string{parked},
		Ignored: []string{"ignored"},
		Links: []Link{
			{Name: "ignored", Target: "127.0.0.1:5173", Secure: true},
		},
	}).Resolve()

	if len(resolved) != 2 {
		t.Fatalf("resolved = %#v, want ignored explicit link plus visible parked site", resolved)
	}
	byName := resolvedByName(resolved)
	if got := byName["ignored"]; got.Kind != KindProxy || got.Target != "127.0.0.1:5173" {
		t.Fatalf("ignored explicit link = %#v", got)
	}
	if got := byName["visible"]; got.Kind != KindStatic || got.Path != filepath.Join(parked, "visible") {
		t.Fatalf("visible parked site = %#v", got)
	}
}

func TestResolveAliasesConcreteAndProxySites(t *testing.T) {
	root := t.TempDir()
	parked := filepath.Join(root, "parked")
	tracked := filepath.Join(parked, "tracked")
	linked := filepath.Join(root, "linked", "public")
	for _, dir := range []string{tracked, linked} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(linked, "index.php"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &State{
		Parked:     []string{parked},
		DefaultPHP: "8.4",
		Links: []Link{
			{Name: "app", Path: filepath.Dir(linked), Root: "public", PHP: "8.3", Secure: true},
			{Name: "vite", Target: "127.0.0.1:5173", Secure: false},
		},
		Aliases: []Alias{
			{Name: "api", Target: "app"},
			{Name: "web", Target: "tracked"},
			{Name: "frontend", Target: "vite"},
		},
	}

	byName := resolvedByName(state.Resolve())
	if got := byName["api"]; got.AliasOf != "app" || got.Kind != KindPHP || got.Path != filepath.Dir(linked) || got.Docroot != linked || got.PHP != "8.3" || !got.Secure {
		t.Fatalf("api alias = %#v", got)
	}
	if got := byName["web"]; got.AliasOf != "tracked" || got.Kind != KindStatic || got.Path != tracked || !got.Secure {
		t.Fatalf("web alias = %#v", got)
	}
	if got := byName["frontend"]; got.AliasOf != "vite" || got.Kind != KindProxy || got.Target != "127.0.0.1:5173" || got.Secure {
		t.Fatalf("frontend alias = %#v", got)
	}
}

func TestResolveAliasChainUsesFinalTargetConfig(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(project, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "public", "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved := (&State{
		DefaultPHP: "8.4",
		Links:      []Link{{Name: "app", Path: project, Root: "public", Secure: true}},
		Aliases: []Alias{
			{Name: "api", Target: "app"},
			{Name: "v1", Target: "api"},
		},
	}).Resolve()

	byName := resolvedByName(resolved)
	if got := byName["v1"]; got.AliasOf != "api" || got.Kind != KindPHP || got.Path != project || got.Docroot != filepath.Join(project, "public") || got.PHP != "8.4" {
		t.Fatalf("alias chain = %#v", got)
	}
	if got := byName["api"]; got.AliasOf != "app" {
		t.Fatalf("alias should keep immediate target, got %#v", got)
	}
}

func TestResolveAllowsDottedNamesAcrossTrackedLinksAndAliases(t *testing.T) {
	root := t.TempDir()
	parked := filepath.Join(root, "parked")
	tracked := filepath.Join(parked, "api.app", "public")
	linked := filepath.Join(root, "linked", "public")
	for _, dir := range []string{tracked, linked} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resolved := (&State{
		Parked: []string{parked},
		Links:  []Link{{Name: "shop.app", Path: filepath.Dir(linked), Root: "public", Secure: true}},
		Aliases: []Alias{
			{Name: "cdn.app", Target: "shop.app"},
		},
	}).Resolve()

	byName := resolvedByName(resolved)
	if got := byName["api.app"]; got.Kind != KindStatic || got.Docroot != tracked || !got.Secure {
		t.Fatalf("dotted tracked site = %#v", got)
	}
	if got := byName["shop.app"]; got.Kind != KindStatic || got.Docroot != linked || !got.Secure {
		t.Fatalf("dotted link = %#v", got)
	}
	if got := byName["cdn.app"]; got.AliasOf != "shop.app" || got.Kind != KindStatic || got.Docroot != linked {
		t.Fatalf("dotted alias = %#v", got)
	}
}

func TestResolveAliasesSkipMissingCyclesAndConcreteCollisions(t *testing.T) {
	state := &State{
		Links: []Link{
			{Name: "app", Target: "127.0.0.1:8080", Secure: true},
			{Name: "taken", Target: "127.0.0.1:9000", Secure: true},
		},
		Aliases: []Alias{
			{Name: "ok", Target: "app"},
			{Name: "missing", Target: "none"},
			{Name: "a", Target: "b"},
			{Name: "b", Target: "a"},
			{Name: "taken", Target: "app"},
		},
	}

	byName := resolvedByName(state.Resolve())
	for _, name := range []string{"app", "taken", "ok"} {
		if _, exists := byName[name]; !exists {
			t.Fatalf("expected %s in resolved sites: %#v", name, byName)
		}
	}
	for _, name := range []string{"missing", "a", "b"} {
		if _, exists := byName[name]; exists {
			t.Fatalf("did not expect %s in resolved sites: %#v", name, byName)
		}
	}
	if got := byName["taken"]; got.AliasOf != "" || got.Target != "127.0.0.1:9000" {
		t.Fatalf("concrete site should win over alias collision: %#v", got)
	}
}

func TestIgnoredMutationsNormalizeAndSort(t *testing.T) {
	state := &State{}
	AddIgnored(state, "Warboard.test")
	AddIgnored(state, "newaff")
	AddIgnored(state, "warboard")

	if got := strings.Join(state.Ignored, ","); got != "newaff,warboard" {
		t.Fatalf("ignored = %#v", state.Ignored)
	}
	if !state.Ignores("WARBOARD.test") {
		t.Fatal("expected warboard to be ignored")
	}
	if !RemoveIgnored(state, "warboard.test") {
		t.Fatal("expected warboard removal")
	}
	if RemoveIgnored(state, "missing") {
		t.Fatal("missing ignored site should not remove")
	}
	if got := strings.Join(state.Ignored, ","); got != "newaff" {
		t.Fatalf("ignored after removal = %#v", state.Ignored)
	}
}

func TestAliasMutationsReplaceSortAndRemove(t *testing.T) {
	state := &State{}
	AddAlias(state, "app", "web")
	AddAlias(state, "app", "api")
	AddAlias(state, "other", "web")

	if len(state.Aliases) != 2 {
		t.Fatalf("aliases = %#v", state.Aliases)
	}
	if state.Aliases[0] != (Alias{Name: "api", Target: "app"}) || state.Aliases[1] != (Alias{Name: "web", Target: "other"}) {
		t.Fatalf("aliases not sorted/replaced: %#v", state.Aliases)
	}
	if !RemoveAlias(state, "api") {
		t.Fatal("expected api alias removal")
	}
	if RemoveAlias(state, "missing") {
		t.Fatal("missing alias should not remove")
	}
	if len(state.Aliases) != 1 || state.Aliases[0].Name != "web" {
		t.Fatalf("aliases after removal = %#v", state.Aliases)
	}
}

func TestParkedMutationsSetReplaceAndRemoveRoot(t *testing.T) {
	dir := t.TempDir()
	state := &State{}
	AddParked(state, dir, "dist")
	AddParked(state, dir, "public")

	if len(state.Parked) != 1 || state.Parked[0] != dir {
		t.Fatalf("parked = %#v", state.Parked)
	}
	if got := state.ParkedRoots[dir]; got != "public" {
		t.Fatalf("parked root = %q, want public", got)
	}

	AddParked(state, dir, "")
	if _, exists := state.ParkedRoots[dir]; exists {
		t.Fatalf("expected empty root to clear override: %#v", state.ParkedRoots)
	}

	AddParked(state, dir, "dist")
	RemoveParked(state, dir)
	if len(state.Parked) != 0 || state.ParkedRoots != nil {
		t.Fatalf("state after remove = %#v", state)
	}
}

func TestResolveCustomRootRoutingBehavior(t *testing.T) {
	tests := []struct {
		name     string
		root     string
		files    []string
		wantKind Kind
	}{
		{
			name:     "public php root",
			root:     "public",
			files:    []string{"public/index.php"},
			wantKind: KindPHP,
		},
		{
			name:     "dist static root",
			root:     "dist",
			files:    []string{"dist/index.html"},
			wantKind: KindStatic,
		},
		{
			name:     "missing root",
			root:     "missing",
			wantKind: KindStatic,
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

			resolved := (&State{
				Links: []Link{{Name: "app", Path: dir, Root: tt.root}},
			}).Resolve()
			if len(resolved) != 1 {
				t.Fatalf("resolved = %#v", resolved)
			}

			wantDocroot := filepath.Join(dir, tt.root)
			if resolved[0].Kind != tt.wantKind || resolved[0].Docroot != wantDocroot {
				t.Fatalf("resolved site = %#v, want kind %s and docroot %q", resolved[0], tt.wantKind, wantDocroot)
			}
		})
	}
}

func TestResolveLocalValetDriverMarksSiteAsPHP(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "LocalValetDriver.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved := (&State{
		DefaultPHP: "8.4",
		Links:      []Link{{Name: "app", Path: project, Secure: true}},
	}).Resolve()

	if len(resolved) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if got := resolved[0]; got.Kind != KindPHP || got.Docroot != project || got.PHP != "8.4" || got.Driver != "valet" {
		t.Fatalf("local Valet driver site = %#v", got)
	}
}

func TestResolveGlobalValetDriverAppliesToPHPDetectedSites(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	driversDir := filepath.Join(configHome, "routa", "Drivers")
	if err := os.MkdirAll(driversDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(driversDir, "WordPressValetDriver.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved := (&State{
		DefaultPHP: "8.4",
		Links:      []Link{{Name: "app", Path: project, Secure: true}},
	}).Resolve()

	if len(resolved) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if got := resolved[0]; got.Kind != KindPHP || got.Driver != "valet" {
		t.Fatalf("global Valet driver PHP site = %#v", got)
	}
}

func TestResolveServerEnvFilePrefersRoutaEnvOverValetEnv(t *testing.T) {
	project := t.TempDir()
	for _, path := range []string{
		filepath.Join(project, "public", "index.php"),
		filepath.Join(project, ".routa-env.php"),
		filepath.Join(project, ".valet-env.php"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("<?php return [];"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resolved := (&State{
		DefaultPHP: "8.4",
		Links:      []Link{{Name: "app", Path: project, Secure: true}},
	}).Resolve()

	if len(resolved) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if got, want := resolved[0].ServerEnvFile, filepath.Join(project, ".routa-env.php"); got != want {
		t.Fatalf("server env file = %q, want %q", got, want)
	}
}

func TestResolveServerEnvFileFallsBackToValetEnv(t *testing.T) {
	project := t.TempDir()
	for _, path := range []string{
		filepath.Join(project, "public", "index.php"),
		filepath.Join(project, ".valet-env.php"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("<?php return [];"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resolved := (&State{
		DefaultPHP: "8.4",
		Links:      []Link{{Name: "app", Path: project, Secure: true}},
	}).Resolve()

	if len(resolved) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if got, want := resolved[0].ServerEnvFile, filepath.Join(project, ".valet-env.php"); got != want {
		t.Fatalf("server env file = %q, want %q", got, want)
	}
}

func TestResolveNestedRootOverridesForLinksAndParkedDirs(t *testing.T) {
	root := t.TempDir()
	parked := filepath.Join(root, "parked")
	api := filepath.Join(parked, "api")
	app := filepath.Join(root, "app")
	for _, file := range []string{
		filepath.Join(api, "web", "public", "index.php"),
		filepath.Join(app, "frontend", "public", "index.html"),
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resolved := (&State{
		Parked:      []string{parked},
		ParkedRoots: map[string]string{parked: filepath.Join("web", "public")},
		DefaultPHP:  "8.4",
		Links:       []Link{{Name: "app", Path: app, Root: filepath.Join("frontend", "public"), Secure: true}},
	}).Resolve()

	byName := resolvedByName(resolved)
	if got := byName["api"]; got.Kind != KindPHP || got.Docroot != filepath.Join(api, "web", "public") || got.PHP != "8.4" {
		t.Fatalf("nested parked root = %#v", got)
	}
	if got := byName["app"]; got.Kind != KindStatic || got.Docroot != filepath.Join(app, "frontend", "public") || got.PHP != "" {
		t.Fatalf("nested link root = %#v", got)
	}
}

func TestResolveAbsoluteParkedRootUsesSameDocrootForEveryChild(t *testing.T) {
	root := t.TempDir()
	parked := filepath.Join(root, "parked")
	sharedDocroot := filepath.Join(root, "shared-public")
	for _, dir := range []string{
		filepath.Join(parked, "api"),
		filepath.Join(parked, "web"),
		sharedDocroot,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sharedDocroot, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved := (&State{
		Parked:      []string{parked},
		ParkedRoots: map[string]string{parked: sharedDocroot},
		DefaultPHP:  "8.4",
	}).Resolve()

	byName := resolvedByName(resolved)
	for _, name := range []string{"api", "web"} {
		if got := byName[name]; got.Kind != KindPHP || got.Docroot != sharedDocroot || got.PHP != "8.4" {
			t.Fatalf("%s absolute parked root = %#v", name, got)
		}
	}
}

func TestResolveAbsoluteLinkRootUsesProvidedDocroot(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	docroot := filepath.Join(root, "shared-public")
	for _, dir := range []string{project, docroot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(docroot, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved := (&State{
		DefaultPHP: "8.4",
		Links:      []Link{{Name: "app", Path: project, Root: docroot, Secure: true}},
	}).Resolve()
	if len(resolved) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if got := resolved[0]; got.Kind != KindPHP || got.Path != project || got.Docroot != docroot || got.PHP != "8.4" {
		t.Fatalf("absolute link root = %#v", got)
	}
}

func TestResolveLowercaseNameCollisionUsesLaterDirectoryEntry(t *testing.T) {
	parked := t.TempDir()
	upper := filepath.Join(parked, "App")
	lower := filepath.Join(parked, "app")
	for _, dir := range []string{upper, lower} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	resolved := (&State{Parked: []string{parked}}).Resolve()
	if len(resolved) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if got := resolved[0]; got.Name != "app" || got.Path != lower {
		t.Fatalf("lowercase collision should use later sorted directory entry: %#v", got)
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

func TestResolveSkipsInvalidAndHiddenParkedDirsWithRootOverride(t *testing.T) {
	parked := t.TempDir()
	for _, dir := range []string{"valid", ".hidden", "Bad_Name"} {
		if err := os.MkdirAll(filepath.Join(parked, dir, "public"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parked, dir, "public", "index.html"), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	resolved := (&State{
		Parked:      []string{parked},
		ParkedRoots: map[string]string{parked: "public"},
	}).Resolve()
	if len(resolved) != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	if got := resolved[0]; got.Name != "valid" || got.Docroot != filepath.Join(parked, "valid", "public") {
		t.Fatalf("resolved site = %#v", got)
	}
}

func TestValidateProxyTargetVariants(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantErr string
	}{
		{name: "ipv4", target: "127.0.0.1:5173"},
		{name: "localhost", target: "localhost:3000"},
		{name: "ipv6", target: "[::1]:5173"},
		{name: "leading whitespace", target: " 127.0.0.1:5173", wantErr: "invalid proxy target"},
		{name: "missing host", target: ":5173", wantErr: "host cannot be empty"},
		{name: "missing port", target: "127.0.0.1:", wantErr: "port must be 1-65535"},
		{name: "zero port", target: "127.0.0.1:0", wantErr: "port must be 1-65535"},
		{name: "high port", target: "127.0.0.1:65536", wantErr: "port must be 1-65535"},
		{name: "non-numeric port", target: "127.0.0.1:nope", wantErr: "port must be 1-65535"},
		{name: "missing host port separator", target: "5173", wantErr: "expected host:port"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProxyTarget(tt.target)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateProxyTarget(%q) error = %v", tt.target, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateProxyTarget(%q) expected error containing %q", tt.target, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateProxyTarget(%q) error = %v, want %q", tt.target, err, tt.wantErr)
			}
		})
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
			name:        "public php front controller without composer",
			files:       []string{"public/index.php"},
			wantKind:    KindPHP,
			wantDocroot: "public",
		},
		{
			name:        "public php wins over public static index",
			files:       []string{"public/index.php", "public/index.html"},
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
			name:        "dist wins over later static build outputs",
			files:       []string{"dist/index.html", "out/index.html", "build/index.html", "_site/index.html"},
			wantKind:    KindStatic,
			wantDocroot: "dist",
		},
		{
			name:        "public static root",
			files:       []string{"public/index.html"},
			wantKind:    KindStatic,
			wantDocroot: "public",
		},
		{
			name:        "public static wins over built static output",
			files:       []string{"public/index.html", "dist/index.html"},
			wantKind:    KindStatic,
			wantDocroot: "public",
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

	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "sites", "foo.caddy"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"http://foo.test, http://*.foo.test {",
		"root * " + strconv.Quote(docroot),
		"try_files {path} {path}/ /index.html",
		"output file " + strconv.Quote(filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "log", "foo.log")),
		"roll_size 10MiB",
		"roll_keep 5",
		"roll_keep_for 720h",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered fragment missing %q:\n%s", want, content)
		}
	}
}

func TestWriteFragmentsSecureToggleForStaticSites(t *testing.T) {
	tests := []struct {
		name       string
		secure     bool
		want       []string
		wantAbsent []string
	}{
		{
			name:   "secure",
			secure: true,
			want:   []string{"app.test, *.app.test {", "issuer internal {", "lifetime 396d"},
		},
		{
			name:       "insecure",
			secure:     false,
			want:       []string{"http://app.test, http://*.app.test {", "# secure=false: HTTP only"},
			wantAbsent: []string{"issuer internal", "lifetime 396d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			t.Setenv("XDG_STATE_HOME", t.TempDir())

			if err := WriteFragments([]Resolved{{
				Name:    "app",
				Docroot: t.TempDir(),
				Kind:    KindStatic,
				Secure:  tt.secure,
			}}); err != nil {
				t.Fatal(err)
			}

			content := readFragment(t, "app")
			for _, want := range tt.want {
				if !strings.Contains(content, want) {
					t.Fatalf("rendered fragment missing %q:\n%s", want, content)
				}
			}
			for _, unwanted := range tt.wantAbsent {
				if strings.Contains(content, unwanted) {
					t.Fatalf("rendered fragment should not include %q:\n%s", unwanted, content)
				}
			}
		})
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
		"app.test, *.app.test {",
		"issuer internal {",
		"lifetime 396d",
		"root * " + strconv.Quote(docroot),
		"php_fastcgi " + strconv.Quote("unix/"+filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "php-fpm-8.4-app.sock")) + " {",
		"env PHP_VALUE " + strconv.Quote("auto_prepend_file="+filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "routa-server-env.php")),
		"file_server",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered fragment missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "try_files {path} {path}/ /index.html") {
		t.Fatalf("PHP fragment should leave routing to php_fastcgi:\n%s", content)
	}

	prepender, err := os.ReadFile(filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "routa-server-env.php"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".routa-env.php", ".valet-env.php", "$_SERVER", "ROUTA_SITE_NAME"} {
		if !strings.Contains(string(prepender), want) {
			t.Fatalf("server env prepender missing %q:\n%s", want, prepender)
		}
	}
}

func TestWriteFragmentsRendersPHPSiteWithSameSocketWhenEnvExists(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	project := t.TempDir()
	docroot := filepath.Join(project, "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".env"), []byte("APP_ENV=local"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docroot, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved := (&State{
		DefaultPHP: "8.4",
		Links:      []Link{{Name: "app", Path: project, Root: "public", Secure: true}},
	}).Resolve()
	if err := WriteFragments(resolved); err != nil {
		t.Fatal(err)
	}

	content := readFragment(t, "app")
	want := "php_fastcgi " + strconv.Quote("unix/"+filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "php-fpm-8.4-app.sock"))
	if !strings.Contains(content, want) {
		t.Fatalf("rendered fragment missing %q:\n%s", want, content)
	}
}

func TestWriteFragmentsRendersValetDriverRouter(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	project := t.TempDir()
	docroot := filepath.Join(project, "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteFragments([]Resolved{{
		Name:    "app",
		Path:    project,
		Docroot: docroot,
		Kind:    KindPHP,
		PHP:     "8.4",
		Secure:  true,
		Driver:  "valet",
	}}); err != nil {
		t.Fatal(err)
	}

	content := readFragment(t, "app")
	for _, want := range []string{
		"root * " + strconv.Quote(filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa")),
		"php_fastcgi " + strconv.Quote("unix/"+filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "php-fpm-8.4-app.sock")) + " {",
		"env ROUTA_SITE_PATH " + strconv.Quote(project),
		"env ROUTA_SITE_NAME " + strconv.Quote("app"),
		"env ROUTA_DOCROOT " + strconv.Quote(docroot),
		"env ROUTA_VALET_DRIVER_DIRS " + strconv.Quote(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "routa", "Drivers")+string(os.PathListSeparator)+filepath.Join(os.Getenv("HOME"), ".config", "valet", "Drivers")),
		"env PHP_VALUE " + strconv.Quote("auto_prepend_file="+filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "routa-server-env.php")),
		"try_files /routa-valet-router.php",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered Valet driver fragment missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{"file_server", "root * " + strconv.Quote(docroot)} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("Valet driver fragment should not include %q:\n%s", unwanted, content)
		}
	}

	router, err := os.ReadFile(filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "routa-valet-router.php"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"LocalValetDriver.php", "*ValetDriver.php", "serves(", "isStaticFile(", "frontControllerPath("} {
		if !strings.Contains(string(router), want) {
			t.Fatalf("Valet router missing %q:\n%s", want, router)
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
		"respond \"routa: app is a PHP site but no PHP version is installed. Run 'routa php install <ver>'.\" 503",
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
		"vite.test, *.vite.test {",
		"issuer internal {",
		"lifetime 396d",
		"reverse_proxy " + strconv.Quote("127.0.0.1:5173"),
		"header_up Host {host}",
		"header_up X-Forwarded-Proto {scheme}",
		"output file " + strconv.Quote(filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "log", "vite.log")),
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

func TestWriteFragmentsRendersWildcardHostsWithExplicitSubdomain(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := WriteFragments([]Resolved{
		{
			Name:    "app",
			Docroot: t.TempDir(),
			Kind:    KindStatic,
			Secure:  true,
		},
		{
			Name:    "api.app",
			Docroot: t.TempDir(),
			Kind:    KindStatic,
			Secure:  true,
		},
	}); err != nil {
		t.Fatal(err)
	}

	app := readFragment(t, "app")
	if !strings.Contains(app, "app.test, *.app.test {") {
		t.Fatalf("app fragment missing wildcard host:\n%s", app)
	}
	api := readFragment(t, "api.app")
	if !strings.Contains(api, "api.app.test, *.api.app.test {") {
		t.Fatalf("explicit subdomain fragment missing exact host:\n%s", api)
	}
}

func TestWriteFragmentsRendersAliasSiteAsSeparateHost(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	docroot := filepath.Join(t.TempDir(), "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}

	resolved := (&State{
		Links:   []Link{{Name: "app", Path: filepath.Dir(docroot), Root: "public", Secure: true}},
		Aliases: []Alias{{Name: "api", Target: "app"}},
	}).Resolve()
	if err := WriteFragments(resolved); err != nil {
		t.Fatal(err)
	}

	content := readFragment(t, "api")
	for _, want := range []string{
		"api.test, *.api.test {",
		"root * " + strconv.Quote(docroot),
		"output file " + strconv.Quote(filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "log", "api.log")),
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered alias fragment missing %q:\n%s", want, content)
		}
	}
}

func TestWriteFragmentsRendersAliasChainAsSeparateHosts(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	docroot := filepath.Join(t.TempDir(), "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}

	resolved := (&State{
		Links: []Link{{Name: "app", Path: filepath.Dir(docroot), Root: "public", Secure: true}},
		Aliases: []Alias{
			{Name: "api", Target: "app"},
			{Name: "v1", Target: "api"},
		},
	}).Resolve()
	if err := WriteFragments(resolved); err != nil {
		t.Fatal(err)
	}

	content := readFragment(t, "v1")
	for _, want := range []string{
		"v1.test, *.v1.test {",
		"root * " + strconv.Quote(docroot),
		"output file " + strconv.Quote(filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "log", "v1.log")),
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered alias chain fragment missing %q:\n%s", want, content)
		}
	}
}

func TestWriteFragmentsRendersAliasWithDistinctPHPSocket(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	project := t.TempDir()
	docroot := filepath.Join(project, "public")
	if err := os.MkdirAll(docroot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docroot, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}

	resolved := (&State{
		DefaultPHP: "8.4",
		Links:      []Link{{Name: "app", Path: project, Root: "public", Secure: true}},
		Aliases:    []Alias{{Name: "api", Target: "app"}},
	}).Resolve()
	if err := WriteFragments(resolved); err != nil {
		t.Fatal(err)
	}

	content := readFragment(t, "api")
	want := "php_fastcgi " + strconv.Quote("unix/"+filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "run", "php-fpm-8.4-api.sock"))
	if !strings.Contains(content, want) {
		t.Fatalf("rendered alias fragment missing %q:\n%s", want, content)
	}
}

func TestWriteFragmentsRendersProxyAlias(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	resolved := (&State{
		Links:   []Link{{Name: "vite", Target: "127.0.0.1:5173", Secure: true}},
		Aliases: []Alias{{Name: "frontend", Target: "vite"}},
	}).Resolve()
	if err := WriteFragments(resolved); err != nil {
		t.Fatal(err)
	}

	content := readFragment(t, "frontend")
	for _, want := range []string{
		"frontend.test, *.frontend.test {",
		"issuer internal {",
		"lifetime 396d",
		"reverse_proxy " + strconv.Quote("127.0.0.1:5173"),
		"output file " + strconv.Quote(filepath.Join(os.Getenv("XDG_STATE_HOME"), "routa", "log", "frontend.log")),
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered proxy alias fragment missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{"root *", "file_server", "php_fastcgi"} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("proxy alias fragment should not include %q:\n%s", unwanted, content)
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

func TestWriteFragmentsRemovesStaleAliasFragments(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	project := t.TempDir()
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	withAlias := (&State{
		Links:   []Link{{Name: "app", Path: project, Secure: true}},
		Aliases: []Alias{{Name: "api", Target: "app"}},
	}).Resolve()
	if err := WriteFragments(withAlias); err != nil {
		t.Fatal(err)
	}
	withoutAlias := (&State{
		Links: []Link{{Name: "app", Path: project, Secure: true}},
	}).Resolve()
	if err := WriteFragments(withoutAlias); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(fragmentPath("api")); !os.IsNotExist(err) {
		t.Fatalf("alias fragment should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(fragmentPath("app")); err != nil {
		t.Fatalf("concrete fragment should remain: %v", err)
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

func TestResolvePathPrefersTrackedChildOverLinkedParent(t *testing.T) {
	root := t.TempDir()
	tracked := filepath.Join(root, "tracked")
	child := filepath.Join(tracked, "api")
	if err := os.MkdirAll(filepath.Join(child, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	state := &State{
		Parked: []string{tracked},
		Links:  []Link{{Name: "workspace", Path: tracked, Secure: true}},
	}

	parentMatches := state.ResolvePath(tracked)
	if len(parentMatches) != 1 || parentMatches[0].Name != "workspace" {
		t.Fatalf("tracked root should match linked parent: %#v", parentMatches)
	}

	childMatches := state.ResolvePath(filepath.Join(child, "nested"))
	if len(childMatches) != 1 || childMatches[0].Name != "api" {
		t.Fatalf("tracked child should win over linked parent: %#v", childMatches)
	}
}

func TestResolvePathDoesNotMatchSiblingPathPrefixes(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "app")
	sibling := filepath.Join(root, "app-old")
	for _, dir := range []string{project, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	state := &State{
		Links: []Link{{Name: "app", Path: project, Secure: true}},
	}

	rootMatches := state.ResolvePath(project)
	if len(rootMatches) != 1 || rootMatches[0].Name != "app" {
		t.Fatalf("site root should match app: %#v", rootMatches)
	}

	siblingMatches := state.ResolvePath(sibling)
	if len(siblingMatches) != 0 {
		t.Fatalf("sibling with path prefix should not match app: %#v", siblingMatches)
	}
}

func TestResolvePathMatchesCleanedLinkPaths(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "app")
	if err := os.MkdirAll(filepath.Join(project, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	state := &State{
		Links: []Link{{Name: "app", Path: filepath.Join(project, "..", "app"), Secure: true}},
	}

	matches := state.ResolvePath(filepath.Join(project, "subdir"))
	if len(matches) != 1 || matches[0].Name != "app" {
		t.Fatalf("cleaned link path should match project child: %#v", matches)
	}
}

func TestResolvePathReturnsConcreteAndAliasMatchesButSkipsProxies(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(project, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	state := &State{
		Links: []Link{
			{Name: "app", Path: project, Secure: true},
			{Name: "vite", Target: "127.0.0.1:5173", Secure: true},
		},
		Aliases: []Alias{{Name: "api", Target: "app"}},
	}

	matches := state.ResolvePath(filepath.Join(project, "subdir"))
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want concrete plus alias: %#v", len(matches), matches)
	}
	if matches[0].Name != "api" || matches[1].Name != "app" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	for _, match := range matches {
		if match.Kind == KindProxy {
			t.Fatalf("proxy should not match a filesystem path: %#v", matches)
		}
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
	return filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "sites", name+".caddy")
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
