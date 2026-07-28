package php

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveVersionRemovesPatchDirectoryAndAliases(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	phpDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "php")
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

	phpDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "php")
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

	phpDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "php")
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

	phpDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "php")
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

func TestDownloadAndExtractRetriesInterruptedBody(t *testing.T) {
	archive := testTarGz(t, "php binary")
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/gzip")
		if attempts == 1 {
			w.Header().Set("Content-Length", "999999")
			_, _ = w.Write(archive[:len(archive)/2])
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "php")
	var out bytes.Buffer
	if err := downloadAndExtract(context.Background(), server.URL, dest, &out); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "php binary" {
		t.Fatalf("dest = %q", data)
	}
	if !strings.Contains(out.String(), "retrying download (2/3)") {
		t.Fatalf("retry output missing:\n%s", out.String())
	}
}

func TestDownloadAndExtractDoesNotRetryNotFound(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		http.NotFound(w, nil)
	}))
	defer server.Close()

	err := downloadAndExtract(context.Background(), server.URL, filepath.Join(t.TempDir(), "php"), io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestInstallExtensionDownloadsSharedExtension(t *testing.T) {
	for _, ext := range ManagedExtensions() {
		t.Run(ext.Name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			archive := testTarGz(t, ext.Name+" so")
			want := fmt.Sprintf("routa_php_%s_8.4.20_linux_", ext.Name)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, want) {
					t.Errorf("path = %s, want one containing %s", r.URL.Path, want)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/gzip")
				_, _ = w.Write(archive)
			}))
			defer server.Close()
			t.Setenv("ROUTA_PHP_EXT_BASE_URL", server.URL)

			var out bytes.Buffer
			ok, err := InstallExtension(context.Background(), ext, "8.4.20", &out)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatalf("expected %s to be installed", ext.Name)
			}
			data, err := os.ReadFile(ext.Path("8.4.20"))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != ext.Name+" so" {
				t.Fatalf("%s.so = %q", ext.Name, data)
			}
			if !strings.Contains(out.String(), ext.Name) {
				t.Fatalf("install output missing %s download:\n%s", ext.Name, out.String())
			}
		})
	}
}

func TestInstallExtensionSkipsMissingArtifact(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	t.Setenv("ROUTA_PHP_EXT_BASE_URL", server.URL)

	ok, err := InstallExtension(context.Background(), PcovExtension, "8.4.20", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected missing artifact to be skipped")
	}
	if _, err := os.Stat(PcovExtension.Path("8.4.20")); !os.IsNotExist(err) {
		t.Fatalf("pcov.so exists after missing artifact: %v", err)
	}
}

func testTarGz(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte(content)
	if err := tw.WriteHeader(&tar.Header{Name: "php", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSupportsSharedExtensionsReportsUnknownRatherThanGuessing(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "script")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"missing":  filepath.Join(dir, "does-not-exist"),
		"not-elf":  script,
		"data-dir": dir,
	} {
		if _, known := SupportsSharedExtensions(path); known {
			t.Fatalf("%s: expected an unknown verdict, got a definite one", name)
		}
	}
}

func TestSupportsSharedExtensionsDetectsDynamicBinary(t *testing.T) {
	// A dynamically-linked ELF carries PT_INTERP; a statically-linked libc
	// build does not, and that is exactly what routa must refuse to load into.
	var dynamic string
	for _, candidate := range []string{"/bin/sh", "/usr/bin/env", "/bin/ls"} {
		if _, known := SupportsSharedExtensions(candidate); known {
			dynamic = candidate
			break
		}
	}
	if dynamic == "" {
		t.Skip("no inspectable system ELF binary available")
	}
	supported, known := SupportsSharedExtensions(dynamic)
	if !known || !supported {
		t.Fatalf("%s: supported = %t, known = %t, want a dynamically-linked verdict", dynamic, supported, known)
	}
}

func TestCLIRejectsSharedExtensionsNeverGuessesWithoutABinary(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if CLIRejectsSharedExtensions("8.4") {
		t.Fatal("must not claim rejection when the CLI cannot be inspected")
	}
}

func TestBinPathPrefersDynamicCLIWhenPresent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	binDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "routa", "php", "8.4", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "php"), []byte("stock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := BinPath("8.4"); got != StockBinPath("8.4") {
		t.Fatalf("BinPath = %q, want the stock CLI %q", got, StockBinPath("8.4"))
	}

	dynamic := filepath.Join(binDir, DynamicCLIName)
	if err := os.WriteFile(dynamic, []byte("dynamic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := BinPath("8.4"); got != dynamic {
		t.Fatalf("BinPath = %q, want the dynamic CLI %q", got, dynamic)
	}
}
