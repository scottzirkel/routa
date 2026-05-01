package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/site"
)

var trackRoot string

var trackCmd = &cobra.Command{
	Use:     "track [dir]",
	Aliases: []string{"park"},
	Short:   "Track a directory — every subdir becomes <name>.test",
	Long: `Track a directory. Every immediate subdirectory becomes <name>.test.
By default routa auto-detects each subdirectory's docroot. Use --root to apply
the same docroot override to every discovered child, e.g. --root dist.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		dir, err := resolveDir(args)
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		site.AddParked(s, dir, trackRoot)
		msg := fmt.Sprintf("tracking %s", dir)
		if trackRoot != "" {
			msg += fmt.Sprintf("  (root=%s)", trackRoot)
		}
		return commitAndReload(s, msg)
	},
}

func init() {
	trackCmd.Flags().StringVar(&trackRoot, "root", "", "override docroot for every tracked child (relative to each child, or absolute)")
}

var untrackCmd = &cobra.Command{
	Use:     "untrack [dir]",
	Aliases: []string{"unpark"},
	Short:   "Stop tracking a directory",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		dir, err := resolveDir(args)
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		site.RemoveParked(s, dir)
		return commitAndReload(s, fmt.Sprintf("stopped tracking %s", dir))
	},
}

var linkRoot string

var linkCmd = &cobra.Command{
	Use:   "link [name]",
	Short: "Link the current directory as <name>.test (defaults to dir basename)",
	Long: `Link the current directory as <name>.test. By default routa auto-detects
the docroot (Laravel public/, Astro dist/, etc). Use --root to override
when the heuristic picks the wrong dir — e.g. for a vite build you might
say --root dist, or for a custom layout --root web/public.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		name := filepath.Base(cwd)
		if len(args) == 1 {
			name = args[0]
		}
		name, err = normalizeSiteName(name)
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		site.AddLink(s, site.Link{Name: name, Path: cwd, Root: linkRoot, Secure: true})
		msg := fmt.Sprintf("linked %s → %s.test", cwd, name)
		if linkRoot != "" {
			msg += fmt.Sprintf("  (root=%s)", linkRoot)
		}
		return commitAndReload(s, msg)
	},
}

func init() {
	linkCmd.Flags().StringVar(&linkRoot, "root", "", "override docroot (relative to current dir, or absolute)")
}

var unlinkCmd = &cobra.Command{
	Use:   "unlink <name>",
	Short: "Remove a linked site",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name, err := normalizeSiteName(args[0])
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		if !site.RemoveLink(s, name) {
			return fmt.Errorf("no link named %s", name)
		}
		return commitAndReload(s, fmt.Sprintf("unlinked %s", name))
	},
}

var aliasCmd = &cobra.Command{
	Use:   "alias <existing> <new>",
	Short: "Register another .test name for an existing site",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		target, err := normalizeSiteName(args[0])
		if err != nil {
			return err
		}
		name, err := normalizeSiteName(args[1])
		if err != nil {
			return err
		}
		if name == target {
			return fmt.Errorf("alias name must differ from target")
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		resolved := s.Resolve()
		if !resolvedHasName(resolved, target) {
			return fmt.Errorf("no site named %s", target)
		}
		if resolvedHasConcreteName(s, name) {
			return fmt.Errorf("cannot alias %s: a tracked or linked site already uses that name", name)
		}
		site.AddAlias(s, target, name)
		return commitAndReload(s, fmt.Sprintf("alias %s.test → %s.test", name, target))
	},
}

var unaliasCmd = &cobra.Command{
	Use:   "unalias <name>",
	Short: "Remove a site alias",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name, err := normalizeSiteName(args[0])
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		if !site.RemoveAlias(s, name) {
			return fmt.Errorf("no alias named %s", name)
		}
		return commitAndReload(s, fmt.Sprintf("removed alias %s.test", name))
	},
}

var ignoreCmd = &cobra.Command{
	Use:   "ignore <name>",
	Short: "Ignore an auto-discovered tracked site",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name, err := normalizeSiteName(args[0])
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		site.AddIgnored(s, name)
		return commitAndReload(s, fmt.Sprintf("ignored %s.test", name))
	},
}

