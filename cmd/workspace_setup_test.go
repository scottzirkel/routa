package cmd

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scottzirkel/routa/internal/site"
	"github.com/scottzirkel/routa/internal/workspace"
)

// setWorkspaceFlags sets the setup flags for one test and puts them back.
func setWorkspaceFlags(t *testing.T, run, services []string, noRun, start bool, siteName string) {
	t.Helper()
	oldRun, oldServices, oldNoRun, oldStart, oldSite := workspaceRun, workspaceServices, workspaceNoRun, workspaceStart, workspaceSite
	workspaceRun, workspaceServices, workspaceNoRun, workspaceStart, workspaceSite = run, services, noRun, start, siteName
	t.Cleanup(func() {
		workspaceRun, workspaceServices, workspaceNoRun, workspaceStart, workspaceSite = oldRun, oldServices, oldNoRun, oldStart, oldSite
	})
}

func TestParseServiceSpec(t *testing.T) {
	ok := map[string]serviceSpec{
		"redis":                              {name: "redis"},
		"Redis":                              {name: "redis"},
		"mail":                               {name: "mailpit"},
		"mailpit":                            {name: "mailpit"},
		"meilisearch:1.12":                   {name: "meilisearch", version: "1.12"},
		"typesense":                          {name: "typesense"},
		"minio:RELEASE.2026-05-01T00-00-00Z": {name: "minio", version: "RELEASE.2026-05-01T00-00-00Z"},
	}
	for in, want := range ok {
		got, err := parseServiceSpec(in)
		if err != nil || got != want {
			t.Errorf("%s: got %#v, %v", in, got, err)
		}
	}
	bad := map[string]string{
		"redis:7":       "takes no version",
		"mysql:8.4":     "databases",
		"postgres":      "databases",
		"elasticsearch": "unknown service",
		"meilisearch:":  "version",
	}
	for in, want := range bad {
		if _, err := parseServiceSpec(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to mention %q", in, err, want)
		}
	}
}

func TestServiceStartArgsKeepTheConfiguredPorts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	args, err := serviceSpec{name: "meilisearch", version: "1.12"}.startArgs()
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 6 || args[0] != "search" || args[1] != "start" || args[4] != "on" || args[5] == "" {
		t.Fatalf("args = %q, want search start meilisearch 1.12 on <port>", args)
	}
	args, err = serviceSpec{name: "mailpit"}.startArgs()
	if err != nil || len(args) != 6 || args[0] != "mail" || args[4] != "--smtp-port" {
		t.Fatalf("args = %q, %v", args, err)
	}
	if args, _ := (serviceSpec{name: "redis"}).startArgs(); !reflect.DeepEqual(args, []string{"redis", "start"}) {
		t.Fatalf("redis args = %q", args)
	}
}

func TestPlanWorkspaceSetupCombinesTheSiteAndFlags(t *testing.T) {
	main := t.TempDir()
	writeTestFile(t, filepath.Join(main, workspace.ProjectFile), `{"workspace": {"services": ["redis"], "run": ["npm run build"]}}`)

	setWorkspaceFlags(t, []string{"composer test:setup"}, []string{"mailpit", "redis"}, false, false, "")
	plan, err := planWorkspaceSetup(main)
	if err != nil {
		t.Fatal(err)
	}
	if want := []serviceSpec{{name: "redis"}, {name: "mailpit"}}; !reflect.DeepEqual(plan.services, want) {
		t.Fatalf("services = %#v, want each once, the site's first", plan.services)
	}
	want := []workspace.SetupCommand{{Command: "npm run build", From: "site"}, {Command: "composer test:setup", From: "flag"}}
	if !reflect.DeepEqual(plan.commands, want) {
		t.Fatalf("commands = %#v", plan.commands)
	}

	setWorkspaceFlags(t, []string{"composer test:setup"}, nil, true, false, "")
	plan, err = planWorkspaceSetup(main)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.commands) != 1 || plan.commands[0].From != "flag" {
		t.Fatalf("--no-run: commands = %#v, want only --run", plan.commands)
	}
	if len(plan.services) != 1 {
		t.Fatalf("--no-run: services = %#v, want the site's services kept", plan.services)
	}
}

func TestPlanWorkspaceSetupRefusesMistakesUpFront(t *testing.T) {
	main := t.TempDir()
	setWorkspaceFlags(t, nil, []string{"mysql:8.4"}, false, false, "")
	if _, err := planWorkspaceSetup(main); err == nil {
		t.Fatal("accepted a database as a service")
	}
	setWorkspaceFlags(t, []string{" "}, nil, false, false, "")
	if _, err := planWorkspaceSetup(main); err == nil {
		t.Fatal("accepted an empty --run")
	}
	setWorkspaceFlags(t, nil, nil, false, false, "")
	writeTestFile(t, filepath.Join(main, workspace.ProjectFile), `{"workspace": {"commands": []}}`)
	if _, err := planWorkspaceSetup(main); err == nil {
		t.Fatal("accepted an unknown key")
	}
}

