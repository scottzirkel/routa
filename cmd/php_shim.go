package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/paths"
)

// A shell alias only exists inside an interactive shell, so `#!/usr/bin/env php`
// scripts — vendor/bin/pest, vendor/bin/phpunit, composer — never see it and run
// whatever php PATH finds first. The shim is a real executable, so those scripts
// resolve to the same PHP routa itself would run.
const phpShimMarker = "# managed by routa"

var phpShimDir string
var phpShimForce bool

var phpShimCmd = &cobra.Command{
	Use:   "shim",
	Short: "Manage the php shim that puts routa's PHP on your PATH",
}

var phpShimInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install a php shim so scripts and editors use routa's PHP",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		routaBin, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate the routa binary: %w", err)
		}
		dir := shimDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(dir, "php")

		if existing, err := os.ReadFile(path); err == nil {
			if !strings.Contains(string(existing), phpShimMarker) && !phpShimForce {
				return fmt.Errorf("%s already exists and was not created by routa; pass --force to replace it", path)
			}
		} else if !os.IsNotExist(err) {
			return err
		}

		if err := os.WriteFile(path, []byte(phpShimContent(routaBin)), 0o755); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "installed php shim at %s\n", path)

		switch found := firstPHPOnPath(); {
		case found == "":
			fmt.Fprintf(cmd.ErrOrStderr(), "  warning: %s is not on your PATH, so the shim will not be used\n", dir)
		case found != path:
			fmt.Fprintf(cmd.ErrOrStderr(), "  warning: %s comes first on your PATH and will win; move %s ahead of it\n", found, dir)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "an `alias php=\"routa php\"` is now redundant and can be removed")
		return nil
	},
}

var phpShimUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the routa php shim",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		path := filepath.Join(shimDir(), "php")
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			fmt.Fprintf(cmd.OutOrStdout(), "no php shim at %s\n", path)
			return nil
		}
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), phpShimMarker) && !phpShimForce {
			return fmt.Errorf("%s was not created by routa; pass --force to remove it anyway", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", path)
		return nil
	},
}

var phpShimStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show which php scripts and editors will actually run",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		path := filepath.Join(shimDir(), "php")
		installed := false
		if data, err := os.ReadFile(path); err == nil {
			installed = strings.Contains(string(data), phpShimMarker)
		}
		found := firstPHPOnPath()

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tVALUE")
		fmt.Fprintf(w, "shim\t%s\n", path)
		fmt.Fprintf(w, "installed\t%t\n", installed)
		fmt.Fprintf(w, "php on PATH\t%s\n", valueOrDefault(found, "(none)"))
		if ctx, err := currentPHPContext(); err == nil {
			fmt.Fprintf(w, "routa would run\t%s\n", ctx.Bin)
		}
		if err := w.Flush(); err != nil {
			return err
		}

		if installed && found != "" && found != path {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"\nwarning: the shim is installed but %s comes first on your PATH.\n"+
					"  Scripts using #!/usr/bin/env php will run that one instead.\n", found)
		}
		if !installed {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"\nScripts using #!/usr/bin/env php (vendor/bin/pest, composer) do not see a\n"+
					"shell alias and will run %s. Install the shim with: routa php shim install\n",
				valueOrDefault(found, "the system php"))
		}
		return nil
	},
}

func shimDir() string {
	if phpShimDir != "" {
		return phpShimDir
	}
	return paths.UserBinDir()
}

func phpShimContent(routaBin string) string {
	return fmt.Sprintf(`#!/bin/sh
%s — resolves php to the version routa selects for the current directory.
# Remove with: routa php shim uninstall
exec %s php "$@"
`, phpShimMarker, shellQuote(routaBin))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// firstPHPOnPath is what the kernel would pick for a #!/usr/bin/env php script.
func firstPHPOnPath() string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, "php")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		return candidate
	}
	return ""
}

func init() {
	phpShimCmd.AddCommand(phpShimInstallCmd, phpShimUninstallCmd, phpShimStatusCmd)
	for _, c := range []*cobra.Command{phpShimInstallCmd, phpShimUninstallCmd, phpShimStatusCmd} {
		c.Flags().StringVar(&phpShimDir, "dir", "", "Directory to install the shim into (default ~/.local/bin)")
	}
	phpShimInstallCmd.Flags().BoolVar(&phpShimForce, "force", false, "Replace an existing php that routa did not create")
	phpShimUninstallCmd.Flags().BoolVar(&phpShimForce, "force", false, "Remove an existing php that routa did not create")
	phpCmd.AddCommand(phpShimCmd)
}
