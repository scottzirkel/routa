package services

import (
	"debug/elf"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeFakeELF writes just an ELF header, enough for debug/elf to read the
// class, machine, and type.
func writeFakeELF(t *testing.T, path string, class elf.Class, machine elf.Machine, typ elf.Type) {
	t.Helper()
	var b [64]byte
	copy(b[:4], elf.ELFMAG)
	b[elf.EI_CLASS] = byte(class)
	b[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	b[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(b[16:], uint16(typ))
	binary.LittleEndian.PutUint16(b[18:], uint16(machine))
	binary.LittleEndian.PutUint32(b[20:], uint32(elf.EV_CURRENT))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b[:], 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLinkNcurses5CompatUsesMatchingNcurses6(t *testing.T) {
	lib32, lib64 := t.TempDir(), t.TempDir()
	// A 32-bit libtinfo.so.6 comes first and must be skipped.
	writeFakeELF(t, filepath.Join(lib32, "libtinfo.so.6"), elf.ELFCLASS32, elf.EM_386, elf.ET_DYN)
	writeFakeELF(t, filepath.Join(lib64, "libtinfo.so.6"), elf.ELFCLASS64, elf.EM_X86_64, elf.ET_DYN)
	writeFakeELF(t, filepath.Join(lib64, "libncursesw.so.6"), elf.ELFCLASS64, elf.EM_X86_64, elf.ET_DYN)

	private := filepath.Join(t.TempDir(), "lib", "private")
	needed := []string{"libssl.so.3", "libncurses.so.5", "libtinfo.so.5"}
	created, err := linkNcurses5Compat(private, needed, elf.ELFCLASS64, elf.EM_X86_64, []string{lib32, lib64})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(private, "libncurses.so.5"), filepath.Join(private, "libtinfo.so.5")}
	if !reflect.DeepEqual(created, want) {
		t.Fatalf("created = %#v, want %#v", created, want)
	}
	for link, target := range map[string]string{
		"libncurses.so.5": filepath.Join(lib64, "libncursesw.so.6"),
		"libtinfo.so.5":   filepath.Join(lib64, "libtinfo.so.6"),
	} {
		if got, err := os.Readlink(filepath.Join(private, link)); err != nil || got != target {
			t.Errorf("%s -> %q, %v; want %s", link, got, err, target)
		}
	}
}

func TestLinkNcurses5CompatKeepsExistingLibraries(t *testing.T) {
	lib := t.TempDir()
	writeFakeELF(t, filepath.Join(lib, "libtinfo.so.6"), elf.ELFCLASS64, elf.EM_X86_64, elf.ET_DYN)
	private := t.TempDir()
	if err := os.WriteFile(filepath.Join(private, "libtinfo.so.5"), []byte("real ncurses 5"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := linkNcurses5Compat(private, []string{"libtinfo.so.5"}, elf.ELFCLASS64, elf.EM_X86_64, []string{lib})
	if err != nil || len(created) != 0 {
		t.Fatalf("created = %#v, %v; want nothing", created, err)
	}
}

func TestFindLibrarySkipsExecutablesAndOtherMachines(t *testing.T) {
	dir := t.TempDir()
	writeFakeELF(t, filepath.Join(dir, "libtinfo.so.6"), elf.ELFCLASS64, elf.EM_AARCH64, elf.ET_DYN)
	writeFakeELF(t, filepath.Join(dir, "libncursesw.so.6"), elf.ELFCLASS64, elf.EM_X86_64, elf.ET_EXEC)
	if got := findLibrary([]string{dir}, []string{"libtinfo.so.6", "libncursesw.so.6"}, elf.ELFCLASS64, elf.EM_X86_64); got != "" {
		t.Fatalf("findLibrary = %q, want none", got)
	}
}

func TestEnsureMySQLClientRunsLeavesWorkingClientAlone(t *testing.T) {
	install := t.TempDir()
	if err := os.MkdirAll(filepath.Join(install, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "bin", "mysql"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	starts := func(string) error { calls++; return nil }
	if err := ensureMySQLClientRuns(install, nil, starts, io.Discard); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("starts called %d times, want 1", calls)
	}
	if _, err := os.Stat(filepath.Join(install, "lib", "private")); !os.IsNotExist(err) {
		t.Fatalf("lib/private created for a working client: %v", err)
	}
}

func TestEnsureMySQLClientRunsReportsAnUnfixableClient(t *testing.T) {
	install := t.TempDir()
	// Not an ELF file, so routa cannot read what it links against.
	if err := os.MkdirAll(filepath.Join(install, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "bin", "mysql"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := errors.New("cannot start")
	if err := ensureMySQLClientRuns(install, nil, func(string) error { return broken }, io.Discard); !errors.Is(err, broken) {
		t.Fatalf("err = %v, want the start error", err)
	}
}

func TestEnsureMySQLClientRunsWithoutAClient(t *testing.T) {
	if err := ensureMySQLClientRuns(t.TempDir(), nil, func(string) error { t.Fatal("started a missing client"); return nil }, io.Discard); err != nil {
		t.Fatal(err)
	}
}
