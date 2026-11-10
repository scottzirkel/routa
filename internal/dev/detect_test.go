package dev

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectNodeUsesPackageManagerLockfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"dev":"vite --host 127.0.0.1"}}`)
	write(t, filepath.Join(dir, "pnpm-lock.yaml"), "")

	spec, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Kind != "node" || spec.DefaultPort != 5173 {
		t.Fatalf("spec = %#v", spec)
	}
	if want := []string{"pnpm", "run", "dev"}; !reflect.DeepEqual(spec.Command, want) {
		t.Fatalf("command = %#v, want %#v", spec.Command, want)
	}
}

func TestDetectNodePackageManagerPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		lockfiles []string
		want      []string
	}{
		{
			name:      "defaults to npm without lockfile",
			lockfiles: nil,
			want:      []string{"npm", "run", "dev"},
		},
		{
			name:      "pnpm wins over yarn and bun",
			lockfiles: []string{"pnpm-lock.yaml", "yarn.lock", "bun.lock"},
			want:      []string{"pnpm", "run", "dev"},
		},
		{
			name:      "yarn wins over bun",
			lockfiles: []string{"yarn.lock", "bun.lockb"},
			want:      []string{"yarn", "dev"},
		},
		{
			name:      "bun lockfile",
			lockfiles: []string{"bun.lock"},
			want:      []string{"bun", "run", "dev"},
		},
		{
			name:      "legacy bun binary lockfile",
			lockfiles: []string{"bun.lockb"},
			want:      []string{"bun", "run", "dev"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "package.json"), `{"scripts":{"dev":"vite --host 127.0.0.1"}}`)
			for _, lockfile := range tt.lockfiles {
				write(t, filepath.Join(dir, lockfile), "")
			}

			spec, err := Detect(dir)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Kind != "node" || !reflect.DeepEqual(spec.Command, tt.want) || spec.DefaultPort != 5173 {
				t.Fatalf("spec = %#v, want command %#v", spec, tt.want)
			}
		})
	}
}

func TestDetectNodeFallsThroughWithoutDevScript(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "malformed package json", content: `{`},
		{name: "missing scripts", content: `{}`},
		{name: "missing dev script", content: `{"scripts":{"build":"vite build"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "package.json"), tt.content)
			write(t, filepath.Join(dir, "manage.py"), "")

			spec, err := Detect(dir)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"python", "manage.py", "runserver", "127.0.0.1:8000"}; spec.Kind != "django" || !reflect.DeepEqual(spec.Command, want) {
				t.Fatalf("spec = %#v", spec)
			}
		})
	}
}

func TestDetectNodeDevScriptWinsOverFrameworkMarkers(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"dev":"vite --host 127.0.0.1"}}`)
	write(t, filepath.Join(dir, "pnpm-lock.yaml"), "")
	write(t, filepath.Join(dir, "Gemfile"), `gem "rails"`)
	write(t, filepath.Join(dir, "manage.py"), "")

	spec, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"pnpm", "run", "dev"}; spec.Kind != "node" || !reflect.DeepEqual(spec.Command, want) || spec.DefaultPort != 5173 {
		t.Fatalf("spec = %#v, want node command %#v", spec, want)
	}
}

func TestDetectRailsPrefersBinDev(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Gemfile"), `gem "rails"`)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "bin", "dev"), "#!/usr/bin/env sh\n")

	spec, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bin/dev"}; spec.Kind != "rails" || !reflect.DeepEqual(spec.Command, want) || spec.DefaultPort != 3000 {
		t.Fatalf("spec = %#v", spec)
	}
}

func TestDetectRailsCommandFallbacks(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  []string
	}{
		{
			name: "bin rails",
			setup: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "bin", "rails"), "#!/usr/bin/env ruby\n")
			},
			want: []string{"bin/rails", "server", "-p", "3000"},
		},
		{
			name:  "bundle exec",
			setup: func(t *testing.T, dir string) {},
			want:  []string{"bundle", "exec", "rails", "server", "-p", "3000"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "Gemfile"), `gem "rails"`)
			tt.setup(t, dir)

			spec, err := Detect(dir)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Kind != "rails" || !reflect.DeepEqual(spec.Command, tt.want) || spec.DefaultPort != 3000 {
				t.Fatalf("spec = %#v, want command %#v", spec, tt.want)
			}
		})
	}
}

func TestDetectPhoenix(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "mix.exs"), `{:phoenix, "~> 1.8"}`)

	spec, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"mix", "phx.server"}; spec.Kind != "phoenix" || !reflect.DeepEqual(spec.Command, want) || spec.DefaultPort != 4000 {
		t.Fatalf("spec = %#v", spec)
	}
}

func TestDetectDjango(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "manage.py"), "")

	spec, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"python", "manage.py", "runserver", "127.0.0.1:8000"}; spec.Kind != "django" || !reflect.DeepEqual(spec.Command, want) || spec.DefaultPort != 8000 {
		t.Fatalf("spec = %#v", spec)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectForPHPSitePrefersArtisanDev(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "artisan"), "")
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"dev":"vite"}}`)

	spec, ok := DetectForPHPSite(dir, func() bool { return true })
	if !ok {
		t.Fatal("expected a dev command")
	}
	if want := []string{"php", "artisan", "dev"}; spec.Kind != "laravel" || !reflect.DeepEqual(spec.Command, want) {
		t.Fatalf("spec = %#v", spec)
	}
}

func TestDetectForPHPSiteFallsBackToViteWithoutArtisanDev(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "artisan"), "")
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"dev":"vite"}}`)

	spec, ok := DetectForPHPSite(dir, func() bool { return false })
	if !ok {
		t.Fatal("expected a dev command")
	}
	if want := []string{"npm", "run", "dev"}; !reflect.DeepEqual(spec.Command, want) {
		t.Fatalf("command = %#v, want %#v", spec.Command, want)
	}
	if spec.DefaultPort != 0 {
		t.Fatalf("PHP sites never proxy, so no port should be set: %#v", spec)
	}
}

func TestDetectForPHPSiteOnlyAsksArtisanWhenItExists(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"dev":"vite"}}`)

	asked := false
	if _, ok := DetectForPHPSite(dir, func() bool { asked = true; return true }); !ok {
		t.Fatal("expected a dev command")
	}
	if asked {
		t.Fatal("artisan was queried in a directory without an artisan file")
	}
}

func TestDetectForPHPSiteWithNothingToRun(t *testing.T) {
	if spec, ok := DetectForPHPSite(t.TempDir(), func() bool { return false }); ok {
		t.Fatalf("spec = %#v, want none", spec)
	}
}

func TestArtisanListHasCommandMatchesWholeNames(t *testing.T) {
	raw := "about              Display basic information about your application\n" +
		"db:show            Display information about the given database\n" +
		"down               Put the application into maintenance mode\n"
	if ArtisanListHasCommand(raw, "dev") {
		t.Fatal("dev matched a list without it")
	}
	if !ArtisanListHasCommand(raw+"dev                Run the development server\n", "dev") {
		t.Fatal("dev not found")
	}
	if ArtisanListHasCommand("dev:tools  something\n", "dev") {
		t.Fatal("dev matched a longer command name")
	}
}