func TestSetupFailureSaysHowToFinish(t *testing.T) {
	setWorkspaceFlags(t, nil, nil, false, true, "shop")
	code := 1
	results := []workspace.CommandResult{
		{Command: "npm run build", Status: "ok"},
		{Command: "composer test:setup", Status: "failed", ExitCode: &code, Tail: "Could not open input file\nmore"},
		{Command: "echo 'done'", Status: "skipped"},
	}
	msg := setupHint{name: "5292", path: "/code/shop-worktrees/5292"}.failure(results, 1).Error()
	for _, want := range []string{
		"setup command 2 of 3 failed (exit code 1): composer test:setup",
		"    Could not open input file\n    more\n",
		"the workspace is kept at /code/shop-worktrees/5292.",
		`routa workspace setup 5292 --site shop --no-run --run 'composer test:setup' --run 'echo '\''done'\''' --start`,
		"routa workspace rm 5292 --site shop --force",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}

	msg = setupHint{name: "5292"}.failure(results, 1).Error()
	if strings.Contains(msg, "kept") || strings.Contains(msg, "workspace rm") {
		t.Errorf("'setup' on an existing workspace offers to remove it:\n%s", msg)
	}
}

func TestSetupSummary(t *testing.T) {
	code := 0
	ok := []workspace.CommandResult{{Command: "a", Status: "ok", ExitCode: &code, Seconds: 1.25}, {Command: "b", Status: "ok", ExitCode: &code, Seconds: 2}}
	if got := setupSummary(ok); got != "2 commands ran in 3.2s" {
		t.Errorf("got %q", got)
	}
	failed := []workspace.CommandResult{{Command: "a", Status: "ok"}, {Command: "b", Status: "failed"}, {Command: "c", Status: "skipped"}}
	if got := setupSummary(failed); got != "stopped at command 2 of 3: b" {
		t.Errorf("got %q", got)
	}
	if setupSummary(nil) != "" {
		t.Error("summary without commands")
	}
}

func TestWorkspaceSetupFlagsAreOnAddAndSetup(t *testing.T) {
	for _, c := range []string{"add", "setup"} {
		cmd, _, err := workspaceCmd.Find([]string{c})
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{"run", "service", "no-run", "start", "json"} {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("workspace %s has no --%s", c, flag)
			}
		}
	}
	// --run takes whole commands: a comma must not split one in two.
	if err := workspaceAddCmd.Flags().Set("run", "echo a,b"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { workspaceRun = nil })
	if !reflect.DeepEqual(workspaceRun, []string{"echo a,b"}) {
		t.Fatalf("--run = %q", workspaceRun)
	}
}

func TestJSONProgressMovesStdoutToStderr(t *testing.T) {
	old := workspaceJSON
	t.Cleanup(func() { workspaceJSON = old })
	realOut := os.Stdout
	workspaceJSON = true
	out, restore := jsonProgress()
	if out != realOut || os.Stdout != os.Stderr {
		t.Fatal("progress still goes to stdout")
	}
	restore()
	if os.Stdout != realOut {
		t.Fatal("stdout not restored")
	}
}

// captureStdout runs fn and returns what it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = old
	w.Close()
	return <-done
}

func TestWorkspaceSetupRunsCommandsInTheWorktreeAndReportsJSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	base := t.TempDir()
	main, wt := filepath.Join(base, "shop"), filepath.Join(base, "shop-worktrees", "one")
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", main}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeTestFile(t, filepath.Join(main, "README"), "hi\n")
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "init")
	git("worktree", "add", "-q", "-b", "one", wt)
	writeTestFile(t, filepath.Join(main, workspace.ProjectFile), `{"workspace": {"run": ["pwd > where", "echo from the site"]}}`)
	if err := site.Save(&site.State{Links: []site.Link{{Name: "shop", Path: main, Secure: true}}}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(main)
	oldJSON := workspaceJSON
	workspaceJSON = true
	t.Cleanup(func() { workspaceJSON = oldJSON })

	setWorkspaceFlags(t, []string{"echo from a flag"}, nil, false, false, "")
	var runErr error
	out := captureStdout(t, func() { runErr = runWorkspaceSetup(nil, []string{"one"}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	var res workspaceResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout is not only JSON: %v\n%s", err, out)
	}
	if !res.OK || res.Name != "one" || res.URL != "https://shop-one.test" || len(res.Commands) != 3 {
		t.Fatalf("result = %+v", res)
	}
	for i, from := range []string{"site", "site", "flag"} {
		c := res.Commands[i]
		if c.Status != "ok" || c.ExitCode == nil || *c.ExitCode != 0 || c.From != from {
			t.Errorf("command %d = %+v", i, c)
		}
	}
	where, _ := os.ReadFile(filepath.Join(wt, "where"))
	if got, want := strings.TrimSpace(string(where)), wt; got != want {
		if real, _ := filepath.EvalSymlinks(want); got != real {
			t.Fatalf("ran in %q, want the worktree %q", got, want)
		}
	}

	setWorkspaceFlags(t, []string{"exit 4", "echo never"}, nil, true, false, "")
	out = captureStdout(t, func() { runErr = runWorkspaceSetup(nil, []string{"one"}) })
	if runErr == nil {
		t.Fatal("a failing command did not fail setup")
	}
	for _, want := range []string{"setup command 1 of 2 failed (exit code 4): exit 4", "routa workspace setup one --no-run --run 'exit 4' --run 'echo never'"} {
		if !strings.Contains(runErr.Error(), want) {
			t.Errorf("error lacks %q:\n%s", want, runErr)
		}
	}
	res = workspaceResult{}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout is not only JSON after a failure: %v\n%s", err, out)
	}
	if res.OK || len(res.Commands) != 2 || res.Commands[0].Status != "failed" || *res.Commands[0].ExitCode != 4 || res.Commands[1].Status != "skipped" {
		t.Fatalf("result after a failure = %+v", res)
	}
}
