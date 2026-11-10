package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Populated at build time via -ldflags. install.sh sets these from git.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show routa version + build info",
	RunE: func(cmd *cobra.Command, _ []string) error {
		if versionJSON {
			return writeJSON(cmd.OutOrStdout(), map[string]string{
				"version": Version,
				"commit":  Commit,
				"built":   BuildDate,
			})
		}
		fmt.Printf("routa %s\n", Version)
		fmt.Printf("  commit: %s\n", Commit)
		fmt.Printf("  built:  %s\n", BuildDate)
		return nil
	},
}

var versionJSON bool

func init() {
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "emit machine-readable JSON")
	rootCmd.AddCommand(versionCmd)
}
