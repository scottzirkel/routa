package diag

import (
	"os"
	"os/exec"
	"strings"
)

type Status string

const (
	OK     Status = "ok"
	Warn   Status = "warn"
	Fail   Status = "fail"
	Absent Status = "absent"
)

type Check struct {
	Name   string
	Status Status
	Detail string
	Hint   string
}

func Run() []Check {
	return []Check{
		checkResolvConf(),
		checkValetLinux(),
		checkSystemdResolved(),
		checkSystemdUser(),
		checkBinary("caddy", "Required as the reverse proxy. Install: sudo pacman -S caddy"),
		checkBinary("dnsmasq", "Optional fallback. Already present is fine."),
		checkBinary("trust", "Used to install hostr's local CA into the system trust store. From package: p11-kit"),
	}
}

func checkResolvConf() Check {
	c := Check{Name: "/etc/resolv.conf"}
	target, err := os.Readlink("/etc/resolv.conf")
	if err != nil {
		st, statErr := os.Stat("/etc/resolv.conf")
		if statErr != nil {
			c.Status = Fail
			c.Detail = statErr.Error()
			return c
		}
		c.Status = Warn
		c.Detail = "regular file, mode " + st.Mode().String() + " (not a symlink)"
		c.Hint = "Cutover will replace this with a symlink to /run/systemd/resolve/stub-resolv.conf"
		return c
	}
	c.Detail = "symlink → " + target
	switch {
	case strings.Contains(target, "valet-linux"):
		c.Status = Warn
		c.Hint = "valet-linux owns your resolver. `hostr install` runs alongside on alt ports. `hostr cutover` will atomically swap once you've verified."
	case strings.Contains(target, "systemd"):
		c.Status = OK
	default:
		c.Status = Warn
		c.Hint = "Unfamiliar resolver target. Cutover expects to symlink to /run/systemd/resolve/stub-resolv.conf"
	}
	return c
}

func checkValetLinux() Check {
	c := Check{Name: "valet-linux presence"}
	if _, err := os.Stat("/opt/valet-linux"); err == nil {
		c.Status = Warn
		c.Detail = "/opt/valet-linux exists (running alongside)"
		c.Hint = "OK during install — hostr coexists on alt ports. Cutover will stop valet-dns and reclaim 80/443/53."
		return c
	}
	c.Status = OK
	c.Detail = "not installed"
	return c
}

func checkSystemdResolved() Check {
	c := Check{Name: "systemd-resolved"}
	out, _ := exec.Command("systemctl", "is-active", "systemd-resolved.service").CombinedOutput()
	state := strings.TrimSpace(string(out))
	c.Detail = state
	switch state {
	case "active":
		c.Status = OK
	case "inactive", "dead":
		c.Status = Warn
		c.Hint = "Cutover will enable systemd-resolved and add a route sending *.test to hostr's DNS. `install` doesn't touch this."
	default:
		c.Status = Warn
		c.Hint = "Unexpected state — check `systemctl status systemd-resolved`."
	}
	return c
}

func checkSystemdUser() Check {
	c := Check{Name: "systemd --user"}
	out, err := exec.Command("systemctl", "--user", "is-system-running").CombinedOutput()
	if err != nil && len(out) == 0 {
		c.Status = Fail
		c.Detail = err.Error()
		return c
	}
	c.Status = OK
	c.Detail = strings.TrimSpace(string(out))
	return c
}

func checkBinary(name, hint string) Check {
	c := Check{Name: name}
	path, err := exec.LookPath(name)
	if err != nil {
		c.Status = Absent
		c.Hint = hint
		return c
	}
	c.Status = OK
	c.Detail = path
	return c
}
