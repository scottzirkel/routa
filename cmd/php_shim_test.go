package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPHPShimContentIsExecutableAndQuoted(t *testing.T) {
	content := phpShimContent("/home/some one/bin/routa")
	if !strings.HasPrefix(content, "#!/bin/sh\n") {
		t.Fatalf("shim must start with a shebang:\n%s", content)
	}
	if !strings.Contains(content, phpShimMarker) {
		t.Fatalf("shim must carry the marker so uninstall can recognise it:\n%s", content)
	}
	// A path with a space must survive, or the shim silently runs the wrong thing.
	if !strings.Contains(content, `exec '/home/some one/bin/routa' php "$@"`) {
		t.Fatalf("shim did not quote the routa path:\n%s", content)
	}
}

func TestShellQuoteHandlesEmbeddedQuotes(t *testing.T) {
	if got, want := shellQuote(`a'b`), `'a'\''b'`; got != want {
		t.Fatalf("shellQuote = %s, want %s", got, want)
	}
}

func TestFirstPHPOnPathSkipsDirectoriesAndNonExecutables(t *testing.T) {
	root := t.TempDir()
	asDir := filepath.Join(root, "as-dir")
	notExec := filepath.Join(root, "not-exec")
	real := filepath.Join(root, "real")
	for _, d := range []string{asDir, notExec, real} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A directory named php, and a non-executable file named php, must both be
	// skipped — only a runnable php is what a shebang would actually reach.
	if err := os.MkdirAll(filepath.Join(asDir, "php"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notExec, "php"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(real, "php")
	if err := os.WriteFile(want, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", strings.Join([]string{asDir, notExec, real}, string(os.PathListSeparator)))
	if got := firstPHPOnPath(); got != want {
		t.Fatalf("firstPHPOnPath = %q, want %q", got, want)
	}
}
