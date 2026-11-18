package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeProject(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ProjectFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadProjectWithoutAFileIsEmpty(t *testing.T) {
	p, err := LoadProject(t.TempDir())
	if err != nil || !reflect.DeepEqual(p, Project{}) {
		t.Fatalf("got %#v, %v", p, err)
	}
}

func TestLoadProjectReadsWorkspaceSettings(t *testing.T) {
	dir := writeProject(t, `{
  "workspace": {
    "services": ["redis", "meilisearch:1.12"],
    "run": ["npm run build", "composer test:setup"],
    "actions": {"vite": "npm run dev", "queue": "php artisan queue:work"}
  }
}`)
	p, err := LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	w := p.Workspace
	if !reflect.DeepEqual(w.Services, []string{"redis", "meilisearch:1.12"}) ||
		!reflect.DeepEqual(w.Run, []string{"npm run build", "composer test:setup"}) {
		t.Fatalf("got %#v", w)
	}
	if got := w.ActionNames(); !reflect.DeepEqual(got, []string{"queue", "vite"}) {
		t.Fatalf("ActionNames = %v, want them sorted", got)
	}
}

func TestLoadProjectRefusesMistakes(t *testing.T) {
	cases := map[string]string{
		"unknown key":      `{"workspace": {"runs": ["npm run build"]}}`,
		"empty command":    `{"workspace": {"run": ["  "]}}`,
		"empty service":    `{"workspace": {"services": [""]}}`,
		"bad action name":  `{"workspace": {"actions": {"Queue_Worker": "php artisan queue:work"}}}`,
		"empty action":     `{"workspace": {"actions": {"queue": ""}}}`,
		"not json":         `workspace: {}`,
		"wrong value type": `{"workspace": {"run": "npm run build"}}`,
	}
	for name, content := range cases {
		if _, err := LoadProject(writeProject(t, content)); err == nil || !strings.Contains(err.Error(), ProjectFile) {
			t.Errorf("%s: err = %v, want one that names %s", name, err, ProjectFile)
		}
	}
}

func TestValidateActionName(t *testing.T) {
	for _, ok := range []string{"queue", "vite", "queue-high", "a1"} {
		if err := ValidateActionName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Queue", "queue_high", "-queue", "queue-", "q.ueue", strings.Repeat("a", 41)} {
		if err := ValidateActionName(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}
