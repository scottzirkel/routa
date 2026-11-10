package site

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// siteSource is the configuration a site was built from, before detection.
// Worktree sites copy it from their parent so they get the same docroot
// override, PHP version, and HTTPS setting.
type siteSource struct {
	name   string
	path   string
	root   string
	target string
	php    string
	secure bool
}

// resolveWorktrees serves each git worktree of a site's repository as
// <site>-<suffix>.test, where the suffix is the worktree's folder name
// without the repository folder name in front. A worktree at
// ~/code/shop-login and one at ~/code/shop/.claude/worktrees/login both
// become shop-login.test. Wildcard hosts then cover the app's own subdomains.
//
// Names that already exist win, so a worktree that a tracked directory
// already serves (the sibling layout `routa workspace` creates) is left as
// it is. Ignored names stay ignored.
func (s *State) resolveWorktrees(seen map[string]Resolved, sources map[string]siteSource) {
	byRepo := map[string][]siteSource{}
	for _, src := range sources {
		if src.path == "" {
			continue
		}
		repo := filepath.Clean(src.path)
		byRepo[repo] = append(byRepo[repo], src)
	}
	repos := make([]string, 0, len(byRepo))
	for repo := range byRepo {
		repos = append(repos, repo)
	}
	sort.Strings(repos)

	for _, repo := range repos {
		worktrees := GitWorktrees(repo)
		if len(worktrees) == 0 {
			continue
		}
		parent := primarySource(byRepo[repo])
		for _, wt := range worktrees {
			name := WorktreeSiteName(parent.name, repo, wt)
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists || s.Ignores(name) || ValidateName(name) != nil {
				continue
			}
			r := build(name, wt, rebaseRoot(parent.root, repo, wt), "", parent.php, parent.secure, s.DefaultPHP)
			r.WorktreeOf = parent.name
			seen[name] = r
		}
	}
}

// GitWorktrees lists the linked worktrees of the main repository at repo by
// reading git's own records (.git/worktrees/*/gitdir), so resolving sites
// never runs git. Worktrees whose folder is gone are skipped. A repo that is
// itself a linked worktree has a .git file, not a directory, and lists none.
func GitWorktrees(repo string) []string {
	admin := filepath.Join(repo, ".git", "worktrees")
	entries, err := os.ReadDir(admin)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(admin, e.Name(), "gitdir"))
		if err != nil {
			continue
		}
		gitFile := strings.TrimSpace(string(data))
		if gitFile == "" {
			continue
		}
		// git 2.48+ can record relative paths (worktree.useRelativePaths).
		if !filepath.IsAbs(gitFile) {
			gitFile = filepath.Join(admin, e.Name(), gitFile)
		}
		wt := filepath.Dir(filepath.Clean(gitFile))
		if info, err := os.Stat(wt); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, wt)
	}
	sort.Strings(out)
	return out
}

var nonLabelChars = regexp.MustCompile(`[^a-z0-9]+`)

// WorktreeSuffix is the part of a worktree's folder name that identifies it:
// the folder name, lowercased, without a leading "<repo folder>-".
func WorktreeSuffix(repo, worktree string) string {
	name := strings.ToLower(filepath.Base(worktree))
	name = strings.TrimPrefix(name, strings.ToLower(filepath.Base(repo))+"-")
	return strings.Trim(nonLabelChars.ReplaceAllString(name, "-"), "-")
}

// WorktreeSiteName is the site name routa gives a worktree of parent's repo.
func WorktreeSiteName(parent, repo, worktree string) string {
	suffix := WorktreeSuffix(repo, worktree)
	if suffix == "" {
		return ""
	}
	return parent + "-" + suffix
}

// primarySource picks the site that names a repository's worktrees when
// several sites serve the same folder: the one with the fewest labels, so
// affiliate wins over app.affiliate, then the shortest, then alphabetical.
func primarySource(sources []siteSource) siteSource {
	best := sources[0]
	for _, src := range sources[1:] {
		bl, sl := strings.Count(best.name, "."), strings.Count(src.name, ".")
		switch {
		case sl != bl:
			if sl < bl {
				best = src
			}
		case len(src.name) != len(best.name):
			if len(src.name) < len(best.name) {
				best = src
			}
		case src.name < best.name:
			best = src
		}
	}
	return best
}

// PrimarySiteForPath returns the site that names worktrees of the repository
// at path, using the same rule as site resolution.
func (s *State) PrimarySiteForPath(path string) (string, bool) {
	clean := filepath.Clean(path)
	var matches []siteSource
	for _, src := range s.sources() {
		if src.path != "" && filepath.Clean(src.path) == clean {
			matches = append(matches, src)
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	return primarySource(matches).name, true
}

// rebaseRoot moves an absolute docroot override that points inside the
// parent repository to the same place inside the worktree. Relative roots
// already resolve against the worktree.
func rebaseRoot(root, repo, worktree string) string {
	if root == "" || !filepath.IsAbs(root) {
		return root
	}
	rel, err := filepath.Rel(repo, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return root
	}
	return filepath.Join(worktree, rel)
}
