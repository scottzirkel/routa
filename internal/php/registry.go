// Package php manages installed PHP versions: discovery of published builds,
// download/extract, php-fpm config rendering, and a templated systemd user unit.
//
// Builds come from two places. routa publishes its own to the php-builds GitHub
// release, compiled so the CLI and PHP-FPM can both load shared extensions. The
// dl.static-php.dev gnu-bulk channel stopped publishing in July 2026, so it is
// now only a fallback for the versions it already has.
package php

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The static-php.dev channel's listing and download base. Variables so tests
// can point them at a local server.
var (
	bulkListURL = "https://dl.static-php.dev/static-php-cli/gnu-bulk/?format=json"
	bulkBaseURL = "https://dl.static-php.dev/static-php-cli/gnu-bulk"
)

const (
	// defaultRoutaBuildsURL is the GitHub release routa's own PHP builds live in.
	defaultRoutaBuildsURL = "https://github.com/scottzirkel/routa/releases/download/php-builds"
	// routaBuildsIndex lists every published archive with its SHA-256, so the
	// one file is both the list of builds and the checksums to verify them by.
	routaBuildsIndex = "SHA256SUMS"
)

// Source says where a published PHP build comes from.
type Source string

const (
	SourceRouta     Source = "routa"
	SourceStaticPHP Source = "static-php.dev"
)

type listEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}

type Release struct {
	Version Version
	CLIURL  string
	FPMURL  string
	// CLISHA256 and FPMSHA256 are the archives' expected SHA-256 sums. They are
	// empty for static-php.dev builds, which routa has no checksums for.
	CLISHA256 string
	FPMSHA256 string
	Source    Source
}

type Version struct{ Major, Minor, Patch int }

func (v Version) String() string      { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }
func (v Version) MinorString() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

func (v Version) Matches(spec string) bool {
	parts := strings.Split(spec, ".")
	switch len(parts) {
	case 1:
		return strconv.Itoa(v.Major) == parts[0]
	case 2:
		return strconv.Itoa(v.Major) == parts[0] && strconv.Itoa(v.Minor) == parts[1]
	case 3:
		return v.String() == spec
	}
	return false
}

func ParseVersion(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version: %s", s)
	}
	var v Version
	var err error
	if v.Major, err = strconv.Atoi(parts[0]); err != nil {
		return v, err
	}
	if v.Minor, err = strconv.Atoi(parts[1]); err != nil {
		return v, err
	}
	if v.Patch, err = strconv.Atoi(parts[2]); err != nil {
		return v, err
	}
	return v, nil
}

var verRE = regexp.MustCompile(`^php-(\d+)\.(\d+)\.(\d+)-(cli|fpm)-linux-(x86_64|aarch64)\.tar\.gz$`)

// FetchReleases lists the PHP builds published for this machine, oldest first.
// routa's own build wins when both sources have a version. Losing one source is
// not fatal: static-php.dev may disappear, and GitHub can be briefly down. A
// failure to read routa's list is reported to warn, because installs then fall
// back to older builds; only both failing is an error.
func FetchReleases(ctx context.Context, warn io.Writer) ([]Release, error) {
	arch, err := staticPHPArch()
	if err != nil {
		return nil, err
	}
	type result struct {
		rels []Release
		err  error
	}
	own := make(chan result, 1)
	go func() {
		rels, err := fetchRoutaReleases(ctx, routaBuildsURL(), arch)
		own <- result{rels, err}
	}()
	static, staticErr := fetchStaticPHPReleases(ctx, arch)
	routa := <-own

	if routa.err != nil && staticErr != nil {
		return nil, fmt.Errorf("no PHP build list could be read: routa builds: %v; static-php.dev: %v", routa.err, staticErr)
	}
	if routa.err != nil && warn != nil {
		fmt.Fprintf(warn, "  warning: could not read routa's PHP builds (%v); only older static-php.dev builds are available\n", routa.err)
	}
	return mergeReleases(routa.rels, static), nil
}

// routaBuildsURL is the base URL of routa's PHP build release. The override
// serves local or mirrored builds, the same way ROUTA_PHP_EXT_BASE_URL does for
// extensions.
func routaBuildsURL() string {
	if base := strings.TrimRight(os.Getenv("ROUTA_PHP_BUILDS_BASE_URL"), "/"); base != "" {
		return base
	}
	return defaultRoutaBuildsURL
}

