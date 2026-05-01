package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/migrate"
	"github.com/scottzirkel/routa/internal/php"
	"github.com/scottzirkel/routa/internal/site"
)

var migrateDryRun bool

var migrateCmd = &cobra.Command{
	Use:   "migrate-from-valet",
	Short: "Import an existing local PHP dev config into routa",
	Long: `Reads ~/.valet/config.json, ~/.valet/Sites/, and ~/.valet/Nginx/ and
imports tracked directories, linked sites, HTTPS toggles, and per-site PHP
isolation. Safe to run repeatedly — duplicates are de-duped.`,
	RunE: runMigrate,
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "show plan without applying")
	rootCmd.AddCommand(migrateCmd)
}

func runMigrate(_ *cobra.Command, _ []string) error {
	cfg, err := migrate.ReadConfig()
	if err != nil {
		return err
	}
	plan, err := migrate.BuildPlan(cfg)
	if err != nil {
		return err
	}

	fmt.Println("Plan:")
	for _, p := range plan.Parked {
		fmt.Printf("  + park   %s\n", p)
	}
	for _, l := range plan.Links {
		extras := []string{}
		if l.Secure {
			extras = append(extras, "https")
		}
		if l.PHP != "" {
			extras = append(extras, "php="+l.PHP)
		}
		fmt.Printf("  + link   %s.test → %s   %s\n", l.Name, l.Path, strings.Join(extras, " "))
	}
	for _, w := range plan.Warnings {
		fmt.Printf("  ! %s\n", w)
	}

	if migrateDryRun {
		fmt.Println("\n(dry-run; nothing written)")
		return nil
	}

	st, err := site.Load()
	if err != nil {
		return err
	}
	for _, p := range plan.Parked {
		site.AddParked(st, p, "")
	}
	for _, l := range plan.Links {
		site.AddLink(st, l)
	}
	if err := site.Save(st); err != nil {
		return err
	}
	if err := site.WriteFragments(st.Resolve()); err != nil {
		return err
	}
	if err := site.ReloadCaddy(); err != nil {
		return fmt.Errorf("reload caddy: %w", err)
	}
	fmt.Printf("\n✓ imported %d tracked dir(s) and %d link(s).\n", len(plan.Parked), len(plan.Links))

	missing := missingPHP(plan, st)
	if len(missing) > 0 {
		fmt.Println("\nPHP versions referenced by imported sites that aren't installed:")
		for _, v := range missing {
			fmt.Printf("  routa php install %s\n", v)
		}
	}
	return nil
}

func missingPHP(plan *migrate.Plan, st *site.State) []string {
	installed := map[string]bool{}
	if v, _ := php.InstalledVersions(); v != nil {
		for _, i := range v {
			installed[i.Version] = true
		}
	}
	if links, _ := php.Symlinks(); links != nil {
		for k := range links {
			installed[k] = true
		}
	}
	missing := map[string]bool{}
	for _, l := range plan.Links {
		if l.PHP != "" && !installed[l.PHP] {
			missing[l.PHP] = true
		}
	}
	out := make([]string, 0, len(missing))
	for v := range missing {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
