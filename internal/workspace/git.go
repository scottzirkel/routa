package workspace

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree is one entry of `git worktree list`.
type Worktree struct {
	Path   string
	Branch string // short name; empty when detached
	Head   string
}

// MainRepo returns the main worktree of the repository that contains dir,
// even when dir is inside a linked worktree.
func MainRepo(dir string) (string, error) {
	out, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git repository: %w", dir, err)
	}
	common := filepath.Clean(strings.TrimSpace(out))
	if filepath.Base(common) != ".git" {
		return "", fmt.Errorf("unsupported git layout: %s is not a .git directory", common)
	}
	return filepath.Dir(common), nil
}

// ListWorktrees returns the linked worktrees of main, without main itself.
func ListWorktrees(main string) ([]Worktree, error) {
	out, err := git(main, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktreeList(out, main), nil
}

func parseWorktreeList(out, main string) []Worktree {
	var list []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil && filepath.Clean(cur.Path) != filepath.Clean(main) {
			list = append(list, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &Worktree{Path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return list
}

// AddWorktree creates a worktree at path on branch. A branch that already
// exists is checked out; otherwise it is created from `from` (HEAD when empty).
func AddWorktree(main, path, branch, from string) error {
	exists := gitOK(main, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	switch {
	case exists && from != "":
		return fmt.Errorf("branch %s already exists; drop --from to check it out", branch)
	case exists:
		return gitRun(main, "worktree", "add", path, branch)
	case from != "":
		return gitRun(main, "worktree", "add", "-b", branch, path, from)
	default:
		return gitRun(main, "worktree", "add", "-b", branch, path)
	}
}

// RemoveWorktree removes a worktree. Without force, git refuses when it has
// changes or untracked files; ignored files such as vendor/ do not count.
func RemoveWorktree(main, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	return gitRun(main, append(args, path)...)
}

// PruneWorktrees drops git's records of worktrees whose folders are gone.
func PruneWorktrees(main string) error {
	return gitRun(main, "worktree", "prune")
}

// IsTracked reports whether git tracks path in main.
func IsTracked(main, path string) bool {
	return gitOK(main, "ls-files", "--error-unmatch", "--", path)
}

// IgnoredPaths lists the ignored files of main relative to it, with whole
// ignored directories collapsed to one entry ending in "/".
func IgnoredPaths(main string) ([]string, error) {
	out, err := git(main, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return string(out), nil
}

func gitOK(dir string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", dir}, args...)...).Run() == nil
}

func gitRun(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s failed: %w", strings.Join(args[:2], " "), err)
	}
	return nil
}
