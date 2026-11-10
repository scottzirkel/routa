package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// neverCopy holds ignored paths a new worktree must not inherit. Laravel's
// bootstrap/cache and storage/framework hold caches with the parent's
// absolute paths, logs belong to the parent, and public/hot would point the
// worktree at the parent's Vite server.
var neverCopy = []string{
	".git/",
	"public/hot",
	"bootstrap/cache/",
	"storage/framework/",
	"storage/logs/",
}

// CopyPlan picks which of the parent's ignored paths (from IgnoredPaths) to
// copy into a new worktree. It copies only what a working copy needs: the
// .env files, dependencies, built and published assets, keys, and local agent
// instructions (see wantedInWorkspace), plus any path in extra. Caches, logs,
// backups, lock files, local data, other worktrees, and anything in skip stay
// behind. Worktrees, skip, and extra are paths relative to the parent.
func CopyPlan(ignored, worktrees, skip, extra []string) []string {
	var plan []string
	for _, p := range ignored {
		if matchesAny(p, neverCopy) || matchesAny(p, skip) || touchesWorktree(p, worktrees) {
			continue
		}
		if wantedInWorkspace(p) || matchesAny(p, extra) {
			plan = append(plan, p)
		}
	}
	return plan
}

// wantedInWorkspace reports whether an ignored path is something a working
// copy of the app needs.
func wantedInWorkspace(p string) bool {
	clean := strings.TrimSuffix(p, "/")
	base := path.Base(clean)
	switch {
	case clean == ".env" || clean == ".env.testing" || clean == "auth.json":
		return true
	case base == "vendor" || base == "node_modules" || strings.Contains(clean, "/node_modules/") || strings.HasPrefix(clean, "node_modules/"):
		// Dependencies at any depth, such as a Nova component's.
		return true
	case strings.HasPrefix(clean, "public/"):
		// Built and published assets, and the public/storage link.
		return true
	case path.Dir(clean) == "storage" && strings.HasSuffix(base, ".key"):
		// Passport and other keys.
		return true
	case clean == ".ai" || strings.HasPrefix(clean, ".ai/") || clean == "CLAUDE.local.md" || clean == "AGENTS.local.md":
		// Local agent instructions and the files they point to.
		return true
	}
	return false
}

// matchesAny reports whether p is one of patterns or inside one. Patterns
// ending in "/" are directories.
func matchesAny(p string, patterns []string) bool {
	clean := strings.TrimSuffix(p, "/")
	for _, pat := range patterns {
		dir := strings.TrimSuffix(pat, "/")
		if clean == dir || strings.HasPrefix(clean, dir+"/") {
			return true
		}
	}
	return false
}

// touchesWorktree reports whether p is a worktree, inside one, or contains
// one, such as .claude/worktrees/.
func touchesWorktree(p string, worktrees []string) bool {
	clean := strings.TrimSuffix(p, "/")
	for _, wt := range worktrees {
		if clean == wt || strings.HasPrefix(clean, wt+"/") || strings.HasPrefix(wt, clean+"/") {
			return true
		}
	}
	return false
}

// CopyPaths copies each planned path from src to dst with `cp -a
// --reflink=auto`. On copy-on-write filesystems such as btrfs and XFS that
// takes no extra disk until files change; elsewhere it is a normal copy.
// Symlinks that pointed inside src are re-pointed inside dst. git reports a
// directory with nothing but ignored files as one entry, so paths that must
// not be copied (neverCopy and skip) are removed again from inside copied
// directories.
func CopyPaths(src, dst string, plan, skip []string, out io.Writer) error {
	prune := append(append([]string(nil), neverCopy...), skip...)
	for _, p := range plan {
		rel := strings.TrimSuffix(p, "/")
		from := filepath.Join(src, rel)
		to := filepath.Join(dst, rel)
		if _, err := os.Lstat(to); err == nil {
			continue // tracked placeholder or already present
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		fmt.Fprintf(out, "  copy %s\n", p)
		cmd := exec.Command("cp", "-a", "--reflink=auto", "--no-target-directory", from, to)
		if msg, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("copy %s: %w: %s", p, err, strings.TrimSpace(string(msg)))
		}
		if err := repointSymlink(src, dst, to); err != nil {
			return err
		}
		for _, inner := range prune {
			innerRel := strings.TrimSuffix(inner, "/")
			if strings.HasPrefix(innerRel, rel+"/") {
				if err := os.RemoveAll(filepath.Join(dst, innerRel)); err != nil {
					return err
				}
			}
		}
	}
	// Vite's dependency cache records absolute paths; let it rebuild.
	_ = os.RemoveAll(filepath.Join(dst, "node_modules", ".vite"))
	return nil
}

func repointSymlink(src, dst, path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return err
	}
	target, err := os.Readlink(path)
	if err != nil || !filepath.IsAbs(target) {
		return err
	}
	rel, err := filepath.Rel(src, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return os.Symlink(filepath.Join(dst, rel), path)
}

// CopyFile copies one file with copy-on-write where available.
func CopyFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if msg, err := exec.Command("cp", "-a", "--reflink=auto", from, to).CombinedOutput(); err != nil {
		return fmt.Errorf("copy %s: %w: %s", from, err, strings.TrimSpace(string(msg)))
	}
	return nil
}
