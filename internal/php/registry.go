// Package php manages installed PHP versions: discovery from the
// dl.static-php.dev "bulk" musl-static channel (Laravel-ready extension set),
// download/extract, php-fpm config rendering, and a templated systemd user unit.
package php

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	bulkListURL = "https://dl.static-php.dev/static-php-cli/bulk/?format=json"
	bulkBaseURL = "https://dl.static-php.dev/static-php-cli/bulk"
)

type listEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}

type Release struct {
	Version Version
	CLIURL  string
	FPMURL  string
}

type Version struct{ Major, Minor, Patch int }

func (v Version) String() string        { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }
func (v Version) MinorString() string   { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }
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

var verRE = regexp.MustCompile(`^php-(\d+)\.(\d+)\.(\d+)-(cli|fpm)-linux-x86_64\.tar\.gz$`)

func FetchReleases(ctx context.Context) ([]Release, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", bulkListURL, nil)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("listing: HTTP %d", resp.StatusCode)
	}
	var entries []listEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("parse listing: %w", err)
	}

	type pair struct{ cli, fpm string }
	byVer := map[Version]pair{}
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		m := verRE.FindStringSubmatch(e.Name)
		if m == nil {
			continue
		}
		maj, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		pat, _ := strconv.Atoi(m[3])
		v := Version{maj, min, pat}
		p := byVer[v]
		if m[4] == "cli" {
			p.cli = e.Name
		} else {
			p.fpm = e.Name
		}
		byVer[v] = p
	}

	out := make([]Release, 0, len(byVer))
	for v, p := range byVer {
		if p.cli == "" || p.fpm == "" {
			continue
		}
		out = append(out, Release{
			Version: v,
			CLIURL:  bulkBaseURL + "/" + p.cli,
			FPMURL:  bulkBaseURL + "/" + p.fpm,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version.Less(out[j].Version) })
	return out, nil
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
