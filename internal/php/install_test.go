package php

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveVersionRemovesPatchDirectoryAndAliases(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	phpDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "hostr", "php")
	if err := os.MkdirAll(filepath.Join(phpDir, "8.4.1", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("8.4.1", filepath.Join(phpDir, "8.4")); err != nil {
		t.Fatal(err)
	}

	if err := RemoveVersion("8.4"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(phpDir, "8.4"),
		filepath.Join(phpDir, "8.4.1"),
	} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after removal", p)
		}
	}
}

func TestRemoveVersionResolvesMinorWhenAliasIsAlreadyGone(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	phpDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "hostr", "php")
	if err := os.MkdirAll(filepath.Join(phpDir, "8.3.30", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := RemoveVersion("8.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(phpDir, "8.3.30")); !os.IsNotExist(err) {
		t.Fatalf("8.3.30 still exists after removal")
	}
}

func TestRemoveVersionErrorsOnAmbiguousMinor(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	phpDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "hostr", "php")
	for _, version := range []string{"8.3.29", "8.3.30"} {
		if err := os.MkdirAll(filepath.Join(phpDir, version, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := RemoveVersion("8.3"); err == nil {
		t.Fatal("expected ambiguous version error")
	}
}

func TestSymlinksSkipsDanglingAliases(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	phpDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "hostr", "php")
	if err := os.MkdirAll(phpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("8.4.1", filepath.Join(phpDir, "8.4")); err != nil {
		t.Fatal(err)
	}

	links, err := Symlinks()
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("expected no valid links, got %#v", links)
	}
}
