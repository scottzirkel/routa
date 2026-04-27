package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/hostr/internal/php"
	"github.com/scottzirkel/hostr/internal/site"
	"github.com/scottzirkel/hostr/internal/systemd"
)

var phpCmd = &cobra.Command{
	Use:   "php",
	Short: "Manage installed PHP versions (install, list, use, rm)",
}

var phpInstallCmd = &cobra.Command{
	Use:   "install <version>",
	Short: "Install a static PHP build (e.g. 8.3, or 8.3.27 for an exact patch)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec := args[0]
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		fmt.Println("→ fetching release index")
		rels, err := php.FetchReleases(ctx)
		if err != nil {
			return fmt.Errorf("fetch releases: %w", err)
		}
		rel, err := php.Resolve(spec, rels)
		if err != nil {
			return err
		}
		fmt.Printf("→ resolved %s → %s\n", spec, rel.Version)

		if err := php.Install(ctx, *rel, os.Stdout); err != nil {
			return err
		}

		// Write fpm config for both forms so either unit instance works.
		for _, s := range []string{rel.Version.String(), rel.Version.MinorString()} {
			if err := php.WriteFPMConfig(s); err != nil {
				return err
			}
		}
		if err := php.EnsureSystemdTemplate(); err != nil {
			return err
		}
		if err := systemd.DaemonReload(); err != nil {
			return err
		}

		// Start the major.minor instance — that's what sites reference by default.
		unit := fmt.Sprintf("hostr-php@%s.service", rel.Version.MinorString())
		if err := systemd.EnableNow(unit); err != nil {
			return err
		}
		fmt.Printf("→ started %s\n", unit)

		// First install also becomes the default; re-render fragments either way.
		st, err := site.Load()
		if err != nil {
			return err
		}
		if st.DefaultPHP == "" {
			st.DefaultPHP = rel.Version.MinorString()
			if err := site.Save(st); err != nil {
				return err
			}
			fmt.Printf("→ default PHP set to %s\n", st.DefaultPHP)
		}
		if err := site.WriteFragments(st.Resolve()); err != nil {
			return err
		}
		if err := site.ReloadCaddy(); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: caddy reload: %v\n", err)
		}
		return nil
	},
}

var phpListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed PHP versions",
	RunE: func(cmd *cobra.Command, _ []string) error {
		installed, err := php.InstalledVersions()
		if err != nil {
			return err
		}
		if len(installed) == 0 {
			fmt.Println("no PHP versions installed. `hostr php install <version>`")
			return nil
		}
		st, _ := site.Load()
		links, _ := php.Symlinks()

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "VERSION\tDEFAULT\tALIAS")
		for _, i := range installed {
			var aliases []string
			for name, target := range links {
				if target == i.Version {
					aliases = append(aliases, name)
				}
			}
			mark := ""
			if st != nil {
				if st.DefaultPHP == i.Version {
					mark = "*"
				}
				for _, a := range aliases {
					if st.DefaultPHP == a {
						mark = "*"
					}
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", i.Version, mark, strings.Join(aliases, ", "))
		}
		return w.Flush()
	},
}

var phpUseCmd = &cobra.Command{
	Use:   "use <version>",
	Short: "Set the default PHP version (used for new sites without isolation)",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if err := requirePHP(args[0]); err != nil {
			return err
		}
		st, err := site.Load()
		if err != nil {
			return err
		}
		st.DefaultPHP = args[0]
		if err := site.Save(st); err != nil {
			return err
		}
		if err := site.WriteFragments(st.Resolve()); err != nil {
			return err
		}
		if err := site.ReloadCaddy(); err != nil {
			return err
		}
		fmt.Printf("default PHP set to %s\n", args[0])
		return nil
	},
}

func requirePHP(version string) error {
	installed, _ := php.InstalledVersions()
	for _, i := range installed {
		if i.Version == version {
			return nil
		}
	}
	links, _ := php.Symlinks()
	if _, ok := links[version]; ok {
		return nil
	}

	// Build "8.3.30 (8.3), 8.4.20 (8.4)" — patch versions with their aliases.
	aliasesOf := map[string][]string{}
	for alias, target := range links {
		aliasesOf[target] = append(aliasesOf[target], alias)
	}
	var labels []string
	for _, i := range installed {
		label := i.Version
		if a, ok := aliasesOf[i.Version]; ok {
			label += " (" + strings.Join(a, ", ") + ")"
		}
		labels = append(labels, label)
	}
	hint := "no PHP versions are installed"
	if len(labels) > 0 {
		hint = "installed: " + strings.Join(labels, ", ")
	}
	return fmt.Errorf("PHP %s is not installed (%s). Run: hostr php install %s", version, hint, version)
}

var phpRmCmd = &cobra.Command{
	Use:   "rm <version>",
	Short: "Remove an installed PHP version",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		v := args[0]
		// stop both the patch and minor instances if present
		_ = systemd.DisableNow(fmt.Sprintf("hostr-php@%s.service", v))
		// also try minor form derived from patch (best-effort)
		if pv, err := php.ParseVersion(v); err == nil {
			_ = systemd.DisableNow(fmt.Sprintf("hostr-php@%s.service", pv.MinorString()))
		}
		if err := php.RemoveVersion(v); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", v)
		return nil
	},
}

func init() {
	phpCmd.AddCommand(phpInstallCmd, phpListCmd, phpUseCmd, phpRmCmd)
	rootCmd.AddCommand(phpCmd)
}
