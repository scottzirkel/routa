package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/services"
	"github.com/scottzirkel/routa/internal/site"
	"github.com/scottzirkel/routa/internal/workspace"
)

var (
	workspaceBranch   string
	workspaceFrom     string
	workspaceDatabase string
	workspaceListJSON bool
	workspaceForce    bool
	workspaceDropDB   bool
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Create, list, and remove git worktrees of a site",
	Long: `A workspace is a git worktree of a site's repository with its own .test
domain, so several branches (or coding agents) can run side by side.

'routa workspace add 5194' inside ~/code/shop creates ~/code/shop-5194 on a new
branch, served at https://shop-5194.test. routa copies the parent's ignored
files that a working copy needs (.env, vendor/, node_modules/, build output),
using copy-on-write on filesystems that support it, and renames the site's
hosts in the new .env. Subdomains such as app.shop-5194.test work too.

routa also serves worktrees it did not create, such as those coding agents
make inside the repository, at <site>-<folder name>.test after 'routa reload'.`,
}

var workspaceAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Create a worktree of the current site with its own .test domain",
	Long: `Creates a worktree beside the current site's repository and serves it.

The database is shared with the parent by default. --database clone copies the
parent's MySQL, MariaDB, Postgres, or SQLite database into a new one named
<database>_<name>. --database empty creates that database empty and runs
'php artisan migrate' when the app has artisan.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspaceAdd,
}

var workspaceListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the worktrees of the current site's repository",
	Args:    cobra.NoArgs,
	RunE:    runWorkspaceList,
}

var workspaceRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"remove"},
	Short:   "Remove a worktree of the current site",
	Long: `Removes a worktree and stops serving it. git refuses to remove a worktree
with changes or untracked files unless --force is passed; ignored files such
as vendor/ do not count. The branch is kept.

--drop-database also drops the workspace's database, but only when its .env
names a different database than the parent's.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspaceRm,
}

func init() {
	workspaceAddCmd.Flags().StringVar(&workspaceBranch, "branch", "", "branch to create or check out (default: the workspace name)")
	workspaceAddCmd.Flags().StringVar(&workspaceFrom, "from", "", "commit or branch the new branch starts from (default: HEAD)")
	workspaceAddCmd.Flags().StringVar(&workspaceDatabase, "database", "share", "share, clone, or empty")
	workspaceListCmd.Flags().BoolVar(&workspaceListJSON, "json", false, "emit machine-readable JSON")
	workspaceRmCmd.Flags().BoolVar(&workspaceForce, "force", false, "remove even with uncommitted changes or untracked files")
	workspaceRmCmd.Flags().BoolVar(&workspaceDropDB, "drop-database", false, "drop the workspace's own database")
	workspaceCmd.AddCommand(workspaceAddCmd, workspaceListCmd, workspaceRmCmd)
	rootCmd.AddCommand(workspaceCmd)
}

// workspaceRepo finds the repository and parent site for the current
// directory, which may be inside the main checkout or one of its worktrees.
func workspaceRepo() (main, parent string, st *site.State, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", nil, err
	}
	main, err = workspace.MainRepo(cwd)
	if err != nil {
		return "", "", nil, err
	}
	st, err = site.Load()
	if err != nil {
		return "", "", nil, err
	}
	parent, ok := st.PrimarySiteForPath(main)
	if !ok {
		return "", "", nil, fmt.Errorf("no routa site serves %s; run 'routa link' there first", main)
	}
	return main, parent, st, nil
}

