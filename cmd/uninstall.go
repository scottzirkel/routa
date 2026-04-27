package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/hostr/internal/ca"
	"github.com/scottzirkel/hostr/internal/paths"
	"github.com/scottzirkel/hostr/internal/systemd"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Reverse `hostr install` — stop services, remove units, untrust CA",
	Long: `Stops hostr-caddy and hostr-dns, removes their unit files, and untrusts
the local CA. Does NOT touch sites/PHP versions you've installed — pass
--purge to remove ~/.local/share/hostr/ and ~/.config/hostr/ as well.`,
	RunE: runUninstall,
}

var purge bool

func init() {
	uninstallCmd.Flags().BoolVar(&purge, "purge", false, "also delete data/state/config directories")
	rootCmd.AddCommand(uninstallCmd)
}

func runUninstall(_ *cobra.Command, _ []string) error {
	for _, u := range []string{"hostr-caddy.service", "hostr-dns.service"} {
		fmt.Printf("→ disable %s\n", u)
		_ = systemd.DisableNow(u) // ignore: unit may not exist
		_ = os.Remove(filepath.Join(paths.SystemdUserDir(), u))
	}
	_ = systemd.DaemonReload()

	fmt.Println("→ untrust Caddy local CA (will sudo)")
	if err := ca.Uninstall(); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: %v\n", err)
	}

	if purge {
		for _, d := range []string{paths.DataDir(), paths.StateDir(), paths.ConfigDir()} {
			fmt.Printf("→ rm -rf %s\n", d)
			if err := os.RemoveAll(d); err != nil {
				return err
			}
		}
	}
	fmt.Println("Done.")
	return nil
}
