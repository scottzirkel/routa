package workspace

import (
	"fmt"
	"strconv"
	"strings"
)

// Actions are commands that keep running for a workspace, such as a queue
// worker. Each runs as a transient systemd user unit named
// routa-action-<site>_<action>.service. Site names never contain "_", so the
// unit name always splits back into the two.
const actionUnitPrefix = "routa-action-"

// AllActionUnits matches the unit of every action of every workspace.
const AllActionUnits = actionUnitPrefix + "*.service"

// ActionUnit is the unit that runs action for the workspace served as site.
func ActionUnit(site, action string) string {
	return actionUnitPrefix + site + "_" + action + ".service"
}

// ActionUnits matches the units of every action of the workspace served as site.
func ActionUnits(site string) string {
	return actionUnitPrefix + site + "_*.service"
}

// ActionOfUnit splits an action's unit name into its site and action.
func ActionOfUnit(unit string) (site, action string, ok bool) {
	rest, ok := strings.CutPrefix(unit, actionUnitPrefix)
	if !ok {
		return "", "", false
	}
	rest, ok = strings.CutSuffix(rest, ".service")
	if !ok {
		return "", "", false
	}
	i := strings.LastIndex(rest, "_")
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// actionEnvKeys are the variables an action takes from the shell that starts
// it. The systemd user manager has its own minimal environment, so PATH must
// come along for the action to find node, composer, and routa's PHP.
var actionEnvKeys = []string{"PATH", "PHP_BINARY", "PHPRC"}

// ActionRunArgs are the systemd-run arguments that start command in dir as
// unit. A command that crashes is restarted after a second; one that keeps
// crashing hits systemd's start limit and stays failed, so its logs explain
// why. Stopping the unit sends SIGTERM to every process it started.
func ActionRunArgs(unit, description, dir string, env []string, command string) []string {
	args := []string{
		"--user",
		"--quiet",
		"--unit=" + unit,
		"--description=" + description,
		"--working-directory=" + dir,
		"--property=Restart=on-failure",
		"--property=RestartSec=1",
	}
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		for _, want := range actionEnvKeys {
			if key == want {
				args = append(args, "--setenv="+kv)
			}
		}
	}
	return append(args, "--", "/bin/sh", "-c", command)
}

// UnitState is the part of `systemctl show` that says how an action is doing.
type UnitState struct {
	Unit             string
	LoadState        string
	ActiveState      string
	SubState         string
	Result           string
	ExecMainStatus   int
	NRestarts        int
	WorkingDirectory string
}

// UnitStateProperties are the properties ParseUnitStates reads, for
// `systemctl show --property=...`.
const UnitStateProperties = "Id,LoadState,ActiveState,SubState,Result,ExecMainStatus,NRestarts,WorkingDirectory"

// ParseUnitStates reads `systemctl show` output for several units, which
// separates each unit's properties with a blank line.
func ParseUnitStates(out string) []UnitState {
	var states []UnitState
	var cur *UnitState
	flush := func() {
		if cur != nil && cur.Unit != "" {
			states = append(states, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if cur == nil {
			cur = &UnitState{}
		}
		key, value, _ := strings.Cut(line, "=")
		switch key {
		case "Id":
			cur.Unit = value
		case "LoadState":
			cur.LoadState = value
		case "ActiveState":
			cur.ActiveState = value
		case "SubState":
			cur.SubState = value
		case "Result":
			cur.Result = value
		case "ExecMainStatus":
			cur.ExecMainStatus, _ = strconv.Atoi(value)
		case "NRestarts":
			cur.NRestarts, _ = strconv.Atoi(value)
		case "WorkingDirectory":
			cur.WorkingDirectory = value
		}
	}
	flush()
	return states
}

// Running reports whether the unit is up, or on its way up after a crash.
func (s UnitState) Running() bool {
	switch s.ActiveState {
	case "active", "activating", "reloading", "refreshing":
		return true
	}
	return false
}

// Label says how the action is doing in a word or two.
func (s UnitState) Label() string {
	switch {
	case s.ActiveState == "active":
		return "running"
	case s.ActiveState == "activating" && s.SubState == "auto-restart":
		return "restarting"
	case s.Running():
		return "starting"
	case s.ActiveState == "deactivating":
		return "stopping"
	case s.ActiveState == "failed" && s.Result == "signal":
		return "failed (killed by a signal)"
	case s.ActiveState == "failed" && s.ExecMainStatus != 0:
		// A command that kept crashing ends with Result=start-limit-hit;
		// its last exit code says more.
		return fmt.Sprintf("failed (exit %d)", s.ExecMainStatus)
	case s.ActiveState == "failed":
		return "failed (" + s.Result + ")"
	}
	return "stopped"
}
