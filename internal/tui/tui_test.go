package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/services"
	"github.com/scottzirkel/routa/internal/site"
)

func TestProblemReasons(t *testing.T) {
	s := site.Resolved{Name: "app", Kind: site.KindStatic, Docroot: "/missing"}
	m := model{
		docExists: map[string]bool{"app": false},
		results:   map[string]probeResult{"app": {code: 500, latency: time.Millisecond}},
	}

	got := m.problemReasons(s)
	want := []string{"missing docroot", "HTTP 500"}
	if len(got) != len(want) {
		t.Fatalf("problemReasons() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("problemReasons()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestActionEligibility(t *testing.T) {
	m := model{links: map[string]site.Link{"linked": {Name: "linked"}}}

	if !m.isExplicitLink("linked") {
		t.Fatal("linked site should be eligible for explicit-link actions")
	}
	if m.isExplicitLink("parked") {
		t.Fatal("parked site should not be eligible for explicit-link actions")
	}
	if m.canChangeRoot(site.Resolved{Name: "vite", Kind: site.KindProxy}) {
		t.Fatal("proxy site should not be eligible for root changes")
	}
}

func TestSortProblemsFirst(t *testing.T) {
	a := site.Resolved{Name: "ok", Kind: site.KindStatic}
	b := site.Resolved{Name: "bad", Kind: site.KindStatic}
	m := model{
		sort:      sortProblems,
		docExists: map[string]bool{"ok": true, "bad": false},
		results:   map[string]probeResult{},
	}

	if !m.siteLess(b, a) {
		t.Fatal("problem site should sort before non-problem site")
	}
}

func TestDisplayItemsCollapseGroup(t *testing.T) {
	m := model{
		sites: []site.Resolved{
			{Name: "app", Kind: site.KindStatic},
			{Name: "api.app", Kind: site.KindStatic},
		},
		docExists: map[string]bool{"app": true, "api.app": true},
		results:   map[string]probeResult{},
		collapsed: map[string]bool{"app": true},
	}

	items := m.displayItems()
	if len(items) != 1 {
		t.Fatalf("collapsed display item count = %d, want 1", len(items))
	}
	if !items[0].collapsed || items[0].childCount != 1 {
		t.Fatalf("collapsed parent metadata = %#v, want collapsed with one child", items[0])
	}
}

func TestConfigPortParsesServiceConfigs(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "equals", body: "port=3307\n", want: "3307"},
		{name: "spaced equals", body: "port = 5433\n", want: "5433"},
		{name: "quoted", body: "port = '5434'\n", want: "5434"},
		{name: "redis style", body: "bind 127.0.0.1\nport 6380\n", want: "6380"},
	} {
		path := filepath.Join(dir, tc.name+".conf")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := configPort(path, "9999"); got != tc.want {
			t.Fatalf("%s port = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUnitFlagPortParsesExecStartFlags(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.MkdirAll(paths.SystemdUserDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	unit := "routa-example.service"
	body := `[Service]
ExecStart=/usr/bin/minio server --address 127.0.0.1:9002 --console-address 127.0.0.1:9003 /data
`
	if err := os.WriteFile(filepath.Join(paths.SystemdUserDir(), unit), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := unitFlagPort(unit, "--address", "9000"); got != "9002" {
		t.Fatalf("api port = %q, want 9002", got)
	}
	if got := unitFlagPort(unit, "--console-address", "9001"); got != "9003" {
		t.Fatalf("console port = %q, want 9003", got)
	}
}

func TestCollectOptionalServicesIncludesRedis(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(services.RedisConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(services.RedisConfigPath(), []byte("port 6380\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := collectOptionalServices()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("service count = %d, want 1: %#v", len(got), got)
	}
	if got[0].Name != "redis" || got[0].Unit != services.RedisUnitName || got[0].Ports != "6380" {
		t.Fatalf("redis service = %#v", got[0])
	}
}

func TestRenderServicesShowsStatusPortAndDataDir(t *testing.T) {
	m := model{services: []optionalService{{
		Name:    "mysql 8.0/app",
		Unit:    "routa-mysql@8.0_app.service",
		Ports:   "3310",
		DataDir: "/tmp/routa/services/mysql/8.0/instances/app",
		Active:  true,
	}}}

	// Styles always render at full color; compare the visible text.
	got := ansi.Strip(strings.Join(m.renderServices(80), "\n"))
	for _, want := range []string{"OPTIONAL SERVICES", "up", "mysql 8.0/app 3310", "/tmp/routa/services/mysql/8.0/instances/app"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered services missing %q:\n%s", want, got)
		}
	}
}

func TestServiceSelectionWraps(t *testing.T) {
	m := model{services: []optionalService{
		{Name: "mysql", Unit: "routa-mysql.service"},
		{Name: "redis", Unit: services.RedisUnitName},
	}}

	m.moveServiceCursor(-1)
	if got := m.selectedService(); got == nil || got.Name != "redis" {
		t.Fatalf("selected service after reverse wrap = %#v, want redis", got)
	}
	m.moveServiceCursor(1)
	if got := m.selectedService(); got == nil || got.Name != "mysql" {
		t.Fatalf("selected service after forward wrap = %#v, want mysql", got)
	}
}

func TestServiceToggleActionAndConfirmText(t *testing.T) {
	down := optionalService{Name: "redis", Unit: services.RedisUnitName}
	up := optionalService{Name: "mysql 8.0/app", Unit: "routa-mysql@8.0_app.service", Active: true}

	if got := serviceToggleAction(down); got != actionServiceStart {
		t.Fatalf("down service toggle = %v, want start", got)
	}
	if got := serviceConfirmText(actionServiceStart, down); got != "start and enable redis" {
		t.Fatalf("start confirm = %q", got)
	}
	if got := serviceToggleAction(up); got != actionServiceStop {
		t.Fatalf("up service toggle = %v, want stop", got)
	}
	if got := serviceConfirmText(actionServiceStop, up); got != "stop and disable mysql 8.0/app" {
		t.Fatalf("stop confirm = %q", got)
	}
	if got := serviceActionLabel(actionServiceRestart); got != "restarted" {
		t.Fatalf("restart label = %q", got)
	}
}

func TestServiceActionKeysCreateConfirmation(t *testing.T) {
	m := model{services: []optionalService{{
		Name: "redis",
		Unit: services.RedisUnitName,
	}}}

	updated, _ := m.Update(keyPress("v"))
	got := updated.(model)
	if got.confirm == nil || got.confirm.kind != actionServiceStart || got.confirm.service.Name != "redis" {
		t.Fatalf("toggle confirm = %#v", got.confirm)
	}

	m.services[0].Active = true
	updated, _ = m.Update(keyPress("V"))
	got = updated.(model)
	if got.confirm == nil || got.confirm.kind != actionServiceRestart || got.confirm.service.Name != "redis" {
		t.Fatalf("restart confirm = %#v", got.confirm)
	}
}

func TestSpaceTogglesGroupCollapse(t *testing.T) {
	m := model{
		sites: []site.Resolved{
			{Name: "app", Kind: site.KindStatic},
			{Name: "api.app", Kind: site.KindStatic},
		},
		docExists: map[string]bool{"app": true, "api.app": true},
		results:   map[string]probeResult{},
		collapsed: map[string]bool{},
	}

	updated, _ := m.Update(keyPress(" "))

	if !updated.(model).collapsed["app"] {
		t.Fatal("space should collapse the selected group")
	}
}

func TestSearchAcceptsTypedAndPastedText(t *testing.T) {
	m := model{searching: true}
	for _, msg := range []tea.Msg{
		keyPress("a"),
		keyPress(" "),
		tea.PasteMsg{Content: "api\n"},
		tea.KeyPressMsg{Code: tea.KeyBackspace},
	} {
		updated, _ := m.Update(msg)
		m = updated.(model)
	}

	if m.filt.search != "a ap" {
		t.Fatalf("search = %q, want %q", m.filt.search, "a ap")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if updated.(model).searching {
		t.Fatal("enter should leave search mode")
	}
}

func TestRootEditAcceptsPastedPath(t *testing.T) {
	m := model{rootEdit: &rootEditState{}}

	updated, _ := m.Update(tea.PasteMsg{Content: "/srv/app\n"})
	updated, _ = updated.Update(keyPress("2"))

	if got := updated.(model).rootEdit.value; got != "/srv/app2" {
		t.Fatalf("root edit = %q, want %q", got, "/srv/app2")
	}
}

// keyPress builds the press a terminal reports for one printable key, with
// " " standing for the space bar.
func keyPress(s string) tea.KeyPressMsg {
	if s == " " {
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}
