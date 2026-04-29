package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/hostr/internal/php"
	"github.com/scottzirkel/hostr/internal/site"
	"github.com/scottzirkel/hostr/internal/systemd"
)

var phpCmd = &cobra.Command{
	Use:                "php [args...]",
	Short:              "Run hostr PHP for the current site, or manage installed PHP versions",
	Args:               cobra.ArbitraryArgs,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runPHPProxy(cmd, args)
	},
}

var composerCmd = &cobra.Command{
	Use:                "composer [args...]",
	Short:              "Run Composer with the hostr PHP version for the current site",
	Args:               cobra.ArbitraryArgs,
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runComposerProxy(cmd, args)
	},
}

var whichPHPCmd = &cobra.Command{
	Use:   "which-php",
	Short: "Print the hostr PHP binary selected for the current site",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, err := currentPHPContext()
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), ctx.Bin)
		return nil
	},
}

var phpExtCmd = &cobra.Command{
	Use:   "ext",
	Short: "Inspect compiled PHP extensions",
}

var phpExtListCmd = &cobra.Command{
	Use:   "list <version>",
	Short: "List compiled PHP extensions for a version",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec := args[0]
		if err := requirePHP(spec); err != nil {
			return err
		}
		modules, err := php.Modules(spec)
		if err != nil {
			return err
		}
		for _, module := range modules {
			fmt.Fprintln(cmd.OutOrStdout(), module)
		}
		return nil
	},
}

var phpINICmd = &cobra.Command{
	Use:   "ini",
	Short: "Manage per-version PHP ini settings",
}

var phpINIShowCmd = &cobra.Command{
	Use:   "show <version>",
	Short: "Show per-version PHP ini settings",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec := args[0]
		if err := requirePHP(spec); err != nil {
			return err
		}
		settings, err := php.LoadINISettings(spec)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", php.INIPath(spec))
		if len(settings) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no settings")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tVALUE")
		for _, setting := range settings {
			fmt.Fprintf(w, "%s\t%s\n", setting.Key, setting.Value)
		}
		return w.Flush()
	},
}

var phpINIPathCmd = &cobra.Command{
	Use:   "path <version>",
	Short: "Print the per-version PHP ini path",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requirePHP(args[0]); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), php.INIPath(args[0]))
		return nil
	},
}

var phpINISetCmd = &cobra.Command{
	Use:   "set <version> <key> <value>",
	Short: "Set a per-version PHP ini value",
	Args:  cobra.MinimumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec := args[0]
		if err := requirePHP(spec); err != nil {
			return err
		}
		if err := php.SetINISetting(spec, args[1], strings.Join(args[2:], " ")); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "set %s for PHP %s\n", args[1], spec)
		return applyPHPINIChange(cmd, spec)
	},
}

var phpINIUnsetCmd = &cobra.Command{
	Use:   "unset <version> <key>",
	Short: "Remove a per-version PHP ini value",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec := args[0]
		if err := requirePHP(spec); err != nil {
			return err
		}
		if err := php.UnsetINISetting(spec, args[1]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "unset %s for PHP %s\n", args[1], spec)
		return applyPHPINIChange(cmd, spec)
	},
}

var phpINIEditCmd = &cobra.Command{
	Use:   "edit <version>",
	Short: "Open the per-version PHP ini file in $EDITOR",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		spec := args[0]
		if err := requirePHP(spec); err != nil {
			return err
		}
		if err := php.EnsureINIFile(spec); err != nil {
			return err
		}
		editor := os.Getenv("VISUAL")
		if editor == "" {
			editor = os.Getenv("EDITOR")
		}
		if editor == "" {
			editor = "vi"
		}
		parts := strings.Fields(editor)
		if len(parts) == 0 {
			return fmt.Errorf("empty editor command")
		}
		editArgs := append(parts[1:], php.INIPath(spec))
		c := exec.Command(parts[0], editArgs...)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return err
		}
		if _, err := php.LoadINISettings(spec); err != nil {
			return err
		}
		return applyPHPINIChange(cmd, spec)
	},
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