var unignoreCmd = &cobra.Command{
	Use:   "unignore <name>",
	Short: "Restore an ignored tracked site",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name, err := normalizeSiteName(args[0])
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		if !site.RemoveIgnored(s, name) {
			return fmt.Errorf("no ignored site named %s", name)
		}
		return commitAndReload(s, fmt.Sprintf("restored %s.test", name))
	},
}

var isolateCmd = &cobra.Command{
	Use:   "isolate <name> <php-version>",
	Short: "Pin a site to a specific PHP version",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		name, err := normalizeSiteName(args[0])
		if err != nil {
			return err
		}
		ver := args[1]
		if err := requirePHP(ver); err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		for i, l := range s.Links {
			if l.Name == name {
				s.Links[i].PHP = ver
				return commitAndReload(s, fmt.Sprintf("pinned %s to PHP %s", name, ver))
			}
		}
		return fmt.Errorf("no link named %s — for tracked sites, use `link %s` first to override", name, name)
	},
}

var proxyCmd = &cobra.Command{
	Use:   "proxy <name> <target>",
	Short: "Reverse-proxy <name>.test to a local target (e.g. 5173, :5173, or 127.0.0.1:5173)",
	Long: `Adds or updates a link that reverse-proxies <name>.test to a local backend.
Useful for Vite, Next, Astro, Rails, or anything you'd otherwise hit at localhost:<port>.
Caddy auto-handles WebSocket upgrades, so HMR works.`,
	Args: cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		name, err := normalizeSiteName(args[0])
		if err != nil {
			return err
		}
		target, err := normalizeProxyTarget(args[1])
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		site.AddLink(s, site.Link{Name: name, Target: target, Secure: true})
		return commitAndReload(s, fmt.Sprintf("proxy %s.test → %s", name, target))
	},
}

func normalizeProxyTarget(t string) (string, error) {
	t = strings.TrimSpace(t)
	if !strings.Contains(t, ":") {
		t = "127.0.0.1:" + t
	} else if strings.HasPrefix(t, ":") {
		t = "127.0.0.1" + t
	}
	if err := site.ValidateProxyTarget(t); err != nil {
		return "", err
	}
	return t, nil
}

var secureCmd = &cobra.Command{
	Use:   "secure <name>",
	Short: "Toggle HTTPS for a linked site (default: on)",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name, err := normalizeSiteName(args[0])
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		for i, l := range s.Links {
			if l.Name == name {
				s.Links[i].Secure = !l.Secure
				state := "on"
				if !s.Links[i].Secure {
					state = "off"
				}
				return commitAndReload(s, fmt.Sprintf("secure %s: %s", name, state))
			}
		}
		return fmt.Errorf("no link named %s", name)
	},
}

func init() {
	rootCmd.AddCommand(trackCmd, untrackCmd, linkCmd, unlinkCmd, aliasCmd, unaliasCmd, ignoreCmd, unignoreCmd, isolateCmd, secureCmd, proxyCmd)
}

func resolveDir(args []string) (string, error) {
	if len(args) == 1 {
		return filepath.Abs(args[0])
	}
	return os.Getwd()
}

func normalizeSiteName(name string) (string, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".test")
	if err := site.ValidateName(name); err != nil {
		return "", err
	}
	return name, nil
}

func resolvedHasName(resolved []site.Resolved, name string) bool {
	for _, r := range resolved {
		if r.Name == name {
			return true
		}
	}
	return false
}

func resolvedHasConcreteName(s *site.State, name string) bool {
	for _, r := range (&site.State{Parked: s.Parked, ParkedRoots: s.ParkedRoots, Ignored: s.Ignored, Links: s.Links, DefaultPHP: s.DefaultPHP}).Resolve() {
		if r.Name == name {
			return true
		}
	}
	return false
}

func commitAndReload(s *site.State, msg string) error {
	if err := site.Save(s); err != nil {
		return err
	}
	if err := site.WriteFragments(s.Resolve()); err != nil {
		return err
	}
	if err := site.ReloadCaddy(); err != nil {
		return fmt.Errorf("reload caddy: %w", err)
	}
	fmt.Println(msg)
	return nil
}
