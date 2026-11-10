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

Every workspace gets its own database, read from the parent's .env; it never
shares the parent's, so migrations and seeders in a workspace cannot touch the
parent's data. By default (--database clone) routa copies the parent's MySQL,
MariaDB, Postgres, or SQLite database into a new one named <database>_<name>.
--database empty creates that database empty and runs 'php artisan migrate'
when the app has artisan. When routa cannot give the workspace its own
database, for example because .env uses DB_URL, it refuses before creating
anything.`,
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

The workspace's database is kept unless --drop-database is passed. routa only
ever drops a database on the parent's server that differs from the parent's
own; a SQLite file goes with the worktree.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspaceRm,
}

func init() {
	workspaceAddCmd.Flags().StringVar(&workspaceBranch, "branch", "", "branch to create or check out (default: the workspace name)")
	workspaceAddCmd.Flags().StringVar(&workspaceFrom, "from", "", "commit or branch the new branch starts from (default: HEAD)")
	workspaceAddCmd.Flags().StringVar(&workspaceDatabase, "database", "clone", "clone or empty")
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
	if mode != "clone" && mode != "empty" {
		return fmt.Errorf("--database must be clone or empty; workspaces never share the parent's database")
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

// planWorkspaceDatabase gives the workspace its own database. Workspaces
// never share the parent's: a migration or seeder run in one would change the
// parent's data. When routa cannot give the workspace its own database it
// refuses, before creating anything.
func planWorkspaceDatabase(main, wtPath, suffix, mode, envContent string) (workspaceDB, error) {
	plan := workspaceDB{envSet: map[string]string{}, apply: func() error { return nil }}
	db := workspace.ProjectDatabaseFor(main, workspace.ParseEnv(envContent))
	hasArtisan := fileExists(filepath.Join(main, "artisan"))
	refuse := func(reason string) error {
		return fmt.Errorf("%s, so routa cannot give the workspace its own database (workspaces never share the parent's)", reason)
	}

	switch db.Kind {
	case "none":
		return plan, nil
	case "url":
		return plan, refuse("DB_URL is set in " + main + "/.env")
	case "unknown":
		return plan, refuse(db.Problem)
	case "sqlite":
		return planSQLiteWorkspace(plan, main, wtPath, mode, db.File, hasArtisan)
	}

	if db.Name == "" {
		return plan, refuse("DB_DATABASE is not set in " + main + "/.env")
	}
	name, err := workspace.CopyName(db.Name, suffix)
	if err != nil {
		return plan, err
	}
	tools, err := databaseToolsFor(db.Database)
	if err != nil {
		return plan, err
	}
	plan.envSet["DB_DATABASE"] = name
	if mode == "clone" {
		plan.summary = fmt.Sprintf("%s (cloned from %s)", name, db.Name)
		plan.apply = func() error {
			fmt.Printf("→ cloning database %s into %s\n", db.Name, name)
			return workspace.CloneDatabase(db.Database, tools, name)
		}
		return plan, nil
	}
	plan.summary = name + " (empty)"
	plan.migrate = hasArtisan
	plan.apply = func() error {
		fmt.Printf("→ creating empty database %s\n", name)
		return workspace.CreateDatabase(db.Database, tools, name)
	}
	return plan, nil
}

// planSQLiteWorkspace gives the workspace its own copy of a SQLite file at the
// same place inside the worktree. A file outside the project goes to the
// worktree's database/ directory, with DB_DATABASE pointing at it.
func planSQLiteWorkspace(plan workspaceDB, main, wtPath, mode, parentFile string, hasArtisan bool) (workspaceDB, error) {
	var wtFile string
	if rel, err := filepath.Rel(main, parentFile); err == nil && !strings.HasPrefix(rel, "..") {
		for _, ext := range []string{"", "-wal", "-shm", "-journal"} {
			plan.skip = append(plan.skip, rel+ext)
		}
		wtFile = filepath.Join(wtPath, rel)
	} else {
		wtFile = filepath.Join(wtPath, "database", filepath.Base(parentFile))
	}
	// Keep a relative or default DB_DATABASE as it is: it already resolves
	// inside the worktree. Point an absolute one at the worktree's copy.
	if env := workspace.ParseEnv(readFileOrEmpty(filepath.Join(main, ".env"))); filepath.IsAbs(env["DB_DATABASE"]) {
		plan.envSet["DB_DATABASE"] = wtFile
	}

	if mode == "clone" {
		plan.summary = wtFile + " (cloned)"
		plan.apply = func() error {
			if !fileExists(parentFile) {
				return fmt.Errorf("SQLite database %s does not exist", parentFile)
			}
			fmt.Printf("→ cloning %s\n", parentFile)
			return workspace.CopyFile(parentFile, wtFile)
		}
		return plan, nil
	}
	plan.summary = wtFile + " (empty)"
	plan.migrate = hasArtisan
	plan.apply = func() error {
		if err := os.MkdirAll(filepath.Dir(wtFile), 0o755); err != nil {
			return err
		}
		return os.WriteFile(wtFile, nil, 0o644)
	}
	return plan, nil
}

func readFileOrEmpty(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
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
	Name                 string `json:"name"`
	Branch               string `json:"branch"`
	Path                 string `json:"path"`
	URL                  string `json:"url,omitempty"`
	Database             string `json:"database,omitempty"`
	SharesParentDatabase bool   `json:"shares_parent_database"`
}

// checkoutDatabase reads the database a checkout's .env points at. A checkout
// without a .env has no database of its own to compare.
func checkoutDatabase(dir string) (workspace.ProjectDatabase, bool) {
	data, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		return workspace.ProjectDatabase{}, false
	}
	return workspace.ProjectDatabaseFor(dir, workspace.ParseEnv(string(data))), true
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
	parentDB, hasParentDB := checkoutDatabase(main)
	entries := []workspaceListEntry{}
	for _, wt := range wts {
		e := workspaceListEntry{Name: site.WorktreeSuffix(main, wt.Path), Branch: wt.Branch, Path: wt.Path}
		if sites := sitesAtPath(st, wt.Path); len(sites) > 0 {
			e.URL = resolvedSiteURL(sites[0])
		}
		if own, ok := checkoutDatabase(wt.Path); ok {
			e.Database = own.Label()
			e.SharesParentDatabase = hasParentDB && own.Same(parentDB)
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
	fmt.Fprintln(w, "NAME\tBRANCH\tURL\tDATABASE\tPATH")
	var sharing []string
	for _, e := range entries {
		branch, url, database := e.Branch, e.URL, e.Database
		if branch == "" {
			branch = "(detached)"
		}
		if url == "" {
			url = "- (run routa reload)"
		}
		if database == "" {
			database = "-"
		}
		if e.SharesParentDatabase {
			database += " (SHARED)"
			sharing = append(sharing, e.Name)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.Name, branch, url, database, e.Path)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for i, name := range sharing {
		if i == 0 {
			fmt.Fprintln(cmd.OutOrStderr())
		}
		fmt.Fprintf(cmd.OutOrStderr(), "! %s shares the parent's database, so migrations and seeders run there change the parent's data.\n", name)
	}
	if len(sharing) > 0 {
		fmt.Fprintln(cmd.OutOrStderr(), "  Give it its own DB_DATABASE, or recreate it with 'routa workspace add'.")
	}
	return nil
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
	drop := workspaceServerDatabase(main, target.Path)

	if err := workspace.RemoveWorktree(main, target.Path, workspaceForce); err != nil {
		return err
	}
	fmt.Printf("→ removed worktree %s\n", target.Path)
	switch {
	case drop.name == "":
	case !workspaceDropDB:
		fmt.Printf("→ kept database %s; pass --drop-database to drop it with the workspace\n", drop.name)
	default:
		tools, err := databaseToolsFor(drop.via)
		if err != nil {
			return err
		}
		if err := workspace.DropDatabase(drop.via, tools, drop.name); err != nil {
			return err
		}
		fmt.Printf("→ dropped database %s\n", drop.name)
	}
	if err := commitAndReload(st, fmt.Sprintf("stopped serving workspace %s", want)); err != nil {
		return err
	}
	if target.Branch != "" {
		fmt.Printf("branch %s is kept; delete it with: git branch -d %s\n", target.Branch, target.Branch)
	}
	return nil
}

type serverDatabase struct {
	name string
	via  workspace.Database // connection to drop it through
}

// workspaceServerDatabase returns the workspace's own database on the
// parent's server, if it has one. The parent's database is never returned,
// so dropping can never touch it. A SQLite file goes with the worktree.
func workspaceServerDatabase(main, wtPath string) serverDatabase {
	own, ok := checkoutDatabase(wtPath)
	parent, hasParent := checkoutDatabase(main)
	if !ok || !hasParent || own.Kind != "server" || own.Name == "" || !own.SameServer(parent) || own.Same(parent) {
		return serverDatabase{}
	}
	// Connect through the parent's database: Postgres cannot drop the
	// database a session is connected to.
	return serverDatabase{name: own.Name, via: parent.Database}
}