func applyPHPINIChange(cmd *cobra.Command, spec string) error {
	if err := php.WriteFPMConfig(spec); err != nil {
		return err
	}
	unit := fmt.Sprintf("hostr-php@%s.service", spec)
	if !systemd.IsActive(unit) {
		fmt.Fprintf(cmd.OutOrStdout(), "generated FPM config; %s is not running\n", unit)
		return nil
	}
	if err := systemd.RunSystemctl("--user", "restart", unit); err != nil {
		return fmt.Errorf("restart %s: %w", unit, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "restarted %s\n", unit)
	return nil
}

type phpCLIContext struct {
	Spec      string
	Bin       string
	INIDir    string
	SiteNames []string
}

func currentPHPContext() (*phpCLIContext, error) {
	st, err := site.Load()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	matches := st.ResolvePath(cwd)
	specs := map[string]bool{}
	var siteNames []string
	for _, match := range matches {
		if match.Kind != site.KindPHP || match.PHP == "" {
			continue
		}
		specs[match.PHP] = true
		siteNames = append(siteNames, match.Name)
	}

	spec := st.DefaultPHP
	if len(specs) > 0 {
		var resolved []string
		for s := range specs {
			resolved = append(resolved, s)
		}
		sort.Strings(resolved)
		if len(resolved) > 1 {
			sort.Strings(siteNames)
			return nil, fmt.Errorf("current directory matches sites with different PHP versions (%s): %s", strings.Join(resolved, ", "), strings.Join(siteNames, ", "))
		}
		spec = resolved[0]
	}
	if spec == "" {
		return nil, fmt.Errorf("no hostr PHP version is configured. Run: hostr php install <version>")
	}
	if err := requirePHP(spec); err != nil {
		return nil, err
	}
	bin := php.BinPath(spec)
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("PHP binary for %s not found at %s: %w", spec, bin, err)
	}
	iniDir, err := php.WriteCLIConfig(spec)
	if err != nil {
		return nil, err
	}
	return &phpCLIContext{
		Spec:      spec,
		Bin:       bin,
		INIDir:    iniDir,
		SiteNames: siteNames,
	}, nil
}

func runPHPProxy(_ *cobra.Command, args []string) error {
	ctx, err := currentPHPContext()
	if err != nil {
		return err
	}
	args = trimArgSeparator(args)
	return runWithHostrPHP(ctx, ctx.Bin, args)
}

func runComposerProxy(_ *cobra.Command, args []string) error {
	ctx, err := currentPHPContext()
	if err != nil {
		return err
	}
	composer, err := exec.LookPath("composer")
	if err != nil {
		return fmt.Errorf("composer not found in PATH")
	}
	args = trimArgSeparator(args)
	return runWithHostrPHP(ctx, composer, args)
}

func trimArgSeparator(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}

func runWithHostrPHP(ctx *phpCLIContext, name string, args []string) error {
	c := exec.Command(name, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = envWithHostrPHP(ctx)
	return c.Run()
}

func envWithHostrPHP(ctx *phpCLIContext) []string {
	phpDir := filepath.Dir(ctx.Bin)
	env := os.Environ()
	pathSet := false
	binarySet := false
	phprcSet := false
	for i, value := range env {
		switch {
		case strings.HasPrefix(value, "PATH="):
			env[i] = "PATH=" + phpDir + string(os.PathListSeparator) + strings.TrimPrefix(value, "PATH=")
			pathSet = true
		case strings.HasPrefix(value, "PHP_BINARY="):
			env[i] = "PHP_BINARY=" + ctx.Bin
			binarySet = true
		case strings.HasPrefix(value, "PHPRC="):
			env[i] = "PHPRC=" + ctx.INIDir
			phprcSet = true
		}
	}
	if !pathSet {
		env = append(env, "PATH="+phpDir)
	}
	if !binarySet {
		env = append(env, "PHP_BINARY="+ctx.Bin)
	}
	if !phprcSet {
		env = append(env, "PHPRC="+ctx.INIDir)
	}
	return env
}

var phpRmCmd = &cobra.Command{
	Use:   "rm <version>",
	Short: "Remove an installed PHP version",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		v := args[0]
		stopSpecs := []string{v}
		if target, ok, err := php.AliasTarget(v); err != nil {
			return err
		} else if ok {
			stopSpecs = append(stopSpecs, target)
		}
		for _, spec := range append([]string{}, stopSpecs...) {
			if pv, err := php.ParseVersion(spec); err == nil {
				stopSpecs = append(stopSpecs, pv.MinorString())
			}
		}
		seen := map[string]bool{}
		for _, spec := range stopSpecs {
			if spec == "" || seen[spec] {
				continue
			}
			seen[spec] = true
			_ = systemd.DisableNow(fmt.Sprintf("hostr-php@%s.service", spec))
		}
		if err := php.RemoveVersion(v); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", v)
		return nil
	},
}

func init() {
	phpExtCmd.AddCommand(phpExtListCmd)
	phpINICmd.AddCommand(phpINIShowCmd, phpINIPathCmd, phpINISetCmd, phpINIUnsetCmd, phpINIEditCmd)
	phpINISetCmd.Flags().SetInterspersed(false)
	phpCmd.AddCommand(phpInstallCmd, phpListCmd, phpUseCmd, phpRmCmd)
	phpCmd.AddCommand(phpExtCmd)
	phpCmd.AddCommand(phpINICmd)
	rootCmd.AddCommand(phpCmd, composerCmd, whichPHPCmd)
}
