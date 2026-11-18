package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/workspace"
)

var (
	workspaceActionsJSON bool
	workspaceLogsFollow  bool
	workspaceLogsLines   int
)

const actionsHelp = `Actions are commands that keep running for a workspace, such as a queue
worker or a Vite dev server. The site names them in ` + workspace.ProjectFile + ` in its main
checkout:

  {
    "workspace": {
      "actions": {
        "queue": "php artisan queue:work",
        "vite": "npm run dev"
      }
    }
  }

Each action runs in the worktree as a systemd user unit, with routa's PHP for
the site first on PATH and the rest of PATH from the shell that starts it.
systemd keeps its logs and restarts it if it crashes. Actions start only when
asked: with 'routa workspace start', or 'workspace add --start' once setup
succeeds. 'workspace rm' stops them.

A running Vite writes public/hot, and while that file exists a Laravel app
loads its scripts and styles from Vite instead of the built files. Stop a Vite
action before browser tests that should use the build.`

var workspaceStartCmd = &cobra.Command{
	Use:   "start <name> [action...]",
	Short: "Start a workspace's actions: all of them, or the ones named",
	Long:  actionsHelp,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		t, project, names, err := actionTarget(args[0], args[1:])
		if err != nil {
			return err
		}
		_, err = startActions(t, project, names)
		return err
	},
}

var workspaceStopCmd = &cobra.Command{
	Use:   "stop <name> [action...]",
	Short: "Stop a workspace's actions: all of them, or the ones named",
	Long:  actionsHelp,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		t, err := resolveWorkspace(args[0])
		if err != nil {
			return err
		}
		for _, name := range args[1:] {
			if err := workspace.ValidateActionName(name); err != nil {
				return err
			}
		}
		return stopActions(t, args[1:])
	},
}

var workspaceRestartCmd = &cobra.Command{
	Use:   "restart <name> [action...]",
	Short: "Restart a workspace's actions with their current commands",
	Long:  actionsHelp,
	Args:  cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		t, project, names, err := actionTarget(args[0], args[1:])
		if err != nil {
			return err
		}
		if err := stopActions(t, names); err != nil {
			return err
		}
		_, err = startActions(t, project, names)
		return err
	},
}

var workspaceActionsCmd = &cobra.Command{
	Use:   "actions <name>",
	Short: "List a workspace's actions and whether each is running",
	Long:  actionsHelp,
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkspaceActions,
}

var workspaceLogsCmd = &cobra.Command{
	Use:   "logs <name> <action>",
	Short: "Show the logs of a workspace's action",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		t, err := resolveWorkspace(args[0])
		if err != nil {
			return err
		}
		if err := workspace.ValidateActionName(args[1]); err != nil {
			return err
		}
		jargs := []string{"--user", "-u", workspace.ActionUnit(t.site, args[1]), "-n", strconv.Itoa(workspaceLogsLines), "--no-pager"}
		if workspaceLogsFollow {
			jargs = append(jargs, "-f")
		}
		c := exec.Command("journalctl", jargs...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	},
}

func init() {
	workspaceActionsCmd.Flags().BoolVar(&workspaceActionsJSON, "json", false, "emit machine-readable JSON")
	workspaceLogsCmd.Flags().BoolVarP(&workspaceLogsFollow, "follow", "f", false, "keep printing new lines")
	workspaceLogsCmd.Flags().IntVarP(&workspaceLogsLines, "lines", "n", 100, "how many recent lines to show")
	workspaceCmd.AddCommand(workspaceStartCmd, workspaceStopCmd, workspaceRestartCmd, workspaceActionsCmd, workspaceLogsCmd)
}

// actionResult is one action in --json output.
type actionResult struct {
	Name string `json:"name"`
	// Command is empty for a unit still running an action that the project
	// file no longer lists.
	Command string `json:"command,omitempty"`
	Unit    string `json:"unit"`
	State   string `json:"state"`
}

// actionTarget resolves the workspace and the actions to act on: the names
// given, or every action the project file lists.
func actionTarget(name string, names []string) (workspaceTarget, workspace.Project, []string, error) {
	t, err := resolveWorkspace(name)
	if err != nil {
		return t, workspace.Project{}, nil, err
	}
	project, err := workspace.LoadProject(t.main)
	if err != nil {
		return t, project, nil, err
	}
	defined := project.Workspace.ActionNames()
	if len(defined) == 0 {
		return t, project, nil, fmt.Errorf("the site has no actions; name them under workspace.actions in %s", filepath.Join(t.main, workspace.ProjectFile))
	}
	if len(names) == 0 {
		return t, project, defined, nil
	}
	for _, n := range names {
		if _, ok := project.Workspace.Actions[n]; !ok {
			return t, project, nil, fmt.Errorf("no action named %s; %s lists %s", n, workspace.ProjectFile, strings.Join(defined, ", "))
		}
	}
	return t, project, names, nil
}

