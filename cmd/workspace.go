package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
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
	workspaceDryRun   bool
	workspaceSite     string
	workspaceCopy     []string
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

var workspacePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Drop test databases that removed worktrees left behind",
	Long: `Drops the test databases of worktrees that no longer exist. 'routa workspace
rm' does this after every removal; run it yourself after removing a worktree
another way, such as with 'git worktree remove'.

A database counts as left behind when it is named <test database>_<name>, such
as testing_4764, after one of the main checkout's test databases, and no
current worktree has that name. Its _test_N parallel copies go with it. The
main checkout's own test databases, and any database a current checkout's .env
or .env.testing names, are never dropped. Only servers on this machine are
touched. routa learns the servers and names by booting the main checkout's app
with APP_ENV=testing.`,
	Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		main, _, _, err := workspaceRepo()
		if err != nil {
			return err
		}
		return pruneStaleTestDatabases(main, workspaceDryRun)
	},
}

func init() {
	workspaceCmd.PersistentFlags().StringVar(&workspaceSite, "site", "", "site to work on, instead of the one whose repository holds the current folder")
	workspacePruneCmd.Flags().BoolVar(&workspaceDryRun, "dry-run", false, "list what would be dropped without dropping it")
	workspaceAddCmd.Flags().StringVar(&workspaceBranch, "branch", "", "branch to create or check out (default: the workspace name)")
	workspaceAddCmd.Flags().StringVar(&workspaceFrom, "from", "", "commit or branch the new branch starts from (default: the remote's main branch, fetched first; HEAD for the main checkout's current branch)")
	workspaceAddCmd.Flags().StringVar(&workspaceDatabase, "database", "clone", "clone or empty")
	workspaceAddCmd.Flags().StringSliceVar(&workspaceCopy, "copy", nil, "also copy this ignored path from the parent (repeatable)")
	workspaceListCmd.Flags().BoolVar(&workspaceListJSON, "json", false, "emit machine-readable JSON")
	workspaceRmCmd.Flags().BoolVar(&workspaceForce, "force", false, "remove even with uncommitted changes or untracked files")
	workspaceRmCmd.Flags().BoolVar(&workspaceDropDB, "drop-database", false, "drop the workspace's own database")
	workspaceCmd.AddCommand(workspaceAddCmd, workspaceListCmd, workspaceRmCmd, workspacePruneCmd)
	rootCmd.AddCommand(workspaceCmd)
}

