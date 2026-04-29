package site

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered fragment missing %q:\n%s", want, content)
		}
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