// startActions starts each named action that is not running yet. A second
// later it checks on the ones it started, so a command that dies at once is
// reported rather than shown as running.
func startActions(t workspaceTarget, project workspace.Project, names []string) ([]actionResult, error) {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return nil, fmt.Errorf("systemd-run not found; workspace actions run as systemd user units")
	}
	units := make([]string, len(names))
	for i, name := range names {
		units[i] = workspace.ActionUnit(t.site, name)
	}
	states, err := unitStates(units)
	if err != nil {
		return nil, err
	}
	env := workspaceCommandEnv(t.path)
	out := make([]actionResult, 0, len(names))
	var started []int
	for i, name := range names {
		r := actionResult{Name: name, Command: project.Workspace.Actions[name], Unit: units[i]}
		if states[r.Unit].Running() {
			fmt.Printf("→ action %s is running\n", name)
			r.State = "running"
			out = append(out, r)
			continue
		}
		// A unit that failed stays loaded until it is reset, and blocks a
		// new unit with its name.
		_ = exec.Command("systemctl", "--user", "reset-failed", r.Unit).Run()
		desc := fmt.Sprintf("routa action %s for %s.test", name, t.site)
		if b, err := exec.Command("systemd-run", workspace.ActionRunArgs(r.Unit, desc, t.path, env, r.Command)...).CombinedOutput(); err != nil {
			msg := strings.TrimSpace(string(b))
			if msg == "" {
				msg = err.Error()
			}
			return out, fmt.Errorf("start action %s: %s", name, msg)
		}
		fmt.Printf("→ started action %s: %s\n", name, r.Command)
		r.State = "started"
		started = append(started, len(out))
		out = append(out, r)
	}
	if len(started) == 0 {
		return out, nil
	}
	time.Sleep(time.Second)
	states, err = unitStates(units)
	if err != nil {
		return out, nil
	}
	for _, i := range started {
		s := states[out[i].Unit]
		if s.ActiveState == "active" && s.NRestarts == 0 {
			continue
		}
		a := out[i]
		logs := fmt.Sprintf("'routa workspace logs %s %s'", t.name, a.Name)
		switch label := s.Label(); {
		case label == "stopped":
			out[i].State = label
			fmt.Fprintf(os.Stderr, "  warning: action %s finished within a second. Actions are for commands that keep running; a setup step that finishes belongs in workspace.run. See %s\n", a.Name, logs)
		case s.NRestarts > 0 && s.Running():
			out[i].State = "restarting"
			fmt.Fprintf(os.Stderr, "  warning: action %s crashed and is restarting; see %s\n", a.Name, logs)
		default:
			out[i].State = label
			fmt.Fprintf(os.Stderr, "  warning: action %s is %s a second after it started; see %s\n", a.Name, label, logs)
		}
	}
	return out, nil
}

// stopActions stops the named actions, or every action unit the workspace
// has running when none are named, including ones the project file no
// longer lists.
func stopActions(t workspaceTarget, names []string) error {
	var units []string
	if len(names) == 0 {
		loaded, err := loadedUnits(workspace.ActionUnits(t.site))
		if err != nil {
			return err
		}
		units = loaded
		if len(units) == 0 {
			fmt.Println("→ no actions are running")
			return nil
		}
	} else {
		for _, name := range names {
			units = append(units, workspace.ActionUnit(t.site, name))
		}
	}
	return stopUnits(units)
}

// stopUnits stops the units that are loaded and says which ones it stopped.
func stopUnits(units []string) error {
	states, err := unitStates(units)
	if err != nil {
		return err
	}
	var loaded []string
	for _, u := range units {
		_, name, _ := workspace.ActionOfUnit(u)
		s := states[u]
		if s.LoadState != "loaded" || (!s.Running() && s.ActiveState != "failed") {
			fmt.Printf("→ action %s is not running\n", name)
			continue
		}
		loaded = append(loaded, u)
	}
	if len(loaded) == 0 {
		return nil
	}
	if b, err := exec.Command("systemctl", append([]string{"--user", "stop"}, loaded...)...).CombinedOutput(); err != nil {
		return fmt.Errorf("stop %s: %s", strings.Join(loaded, " "), strings.TrimSpace(string(b)))
	}
	_ = exec.Command("systemctl", append([]string{"--user", "reset-failed"}, loaded...)...).Run()
	for _, u := range loaded {
		_, name, _ := workspace.ActionOfUnit(u)
		fmt.Printf("→ stopped action %s\n", name)
	}
	return nil
}