func fetchRoutaReleases(ctx context.Context, base, arch string) ([]Release, error) {
	body, err := fetchIndex(ctx, base+"/"+routaBuildsIndex)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return parseChecksums(body, base, arch)
}

func fetchStaticPHPReleases(ctx context.Context, arch string) ([]Release, error) {
	body, err := fetchIndex(ctx, bulkListURL)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var entries []listEntry
	if err := json.NewDecoder(body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("parse listing: %w", err)
	}
	var files []publishedFile
	for _, e := range entries {
		if !e.IsDir {
			files = append(files, publishedFile{name: e.Name})
		}
	}
	return pairReleases(files, bulkBaseURL, arch, SourceStaticPHP), nil
}

func fetchIndex(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, downloadStatusError{URL: url, Code: resp.StatusCode}
	}
	return resp.Body, nil
}

var sha256RE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// parseChecksums reads a sha256sum-format file ("<sum>  <name>" per line) into
// releases. A line that is not a well-formed sum is skipped, so a build never
// appears without the checksum its download is verified against.
func parseChecksums(r io.Reader, base, arch string) ([]Release, error) {
	var files []publishedFile
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		sum := strings.ToLower(fields[0])
		if !sha256RE.MatchString(sum) {
			continue
		}
		// sha256sum marks binary-mode entries with a leading "*".
		files = append(files, publishedFile{name: strings.TrimPrefix(fields[1], "*"), sha256: sum})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return pairReleases(files, base, arch, SourceRouta), nil
}

type publishedFile struct {
	name   string
	sha256 string
}

// pairReleases matches each version's CLI and FPM archives for arch. A version
// missing either one is left out, since an install needs both.
func pairReleases(files []publishedFile, base, arch string, source Source) []Release {
	byVer := map[Version]*Release{}
	for _, f := range files {
		m := verRE.FindStringSubmatch(f.name)
		if m == nil || m[5] != arch {
			continue
		}
		maj, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		pat, _ := strconv.Atoi(m[3])
		v := Version{maj, min, pat}
		r := byVer[v]
		if r == nil {
			r = &Release{Version: v, Source: source}
			byVer[v] = r
		}
		url := base + "/" + f.name
		if m[4] == "cli" {
			r.CLIURL, r.CLISHA256 = url, f.sha256
		} else {
			r.FPMURL, r.FPMSHA256 = url, f.sha256
		}
	}
	out := make([]Release, 0, len(byVer))
	for _, r := range byVer {
		if r.CLIURL == "" || r.FPMURL == "" {
			continue
		}
		out = append(out, *r)
	}
	sortReleases(out)
	return out
}

// mergeReleases combines two release lists. When both have a version, the one
// from preferred is kept.
func mergeReleases(preferred, fallback []Release) []Release {
	byVer := map[Version]Release{}
	for _, r := range fallback {
		byVer[r.Version] = r
	}
	for _, r := range preferred {
		byVer[r.Version] = r
	}
	out := make([]Release, 0, len(byVer))
	for _, r := range byVer {
		out = append(out, r)
	}
	sortReleases(out)
	return out
}

func sortReleases(rels []Release) {
	sort.Slice(rels, func(i, j int) bool { return rels[i].Version.Less(rels[j].Version) })
}

func staticPHPArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "aarch64", nil
	default:
		return "", fmt.Errorf("unsupported PHP archive architecture %q", runtime.GOARCH)
	}
}

// Resolve picks the highest version matching spec ("8.3", "8.3.30", or "latest").
func Resolve(spec string, rels []Release) (*Release, error) {
	if spec == "latest" || spec == "" {
		if len(rels) == 0 {
			return nil, fmt.Errorf("no releases available")
		}
		r := rels[len(rels)-1]
		return &r, nil
	}
	var match *Release
	for i := range rels {
		if rels[i].Version.Matches(spec) {
			r := rels[i]
			match = &r
		}
	}
	if match == nil {
		return nil, fmt.Errorf("no PHP build matching %q", spec)
	}
	return match, nil
}
