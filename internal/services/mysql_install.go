package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var mysqlTarballRE = regexp.MustCompile(`mysql-([0-9]+\.[0-9]+\.[0-9]+)-linux-glibc(2\.(?:17|28))-x86_64(-minimal)?\.tar\.xz`)

type MySQLRelease struct {
	Version string
	File    string
	URL     string
	Minimal bool
	Glibc   string
}

func ManagedMySQLBinaryPath(version string) string {
	return managedBinaryPath("mysql", version, MySQLBinaryName)
}

func InstallMySQL(ctx context.Context, spec string, out io.Writer) (*MySQLRelease, error) {
	if err := ValidateMySQLVersion(spec); err != nil {
		return nil, err
	}
	if _, err := os.Stat(ManagedMySQLBinaryPath(spec)); err == nil {
		fmt.Fprintf(out, "already installed mysql %s at %s\n", spec, ManagedMySQLBinaryPath(spec))
		warnIfMySQLClientBroken(spec, out)
		return &MySQLRelease{Version: spec}, nil
	}

	fmt.Fprintln(out, "fetching MySQL release index")
	releases, err := FetchMySQLReleases(ctx, spec)
	if err != nil {
		return nil, err
	}
	release, err := ResolveMySQLRelease(spec, releases)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "resolved mysql %s -> %s\n", spec, release.Version)

	if _, err := os.Stat(ManagedMySQLBinaryPath(release.Version)); err == nil {
		if err := refreshManagedMySQLAlias(spec, release.Version); err != nil {
			return nil, err
		}
		fmt.Fprintf(out, "already installed mysql %s at %s\n", release.Version, ManagedMySQLBinaryPath(release.Version))
		warnIfMySQLClientBroken(release.Version, out)
		return release, nil
	}

	if err := downloadAndExtractMySQL(ctx, *release, out); err != nil {
		return nil, err
	}
	if err := refreshManagedMySQLAlias(spec, release.Version); err != nil {
		return nil, err
	}
	warnIfMySQLClientBroken(release.Version, out)
	return release, nil
}

// warnIfMySQLClientBroken repairs the bundled mysql client when it can, and
// only warns when it cannot: the server runs without it.
func warnIfMySQLClientBroken(version string, out io.Writer) {
	if err := EnsureMySQLClientRuns(version, out); err != nil {
		fmt.Fprintf(out, "warning: the bundled mysql client cannot start, so routa will use a mysql or mariadb client from PATH instead: %v\n", err)
	}
}

func FetchMySQLReleases(ctx context.Context, spec string) ([]MySQLRelease, error) {
	line := mysqlReleaseLine(spec)
	url := fmt.Sprintf("https://dev.mysql.com/downloads/mysql/%s.html?os=2", line)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return ParseMySQLReleases(string(body)), nil
}

func ParseMySQLReleases(body string) []MySQLRelease {
	seen := map[string]bool{}
	var out []MySQLRelease
	for _, match := range mysqlTarballRE.FindAllStringSubmatch(body, -1) {
		file := match[0]
		if strings.HasPrefix(file, "mysql-test-") || seen[file] {
			continue
		}
		seen[file] = true
		out = append(out, MySQLRelease{
			Version: match[1],
			File:    file,
			URL:     mysqlDownloadURL(match[1], file),
			Minimal: match[3] == "-minimal",
			Glibc:   match[2],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Version == out[j].Version {
			if out[i].Minimal != out[j].Minimal {
				return out[i].Minimal
			}
			return out[i].Glibc < out[j].Glibc
		}
		return versionLess(out[i].Version, out[j].Version)
	})
	return out
}

func ResolveMySQLRelease(spec string, releases []MySQLRelease) (*MySQLRelease, error) {
	var matches []MySQLRelease
	for _, release := range releases {
		if versionLabelMatches(release.Version, spec) {
			matches = append(matches, release)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no MySQL release matching %q", spec)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Version != matches[j].Version {
			return versionLess(matches[i].Version, matches[j].Version)
		}
		if matches[i].Minimal != matches[j].Minimal {
			return !matches[i].Minimal && matches[j].Minimal
		}
		return matches[i].Glibc > matches[j].Glibc
	})
	best := matches[len(matches)-1]
	return &best, nil
}

func mysqlReleaseLine(spec string) string {
	parts := strings.Split(spec, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return spec
}

func mysqlDownloadURL(version, file string) string {
	return fmt.Sprintf("https://dev.mysql.com/get/Downloads/MySQL-%s/%s", mysqlReleaseLine(version), file)
}

func downloadAndExtractMySQL(ctx context.Context, release MySQLRelease, out io.Writer) error {
	tmp, err := os.MkdirTemp("", "routa-mysql-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	archive := filepath.Join(tmp, release.File)
	if err := downloadFile(ctx, release.URL, archive, out); err != nil {
		return err
	}

	target := managedBinaryDir("mysql", release.Version)
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	extractDir := target + ".tmp"
	_ = os.RemoveAll(extractDir)
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("tar", "-xJf", archive, "-C", extractDir, "--strip-components=1")
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(extractDir)
		return fmt.Errorf("extract %s: %w", release.File, err)
	}
	if _, err := os.Stat(filepath.Join(extractDir, "bin", MySQLBinaryName)); err != nil {
		_ = os.RemoveAll(extractDir)
		return fmt.Errorf("extracted MySQL archive is missing bin/%s: %w", MySQLBinaryName, err)
	}
	_ = os.RemoveAll(target)
	if err := os.Rename(extractDir, target); err != nil {
		_ = os.RemoveAll(extractDir)
		return err
	}
	fmt.Fprintf(out, "installed mysql %s at %s\n", release.Version, target)
	return nil
}

func downloadFile(ctx context.Context, url, path string, out io.Writer) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	client := &http.Client{Timeout: 20 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	pr := &serviceProgressReader{r: resp.Body, total: resp.ContentLength, out: out}
	_, err = io.Copy(f, pr)
	fmt.Fprintln(out)
	return err
}

type serviceProgressReader struct {
	r       io.Reader
	total   int64
	read    int64
	out     io.Writer
	lastPct int
}

func (p *serviceProgressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if p.total > 0 {
		pct := int(p.read * 100 / p.total)
		if pct >= p.lastPct+5 {
			p.lastPct = pct
			fmt.Fprintf(p.out, "  %d%%", pct)
		}
	}
	return n, err
}

func refreshManagedMySQLAlias(spec, resolved string) error {
	if spec == resolved {
		return nil
	}
	link := managedBinaryDir("mysql", spec)
	_ = os.Remove(link)
	return os.Symlink(resolved, link)
}

func versionLess(a, b string) bool {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for len(ap) < len(bp) {
		ap = append(ap, "0")
	}
	for len(bp) < len(ap) {
		bp = append(bp, "0")
	}
	for i := range ap {
		an, aerr := strconv.Atoi(ap[i])
		bn, berr := strconv.Atoi(bp[i])
		if aerr != nil || berr != nil {
			return a < b
		}
		if an == bn {
			continue
		}
		return an < bn
	}
	return false
}
