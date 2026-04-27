package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/hostr/internal/site"
)

var parkCmd = &cobra.Command{
	Use:   "park [dir]",
	Short: "Mark a directory as parked — every subdir becomes <name>.test",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		dir, err := resolveDir(args)
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		site.AddParked(s, dir)
		return commitAndReload(s, fmt.Sprintf("parked %s", dir))
	},
}

var unparkCmd = &cobra.Command{
	Use:   "unpark [dir]",
	Short: "Remove a parked directory",
	Args:  cobra.MaximumNArgs(1),
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
		return commitAndReload(s, fmt.Sprintf("unparked %s", dir))
	},
}

var linkCmd = &cobra.Command{
	Use:   "link [name]",
	Short: "Link the current directory as <name>.test (defaults to dir basename)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		name := filepath.Base(cwd)
		if len(args) == 1 {
			name = args[0]
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		site.AddLink(s, site.Link{Name: name, Path: cwd, Secure: true})
		return commitAndReload(s, fmt.Sprintf("linked %s → %s.test", cwd, name))
	},
}

var unlinkCmd = &cobra.Command{
	Use:   "unlink <name>",
	Short: "Remove a linked site",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		s, err := site.Load()
		if err != nil {
			return err
		}
		if !site.RemoveLink(s, args[0]) {
			return fmt.Errorf("no link named %s", args[0])
		}
		return commitAndReload(s, fmt.Sprintf("unlinked %s", args[0]))
	},
}

var isolateCmd = &cobra.Command{
	Use:   "isolate <name> <php-version>",
	Short: "Pin a site to a specific PHP version",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		name, ver := args[0], args[1]
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
		return fmt.Errorf("no link named %s — for parked sites, use `link %s` first to override", name, name)
	},
}

var secureCmd = &cobra.Command{
	Use:   "secure <name>",
	Short: "Toggle HTTPS for a linked site (default: on)",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		s, err := site.Load()
		if err != nil {
			return err
		}
		for i, l := range s.Links {
			if l.Name == args[0] {
				s.Links[i].Secure = !l.Secure
				state := "on"
				if !s.Links[i].Secure {
					state = "off"
				}
				return commitAndReload(s, fmt.Sprintf("secure %s: %s", args[0], state))
			}
		}
		return fmt.Errorf("no link named %s", args[0])
	},
}

func init() {
	rootCmd.AddCommand(parkCmd, unparkCmd, linkCmd, unlinkCmd, isolateCmd, secureCmd)
}

func resolveDir(args []string) (string, error) {
	if len(args) == 1 {
		return filepath.Abs(args[0])
	}
	return os.Getwd()
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
