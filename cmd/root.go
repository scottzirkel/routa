package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/tui"
)

var rootCmd = &cobra.Command{
	Use:   "routa",
	Short: "Local web dev server for Linux: PHP + static sites, per-site PHP versions, auto HTTPS",
	Long: `routa serves local PHP and static sites under *.test with auto-issued HTTPS.
Per-site PHP version isolation. Single static binary. Caddy + php-fpm under
systemd user units. No daemon of its own.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		return tui.Run()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		if exit, ok := err.(interface{ ExitCode() int }); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
