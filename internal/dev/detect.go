package dev

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Spec struct {
	Kind        string
	Command     []string
	DefaultPort int
}

func Detect(dir string) (Spec, error) {
	if spec, ok := detectNode(dir); ok {
		return spec, nil
	}
	if spec, ok := detectRails(dir); ok {
		return spec, nil
	}
	if spec, ok := detectPhoenix(dir); ok {
		return spec, nil
	}
	if spec, ok := detectDjango(dir); ok {
		return spec, nil
	}
	return Spec{}, fmt.Errorf("no dev server detected in %s; pass a command after --", dir)
}

// DetectForPHPSite picks the command to run beside a PHP site. PHP-FPM keeps
// serving the site, so this command is never proxied: it only builds assets or
// runs workers. Laravel apps that ship an `artisan dev` command use it, and
// anything else with a package.json dev script (usually Vite) runs that.
// hasArtisanDev is only called when the directory has an artisan file.
func DetectForPHPSite(dir string, hasArtisanDev func() bool) (Spec, bool) {
	if exists(filepath.Join(dir, "artisan")) && hasArtisanDev() {
		return Spec{Kind: "laravel", Command: []string{"php", "artisan", "dev"}}, true
	}
	if spec, ok := detectNode(dir); ok {
		spec.DefaultPort = 0
		return spec, true
	}
	return Spec{}, false
}

// ArtisanListHasCommand reports whether `php artisan list --raw` output names
// the given command. Each raw line starts with the command name.
func ArtisanListHasCommand(raw, name string) bool {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}

func detectNode(dir string) (Spec, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return Spec{}, false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.Scripts["dev"] == "" {
		return Spec{}, false
	}
	manager := "npm"
	args := []string{"run", "dev"}
	switch {
	case exists(filepath.Join(dir, "pnpm-lock.yaml")):
		manager = "pnpm"
	case exists(filepath.Join(dir, "yarn.lock")):
		manager = "yarn"
		args = []string{"dev"}
	case exists(filepath.Join(dir, "bun.lockb")) || exists(filepath.Join(dir, "bun.lock")):
		manager = "bun"
		args = []string{"run", "dev"}
	}
	return Spec{Kind: "node", Command: append([]string{manager}, args...), DefaultPort: 5173}, true
}

func detectRails(dir string) (Spec, bool) {
	if !fileContains(filepath.Join(dir, "Gemfile"), "rails") {
		return Spec{}, false
	}
	if exists(filepath.Join(dir, "bin", "dev")) {
		return Spec{Kind: "rails", Command: []string{"bin/dev"}, DefaultPort: 3000}, true
	}
	if exists(filepath.Join(dir, "bin", "rails")) {
		return Spec{Kind: "rails", Command: []string{"bin/rails", "server", "-p", "3000"}, DefaultPort: 3000}, true
	}
	return Spec{Kind: "rails", Command: []string{"bundle", "exec", "rails", "server", "-p", "3000"}, DefaultPort: 3000}, true
}

func detectPhoenix(dir string) (Spec, bool) {
	if !fileContains(filepath.Join(dir, "mix.exs"), "phoenix") {
		return Spec{}, false
	}
	return Spec{Kind: "phoenix", Command: []string{"mix", "phx.server"}, DefaultPort: 4000}, true
}

func detectDjango(dir string) (Spec, bool) {
	if !exists(filepath.Join(dir, "manage.py")) {
		return Spec{}, false
	}
	return Spec{Kind: "django", Command: []string{"python", "manage.py", "runserver", "127.0.0.1:8000"}, DefaultPort: 8000}, true
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fileContains(path, needle string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), strings.ToLower(needle))
}
