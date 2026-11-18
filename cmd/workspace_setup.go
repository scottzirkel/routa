package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/services"
	"github.com/scottzirkel/routa/internal/systemd"
	"github.com/scottzirkel/routa/internal/workspace"
)

var (
	workspaceRun      []string
	workspaceServices []string
	workspaceNoRun    bool
	workspaceStart    bool
	workspaceJSON     bool
)

var workspaceSetupCmd = &cobra.Command{
	Use:   "setup <name>",
	Short: "Run a workspace's setup steps: services, commands, then actions",
	Long: `Runs the steps 'routa workspace add' runs once a workspace's files and
database are ready, in an existing worktree:

1. Makes sure the routa services the site lists, and any --service, are
   running.
2. Runs the site's setup commands, then any --run, in order, in the worktree.
   It stops at the first command that fails.
3. With --start, starts the site's actions.

The site lists its services, commands, and actions in ` + workspace.ProjectFile + ` in its
main checkout. Use it to finish a workspace whose setup failed, or to set up a
worktree made another way.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspaceSetup,
}

func init() {
	for _, c := range []*cobra.Command{workspaceAddCmd, workspaceSetupCmd} {
		c.Flags().StringArrayVar(&workspaceRun, "run", nil, "also run this setup command in the worktree, after the site's own (repeatable)")
		c.Flags().StringArrayVar(&workspaceServices, "service", nil, "make sure this routa service is running first, such as redis or meilisearch:1.12 (repeatable)")
		c.Flags().BoolVar(&workspaceNoRun, "no-run", false, "skip the setup commands in "+workspace.ProjectFile)
		c.Flags().BoolVar(&workspaceStart, "start", false, "start the actions in "+workspace.ProjectFile+" once setup succeeds")
		c.Flags().BoolVar(&workspaceJSON, "json", false, "print progress to stderr and a JSON summary to stdout")
	}
	workspaceCmd.AddCommand(workspaceSetupCmd)
}

// workspaceSetup is what runs in a workspace once its files and database are
// ready: services first, then commands, then actions.
type workspaceSetup struct {
	project  workspace.Project
	services []serviceSpec
	commands []workspace.SetupCommand
}

// planWorkspaceSetup reads the site's project file in main and the flags.
// Mistakes show up here, before anything is created.
func planWorkspaceSetup(main string) (workspaceSetup, error) {
	project, err := workspace.LoadProject(main)
	if err != nil {
		return workspaceSetup{}, err
	}
	plan := workspaceSetup{project: project}
	seen := map[serviceSpec]bool{}
	for _, name := range append(append([]string{}, project.Workspace.Services...), workspaceServices...) {
		spec, err := parseServiceSpec(name)
		if err != nil {
			return workspaceSetup{}, err
		}
		if !seen[spec] {
			seen[spec] = true
			plan.services = append(plan.services, spec)
		}
	}
	if !workspaceNoRun {
		for _, c := range project.Workspace.Run {
			plan.commands = append(plan.commands, workspace.SetupCommand{Command: c, From: "site"})
		}
	}
	for _, c := range workspaceRun {
		if strings.TrimSpace(c) == "" {
			return workspaceSetup{}, fmt.Errorf("--run needs a command")
		}
		plan.commands = append(plan.commands, workspace.SetupCommand{Command: c, From: "flag"})
	}
	return plan, nil
}

// workspaceResult is the --json summary of 'workspace add' and 'setup'.
type workspaceResult struct {
	Name     string                    `json:"name"`
	Path     string                    `json:"path"`
	Branch   string                    `json:"branch,omitempty"`
	URL      string                    `json:"url,omitempty"`
	Database string                    `json:"database,omitempty"`
	Services []serviceResult           `json:"services"`
	Commands []workspace.CommandResult `json:"commands"`
	Actions  []actionResult            `json:"actions"`
	OK       bool                      `json:"ok"`
}

func newWorkspaceResult(name, path string) workspaceResult {
	return workspaceResult{
		Name:     name,
		Path:     path,
		Services: []serviceResult{},
		Commands: []workspace.CommandResult{},
		Actions:  []actionResult{},
	}
}

// jsonProgress points os.Stdout at stderr while --json is set, so progress
// from routa and from the commands it runs never mixes with the JSON. It
// returns the real stdout for the JSON, and a func that puts it back.
func jsonProgress() (*os.File, func()) {
	stdout := os.Stdout
	if !workspaceJSON {
		return stdout, func() {}
	}
	os.Stdout = os.Stderr
	return stdout, func() { os.Stdout = stdout }
}

func runWorkspaceSetup(_ *cobra.Command, args []string) error {
	stdout, restore := jsonProgress()
	defer restore()
	t, err := resolveWorkspace(args[0])
	if err != nil {
		return err
	}
	plan, err := planWorkspaceSetup(t.main)
	if err != nil {
		return err
	}
	res := newWorkspaceResult(t.name, t.path)
	res.URL = "https://" + t.site + ".test"
	var runErr error
	res.Services, runErr = ensureServices(plan.services)
	if runErr == nil {
		runErr = finishSetup(t, plan, &res, setupHint{name: t.name})
	}
	if runErr == nil {
		fmt.Printf("\nworkspace %s set up\n", t.name)
	}
	if workspaceJSON {
		res.OK = runErr == nil
		if err := writeJSON(stdout, res); err != nil {
			return err
		}
	}
	return runErr
}

// finishSetup runs the setup commands and then starts the actions, filling
// in res. Services are already running. It stops at the first step that
// fails.
func finishSetup(t workspaceTarget, plan workspaceSetup, res *workspaceResult, hint setupHint) error {
	if err := runSetupCommands(t.path, plan.commands, res, hint); err != nil {
		return err
	}
	if !workspaceStart || len(plan.project.Workspace.Actions) == 0 {
		return nil
	}
	actions, err := startActions(t, plan.project, plan.project.Workspace.ActionNames())
	res.Actions = append(res.Actions, actions...)
	return err
}

func runSetupCommands(dir string, commands []workspace.SetupCommand, res *workspaceResult, hint setupHint) error {
	if len(commands) == 0 {
		return nil
	}
	results, failed := workspace.RunSetup(dir, workspaceCommandEnv(dir), commands, os.Stdout, func(i int, c workspace.SetupCommand) {
		fmt.Printf("→ running %s (%d of %d)\n", c.Command, i+1, len(commands))
	})
	res.Commands = append(res.Commands, results...)
	if failed >= 0 {
		return hint.failure(results, failed)
	}
	return nil
}

// workspaceCommandEnv is the environment setup commands and actions run
// with: routa's PHP for the folder first on PATH, as 'routa php' uses.
// Node and everything else come from the caller's PATH.
func workspaceCommandEnv(dir string) []string {
	ctx, err := phpContextForDir(dir)
	if err != nil {
		if !errors.Is(err, errNoPHPConfigured) {
			fmt.Fprintf(os.Stderr, "  warning: commands use the php on PATH, since routa's PHP is not ready: %v\n", err)
		}
		return os.Environ()
	}
	return envWithRoutaPHP(ctx)
}

// setupHint says how to finish a workspace after a setup command fails.
type setupHint struct {
	name string
	path string // set by 'add', which also says the workspace is kept
}

func (h setupHint) failure(results []workspace.CommandResult, failed int) error {
	r := results[failed]
	var b strings.Builder
	code := "it could not run"
	if r.ExitCode != nil && *r.ExitCode >= 0 {
		code = fmt.Sprintf("exit code %d", *r.ExitCode)
	} else if r.ExitCode != nil {
		code = "it was killed by a signal"
	}
	fmt.Fprintf(&b, "setup command %d of %d failed (%s): %s\n", failed+1, len(results), code, r.Command)
	if r.Tail != "" {
		b.WriteString("  end of its output:\n")
		for _, line := range strings.Split(r.Tail, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}
	if h.path != "" {
		fmt.Fprintf(&b, "the workspace is kept at %s.\n", h.path)
	}
	b.WriteString("Run this command and the ones after it with:\n  " + h.rerun(results[failed:]))
	if h.path != "" {
		b.WriteString("\nor remove the workspace with:\n  routa workspace rm " + workspace.ShellQuote(h.name) + h.siteFlag() + " --force")
	}
	return errors.New(b.String())
}

// rerun is the 'routa workspace setup' command that runs the remaining
// commands. --no-run keeps the site's commands that already ran from running
// again; each remaining one is passed with --run instead.
func (h setupHint) rerun(remaining []workspace.CommandResult) string {
	parts := []string{"routa workspace setup", workspace.ShellQuote(h.name)}
	if s := h.siteFlag(); s != "" {
		parts = append(parts, strings.TrimSpace(s))
	}
	parts = append(parts, "--no-run")
	for _, r := range remaining {
		parts = append(parts, "--run", workspace.ShellQuote(r.Command))
	}
	if workspaceStart {
		parts = append(parts, "--start")
	}
	return strings.Join(parts, " ")
}

// setupSummary is the setup line of 'workspace add': how many commands ran
// and how long they took, or where they stopped.
func setupSummary(results []workspace.CommandResult) string {
	if len(results) == 0 {
		return ""
	}
	var total float64
	for i, r := range results {
		total += r.Seconds
		if r.Status == "failed" {
			return fmt.Sprintf("stopped at command %d of %d: %s", i+1, len(results), r.Command)
		}
	}
	noun := "commands"
	if len(results) == 1 {
		noun = "command"
	}
	return fmt.Sprintf("%d %s ran in %.1fs", len(results), noun, total)
}

// actionsSummary lists the actions and how each is doing.
func actionsSummary(actions []actionResult) string {
	parts := make([]string, 0, len(actions))
	for _, a := range actions {
		state := a.State
		if state == "started" {
			state = "running"
		}
		parts = append(parts, a.Name+" ("+state+")")
	}
	return strings.Join(parts, ", ")
}

func (h setupHint) siteFlag() string {
	if workspaceSite == "" {
		return ""
	}
	return " --site " + workspace.ShellQuote(workspaceSite)
}

// serviceSpec names a routa service for --service: redis, mailpit, or a
// search or storage engine with an optional version.
type serviceSpec struct {
	name    string
	version string
}

var versionedServices = map[string]func(string) error{
	"meilisearch": services.ValidateMeilisearchVersion,
	"typesense":   services.ValidateTypesenseVersion,
	"minio":       services.ValidateMinIOVersion,
}

func parseServiceSpec(s string) (serviceSpec, error) {
	name, version, hasVersion := strings.Cut(strings.TrimSpace(s), ":")
	name = strings.ToLower(name)
	if name == "mail" {
		name = "mailpit"
	}
	switch name {
	case "redis", "mailpit":
		if hasVersion {
			return serviceSpec{}, fmt.Errorf("routa runs one %s, so --service %s takes no version", name, name)
		}
		return serviceSpec{name: name}, nil
	case "mysql", "mariadb", "postgres", "pgsql", "db":
		return serviceSpec{}, fmt.Errorf("--service %s: databases are not started this way; the workspace copies the parent's database, so its server is already running", s)
	}
	validate, ok := versionedServices[name]
	if !ok {
		return serviceSpec{}, fmt.Errorf("unknown service %q: use redis, mailpit, meilisearch, typesense, or minio, with a version for the last three, such as meilisearch:1.12", s)
	}
	if hasVersion {
		if err := validate(version); err != nil {
			return serviceSpec{}, err
		}
	}
	return serviceSpec{name: name, version: version}, nil
}

func (s serviceSpec) label() string {
	if s.version == "" {
		return s.name
	}
	return s.name + " " + s.version
}

func (s serviceSpec) unit() string {
	switch s.name {
	case "redis":
		return services.RedisUnitName
	case "mailpit":
		return services.MailpitUnitName
	case "meilisearch":
		return services.MeilisearchUnitName(s.version)
	case "typesense":
		return services.TypesenseUnitName(s.version)
	case "minio":
		return services.MinIOUnitName(s.version)
	}
	return ""
}

// startArgs is the routa command that starts the service on the ports its
// unit already has, so starting it never moves it.
func (s serviceSpec) startArgs() ([]string, error) {
	switch s.name {
	case "redis":
		return []string{"redis", "start"}, nil
	case "mailpit":
		web, smtp, err := mailpitConfiguredPorts()
		if err != nil {
			return nil, err
		}
		return []string{"mail", "start", "on", web, "--smtp-port", smtp}, nil
	case "meilisearch", "typesense":
		port, err := searchConfiguredPort(s.name, s.version)
		if err != nil {
			return nil, err
		}
		return []string{"search", "start", s.name, s.version, "on", port}, nil
	case "minio":
		api, console, err := minIOConfiguredPorts(s.version)
		if err != nil {
			return nil, err
		}
		return []string{"storage", "start", "minio", s.version, "on", api, "--console-port", console}, nil
	}
	return nil, fmt.Errorf("unknown service %s", s.name)
}

// withVersion fills in a missing version: the one that is running, or else
// the only one installed.
func (s serviceSpec) withVersion() (serviceSpec, error) {
	if _, versioned := versionedServices[s.name]; !versioned || s.version != "" {
		return s, nil
	}
	var versions []string
	switch s.name {
	case "meilisearch":
		installed, err := services.InstalledMeilisearchInstances()
		if err != nil {
			return s, err
		}
		for _, i := range installed {
			versions = append(versions, i.Version)
		}
	case "typesense":
		installed, err := services.InstalledTypesenseInstances()
		if err != nil {
			return s, err
		}
		for _, i := range installed {
			versions = append(versions, i.Version)
		}
	case "minio":
		installed, err := services.InstalledMinIOInstances()
		if err != nil {
			return s, err
		}
		for _, i := range installed {
			versions = append(versions, i.Version)
		}
	}
	for _, v := range versions {
		if systemd.IsActive(serviceSpec{name: s.name, version: v}.unit()) {
			s.version = v
			return s, nil
		}
	}
	switch len(versions) {
	case 0:
		return s, fmt.Errorf("%s is not installed; name a version to install and start, such as --service %s:<version>", s.name, s.name)
	case 1:
		s.version = versions[0]
		return s, nil
	}
	return s, fmt.Errorf("several %s versions are installed (%s); name one, such as --service %s:%s", s.name, strings.Join(versions, ", "), s.name, versions[len(versions)-1])
}

// serviceResult is one service in the --json summary.
type serviceResult struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Unit    string `json:"unit"`
	Status  string `json:"status"` // running (already) or started
}

// ensureServices starts each service that is not running yet, with routa's
// own start command. Services that already run are left alone.
func ensureServices(specs []serviceSpec) ([]serviceResult, error) {
	var out []serviceResult
	for _, spec := range specs {
		spec, err := spec.withVersion()
		if err != nil {
			return out, err
		}
		r := serviceResult{Name: spec.name, Version: spec.version, Unit: spec.unit(), Status: "running"}
		if systemd.IsActive(r.Unit) {
			fmt.Printf("→ %s is running\n", spec.label())
			out = append(out, r)
			continue
		}
		args, err := spec.startArgs()
		if err != nil {
			return out, err
		}
		fmt.Printf("→ starting %s\n", spec.label())
		if err := runRoutaIn("", args...); err != nil {
			return out, fmt.Errorf("start %s with 'routa %s': %w", spec.label(), strings.Join(args, " "), err)
		}
		r.Status = "started"
		out = append(out, r)
	}
	return out, nil
}