func runWorkspaceAdd(_ *cobra.Command, args []string) error {
	mode := workspaceDatabase
	if mode != "share" && mode != "clone" && mode != "empty" {
		return fmt.Errorf("--database must be share, clone, or empty")
	}
	main, parent, st, err := workspaceRepo()
	if err != nil {
		return err
	}
	suffix := site.WorktreeSuffix(main, args[0])
	if suffix == "" {
		return fmt.Errorf("invalid workspace name %q", args[0])
	}
	wtPath := filepath.Join(filepath.Dir(main), filepath.Base(main)+"-"+suffix)
	if _, err := os.Lstat(wtPath); err == nil {
		return fmt.Errorf("%s already exists", wtPath)
	}
	branch := workspaceBranch
	if branch == "" {
		branch = args[0]
	}

	envContent, err := os.ReadFile(filepath.Join(main, ".env"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := planWorkspaceDatabase(main, wtPath, suffix, mode, string(envContent))
	if err != nil {
		return err
	}

	fmt.Printf("→ creating worktree %s on branch %s\n", wtPath, branch)
	if err := workspace.AddWorktree(main, wtPath, branch, workspaceFrom); err != nil {
		return err
	}
	fail := func(err error) error {
		return fmt.Errorf("%w\nthe worktree exists at %s; finish by hand or run 'routa workspace rm %s --force'", err, wtPath, suffix)
	}

	fmt.Println("→ copying ignored files from the parent")
	ignored, err := workspace.IgnoredPaths(main)
	if err != nil {
		return fail(err)
	}
	plan := workspace.CopyPlan(ignored, worktreesInside(main), db.skip)
	if err := workspace.CopyPaths(main, wtPath, plan, db.skip, os.Stdout); err != nil {
		return fail(err)
	}

	newSite := site.WorktreeSiteName(parent, main, wtPath)
	if sites := sitesAtPath(st, wtPath); len(sites) > 0 {
		newSite = sites[0].Name
	}
	if len(envContent) > 0 {
		rewritten := workspace.RewriteEnv(string(envContent), parent, newSite, db.envSet)
		if err := os.WriteFile(filepath.Join(wtPath, ".env"), []byte(rewritten), 0o600); err != nil {
			return fail(err)
		}
		fmt.Printf("→ wrote .env for %s.test\n", newSite)
	}
	if err := db.apply(); err != nil {
		return fail(err)
	}

	if err := commitAndReload(st, fmt.Sprintf("serving %s.test", newSite)); err != nil {
		return fail(err)
	}
	if db.migrate {
		if err := runRoutaIn(wtPath, "php", "artisan", "migrate", "--no-interaction"); err != nil {
			return fail(fmt.Errorf("migrate: %w", err))
		}
	}

	fmt.Printf("\nworkspace %s ready\n", suffix)
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintf(w, "  path\t%s\n", wtPath)
	fmt.Fprintf(w, "  branch\t%s\n", branch)
	fmt.Fprintf(w, "  url\thttps://%s.test\n", newSite)
	if db.summary != "" {
		fmt.Fprintf(w, "  database\t%s\n", db.summary)
	}
	return w.Flush()
}

// workspaceDB is what a new workspace does about its database.
type workspaceDB struct {
	skip    []string          // parent paths the generic copy must leave alone
	envSet  map[string]string // .env keys to set in the worktree
	apply   func() error
	migrate bool
	summary string
}

func planWorkspaceDatabase(main, wtPath, suffix, mode, envContent string) (workspaceDB, error) {
	plan := workspaceDB{envSet: map[string]string{}, apply: func() error { return nil }}
	env := workspace.ParseEnv(envContent)
	db := workspace.DatabaseFromEnv(env)
	hasArtisan := fileExists(filepath.Join(main, "artisan"))

	switch db.Engine() {
	case "sqlite":
		return planSQLiteWorkspace(plan, main, wtPath, mode, db, hasArtisan)
	case "mysql", "mariadb", "postgres":
	default:
		if mode != "share" {
			if envContent == "" {
				return plan, fmt.Errorf("--database %s needs a .env with DB_CONNECTION in %s", mode, main)
			}
			return plan, fmt.Errorf("--database %s does not support DB_CONNECTION=%q", mode, db.Connection)
		}
		return plan, nil
	}

	if mode == "share" {
		if db.Name != "" {
			plan.summary = db.Name + " (shared with the parent)"
		}
		return plan, nil
	}
	if db.Name == "" {
		return plan, fmt.Errorf("--database %s needs DB_DATABASE in %s/.env", mode, main)
	}
	name, err := workspace.CopyName(db.Name, suffix)
	if err != nil {
		return plan, err
	}
	tools, err := databaseToolsFor(db)
	if err != nil {
		return plan, err
	}
	plan.envSet["DB_DATABASE"] = name
	if mode == "clone" {
		plan.summary = fmt.Sprintf("%s (cloned from %s)", name, db.Name)
		plan.apply = func() error {
			fmt.Printf("→ cloning database %s into %s\n", db.Name, name)
			return workspace.CloneDatabase(db, tools, name)
		}
		return plan, nil
	}
	plan.summary = name + " (empty)"
	plan.migrate = hasArtisan
	plan.apply = func() error {
		fmt.Printf("→ creating empty database %s\n", name)
		return workspace.CreateDatabase(db, tools, name)
	}
	return plan, nil
}

func planSQLiteWorkspace(plan workspaceDB, main, wtPath, mode string, db workspace.Database, hasArtisan bool) (workspaceDB, error) {
	parentFile := db.Name
	if parentFile == "" {
		parentFile = filepath.Join(main, "database", "database.sqlite")
	} else if !filepath.IsAbs(parentFile) {
		parentFile = filepath.Join(main, parentFile)
	}
	rel, err := filepath.Rel(main, parentFile)
	if err != nil || strings.HasPrefix(rel, "..") {
		if mode != "share" {
			return plan, fmt.Errorf("--database %s only supports SQLite files inside the project; %s is outside it", mode, parentFile)
		}
		plan.summary = parentFile + " (shared with the parent)"
		return plan, nil
	}
	for _, ext := range []string{"", "-wal", "-shm", "-journal"} {
		plan.skip = append(plan.skip, rel+ext)
	}
	wtFile := filepath.Join(wtPath, rel)

	switch mode {
	case "share":
		plan.envSet["DB_DATABASE"] = parentFile
		plan.summary = parentFile + " (shared with the parent)"
	case "clone":
		if filepath.IsAbs(db.Name) {
			plan.envSet["DB_DATABASE"] = wtFile
		}
		plan.summary = wtFile + " (cloned)"
		plan.apply = func() error {
			if !fileExists(parentFile) {
				return fmt.Errorf("SQLite database %s does not exist", parentFile)
			}
			fmt.Printf("→ cloning %s\n", rel)
			return workspace.CopyFile(parentFile, wtFile)
		}
	case "empty":
		if filepath.IsAbs(db.Name) {
			plan.envSet["DB_DATABASE"] = wtFile
		}
		plan.summary = wtFile + " (empty)"
		plan.migrate = hasArtisan
		plan.apply = func() error {
			if err := os.MkdirAll(filepath.Dir(wtFile), 0o755); err != nil {
				return err
			}
			return os.WriteFile(wtFile, nil, 0o644)
		}
	}
	return plan, nil
}

// databaseToolsFor picks client tools that match the server: when the port
// belongs to a routa-managed database, that engine and version decide.
func databaseToolsFor(db workspace.Database) (services.DatabaseTools, error) {
	engine, version := db.Engine(), ""
	if db.Host == "127.0.0.1" {
		if entries, err := databaseListEntries(); err == nil {
			for _, e := range entries {
				if e.Port == db.Port {
					engine, version = e.Engine, e.Version
					break
				}
			}
		}
	}
	return services.FindDatabaseTools(engine, version)
}

// worktreesInside lists worktree folders inside main, relative to it, so the
// copy never duplicates one (agents often keep theirs in .claude/worktrees).
func worktreesInside(main string) []string {
	wts, err := workspace.ListWorktrees(main)
	if err != nil {
		return nil
	}
	var out []string
	for _, wt := range wts {
		if rel, err := filepath.Rel(main, wt.Path); err == nil && !strings.HasPrefix(rel, "..") {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}

// sitesAtPath returns the sites served from exactly path, most primary first.
func sitesAtPath(st *site.State, path string) []site.Resolved {
	clean := filepath.Clean(path)
	var out []site.Resolved
	for _, r := range st.Resolve() {
		if r.Path != "" && filepath.Clean(r.Path) == clean && r.AliasOf == "" {
			out = append(out, r)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && lessPrimary(out[j].Name, out[j-1].Name); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func lessPrimary(a, b string) bool {
	if da, db := strings.Count(a, "."), strings.Count(b, "."); da != db {
		return da < db
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func runRoutaIn(dir string, args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

type workspaceListEntry struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	URL    string `json:"url,omitempty"`
}

func runWorkspaceList(cmd *cobra.Command, _ []string) error {
	main, _, st, err := workspaceRepo()
	if err != nil {
		return err
	}
	wts, err := workspace.ListWorktrees(main)
	if err != nil {
		return err
	}
	entries := []workspaceListEntry{}
	for _, wt := range wts {
		e := workspaceListEntry{Name: site.WorktreeSuffix(main, wt.Path), Branch: wt.Branch, Path: wt.Path}
		if sites := sitesAtPath(st, wt.Path); len(sites) > 0 {
			e.URL = resolvedSiteURL(sites[0])
		}
		entries = append(entries, e)
	}
	if workspaceListJSON {
		return writeJSON(cmd.OutOrStdout(), entries)
	}
	if len(entries) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "no worktrees of %s. Create one with 'routa workspace add <name>'.\n", main)
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tBRANCH\tURL\tPATH")
	for _, e := range entries {
		branch, url := e.Branch, e.URL
		if branch == "" {
			branch = "(detached)"
		}
		if url == "" {
			url = "- (run routa reload)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, branch, url, e.Path)
	}
	return w.Flush()
}

func runWorkspaceRm(_ *cobra.Command, args []string) error {
	main, _, st, err := workspaceRepo()
	if err != nil {
		return err
	}
	wts, err := workspace.ListWorktrees(main)
	if err != nil {
		return err
	}
	want := site.WorktreeSuffix(main, args[0])
	var target *workspace.Worktree
	for i, wt := range wts {
		if site.WorktreeSuffix(main, wt.Path) == want {
			target = &wts[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("no worktree named %s; see 'routa workspace list'", args[0])
	}
	cwd, _ := os.Getwd()
	if rel, err := filepath.Rel(target.Path, cwd); err == nil && !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("you are inside %s; run this from the parent checkout", target.Path)
	}

	// Read the workspace's database before git deletes its .env.
	var dropName string
	var dropVia workspace.Database
	if workspaceDropDB {
		dropName, dropVia, err = workspaceOwnDatabase(main, target.Path)
		if err != nil {
			return err
		}
	}

	if err := workspace.RemoveWorktree(main, target.Path, workspaceForce); err != nil {
		return err
	}
	fmt.Printf("→ removed worktree %s\n", target.Path)
	if dropName != "" {
		tools, err := databaseToolsFor(dropVia)
		if err != nil {
			return err
		}
		if err := workspace.DropDatabase(dropVia, tools, dropName); err != nil {
			return err
		}
		fmt.Printf("→ dropped database %s\n", dropName)
	}
	if err := commitAndReload(st, fmt.Sprintf("stopped serving workspace %s", want)); err != nil {
		return err
	}
	if target.Branch != "" {
		fmt.Printf("branch %s is kept; delete it with: git branch -d %s\n", target.Branch, target.Branch)
	}
	return nil
}

// workspaceOwnDatabase returns the database a workspace's .env names when
// it differs from the parent's on the same server, and the parent connection
// to drop it through. A shared or SQLite database returns an empty name.
func workspaceOwnDatabase(main, wtPath string) (string, workspace.Database, error) {
	wtEnv, err := os.ReadFile(filepath.Join(wtPath, ".env"))
	if err != nil {
		return "", workspace.Database{}, fmt.Errorf("--drop-database: read %s/.env: %w", wtPath, err)
	}
	parentEnv, _ := os.ReadFile(filepath.Join(main, ".env"))
	own := workspace.DatabaseFromEnv(workspace.ParseEnv(string(wtEnv)))
	parent := workspace.DatabaseFromEnv(workspace.ParseEnv(string(parentEnv)))
	switch own.Engine() {
	case "mysql", "mariadb", "postgres":
	default:
		fmt.Println("→ no server database to drop")
		return "", own, nil
	}
	if own.Name == "" || own.Name == parent.Name || own.Host != parent.Host || own.Port != parent.Port {
		fmt.Printf("→ database %s is shared with the parent; not dropping it\n", own.Name)
		return "", own, nil
	}
	return own.Name, parent, nil
}
