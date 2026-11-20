package php

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/site"
)

// Per-project PHP settings apply to web requests for every site served from
// one project folder. A project often answers under several names, such as
// affiliate.test and api.affiliate.test, and its git worktrees are served as
// sites of their own. Keying the settings by folder covers all of them with
// one setting, and a worktree inherits its parent project's.
//
// They are rendered into each site's own PHP-FPM pool, on top of the PHP
// version's settings, so they reach web requests only. The CLI keeps the PHP
// version's settings.

// SiteINIPath is the settings file for the project at projectPath. The name
// keeps the folder's name readable and adds a short hash of the full path, so
// two projects with the same folder name never share a file.
func SiteINIPath(projectPath string) string {
	clean := filepath.Clean(projectPath)
	sum := sha256.Sum256([]byte(clean))
	return filepath.Join(paths.ConfigDir(), "sites", fmt.Sprintf("%s-%x.ini", filepath.Base(clean), sum[:4]))
}

func LoadSiteINISettings(projectPath string) ([]INISetting, error) {
	return loadINISettingsAt(SiteINIPath(projectPath))
}

// SetSiteINISetting sets one PHP setting for a project's web requests.
func SetSiteINISetting(projectPath, key, value string) error {
	if key == ExtensionKey || key == ZendExtensionKey {
		return fmt.Errorf("%s cannot be set per project: PHP loads extensions once for each PHP version. Put it in a file in %s instead", key, ConfDir("<version>"))
	}
	if err := EnsureSiteINIFile(projectPath); err != nil {
		return err
	}
	return setINISettingAt(SiteINIPath(projectPath), key, value)
}

// EnsureSiteINIFile creates a project's settings file, with a comment naming
// the project, if it does not exist yet.
func EnsureSiteINIFile(projectPath string) error {
	path := SiteINIPath(projectPath)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeINIFile(path, []string{"; routa PHP settings for web requests to " + filepath.Clean(projectPath)})
}

func UnsetSiteINISetting(projectPath, key string) error {
	return unsetINISettingAt(SiteINIPath(projectPath), key)
}

// SiteProjectPaths lists the project folders whose settings apply to r, in
// the order they layer: a worktree's parent project first, then r's own.
func SiteProjectPaths(r site.Resolved, sites []site.Resolved) []string {
	var out []string
	if r.WorktreeOf != "" {
		for _, other := range sites {
			if other.Name == r.WorktreeOf && other.Path != "" {
				out = append(out, filepath.Clean(other.Path))
				break
			}
		}
	}
	if r.Path != "" {
		own := filepath.Clean(r.Path)
		if len(out) == 0 || out[0] != own {
			out = append(out, own)
		}
	}
	return out
}

// SitesUsingProject lists the PHP sites that a project folder's settings
// reach: every site served from it, and every worktree of those sites.
func SitesUsingProject(projectPath string, sites []site.Resolved) []string {
	projectPath = filepath.Clean(projectPath)
	var names []string
	for _, r := range sites {
		if r.Kind != site.KindPHP {
			continue
		}
		for _, p := range SiteProjectPaths(r, sites) {
			if p == projectPath {
				names = append(names, r.Name)
				break
			}
		}
	}
	sort.Strings(names)
	return names
}

// siteINISettings is the layered per-project settings for r.
func siteINISettings(r site.Resolved, sites []site.Resolved) ([]INISetting, error) {
	var out []INISetting
	for _, p := range SiteProjectPaths(r, sites) {
		settings, err := LoadSiteINISettings(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", SiteINIPath(p), err)
		}
		out = overlayINISettings(out, settings)
	}
	return out, nil
}

// overlayINISettings returns base with each override replacing base's value
// for the same key, and new keys added at the end.
func overlayINISettings(base, overrides []INISetting) []INISetting {
	out := append([]INISetting(nil), base...)
	index := map[string]int{}
	for i, s := range out {
		index[s.Key] = i
	}
	for _, s := range overrides {
		if i, ok := index[s.Key]; ok {
			out[i] = s
			continue
		}
		index[s.Key] = len(out)
		out = append(out, s)
	}
	return out
}
