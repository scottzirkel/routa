package workspace

import (
	"reflect"
	"testing"
)

func TestActionUnitNamesSplitBack(t *testing.T) {
	for _, c := range []struct{ site, action string }{
		{"shop-5194", "queue"},
		{"app.shop-login", "queue-high"},
		{"shop-5194-b", "vite"},
	} {
		unit := ActionUnit(c.site, c.action)
		site, action, ok := ActionOfUnit(unit)
		if !ok || site != c.site || action != c.action {
			t.Errorf("%s split into %q, %q, %v", unit, site, action, ok)
		}
	}
	for _, bad := range []string{"routa-redis.service", "routa-action-shop.service", "routa-action-shop_.service", "routa-action-shop_queue.timer"} {
		if _, _, ok := ActionOfUnit(bad); ok {
			t.Errorf("%s split", bad)
		}
	}
}

func TestActionUnitsPatternIsOneSiteOnly(t *testing.T) {
	// shop-5194-b's units must not match shop-5194's pattern. Site names have
	// no "_", so the separator keeps them apart.
	if got := ActionUnits("shop-5194"); got != "routa-action-shop-5194_*.service" {
		t.Fatalf("pattern = %s", got)
	}
	if ActionUnit("shop-5194-b", "queue") == "routa-action-shop-5194_b-queue.service" {
		t.Fatal("units of two sites collide")
	}
}

func TestActionRunArgsPassOnlyPathAndPHP(t *testing.T) {
	env := []string{"PATH=/routa/php:/usr/bin", "PHP_BINARY=/routa/php/php", "PHPRC=/routa/ini", "HOME=/home/me", "SECRET=x"}
	got := ActionRunArgs("routa-action-shop-1_queue.service", "routa action queue for shop-1.test", "/code/shop-worktrees/1", env, "php artisan queue:work")
	want := []string{
		"--user", "--quiet",
		"--unit=routa-action-shop-1_queue.service",
		"--description=routa action queue for shop-1.test",
		"--working-directory=/code/shop-worktrees/1",
		"--property=Restart=on-failure",
		"--property=RestartSec=1",
		"--setenv=PATH=/routa/php:/usr/bin",
		"--setenv=PHP_BINARY=/routa/php/php",
		"--setenv=PHPRC=/routa/ini",
		"--", "/bin/sh", "-c", "php artisan queue:work",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args =\n%q\nwant\n%q", got, want)
	}
}

func TestParseUnitStates(t *testing.T) {
	out := `Id=routa-action-shop-1_queue.service
LoadState=loaded
ActiveState=active
SubState=running
Result=success
ExecMainStatus=0
WorkingDirectory=/code/shop-worktrees/1

Id=routa-action-shop-1_vite.service
LoadState=loaded
ActiveState=failed
SubState=failed
Result=start-limit-hit
ExecMainStatus=127
NRestarts=5
WorkingDirectory=/code/shop-worktrees/1

Id=routa-action-shop-1_mail.service
LoadState=not-found
ActiveState=inactive
SubState=dead
Result=success
ExecMainStatus=0
WorkingDirectory=
`
	states := ParseUnitStates(out)
	if len(states) != 3 {
		t.Fatalf("states = %#v", states)
	}
	if s := states[0]; !s.Running() || s.Label() != "running" || s.WorkingDirectory != "/code/shop-worktrees/1" {
		t.Errorf("queue = %#v, %s", s, s.Label())
	}
	if s := states[1]; s.Running() || s.Label() != "failed (exit 127)" || s.NRestarts != 5 {
		t.Errorf("vite = %#v, %s", s, s.Label())
	}
	if s := states[2]; s.Running() || s.Label() != "stopped" {
		t.Errorf("mail = %#v, %s", s, s.Label())
	}
	restarting := UnitState{ActiveState: "activating", SubState: "auto-restart"}
	if !restarting.Running() || restarting.Label() != "restarting" {
		t.Errorf("auto-restart = %s", restarting.Label())
	}
}
