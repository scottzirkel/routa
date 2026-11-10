package services

import (
	"context"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ncurses5Replacements maps the ncurses 5 libraries that MySQL 8.0's generic
// builds link the mysql client against to the ncurses 6 libraries that
// provide the same functions. Current distros no longer ship ncurses 5.
var ncurses5Replacements = map[string][]string{
	"libtinfo.so.5":    {"libtinfo.so.6", "libncursesw.so.6", "libncurses.so.6"},
	"libncurses.so.5":  {"libncurses.so.6", "libncursesw.so.6"},
	"libncursesw.so.5": {"libncursesw.so.6"},
}

// systemLibraryDirs is where to look for ncurses 6, most specific first.
// Candidates are checked against the client's ELF class and machine, so a
// 32-bit library in a multilib dir is never picked.
var systemLibraryDirs = []string{
	"/usr/lib64",
	"/lib64",
	"/usr/lib/x86_64-linux-gnu",
	"/lib/x86_64-linux-gnu",
	"/usr/lib/aarch64-linux-gnu",
	"/lib/aarch64-linux-gnu",
	"/usr/lib",
	"/lib",
}

// EnsureMySQLClientRuns makes the mysql client bundled with a routa-managed
// MySQL start on systems without ncurses 5.
//
// The client only calls the classic termcap functions (tgetent, tgetflag,
// tgetnum, tgetstr, tputs), which ncurses 6 provides unchanged, and its
// RPATH searches $ORIGIN/../lib/private first. So routa links the system's
// ncurses 6 libraries into that directory under the ncurses 5 names. Nothing
// outside routa's install is touched, and the links are removed again if the
// client still cannot start.
func EnsureMySQLClientRuns(version string, out io.Writer) error {
	return ensureMySQLClientRuns(managedBinaryDir("mysql", version), systemLibraryDirs, mysqlClientStarts, out)
}

func ensureMySQLClientRuns(installDir string, libDirs []string, starts func(string) error, out io.Writer) error {
	client := filepath.Join(installDir, "bin", "mysql")
	if _, err := os.Stat(client); err != nil {
		return nil
	}
	startErr := starts(client)
	if startErr == nil {
		return nil
	}
	f, err := elf.Open(client)
	if err != nil {
		return startErr
	}
	needed, err := f.ImportedLibraries()
	class, machine := f.Class, f.Machine
	f.Close()
	if err != nil {
		return startErr
	}

	created, err := linkNcurses5Compat(filepath.Join(installDir, "lib", "private"), needed, class, machine, libDirs)
	if err != nil {
		return err
	}
	if len(created) == 0 {
		return startErr
	}
	if err := starts(client); err != nil {
		for _, link := range created {
			_ = os.Remove(link)
		}
		return err
	}
	fmt.Fprintf(out, "linked the system's ncurses 6 into %s so the bundled mysql client can start\n", filepath.Dir(created[0]))
	return nil
}

// linkNcurses5Compat links an ncurses 6 library into dir for each ncurses 5
// library in needed that dir does not already hold, and returns the links it
// created.
func linkNcurses5Compat(dir string, needed []string, class elf.Class, machine elf.Machine, libDirs []string) ([]string, error) {
	var created []string
	for _, lib := range needed {
		replacements, ok := ncurses5Replacements[lib]
		if !ok {
			continue
		}
		link := filepath.Join(dir, lib)
		if _, err := os.Lstat(link); err == nil {
			continue
		}
		target := findLibrary(libDirs, replacements, class, machine)
		if target == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return created, err
		}
		if err := os.Symlink(target, link); err != nil {
			return created, err
		}
		created = append(created, link)
	}
	return created, nil
}

// findLibrary returns the first of names found in dirs that is a shared
// library for the same ELF class and machine.
func findLibrary(dirs, names []string, class elf.Class, machine elf.Machine) string {
	for _, name := range names {
		for _, dir := range dirs {
			path := filepath.Join(dir, name)
			f, err := elf.Open(path)
			if err != nil {
				continue
			}
			ok := f.Class == class && f.Machine == machine && f.Type == elf.ET_DYN
			f.Close()
			if ok {
				return path
			}
		}
	}
	return ""
}

func mysqlClientStarts(client string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, client, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s cannot start: %s", client, strings.TrimSpace(string(out)))
	}
	return nil
}