// stopActionsUnder stops every action whose working folder is inside dir,
// whatever site name its unit has. 'workspace rm' calls it before deleting
// the worktree.
func stopActionsUnder(dir string) error {
	units, err := actionUnitsWhere(func(wd string) bool {
		rel, err := filepath.Rel(dir, wd)
		return err == nil && !strings.HasPrefix(rel, "..")
	})
	if err != nil || len(units) == 0 {
		return err
	}
	return stopUnits(units)
}

// actionUnitsWhere lists loaded action units whose working folder matches.
func actionUnitsWhere(match func(workDir string) bool) ([]string, error) {
	loaded, err := loadedUnits(workspace.AllActionUnits)
	if err != nil || len(loaded) == 0 {
		return nil, err
	}
	states, err := unitStates(loaded)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, u := range loaded {
		if wd := states[u].WorkingDirectory; wd != "" && match(wd) {
			out = append(out, u)
		}
	}
	return out, nil
}

// pruneOrphanActions stops actions whose worktree folder is gone, such as
// after 'git worktree remove'.
func pruneOrphanActions(dryRun bool) error {
	units, err := actionUnitsWhere(func(wd string) bool { return !fileExists(wd) })
	if err != nil || len(units) == 0 {
		return err
	}
	if dryRun {
		for _, u := range units {
			site, name, _ := workspace.ActionOfUnit(u)
			fmt.Printf("→ would stop action %s of %s.test, whose worktree is gone\n", name, site)
		}
		return nil
	}
	return stopUnits(units)
}

func runWorkspaceActions(cmd *cobra.Command, args []string) error {
	t, err := resolveWorkspace(args[0])
	if err != nil {
		return err
	}
	project, err := workspace.LoadProject(t.main)
	if err != nil {
		return err
	}
	names := project.Workspace.ActionNames()
	listed := map[string]bool{}
	for _, n := range names {
		listed[n] = true
	}
	loaded, err := loadedUnits(workspace.ActionUnits(t.site))
	if err != nil {
		return err
	}
	for _, u := range loaded {
		if _, name, ok := workspace.ActionOfUnit(u); ok && !listed[name] {
			names = append(names, name)
		}
	}
	units := make([]string, len(names))
	for i, n := range names {
		units[i] = workspace.ActionUnit(t.site, n)
	}
	states, err := unitStates(units)
	if err != nil {
		return err
	}
	entries := make([]actionResult, 0, len(names))
	for i, n := range names {
		entries = append(entries, actionResult{Name: n, Command: project.Workspace.Actions[n], Unit: units[i], State: states[units[i]].Label()})
	}
	if workspaceActionsJSON {
		return writeJSON(cmd.OutOrStdout(), entries)
	}
	if len(entries) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%s has no actions. Name them under workspace.actions in %s.\n", t.name, filepath.Join(t.main, workspace.ProjectFile))
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ACTION\tSTATE\tCOMMAND")
	for _, e := range entries {
		command := e.Command
		if command == "" {
			command = "(no longer in " + workspace.ProjectFile + "; stop it with 'routa workspace stop " + t.name + " " + e.Name + "')"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.Name, e.State, command)
	}
	return w.Flush()
}

// unitStates reads the state of each unit in one systemctl call. Units that
// do not exist come back as not loaded.
func unitStates(units []string) (map[string]workspace.UnitState, error) {
	out := map[string]workspace.UnitState{}
	if len(units) == 0 {
		return out, nil
	}
	args := append([]string{"--user", "show", "--property=" + workspace.UnitStateProperties}, units...)
	b, err := exec.Command("systemctl", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("read the state of %s: %w", strings.Join(units, " "), err)
	}
	for _, s := range workspace.ParseUnitStates(string(b)) {
		out[s.Unit] = s
	}
	return out, nil
}

// loadedUnits lists the loaded units that match pattern, including failed
// ones.
func loadedUnits(pattern string) ([]string, error) {
	b, err := exec.Command("systemctl", "--user", "list-units", "--all", "--plain", "--no-legend", "--type=service", pattern).Output()
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", pattern, err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return out, nil
}