// workspaceRepo finds the repository and parent site to work on: the one
// --site names, or else the one whose repository holds the current folder,
// which may be the main checkout or one of its worktrees.
func workspaceRepo() (main, parent string, st *site.State, err error) {
	st, err = site.Load()
	if err != nil {
		return "", "", nil, err
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", "", nil, err
	}
	if workspaceSite != "" {
		name := strings.TrimSuffix(workspaceSite, ".test")
		dir = ""
		for _, r := range st.Resolve() {
			if r.Name == name && r.Path != "" {
				dir = r.Path
				break
			}
		}
		if dir == "" {
			return "", "", nil, fmt.Errorf("no routa site named %s serves a folder; see 'routa status'", name)
		}
	}
	const hint = "run routa workspace from inside a site's folder, such as the app's main checkout, or name the site with --site <name>"
	main, err = workspace.MainRepo(dir)
	if err != nil {
		return "", "", nil, fmt.Errorf("%s is not inside a git repository; %s", dir, hint)
	}
	parent, ok := st.PrimarySiteForPath(main)
	if !ok {
		return "", "", nil, fmt.Errorf("no routa site serves %s; %s", main, hint)
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
	from := workspaceFrom
	if from == "" && !workspace.BranchExists(main, branch) {
		base, err := workspace.DefaultBase(main)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not fetch %s, so the branch starts from the last fetched copy: %v\n", base, err)
		}
		from = base
	}

	envContent, err := os.ReadFile(filepath.Join(main, ".env"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := planWorkspaceDatabase(main, wtPath, suffix, mode, string(envContent))
	if err != nil {
		return err
	}

	if from != "" {
		fmt.Printf("→ creating worktree %s on new branch %s from %s\n", wtPath, branch, from)
	} else {
		fmt.Printf("→ creating worktree %s on existing branch %s\n", wtPath, branch)
	}
	if err := workspace.AddWorktree(main, wtPath, branch, from); err != nil {
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
	plan := workspace.CopyPlan(ignored, worktreesInside(main), db.skip, workspaceCopy)
	if err := workspace.CopyPaths(main, wtPath, plan, db.skip, os.Stdout); err != nil {
		return fail(err)
	}

	newSite := site.WorktreeSiteName(parent, main, wtPath)
	if sites := sitesAtPath(st, wtPath); len(sites) > 0 {
		newSite = sites[0].Name
	}
	envPlan, err := planWorkspaceEnv(main, wtPath, suffix, string(envContent))
	if err != nil {
		return fail(err)
	}
	if len(envContent) > 0 {
		rewritten := workspace.RewriteEnv(string(envContent), parent, newSite, mergeEnv(db.envSet, envPlan.redis.App))
		if err := os.WriteFile(filepath.Join(wtPath, ".env"), []byte(rewritten), 0o600); err != nil {
			return fail(err)
		}
		fmt.Printf("→ wrote .env for %s.test\n", newSite)
	}
	if set := mergeEnv(envPlan.tests.Set, envPlan.redis.Tests); len(set) > 0 {
		if err := os.WriteFile(filepath.Join(wtPath, ".env.testing"), []byte(workspace.RewriteEnv(envPlan.testEnv, "", "", set)), 0o600); err != nil {
			return fail(err)
		}
		fmt.Println("→ wrote .env.testing with the workspace's test databases")
	}
	tests, redis := envPlan.tests, envPlan.redis
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
	if len(tests.Databases) > 0 {
		fmt.Fprintf(w, "  tests\t%s\n", strings.Join(tests.Databases, ", "))
	}
	if len(redis.App) > 0 {
		fmt.Fprintf(w, "  redis\t%s\n", redis.Summary(redis.App))
	}
	if len(redis.Tests) > 0 {
		fmt.Fprintf(w, "  test redis\t%s\n", redis.Summary(redis.Tests))
	}
	return w.Flush()
}

// workspaceEnv is what a new workspace changes in its .env and .env.testing.
type workspaceEnv struct {
	tests   workspace.TestEnvPlan
	redis   workspace.RedisPlan
	testEnv string // the copied .env.testing; empty when there is none to edit
}

// planWorkspaceEnv gives the workspace its own test databases and Redis
// databases. A .env.testing that git tracks is left alone, since the edit
// would show up as a change to commit.
func planWorkspaceEnv(main, wtPath, suffix, parentEnv string) (workspaceEnv, error) {
	var plan workspaceEnv
	data, err := os.ReadFile(filepath.Join(wtPath, ".env.testing"))
	hasTestEnv := err == nil
	if err != nil && !os.IsNotExist(err) {
		return plan, err
	}
	if hasTestEnv && workspace.IsTracked(main, ".env.testing") {
		fmt.Println("  warning: git tracks .env.testing, so routa left it alone and tests use the parent's test databases")
		hasTestEnv = false
	}
	testEnv := map[string]string{}
	if hasTestEnv {
		plan.testEnv = string(data)
		testEnv = workspace.ParseEnv(plan.testEnv)
		plan.tests = workspace.PlanTestDatabases(testEnv, suffix)
	}

	checkouts := []string{main}
	if wts, err := workspace.ListWorktrees(main); err == nil {
		for _, wt := range wts {
			if filepath.Clean(wt.Path) != filepath.Clean(wtPath) {
				checkouts = append(checkouts, wt.Path)
			}
		}
	}
	parent := workspace.ParseEnv(parentEnv)
	port := parent["REDIS_PORT"]
	if port == "" {
		port = services.RedisDefaultPort
	}
	if owner := services.StandardPortOwner(port); owner != "" {
		if strings.Contains(parentEnv, "REDIS_") {
			fmt.Printf("  warning: the app's Redis is on port %s, the standard port for %s; routa cannot see which databases other apps use there, so this workspace keeps the parent's Redis databases\n", port, owner)
		}
		plan.redis = workspace.RedisPlan{App: map[string]string{}, Tests: map[string]string{}}
		return plan, nil
	}
	plan.redis, err = workspace.PlanRedis(parent, testEnv, hasTestEnv, workspace.UsedRedisDatabases(checkouts), services.RedisDatabaseCount(port))
	return plan, err
}

func mergeEnv(sets ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, set := range sets {
		for k, v := range set {
			out[k] = v
		}
	}
	return out
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
	if owner := services.StandardPortOwner(db.Port); owner != "" {
		return plan, fmt.Errorf("the app's database is on port %s, the standard port for %s; routa never changes data on a server there, since it belongs to a system service, and workspaces never share the parent's database. Move the app to a routa database first, for example: routa db start %s <version> <name> on <port>", db.Port, owner, db.Engine())
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
			return workspace.CloneDatabase(db.Database, tools, name, os.Stdout)
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
	Name                      string `json:"name"`
	Branch                    string `json:"branch"`
	Path                      string `json:"path"`
	URL                       string `json:"url,omitempty"`
	Database                  string `json:"database,omitempty"`
	SharesParentDatabase      bool   `json:"shares_parent_database"`
	SharesParentTestDatabases bool   `json:"shares_parent_test_databases"`
	SharesParentRedis         bool   `json:"shares_parent_redis"`
}

func readEnvFile(path string) map[string]string {
	return workspace.ParseEnv(readFileOrEmpty(path))
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
		e.SharesParentTestDatabases = len(workspace.SharedTestDatabases(readEnvFile(filepath.Join(main, ".env.testing")), readEnvFile(filepath.Join(wt.Path, ".env.testing")))) > 0
		if fileExists(filepath.Join(wt.Path, ".env")) {
			e.SharesParentRedis = len(workspace.SharedRedisDatabases(readEnvFile(filepath.Join(main, ".env")), readEnvFile(filepath.Join(wt.Path, ".env")))) > 0
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
	var sharing, sharingTests, sharingRedis []string
	for _, e := range entries {
		if e.SharesParentTestDatabases {
			sharingTests = append(sharingTests, e.Name)
		}
		if e.SharesParentRedis {
			sharingRedis = append(sharingRedis, e.Name)
		}
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
	for i, name := range sharingTests {
		if i == 0 {
			fmt.Fprintln(cmd.OutOrStderr())
		}
		fmt.Fprintf(cmd.OutOrStderr(), "! %s runs tests against the parent's test databases; add its name to each database in its .env.testing.\n", name)
	}
	for i, name := range sharingRedis {
		if i == 0 {
			fmt.Fprintln(cmd.OutOrStderr())
		}
		fmt.Fprintf(cmd.OutOrStderr(), "! %s uses the parent's Redis databases; give it its own REDIS_DB and REDIS_CACHE_DB in .env.\n", name)
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

	// Read the workspace's databases before git deletes its .env files.
	drop := workspaceServerDatabase(main, target.Path)

	if err := workspace.RemoveWorktree(main, target.Path, workspaceForce); err != nil {
		return err
	}
	fmt.Printf("→ removed worktree %s\n", target.Path)
	switch {
	case drop.name == "":
	case drop.sharedWith != "":
		fmt.Printf("→ kept database %s: %s uses it too\n", drop.name, drop.sharedWith)
	case drop.standardPort != "":
		fmt.Printf("→ kept database %s: port %s is the standard port for %s, and routa never drops data there\n", drop.name, drop.via.Port, drop.standardPort)
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
	if err := pruneStaleTestDatabases(main, false); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: test database cleanup: %v\n", err)
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
	name         string
	via          workspace.Database // connection to drop it through
	standardPort string             // services whose standard port it is on; never dropped
	sharedWith   string             // another checkout that uses it too; never dropped
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
	drop := serverDatabase{name: own.Name, via: parent.Database, standardPort: services.StandardPortOwner(own.Port)}
	for _, dir := range otherCheckouts(main, wtPath) {
		if other, ok := checkoutDatabase(dir); ok && other.Same(own) {
			drop.sharedWith = dir
			break
		}
	}
	return drop
}

// otherCheckouts lists the folders, besides main and exclude, whose .env
// files might name the same databases: the repository's other worktrees and
// every folder a routa site serves.
func otherCheckouts(main, exclude string) []string {
	seen := map[string]bool{filepath.Clean(main): true, filepath.Clean(exclude): true}
	var out []string
	add := func(dir string) {
		if dir == "" || seen[filepath.Clean(dir)] {
			return
		}
		seen[filepath.Clean(dir)] = true
		out = append(out, dir)
	}
	if wts, err := workspace.ListWorktrees(main); err == nil {
		for _, wt := range wts {
			add(wt.Path)
		}
	}
	if st, err := site.Load(); err == nil {
		for _, r := range st.Resolve() {
			add(r.Path)
		}
	}
	return out
}

// pruneStaleTestDatabases drops the test databases that worktrees left
// behind, after pruning git's records of worktrees whose folders are gone.
// With dryRun it only reports.
//
// Several checks keep it from touching anything else:
//   - The names it looks for come only from the main checkout's .env.testing
//     (testing, affiliate_testing), plus a worktree name and _test_N copies.
//   - It only looks at servers the tests connect to, found by booting the
//     main checkout's app with APP_ENV=testing; read-only and production
//     connections are ignored.
//   - The server must be on this machine, off every service's standard port,
//     and already hold the main checkout's test database or its copies.
//   - Names in any routa site's or worktree's .env or .env.testing are kept,
//     as are worktrees that still exist and databases with open connections.
func pruneStaleTestDatabases(main string, dryRun bool) error {
	if !dryRun {
		if err := workspace.PruneWorktrees(main); err != nil {
			return err
		}
	}
	if !fileExists(filepath.Join(main, "artisan")) || !fileExists(filepath.Join(main, "vendor", "autoload.php")) {
		return nil
	}
	var templates []string
	for key, value := range readEnvFile(filepath.Join(main, ".env.testing")) {
		if workspace.IsTestDatabaseKey(key) && workspace.PlainDatabaseName(value) && !slices.Contains(templates, value) {
			templates = append(templates, value)
		}
	}
	if len(templates) == 0 {
		return nil
	}

	wts, err := workspace.ListWorktrees(main)
	if err != nil {
		return err
	}
	liveSuffixes := map[string]bool{}
	checkouts := []string{main}
	for _, wt := range wts {
		if !fileExists(wt.Path) {
			continue // its folder is gone; git prune drops the record
		}
		liveSuffixes[strings.ReplaceAll(site.WorktreeSuffix(main, wt.Path), "-", "_")] = true
		checkouts = append(checkouts, wt.Path)
	}
	if st, err := site.Load(); err == nil {
		for _, r := range st.Resolve() {
			if r.Path != "" {
				checkouts = append(checkouts, r.Path)
			}
		}
	}
	liveNames := map[string]bool{}
	for _, dir := range checkouts {
		for _, file := range []string{".env", ".env.testing"} {
			for key, value := range readEnvFile(filepath.Join(dir, file)) {
				if workspace.IsTestDatabaseKey(key) {
					liveNames[value] = true
				}
			}
		}
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "php", "-r", workspace.LaravelConnectionsScript)
	cmd.Dir = main
	cmd.Env = append(os.Environ(), "APP_ENV=testing")
	out, runErr := cmd.Output()
	conns, err := workspace.ParseConnections(string(out))
	if err != nil {
		if runErr != nil {
			err = runErr
		}
		return fmt.Errorf("could not read the app's test connections: %w", err)
	}

	verb := "dropped"
	if dryRun {
		verb = "would drop"
	}
	found := false
	for _, server := range workspace.TestServers(conns, templates) {
		where := server.Via.Host + ":" + server.Via.Port
		tools, err := databaseToolsFor(server.Via)
		if err != nil {
			return err
		}
		existing, err := workspace.ListDatabases(server.Via, tools)
		if err != nil {
			return fmt.Errorf("list databases on %s: %w", where, err)
		}
		var hosted []string
		for _, t := range server.Templates {
			if workspace.HostsTestSet(existing, t) {
				hosted = append(hosted, t)
			}
		}
		if len(hosted) == 0 {
			continue // not the server these tests run on
		}
		busy, err := workspace.BusyDatabases(server.Via, tools)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: skipped %s: could not see which databases are in use: %v\n", where, err)
			continue
		}
		var stale []string
		for _, db := range workspace.StaleTestDatabases(existing, hosted, liveSuffixes, liveNames) {
			if !busy[db] {
				stale = append(stale, db)
			}
		}
		if len(stale) == 0 {
			continue
		}
		found = true
		if !dryRun {
			if err := workspace.DropDatabases(server.Via, tools, stale); err != nil {
				return err
			}
		}
		sets := workspace.GroupTestSets(stale)
		bases := make([]string, 0, len(sets))
		for base := range sets {
			bases = append(bases, base)
		}
		sort.Strings(bases)
		for _, base := range bases {
			fmt.Printf("→ %s %s on %s (%d databases)\n", verb, base, where, sets[base])
		}
	}
	if !found && dryRun {
		fmt.Println("no stale test databases")
	}
	return nil
}
