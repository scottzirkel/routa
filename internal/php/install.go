package php

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/scottzirkel/hostr/internal/paths"
)

func versionDir(version string) string { return filepath.Join(paths.PHPDir(), version) }

type Installed struct{ Version string }

func BinPath(spec string) string {
	return filepath.Join(paths.PHPDir(), spec, "bin", "php")
}

func InstalledVersions() ([]Installed, error) {
	entries, err := os.ReadDir(paths.PHPDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Installed
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		// skip symlinks (those are major.minor pointers handled by Symlinks())
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !e.IsDir() {
			continue
		}
		if _, err := ParseVersion(e.Name()); err != nil {
			continue
		}
		out = append(out, Installed{Version: e.Name()})
	}
	return out, nil
}

// Symlinks returns name → target for each major.minor symlink in PHPDir.
func Symlinks() (map[string]string, error) {
	entries, err := os.ReadDir(paths.PHPDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink == 0 {
			continue
		}
		t, err := os.Readlink(filepath.Join(paths.PHPDir(), e.Name()))
		if err != nil {
			continue
		}
		target := filepath.Base(t)
		if _, err := ParseVersion(target); err != nil {
			continue
		}
		if info, err := os.Stat(versionDir(target)); err != nil || !info.IsDir() {
			continue
		}
		out[e.Name()] = target
	}
	return out, nil
}

func AliasTarget(alias string) (string, bool, error) {
	links, err := Symlinks()
	if err != nil {
		return "", false, err
	}
	target, ok := links[alias]
	return target, ok, nil
}

func RemoveVersion(version string) error {
	links, err := Symlinks()
	if err != nil {
		return err
	}

	target := version
	if aliasTarget, ok := links[version]; ok {
		target = aliasTarget
	} else if _, err := ParseVersion(version); err != nil {
		resolved, err := resolveInstalledSpec(version)
		if err != nil {
			return err
		}
		target = resolved
	}
	if _, err := ParseVersion(target); err != nil {
		return fmt.Errorf("invalid PHP version %q", version)
	}
	if info, err := os.Stat(versionDir(target)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("PHP %s is not installed", version)
		}
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("PHP %s is not an installed version directory", version)
	}

	for alias, aliasTarget := range links {
		if alias == version || aliasTarget == target {
			if err := os.Remove(filepath.Join(paths.PHPDir(), alias)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return os.RemoveAll(versionDir(target))
}

func resolveInstalledSpec(spec string) (string, error) {
	installed, err := InstalledVersions()
	if err != nil {
		return "", err
	}
	var matches []string
	for _, i := range installed {
		v, err := ParseVersion(i.Version)
		if err != nil {
			continue
		}
		if v.Matches(spec) {
			matches = append(matches, i.Version)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("PHP %s is not installed", spec)
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("PHP %s matches multiple installed versions: %s", spec, strings.Join(matches, ", "))
	}
}

// Install downloads cli + fpm, writes them to bin/php and bin/php-fpm,
// and refreshes the major.minor symlink.
func Install(ctx context.Context, r Release, out io.Writer) error {
	dir := versionDir(r.Version.String())
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		return err
	}

	tasks := []struct {
		label string
		url   string
		dest  string
	}{
		{"php (cli)", r.CLIURL, filepath.Join(dir, "bin", "php")},
		{"php-fpm  ", r.FPMURL, filepath.Join(dir, "bin", "php-fpm")},
	}
	for _, t := range tasks {
		fmt.Fprintf(out, "  ↓ %s %s\n", t.label, r.Version)
		if err := downloadAndExtract(ctx, t.url, t.dest, out); err != nil {
			return fmt.Errorf("%s: %w", t.label, err)
		}
	}

	// Refresh major.minor symlink: 8.3 -> 8.3.30
	link := filepath.Join(paths.PHPDir(), r.Version.MinorString())
	_ = os.Remove(link)
	if err := os.Symlink(r.Version.String(), link); err != nil {
		return fmt.Errorf("symlink %s: %w", link, err)
	}
	return nil
}

func downloadAndExtract(ctx context.Context, url, destBin string, out io.Writer) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	pr := &progressReader{r: resp.Body, total: resp.ContentLength, out: out}
	gzr, err := gzip.NewReader(pr)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	wrote := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		// Tarballs from dl.static-php.dev contain a single binary at root.
		// Whatever it's named inside, we write to destBin.
		if err := writeFile(destBin, tr); err != nil {
			return err
		}
		wrote = true
	}
	fmt.Fprintln(out)
	if !wrote {
		return fmt.Errorf("no regular file inside tarball")
	}
	return nil
}

func writeFile(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

type progressReader struct {
	r       io.Reader
	total   int64
	read    int64
	out     io.Writer
	lastPct int
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.total > 0 {
		pct := int(p.read * 100 / p.total)
		if pct >= p.lastPct+5 {
			p.lastPct = pct
			fmt.Fprintf(p.out, "\r\033[K    %3d%%  %s / %s", pct, humanBytes(p.read), humanBytes(p.total))
		}
	}
	return n, err
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
