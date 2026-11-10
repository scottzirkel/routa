package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/services"
	"github.com/scottzirkel/routa/internal/site"
	"github.com/scottzirkel/routa/internal/workspace"
)

var workspaceFixDryRun bool

var workspaceFixCmd = &cobra.Command{
	Use:   "fix <name>",
	Short: "Bring an existing worktree in line with what 'workspace add' sets up",
	Long: `Fixes a worktree that was made by hand, or by an older routa, so it matches
what 'routa workspace add' sets up. It only changes what is wrong:

- A vendor or node_modules link into another checkout is replaced with the
  worktree's own copy, so it runs its own code.
- Working files from 'add's copy list that are missing are copied over.
- Hosts of the parent or another worktree in .env are renamed to this site.
- A dev database shared with another checkout, or missing on its server,
  is replaced with the worktree's own copy (--database clone or empty).
- Test database names shared with another checkout get the worktree's name.
  Names that are already the worktree's own are kept.
- Redis database numbers another checkout uses are replaced with free ones.

Run with --dry-run first to see the plan.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspaceFix,
}

func init() {
	workspaceFixCmd.Flags().BoolVar(&workspaceFixDryRun, "dry-run", false, "show what would change without changing it")
	workspaceFixCmd.Flags().StringVar(&workspaceDatabase, "database", "clone", "clone or empty, when the worktree needs its own database")
	workspaceFixCmd.Flags().StringSliceVar(&workspaceCopy, "copy", nil, "also copy this ignored path from the parent (repeatable)")
	workspaceCmd.AddCommand(workspaceFixCmd)
}

// fixStep is one change fix makes: a description and the work.
type fixStep struct {
	what string
	do   func() error
}

func runWorkspaceFix(_ *cobra.Command, args []string) error {
	if workspaceDatabase != "clone" && workspaceDatabase != "empty" {
		return fmt.Errorf("--database must be clone or empty; workspaces never share the parent's database")
	}
	main, parent, st, err := workspaceRepo()
	if err != nil {
		return err
	}
	wts, err := workspace.ListWorktrees(main)
	if err != nil {
		return err
	}
	suffix := site.WorktreeSuffix(main, args[0])
	wtPath := ""
	for _, wt := range wts {
		if site.WorktreeSuffix(main, wt.Path) == suffix && fileExists(wt.Path) {
			wtPath = wt.Path
		}
	}
	if wtPath == "" {
		return fmt.Errorf("no worktree named %s; see 'routa workspace list'", args[0])
	}
	newSite := site.WorktreeSiteName(parent, main, wtPath)
	if sites := sitesAtPath(st, wtPath); len(sites) > 0 {
		newSite = sites[0].Name
	}
	others := otherCheckouts(main, wtPath)
	parentEnvContent := readFileOrEmpty(filepath.Join(main, ".env"))
	parentEnv := workspace.ParseEnv(parentEnvContent)

	var steps []fixStep
	var hints []string

	// 1. Dependency links into another checkout.
	var relinked []string
	for _, dir := range []string{"vendor", "node_modules"} {
		link := filepath.Join(wtPath, dir)
		info, err := os.Lstat(link)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, _ := filepath.EvalSymlinks(link)
		if target == "" || strings.HasPrefix(target+"/", filepath.Clean(wtPath)+"/") {
			continue
		}
		relinked = append(relinked, dir+"/")
		steps = append(steps, fixStep{
			what: fmt.Sprintf("replace %s, a link to %s, with the worktree's own copy", dir, target),
			do:   func() error { return os.Remove(link) },
		})
	}

	// 2. Working files from add's copy list that the worktree lacks.
	ignored, err := workspace.IgnoredPaths(main)
	if err != nil {
		return err
	}
	var missing []string
	for _, p := range workspace.CopyPlan(ignored, worktreesInside(main), sqliteSkips(main, parentEnv), workspaceCopy) {
		rel := strings.TrimSuffix(p, "/")
		if _, err := os.Lstat(filepath.Join(wtPath, rel)); os.IsNotExist(err) || slices.Contains(relinked, p) {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		steps = append(steps, fixStep{
			what: fmt.Sprintf("copy from the parent: %s", strings.Join(missing, ", ")),
			do:   func() error { return workspace.CopyPaths(main, wtPath, missing, nil, os.Stdout) },
		})
		if slices.Contains(missing, "vendor/") && readFileOrEmpty(filepath.Join(main, "composer.lock")) != readFileOrEmpty(filepath.Join(wtPath, "composer.lock")) {
			hints = append(hints, "vendor comes from the parent, whose composer.lock differs: run 'routa composer install' in "+wtPath)
		}
		if slices.Contains(missing, "node_modules/") && readFileOrEmpty(filepath.Join(main, "package-lock.json")) != readFileOrEmpty(filepath.Join(wtPath, "package-lock.json")) {
			hints = append(hints, "node_modules comes from the parent, whose package-lock.json differs: run 'npm install' in "+wtPath)
		}
	}

	// The .env files as they will be once copied, so later steps can plan.
	envPath := filepath.Join(wtPath, ".env")
	envContent := readFileOrEmpty(envPath)
	if envContent == "" && slices.Contains(missing, ".env") {
		envContent = parentEnvContent
	}
	testPath := filepath.Join(wtPath, ".env.testing")
	testContent := readFileOrEmpty(testPath)
	parentTestContent := readFileOrEmpty(filepath.Join(main, ".env.testing"))
	if testContent == "" && slices.Contains(missing, ".env.testing") {
		testContent = parentTestContent
	}
	envSet, testSet := map[string]string{}, map[string]string{}
	newEnv := envContent

	// 3. Hosts that belong to the parent or another worktree.
	if envContent != "" {
		hosts := []string{parent}
		for _, wt := range wts {
			if filepath.Clean(wt.Path) == filepath.Clean(wtPath) {
				continue
			}
			for _, r := range sitesAtPath(st, wt.Path) {
				hosts = append(hosts, r.Name)
			}
		}
		for _, from := range hosts {
			if from == newSite {
				continue
			}
			if renamed := workspace.RewriteEnv(newEnv, from, newSite, nil); renamed != newEnv {
				newEnv = renamed
				steps = append(steps, fixStep{what: fmt.Sprintf(".env: rename %s.test hosts to %s.test", from, newSite)})
			}
		}
	}

	// 4. A dev database of its own.
	var dbPlan workspaceDB
	if envContent != "" {
		own := workspace.ProjectDatabaseFor(wtPath, workspace.ParseEnv(newEnv))
		parentDB := workspace.ProjectDatabaseFor(main, parentEnv)
		reason := ""
		switch {
		case own.Same(parentDB):
			reason = "it uses the parent's database " + own.Label()
		case own.Kind == "server":
			for _, dir := range others {
				if other, ok := checkoutDatabase(dir); ok && other.Same(own) {
					reason = fmt.Sprintf("%s also uses %s", dir, own.Name)
					break
				}
			}
			if reason == "" && own.SameServer(parentDB) && services.StandardPortOwner(own.Port) == "" {
				if tools, err := databaseToolsFor(own.Database); err == nil {
					if names, err := workspace.ListDatabases(own.Database, tools); err == nil && !slices.Contains(names, own.Name) {
						reason = own.Name + " does not exist"
					}
				}
			}
		}
		if reason != "" {
			name, err := workspace.CopyName(parentDB.Name, suffix)
			if err != nil {
				return err
			}
			reuse := false
			if parentDB.Kind == "server" {
				if tools, err := databaseToolsFor(parentDB.Database); err == nil {
					if names, err := workspace.ListDatabases(parentDB.Database, tools); err == nil && slices.Contains(names, name) && !databaseNamedBy(others, parentDB, name) {
						reuse = true
					}
				}
			}
			if reuse {
				envSet["DB_DATABASE"] = name
				steps = append(steps, fixStep{what: fmt.Sprintf(".env: %s, so use the existing %s", reason, name)})
			} else {
				plan, err := planWorkspaceDatabase(main, wtPath, suffix, workspaceDatabase, parentEnvContent)
				if err != nil {
					hints = append(hints, fmt.Sprintf("%s, but routa cannot give the worktree its own: %v", reason, err))
				} else {
					dbPlan = plan
					for k, v := range dbPlan.envSet {
						envSet[k] = v
					}
					steps = append(steps, fixStep{what: fmt.Sprintf("%s, so give the worktree its own: %s", reason, dbPlan.summary), do: dbPlan.apply})
				}
			}
		}
	}

	// 5. Test database names shared with another checkout.
	if testContent != "" && workspace.IsTracked(main, ".env.testing") {
		hints = append(hints, "git tracks .env.testing, so routa leaves its test databases and Redis numbers alone")
		testContent = ""
	}
	testEnv := workspace.ParseEnv(testContent)
	if testContent != "" {
		parentTest := workspace.ParseEnv(parentTestContent)
		usedByOthers := map[string]bool{}
		for _, dir := range append([]string{main}, others...) {
			for k, v := range readEnvFile(filepath.Join(dir, ".env.testing")) {
				if workspace.IsTestDatabaseKey(k) {
					usedByOthers[v] = true
				}
			}
		}
		for _, k := range sortedEnvKeys(testEnv) {
			v := testEnv[k]
			if !workspace.IsTestDatabaseKey(k) || !workspace.PlainDatabaseName(v) || !usedByOthers[v] {
				continue
			}
			base := parentTest[k]
			if !workspace.PlainDatabaseName(base) {
				base = v
			}
			testSet[k] = base + "_" + strings.ReplaceAll(suffix, "-", "_")
			steps = append(steps, fixStep{what: fmt.Sprintf(".env.testing: %s=%s is shared, use %s", k, v, testSet[k])})
		}
	}

	// 6. Redis numbers another checkout uses.
	port := parentEnv["REDIS_PORT"]
	if port == "" {
		port = services.RedisDefaultPort
	}
	if strings.Contains(parentEnvContent, "REDIS_") || strings.Contains(envContent, "REDIS_") {
		if owner := services.StandardPortOwner(port); owner != "" {
			hints = append(hints, fmt.Sprintf("Redis is on port %s, the standard port for %s, so routa leaves its numbers alone", port, owner))
		} else {
			used := workspace.UsedRedisDatabases(append([]string{main}, others...))
			wtEnv := workspace.ParseEnv(newEnv)
			appKeys := workspace.RedisKeys(parentEnv, wtEnv)
			var testKeys []string
			if testContent != "" {
				testKeys = workspace.RedisKeys(testEnv, parentEnv)
			}
			clashes := func(env map[string]string, keys []string, also map[int]bool) bool {
				for _, k := range keys {
					if n, ok := workspace.EffectiveRedisDatabase(env, k); !ok || used[n] || also[n] {
						return true
					}
				}
				return false
			}
			appClash := envContent != "" && clashes(wtEnv, appKeys, nil)
			keptApp := map[int]bool{}
			if !appClash {
				for _, k := range appKeys {
					if n, ok := workspace.EffectiveRedisDatabase(wtEnv, k); ok {
						keptApp[n] = true
					}
				}
			}
			testClash := testContent != "" && clashes(testEnv, testKeys, keptApp)
			need := 0
			if appClash {
				need += len(appKeys)
			}
			if testClash {
				need += len(testKeys)
			}
			if need > 0 {
				avoid := map[int]bool{}
				for n := range used {
					avoid[n] = true
				}
				for n := range keptApp {
					avoid[n] = true
				}
				numbers, err := workspace.FreeRedisDatabases(avoid, need, services.RedisDatabaseCount(port))
				if err != nil {
					return err
				}
				i := 0
				if appClash {
					for _, k := range appKeys {
						envSet[k] = strconv.Itoa(numbers[i])
						i++
					}
					steps = append(steps, fixStep{what: fmt.Sprintf(".env: Redis numbers are shared, use %s", workspace.RedisPlan{}.Summary(pick(envSet, appKeys)))})
				}
				if testClash {
					for _, k := range testKeys {
						testSet[k] = strconv.Itoa(numbers[i])
						i++
					}
					steps = append(steps, fixStep{what: fmt.Sprintf(".env.testing: Redis numbers are shared, use %s", workspace.RedisPlan{}.Summary(pick(testSet, testKeys)))})
				}
			}
		}
	}

	// Write the .env files once, after the copy has put them in place.
	envChanged := newEnv != envContent || len(envSet) > 0
	testChanged := len(testSet) > 0
	if envChanged || testChanged {
		steps = append(steps, fixStep{do: func() error {
			if envChanged {
				if err := os.WriteFile(envPath, []byte(workspace.RewriteEnv(newEnv, "", "", envSet)), 0o600); err != nil {
					return err
				}
			}
			if testChanged {
				if err := os.WriteFile(testPath, []byte(workspace.RewriteEnv(testContent, "", "", testSet)), 0o600); err != nil {
					return err
				}
			}
			if fileExists(filepath.Join(wtPath, "artisan")) {
				return runRoutaIn(wtPath, "php", "artisan", "config:clear")
			}
			return nil
		}})
	}
	if dbPlan.migrate {
		steps = append(steps, fixStep{what: "run php artisan migrate in the new database", do: func() error {
			return runRoutaIn(wtPath, "php", "artisan", "migrate", "--no-interaction")
		}})
	}

	described := 0
	for _, s := range steps {
		if s.what != "" {
			described++
		}
	}
	if described == 0 {
		fmt.Printf("%s already matches what 'routa workspace add' sets up\n", wtPath)
		return nil
	}
	verb := "→"
	if workspaceFixDryRun {
		verb = "would"
	}
	fmt.Printf("%s (%s.test)\n", wtPath, newSite)
	for _, s := range steps {
		if s.what != "" {
			fmt.Printf("%s %s\n", verb, s.what)
		}
	}
	if !workspaceFixDryRun {
		for _, s := range steps {
			if s.do != nil {
				if err := s.do(); err != nil {
					return err
				}
			}
		}
	}
	for _, h := range hints {
		fmt.Println("note:", h)
	}
	return nil
}

// databaseNamedBy reports whether a checkout's .env names database name on
// the same server as db.
func databaseNamedBy(dirs []string, db workspace.ProjectDatabase, name string) bool {
	for _, dir := range dirs {
		if other, ok := checkoutDatabase(dir); ok && other.SameServer(db) && other.Name == name {
			return true
		}
	}
	return false
}

// sqliteSkips keeps the generic copy away from the parent's SQLite files;
// the database step copies them when the worktree needs its own.
func sqliteSkips(main string, env map[string]string) []string {
	db := workspace.ProjectDatabaseFor(main, env)
	if db.Kind != "sqlite" {
		return nil
	}
	rel, err := filepath.Rel(main, db.File)
	if err != nil || strings.HasPrefix(rel, "..") {
		return nil
	}
	return []string{rel, rel + "-wal", rel + "-shm", rel + "-journal"}
}

func pick(m map[string]string, keys []string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}

func sortedEnvKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
