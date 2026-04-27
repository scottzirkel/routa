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
	"time"

	"github.com/scottzirkel/hostr/internal/paths"
)

func versionDir(version string) string { return filepath.Join(paths.PHPDir(), version) }

type Installed struct{ Version string }

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
		info, err := e.Info()
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		t, err := os.Readlink(filepath.Join(paths.PHPDir(), e.Name()))
		if err != nil {
			continue
		}
		out[e.Name()] = t
	}
	return out, nil
}

func RemoveVersion(version string) error { return os.RemoveAll(versionDir(version)) }

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
