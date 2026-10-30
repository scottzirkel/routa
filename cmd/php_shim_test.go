package cmd

import (
	"os"
	"path/filepath"
	"slices"
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
	if !strings.Contains(content, `exec '/home/some one/bin/routa' php -- "$@"`) {
		t.Fatalf("shim did not quote the routa path:\n%s", content)
	}
}

// The shim's arguments belong to PHP, so they must never reach a routa
// subcommand, and any "--" of their own must survive.
func TestPHPShimArgumentsReachPHPUntouched(t *testing.T) {
	for _, phpArgs := range [][]string{
		{"list"},
		{"--", "--install-dir=/tmp/bin", "--filename=composer"},
		{"-v"},
	} {
		shimArgs := append([]string{"php", "--"}, phpArgs...)
		cmd, args, err := rootCmd.Find(shimArgs)
		if err != nil {
			t.Fatalf("Find(%q): %v", shimArgs, err)
		}
		if cmd != phpCmd {
			t.Fatalf("Find(%q) = %q, want the php proxy", shimArgs, cmd.CommandPath())
		}
		if got := trimArgSeparator(args); !slices.Equal(got, phpArgs) {
			t.Fatalf("PHP would receive %q, want %q", got, phpArgs)
		}
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
