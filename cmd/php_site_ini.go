package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/php"
	"github.com/scottzirkel/routa/internal/site"
)

// phpINISite is the --site flag shared by the routa php ini commands. With it
// they manage a project's settings for web requests instead of a PHP version's.
var phpINISite string

const phpINISiteUsage = "Manage the settings for web requests to this site's project instead of a PHP version's"

// resolveSiteProject finds the PHP site named name, plus every resolved site,
// which callers need to see who shares the project's settings.
func resolveSiteProject(name string) (site.Resolved, []site.Resolved, error) {
	name = strings.TrimSuffix(name, ".test")
	st, err := site.Load()
	if err != nil {
		return site.Resolved{}, nil, err
	}
	sites := st.Resolve()
	for _, r := range sites {
		if r.Name != name {
			continue
		}
		if r.Kind != site.KindPHP {
			return r, sites, fmt.Errorf("%s.test is a %s site; PHP settings only apply to PHP sites", name, r.Kind)
		}
		if r.Path == "" {
			return r, sites, fmt.Errorf("%s.test has no project folder to keep settings for", name)
		}
		return r, sites, nil
	}
	return site.Resolved{}, nil, fmt.Errorf("no site named %s", name)
}

// applySiteINIChange re-renders every PHP-FPM config and reloads the running
// instances. A project's settings can reach sites on several PHP versions.
func applySiteINIChange(out io.Writer, project string, sites []site.Resolved) error {
	if err := php.RefreshFPMConfigsForSites(sites); err != nil {
		return err
	}
	names := php.SitesUsingProject(project, sites)
	if len(names) > 0 {
		fmt.Fprintf(out, "applies to %s\n", strings.Join(names, ", "))
	}
	return nil
}

func showSiteINI(out io.Writer, name string) error {
	r, sites, err := resolveSiteProject(name)
	if err != nil {
		return err
	}
	projects := php.SiteProjectPaths(r, sites)
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	found := false
	for _, project := range projects {
		settings, err := php.LoadSiteINISettings(project)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s\n  project %s\n  applies to %s\n", php.SiteINIPath(project), project,
			strings.Join(php.SitesUsingProject(project, sites), ", "))
		for _, s := range settings {
			if !found {
				fmt.Fprintln(w, "KEY\tVALUE\tFROM")
				found = true
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", s.Key, s.Value, project)
		}
	}
	if !found {
		fmt.Fprintln(out, "no settings")
		return nil
	}
	fmt.Fprintln(out)
	return w.Flush()
}

func siteINIPath(out io.Writer, name string) error {
	r, _, err := resolveSiteProject(name)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, php.SiteINIPath(r.Path))
	return nil
}

func setSiteINI(out io.Writer, name, key, value string) error {
	r, sites, err := resolveSiteProject(name)
	if err != nil {
		return err
	}
	if err := php.SetSiteINISetting(r.Path, key, value); err != nil {
		return err
	}
	fmt.Fprintf(out, "set %s for web requests to %s\n", key, r.Path)
	return applySiteINIChange(out, r.Path, sites)
}

func unsetSiteINI(out io.Writer, name, key string) error {
	r, sites, err := resolveSiteProject(name)
	if err != nil {
		return err
	}
	if err := php.UnsetSiteINISetting(r.Path, key); err != nil {
		return err
	}
	fmt.Fprintf(out, "unset %s for web requests to %s\n", key, r.Path)
	return applySiteINIChange(out, r.Path, sites)
}

func editSiteINI(out io.Writer, name string) error {
	r, sites, err := resolveSiteProject(name)
	if err != nil {
		return err
	}
	if err := php.EnsureSiteINIFile(r.Path); err != nil {
		return err
	}
	if err := runEditor(php.SiteINIPath(r.Path)); err != nil {
		return err
	}
	if _, err := php.LoadSiteINISettings(r.Path); err != nil {
		return err
	}
	return applySiteINIChange(out, r.Path, sites)
}

func runEditor(path string) error {
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
	c := exec.Command(parts[0], append(parts[1:], path)...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

var phpINICopyCmd = &cobra.Command{
	Use:   "copy <from-version> <to-version>",
	Short: "Copy one PHP version's settings and conf.d files to another",
	Long: `Copy one PHP version's settings and conf.d files to another, such as after
moving sites from 8.5 to 8.6. A copied setting replaces the other version's
value for the same key. Extension lines are copied commented out, because a
compiled extension only loads in the PHP version it was built for.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to := args[0], args[1]
		if err := requirePHP(to); err != nil {
			return err
		}
		report, err := php.CopySettings(from, to)
		if err != nil {
			return err
		}
		if printCopyReport(cmd.OutOrStdout(), from, to, report) {
			return applyPHPINIChange(cmd, to)
		}
		return nil
	},
}

// printCopyReport describes what CopySettings did, and reports whether it
// changed anything.
func printCopyReport(out io.Writer, from, to string, report php.CopyReport) bool {
	changed := len(report.Settings) > 0 || len(report.TurnedOff) > 0 || len(report.ConfFiles) > 0
	if !changed && len(report.SkippedConfFiles) == 0 {
		fmt.Fprintf(out, "→ PHP %s has no settings to copy\n", from)
		return false
	}
	if len(report.Settings) > 0 {
		fmt.Fprintf(out, "→ carried PHP %s settings over to %s: %s\n", from, to, strings.Join(report.Settings, ", "))
	}
	for _, name := range report.ConfFiles {
		fmt.Fprintf(out, "→ copied conf.d/%s\n", name)
	}
	for _, name := range report.SkippedConfFiles {
		fmt.Fprintf(out, "  kept PHP %s's own conf.d/%s\n", to, name)
	}
	if len(report.TurnedOff) > 0 {
		fmt.Fprintf(out, "  turned off these extensions until you rebuild them for PHP %s:\n", to)
		for _, line := range report.TurnedOff {
			fmt.Fprintf(out, "    %s\n", line)
		}
	}
	return changed
}
