// Package tui is routa's Bubble Tea dashboard.
// Rendered with lipgloss directly because bubbles/table v1 mishandles ANSI
// escapes inside cell text.
package tui

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/php"
	"github.com/scottzirkel/routa/internal/site"
)

// Column widths flex with the terminal. NAME, HTTPS, STAT always show.
// Remaining columns turn on in priority order as width allows: PHP, DOCROOT,
// LAT, KIND. Extra width goes 60/40 to DOCROOT/NAME (paths tend to be long).
const (
	cursorCol = 2 // "❯ " or "  " prefix on every body row
	minName   = 20
)

type layout struct {
	nameW, httpsW, kindW, phpW, statW, latW, docW int
	showKind, showPHP, showLat, showDoc           bool
}

func computeLayout(termWidth int) layout {
	l := layout{
		nameW:  minName,
		httpsW: 7,
		statW:  6,
	}
	usable := termWidth - cursorCol
	used := l.nameW + l.httpsW + l.statW

	// Add optional columns in priority order, each gated on having room
	// for ITSELF *plus* the minimum docroot we still hope to keep.
	if usable-used >= 6 {
		l.showPHP = true
		l.phpW = 6
		used += l.phpW
	}
	if usable-used >= 12 {
		l.showDoc = true
		l.docW = 12
		used += l.docW
	}
	if usable-used >= 8 {
		l.showLat = true
		l.latW = 8
		used += l.latW
	}
	if usable-used >= 8 {
		l.showKind = true
		l.kindW = 8
		used += l.kindW
	}

	if extra := usable - used; extra > 0 {
		if l.showDoc {
			docAdd := extra * 6 / 10
			l.nameW += extra - docAdd
			l.docW += docAdd
		} else {
			l.nameW += extra
		}
	}
	return l
}

var (
	bannerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141")) // bright purple, no bg
	taglineStyle  = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("245"))
	ruleStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245")).Underline(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFB454"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	selectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("237"))
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Bold(true)
	footerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	chipStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Bold(true)
	searchStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4"))
)

type probeResult struct {
	code    int
	latency time.Duration
}

type probeMsg struct {
	name string
	res  probeResult
}

const autoRefreshEvery = 5 * time.Second

type tickMsg time.Time

type healthState struct {
	dnsOK   bool
	caddyOK bool
	httpsOK bool
	altOK   bool
}

type healthMsg healthState

type logPreviewMsg struct {
	name  string
	lines []string
	err   error
}

type actionKind int

const (
	actionNone actionKind = iota
	actionUnlink
	actionSecure
)

type confirmState struct {
	kind actionKind
	site site.Resolved
	text string
}

type rootEditState struct {
	site  site.Resolved
	value string
	err   string
}

type sortMode int

const (
	sortName sortMode = iota
	sortProblems
	sortLatency
	sortKind
)

func (s sortMode) next() sortMode { return (s + 1) % 4 }
func (s sortMode) label() string {
	return [...]string{"name", "problems", "latency", "kind"}[s]
}

type secureFilter int

const (
	secAll secureFilter = iota
	secYes
	secNo
)

func (s secureFilter) next() secureFilter { return (s + 1) % 3 }
func (s secureFilter) label() string {
	return [...]string{"", "secure", "insecure"}[s]
}

type kindFilter int

const (
	kindAll kindFilter = iota
	kindPHP
	kindStatic
	kindProxy
)

func (k kindFilter) next() kindFilter { return (k + 1) % 4 }
func (k kindFilter) label() string {
	return [...]string{"", "php", "static", "proxy"}[k]
}

type codeFilter int

const (
	codeAll codeFilter = iota
	code2xx
	code3xx
	code4xx
	code5xx
	codeErr
	codePending
)

func (c codeFilter) next() codeFilter { return (c + 1) % 7 }
func (c codeFilter) label() string {
	return [...]string{"", "2xx", "3xx", "4xx", "5xx", "err", "pending"}[c]
}

type filters struct {
	secure      secureFilter
	kind        kindFilter
	code        codeFilter
	missingOnly bool
	problemOnly bool
	search      string
}

func (f filters) any() bool {
	return f.secure != secAll || f.kind != kindAll || f.code != codeAll || f.missingOnly || f.problemOnly || f.search != ""
}

type model struct {
	sites     []site.Resolved
	links     map[string]site.Link
	results   map[string]probeResult // missing key = pending
	docExists map[string]bool
	logs      map[string][]string
	health    healthState
	collapsed map[string]bool
	cursor    int
	offset    int
	width     int
	height    int
	filt      filters
	sort      sortMode
	searching bool // true while user is typing into the search box
	help      bool
	auto      bool
	status    string
	confirm   *confirmState
	rootEdit  *rootEditState
}

// --- Init / Update -------------------------------------------------------

func (m model) Init() tea.Cmd {
	return tea.Batch(m.probeAll(), healthCmd(), m.logPreviewSelected())
}

func (m model) probeAll() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.sites))
	for _, s := range m.sites {
		cmds = append(cmds, probeCmd(s.Name))
	}
	return tea.Batch(cmds...)
}

func probeCmd(name string) tea.Cmd {
	return func() tea.Msg {
		c := &http.Client{
			Timeout: 2 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		start := time.Now()
		resp, err := c.Head(siteURL(name))
		elapsed := time.Since(start)
		if err != nil {
			return probeMsg{name: name, res: probeResult{code: -1, latency: elapsed}}
		}
		resp.Body.Close()
		return probeMsg{name: name, res: probeResult{code: resp.StatusCode, latency: elapsed}}
	}
}

func healthCmd() tea.Cmd {
	return func() tea.Msg {
		return healthMsg(collectHealth())
	}
}

func collectHealth() healthState {
	return healthState{
		dnsOK:   portBound("127.0.0.1:1053"),
		caddyOK: systemdUserActive("routa-caddy.service"),
		httpsOK: portBound("127.0.0.1:443") || portBound(":443"),
		altOK:   portBound("127.0.0.1:8443") || portBound(":8443"),
	}
}

func systemdUserActive(unit string) bool {
	out, _ := exec.Command("systemctl", "--user", "is-active", unit).Output()
	return strings.TrimSpace(string(out)) == "active"
}

func openSite(name string) error {
	bin, err := os.Executable()
	if err != nil {
		bin = "routa"
	}
	return exec.Command(bin, "open", name).Start()
}

func refreshTickCmd() tea.Cmd {
	return tea.Tick(autoRefreshEvery, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) logPreviewSelected() tea.Cmd {
	sel := m.selected()
	if sel == nil {
		return nil
	}
	return logPreviewCmd(*sel, 5)
}

func logPreviewCmd(s site.Resolved, n int) tea.Cmd {
	return func() tea.Msg {
		lines, err := readLogPreview(s, n)
		return logPreviewMsg{name: s.Name, lines: lines, err: err}
	}
}

func readLogPreview(s site.Resolved, n int) ([]string, error) {
	files := []string{filepath.Join(paths.LogDir(), s.Name+".log")}
	if s.Kind == site.KindPHP && s.PHP != "" {
		files = append(files, filepath.Join(paths.LogDir(), "php-fpm-"+s.PHP+".log"))
	}
	out := []string{}
	for _, file := range files {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			continue
		}
		b, err := exec.Command("tail", "-n", strconv.Itoa(n), file).Output()
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if line != "" {
				out = append(out, filepath.Base(file)+": "+line)
			}
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.fixOffset()
		return m, nil
	case probeMsg:
		m.results[msg.name] = msg.res
		return m, nil
	case healthMsg:
		m.health = healthState(msg)
		return m, nil
	case logPreviewMsg:
		if msg.err == nil && msg.name != "" {
			m.logs[msg.name] = msg.lines
		}
		return m, nil
	case tickMsg:
		if !m.auto {
			return m, nil
		}
		if err := m.reloadSites(); err != nil {
			m.status = err.Error()
		}
		m.results = map[string]probeResult{}
		m.docExists = checkDocs(m.sites)
		return m, tea.Batch(m.probeAll(), healthCmd(), m.logPreviewSelected(), refreshTickCmd())
	case tea.KeyMsg:
		if m.help {
			switch msg.String() {
			case "?", "esc":
				m.help = false
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}
		if m.confirm != nil {
			switch msg.String() {
			case "y", "enter":
				return m.runConfirmedAction()
			case "n", "esc":
				m.confirm = nil
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}
		if m.rootEdit != nil {
			switch msg.Type {
			case tea.KeyEsc:
				m.rootEdit = nil
			case tea.KeyEnter:
				return m.commitRootEdit()
			case tea.KeyBackspace:
				if len(m.rootEdit.value) > 0 {
					m.rootEdit.value = m.rootEdit.value[:len(m.rootEdit.value)-1]
				}
			case tea.KeyRunes, tea.KeySpace:
				m.rootEdit.value += string(msg.Runes)
			}
			return m, nil
		}
		// search-input mode: capture keystrokes into the filter string
		if m.searching {
			switch msg.Type {
			case tea.KeyEsc:
				m.searching = false
				m.filt.search = ""
				m.resetCursor()
			case tea.KeyEnter:
				m.searching = false
				m.resetCursor()
			case tea.KeyBackspace:
				if len(m.filt.search) > 0 {
					m.filt.search = m.filt.search[:len(m.filt.search)-1]
					m.resetCursor()
				}
			case tea.KeyRunes, tea.KeySpace:
				m.filt.search += string(msg.Runes)
				m.resetCursor()
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			m.help = true
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			m.fixOffset()
			return m, m.logPreviewSelected()
		case "down", "j":
			if m.cursor < m.filteredLen()-1 {
				m.cursor++
			}
			m.fixOffset()
			return m, m.logPreviewSelected()
		case "g", "home":
			m.cursor, m.offset = 0, 0
			return m, m.logPreviewSelected()
		case "G", "end":
			m.cursor = m.filteredLen() - 1
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.fixOffset()
			return m, m.logPreviewSelected()
		case "pgup":
			m.cursor -= m.visibleRows()
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.fixOffset()
			return m, m.logPreviewSelected()
		case "pgdown":
			m.cursor += m.visibleRows()
			if m.cursor >= m.filteredLen() {
				m.cursor = m.filteredLen() - 1
			}
			m.fixOffset()
			return m, m.logPreviewSelected()
		case "o", "enter":
			if sel := m.selected(); sel != nil {
				_ = openSite(sel.Name)
			}
		case "l":
			if sel := m.selected(); sel != nil {
				bin, err := os.Executable()
				if err != nil {
					bin = "routa"
				}
				return m, tea.ExecProcess(
					exec.Command(bin, "logs", sel.Name),
					func(error) tea.Msg { return nil },
				)
			}
		case "r":
			if err := m.reloadSites(); err != nil {
				m.status = err.Error()
				return m, nil
			}
			m.results = map[string]probeResult{}
			m.docExists = checkDocs(m.sites)
			m.status = "refreshed"
			return m, tea.Batch(m.probeAll(), healthCmd(), m.logPreviewSelected())
		case "a":
			m.auto = !m.auto
			if m.auto {
				m.status = "auto refresh on"
				return m, refreshTickCmd()
			}
			m.status = "auto refresh off"
		case "!":
			m.filt.problemOnly = !m.filt.problemOnly
			m.resetCursor()
			return m, m.logPreviewSelected()
		case "z":
			m.sort = m.sort.next()
			m.status = "sort: " + m.sort.label()
			m.resetCursor()
			return m, m.logPreviewSelected()
		case " ":
			if root := m.selectedRoot(); root != "" {
				if m.collapsed == nil {
					m.collapsed = map[string]bool{}
				}
				m.collapsed[root] = !m.collapsed[root]
				m.fixOffset()
				return m, m.logPreviewSelected()
			}
		case "u":
			if sel := m.selected(); sel != nil {
				if !m.isExplicitLink(sel.Name) {
					m.status = sel.Name + ".test is tracked, not an explicit link"
				} else {
					m.confirm = &confirmState{kind: actionUnlink, site: *sel, text: "unlink " + sel.Name + ".test"}
				}
			}
		case "S":
			if sel := m.selected(); sel != nil {
				if !m.isExplicitLink(sel.Name) {
					m.status = sel.Name + ".test is tracked, not an explicit link"
				} else {
					m.confirm = &confirmState{kind: actionSecure, site: *sel, text: "toggle HTTPS for " + sel.Name + ".test"}
				}
			}
		case "R":
			if sel := m.selected(); sel != nil {
				if sel.Kind == site.KindProxy {
					m.status = "proxy sites do not have a docroot"
				} else {
					m.rootEdit = &rootEditState{site: *sel}
				}
			}
		// filters
		case "s":
			m.filt.secure = m.filt.secure.next()
			m.resetCursor()
			return m, m.logPreviewSelected()
		case "t":
			m.filt.kind = m.filt.kind.next()
			m.resetCursor()
			return m, m.logPreviewSelected()
		case "c":
			m.filt.code = m.filt.code.next()
			m.resetCursor()
			return m, m.logPreviewSelected()
		case "m":
			m.filt.missingOnly = !m.filt.missingOnly
			m.resetCursor()
			return m, m.logPreviewSelected()
		case "x":
			m.filt = filters{}
			m.sort = sortName
			m.resetCursor()
			return m, m.logPreviewSelected()
		case "/":
			m.searching = true
		}
	}
	return m, nil
}

func (m *model) resetCursor() { m.cursor, m.offset = 0, 0 }
func (m *model) fixOffset() {
	v := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+v {
		m.offset = m.cursor - v + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m model) runConfirmedAction() (tea.Model, tea.Cmd) {
	if m.confirm == nil {
		return m, nil
	}
	action := *m.confirm
	m.confirm = nil
	var err error
	switch action.kind {
	case actionUnlink:
		err = mutateLink(action.site.Name, func(s *site.State, _ *site.Link) error {
			if !site.RemoveLink(s, action.site.Name) {
				return fmt.Errorf("%s is not an explicit link", action.site.Name)
			}
			return nil
		})
	case actionSecure:
		err = mutateLink(action.site.Name, func(_ *site.State, l *site.Link) error {
			l.Secure = !l.Secure
			return nil
		})
	}
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	if err := m.reloadSites(); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.results = map[string]probeResult{}
	m.status = "updated " + action.site.Name + ".test"
	return m, tea.Batch(m.probeAll(), healthCmd(), m.logPreviewSelected())
}

func (m model) commitRootEdit() (tea.Model, tea.Cmd) {
	if m.rootEdit == nil {
		return m, nil
	}
	edit := *m.rootEdit
	root := strings.TrimSpace(edit.value)
	if root != "" {
		root = filepath.Clean(root)
	}
	err := mutateRoot(edit.site, root)
	if err != nil {
		m.rootEdit.err = err.Error()
		return m, nil
	}
	m.rootEdit = nil
	if err := m.reloadSites(); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.results = map[string]probeResult{}
	m.status = "updated root for " + edit.site.Name + ".test"
	return m, tea.Batch(m.probeAll(), healthCmd(), m.logPreviewSelected())
}

func (m *model) reloadSites() error {
	st, err := site.Load()
	if err != nil {
		return err
	}
	m.sites = st.Resolve()
	m.links = linkMap(st)
	if m.cursor >= m.filteredLen() {
		m.cursor = m.filteredLen() - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.docExists = checkDocs(m.sites)
	m.fixOffset()
	return nil
}

func linkMap(st *site.State) map[string]site.Link {
	out := map[string]site.Link{}
	if st == nil {
		return out
	}
	for _, l := range st.Links {
		out[l.Name] = l
	}
	return out
}

func mutateLink(name string, fn func(*site.State, *site.Link) error) error {
	st, err := site.Load()
	if err != nil {
		return err
	}
	for i := range st.Links {
		if st.Links[i].Name == name {
			if err := fn(st, &st.Links[i]); err != nil {
				return err
			}
			return saveWriteReload(st)
		}
	}
	return fmt.Errorf("%s is not an explicit link", name)
}

func mutateRoot(s site.Resolved, root string) error {
	if s.Kind == site.KindProxy {
		return fmt.Errorf("proxy sites do not have a docroot")
	}
	st, err := site.Load()
	if err != nil {
		return err
	}
	for i := range st.Links {
		if st.Links[i].Name == s.Name {
			st.Links[i].Root = root
			return saveWriteReload(st)
		}
	}
	site.AddLink(st, site.Link{Name: s.Name, Path: s.Path, Root: root, Secure: s.Secure})
	return saveWriteReload(st)
}

func saveWriteReload(st *site.State) error {
	if err := site.Save(st); err != nil {
		return err
	}
	sites := st.Resolve()
	if err := php.RefreshFPMConfigsForSites(sites); err != nil {
		return err
	}
	if err := site.WriteFragments(sites); err != nil {
		return err
	}
	return site.ReloadCaddy()
}

// --- Filtering -----------------------------------------------------------

func (m model) matches(s site.Resolved) bool {
	if m.filt.problemOnly && !m.isProblem(s) {
		return false
	}
	switch m.filt.secure {
	case secYes:
		if !s.Secure {
			return false
		}
	case secNo:
		if s.Secure {
			return false
		}
	}
	switch m.filt.kind {
	case kindPHP:
		if s.Kind != site.KindPHP {
			return false
		}
	case kindStatic:
		if s.Kind != site.KindStatic {
			return false
		}
	case kindProxy:
		if s.Kind != site.KindProxy {
			return false
		}
	}
	res, has := m.results[s.Name]
	switch m.filt.code {
	case codePending:
		if has {
			return false
		}
	case codeErr:
		if !has || res.code != -1 {
			return false
		}
	case code2xx:
		if !has || res.code < 200 || res.code >= 300 {
			return false
		}
	case code3xx:
		if !has || res.code < 300 || res.code >= 400 {
			return false
		}
	case code4xx:
		if !has || res.code < 400 || res.code >= 500 {
			return false
		}
	case code5xx:
		if !has || res.code < 500 || res.code == -1 {
			return false
		}
	}
	if m.filt.missingOnly && m.docExists[s.Name] {
		return false
	}
	if m.filt.search != "" {
		if !strings.Contains(strings.ToLower(s.Name), strings.ToLower(m.filt.search)) {
			return false
		}
	}
	return true
}

func (m model) isProblem(s site.Resolved) bool {
	if s.Kind != site.KindProxy && !m.docExists[s.Name] {
		return true
	}
	res, has := m.results[s.Name]
	return has && (res.code == -1 || res.code >= 400)
}

func (m model) problemReasons(s site.Resolved) []string {
	reasons := []string{}
	if s.Kind != site.KindProxy && !m.docExists[s.Name] {
		reasons = append(reasons, "missing docroot")
	}
	res, has := m.results[s.Name]
	if !has {
		return reasons
	}
	switch {
	case res.code == -1:
		reasons = append(reasons, "probe error")
	case res.code >= 500:
		reasons = append(reasons, "HTTP "+strconv.Itoa(res.code))
	case res.code >= 400:
		reasons = append(reasons, "HTTP "+strconv.Itoa(res.code))
	}
	return reasons
}

func (m model) isExplicitLink(name string) bool {
	_, ok := m.links[name]
	return ok
}

func (m model) canChangeRoot(s site.Resolved) bool {
	return s.Kind != site.KindProxy
}

func (m model) filtered() []site.Resolved {
	if !m.filt.any() {
		return m.sites
	}
	out := make([]site.Resolved, 0, len(m.sites))
	for _, s := range m.sites {
		if m.matches(s) {
			out = append(out, s)
		}
	}
	return out
}

// displayItem is one row in the grouped view: either a real site (parent or
// child) or a synthetic group header used when the parent matches no filter
// but its children do.
type displayItem struct {
	site       *site.Resolved // nil = synthetic header
	isChild    bool
	isLast     bool   // last child in its group (for tree-corner rendering)
	rootName   string // populated for synthetic
	collapsed  bool
	childCount int
}

type siteGroup struct {
	root     string
	parent   *site.Resolved
	children []site.Resolved
}

// rootOf returns the last dot-segment of a site name. "app.affiliate" → "affiliate".
func rootOf(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// displayItems builds the grouped, filtered, cursor-navigable list.
// All grouping is done by *parent root domain*, with filtering applied
// independently to parents and children.
func (m model) displayItems() []displayItem {
	groups := map[string]*siteGroup{}
	roots := []string{}
	for _, s := range m.sites {
		r := rootOf(s.Name)
		g, ok := groups[r]
		if !ok {
			g = &siteGroup{root: r}
			groups[r] = g
			roots = append(roots, r)
		}
		if s.Name == r {
			cp := s
			g.parent = &cp
		} else {
			g.children = append(g.children, s)
		}
	}
	sort.SliceStable(roots, func(i, j int) bool {
		return m.rootLess(groups[roots[i]], groups[roots[j]])
	})

	out := []displayItem{}
	for _, r := range roots {
		g := groups[r]
		var parent *site.Resolved
		if g.parent != nil && m.matches(*g.parent) {
			parent = g.parent
		}
		visibleChildren := make([]site.Resolved, 0, len(g.children))
		for _, c := range g.children {
			if m.matches(c) {
				visibleChildren = append(visibleChildren, c)
			}
		}
		if parent == nil && len(visibleChildren) == 0 {
			continue
		}
		sort.SliceStable(visibleChildren, func(i, j int) bool {
			return m.siteLess(visibleChildren[i], visibleChildren[j])
		})
		collapsed := m.collapsed[r]
		if parent != nil {
			out = append(out, displayItem{site: parent, collapsed: collapsed, childCount: len(visibleChildren)})
		} else {
			out = append(out, displayItem{rootName: r, collapsed: collapsed, childCount: len(visibleChildren)})
		}
		if collapsed {
			continue
		}
		for i, c := range visibleChildren {
			cp := c
			out = append(out, displayItem{
				site:    &cp,
				isChild: true,
				isLast:  i == len(visibleChildren)-1,
			})
		}
	}
	return out
}

func (m model) rootLess(a, b *siteGroup) bool {
	return m.siteLess(groupSortSite(a), groupSortSite(b))
}

func groupSortSite(g *siteGroup) site.Resolved {
	if g.parent != nil {
		return *g.parent
	}
	if len(g.children) > 0 {
		return g.children[0]
	}
	return site.Resolved{Name: g.root}
}

func (m model) siteLess(a, b site.Resolved) bool {
	switch m.sort {
	case sortProblems:
		ap, bp := m.isProblem(a), m.isProblem(b)
		if ap != bp {
			return ap
		}
	case sortLatency:
		ar, ah := m.results[a.Name]
		br, bh := m.results[b.Name]
		if ah != bh {
			return ah
		}
		if ah && bh && ar.latency != br.latency {
			return ar.latency > br.latency
		}
	case sortKind:
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
	}
	return a.Name < b.Name
}

func (m model) filteredLen() int { return len(m.displayItems()) }

func (m model) selected() *site.Resolved {
	items := m.displayItems()
	if m.cursor < 0 || m.cursor >= len(items) {
		return nil
	}
	return items[m.cursor].site // nil for synthetic
}

func (m model) selectedRoot() string {
	items := m.displayItems()
	if m.cursor < 0 || m.cursor >= len(items) {
		return ""
	}
	if items[m.cursor].rootName != "" {
		return items[m.cursor].rootName
	}
	if items[m.cursor].site != nil {
		return rootOf(items[m.cursor].site.Name)
	}
	return ""
}

func (m model) visibleRows() int {
	// banner(1) + summary(1) + filter chips(1) + header(1) + footer(1) + scroll(1) ≈ 6
	h := m.height - 6
	if h < 5 {
		h = 5
	}
	if l := m.filteredLen(); h > l {
		h = l
	}
	if h < 1 {
		h = 1
	}
	return h
}

// --- View ----------------------------------------------------------------

func (m model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if m.help {
		return m.renderHelp()
	}

	tableWidth, inspectorWidth := m.panelWidths()
	body := m.renderTable(tableWidth)
	if inspectorWidth > 0 {
		body = joinColumns(body, m.renderInspector(inspectorWidth, m.visibleRows()+1), tableWidth, "  "+ruleStyle.Render("│")+" ")
	}

	// Adaptive footer — drop hints from the right until it fits.
	footer := footerStyle.Render("  " + fitJoin(m.width-2, " · ",
		"↑↓ move",
		"o open",
		"l logs",
		"/ search",
		"s/t/c/m filters",
		"! problems",
		"z sort",
		"space fold",
		"? help",
		"r refresh",
		"x clear",
		"q quit",
	))

	lines := []string{
		m.renderBanner(), m.summaryLine(), m.filterLine(),
		strings.TrimRight(body, "\n"),
		footer,
	}
	if prompt := m.promptLine(); prompt != "" {
		lines = append(lines, prompt)
	}
	return strings.Join(lines, "\n")
}

func (m model) promptLine() string {
	switch {
	case m.confirm != nil:
		return modalBox("CONFIRM", []string{
			warnStyle.Render(m.confirm.text + "?"),
			dimStyle.Render("y/enter confirm · n/esc cancel"),
		}, m.width)
	case m.rootEdit != nil:
		lines := []string{
			chipStyle.Render("root " + m.rootEdit.site.Name + ".test"),
			m.rootEdit.value + searchStyle.Render("▏"),
			dimStyle.Render("enter save · empty clears override · esc cancel"),
		}
		if m.rootEdit.err != "" {
			lines = append(lines, errStyle.Render(m.rootEdit.err))
		}
		return modalBox("CHANGE ROOT", lines, m.width)
	default:
		return ""
	}
}

func (m model) renderHelp() string {
	lines := []string{
		m.renderBanner(),
		"",
		headerStyle.Render("KEYS"),
		"  ↑/↓ or j/k    move selection",
		"  g/G           top / bottom",
		"  pgup/pgdown   page",
		"  o or enter    open selected site",
		"  l             tail full logs",
		"  r             refresh state, probes, health, logs",
		"  a             toggle auto-refresh",
		"  z             cycle sort mode",
		"  space         collapse or expand the selected root",
		"  /             search",
		"  x             clear filters",
		"  q             quit",
		"",
		headerStyle.Render("FILTERS"),
		"  s             cycle HTTPS",
		"  t             cycle kind",
		"  c             cycle status code",
		"  m             missing docroots",
		"  !             problems only",
		"",
		headerStyle.Render("ACTIONS"),
		"  u             unlink selected explicit link",
		"  S             toggle HTTPS for selected explicit link",
		"  R             change or clear docroot override",
		"",
	}
	return strings.Join([]string{m.renderBanner(), modalBox("HELP", lines[2:], m.width)}, "\n")
}

func (m model) panelWidths() (int, int) {
	if m.width < 116 {
		return m.width, 0
	}
	inspector := m.width * 38 / 100
	if inspector < 44 {
		inspector = 44
	}
	if inspector > 64 {
		inspector = 64
	}
	separator := 4
	table := m.width - inspector - separator
	if table < 68 {
		return m.width, 0
	}
	return table, inspector
}

func (m model) renderBanner() string {
	// Bold purple ROUTA with heavy bars; no background color, so terminal
	// themes do not fight the title.
	label := bannerStyle.Render("ROUTA")
	tagline := taglineStyle.Render("local web dev server")
	leadBars := ruleStyle.Render("━━━ ")
	gap := ruleStyle.Render(" · ")
	core := leadBars + label + gap + tagline + " "
	visible := 4 + 5 + 3 + lipgloss.Width(tagline) + 1
	tailLen := m.width - visible - 1
	if tailLen < 0 {
		tailLen = 0
	}
	return " " + core + ruleStyle.Render(strings.Repeat("━", tailLen))
}

func (m model) summaryLine() string {
	all := m.sites
	filt := m.filtered()
	ok, warn, errC, pending, missing := 0, 0, 0, 0, 0
	for _, s := range all {
		r, has := m.results[s.Name]
		switch {
		case !has:
			pending++
		case r.code == -1 || r.code >= 500:
			errC++
		case r.code >= 400:
			warn++
		default:
			ok++
		}
		if !m.docExists[s.Name] {
			missing++
		}
	}
	missingPart := ""
	if missing > 0 {
		missingPart = fmt.Sprintf("  %s missing", warnStyle.Render(strconv.Itoa(missing)))
	}
	auto := ""
	if m.auto {
		auto = "  " + chipStyle.Render("auto 5s")
	}
	status := ""
	if m.status != "" {
		status = "  " + dimStyle.Render(truncate(m.status, 36))
	}
	return fmt.Sprintf(" %d/%d sites    %s ok  %s warn  %s err  %s pending%s    %s%s%s",
		len(filt), len(all),
		okStyle.Render(strconv.Itoa(ok)),
		warnStyle.Render(strconv.Itoa(warn)),
		errStyle.Render(strconv.Itoa(errC)),
		dimStyle.Render(strconv.Itoa(pending)),
		missingPart,
		m.healthLine(),
		auto,
		status)
}

func (m model) healthLine() string {
	parts := []string{
		healthToken("dns", m.health.dnsOK),
		healthToken("caddy", m.health.caddyOK),
		healthToken("443", m.health.httpsOK),
		healthToken("8443", m.health.altOK),
	}
	return strings.Join(parts, " ")
}

func healthToken(label string, ok bool) string {
	if ok {
		return okStyle.Render(label + ":ok")
	}
	return errStyle.Render(label + ":down")
}

func (m model) renderTable(width int) string {
	lay := computeLayout(width)
	headParts := []string{pad("NAME", lay.nameW), pad("HTTPS", lay.httpsW)}
	if lay.showKind {
		headParts = append(headParts, pad("KIND", lay.kindW))
	}
	if lay.showPHP {
		headParts = append(headParts, pad("PHP", lay.phpW))
	}
	headParts = append(headParts, pad("STAT", lay.statW))
	if lay.showLat {
		headParts = append(headParts, pad("LAT", lay.latW))
	}
	if lay.showDoc {
		headParts = append(headParts, pad("DOCROOT", lay.docW))
	}

	items := m.displayItems()
	var body strings.Builder
	body.WriteString("  " + headerStyle.Render(strings.Join(headParts, "")))
	body.WriteString("\n")
	if len(items) == 0 {
		body.WriteString(dimStyle.Render("  no sites match the current filters"))
		body.WriteString("\n")
		return body.String()
	}

	visible := m.visibleRows()
	end := m.offset + visible
	if end > len(items) {
		end = len(items)
	}
	for i := m.offset; i < end; i++ {
		it := items[i]
		var content string
		if it.site == nil {
			content = m.renderSynthetic(it.rootName, it.collapsed, it.childCount, lay)
		} else {
			content = m.renderRow(*it.site, it.isChild, it.isLast, it.collapsed, it.childCount, lay)
		}
		marker := "  "
		if i == m.cursor {
			marker = cursorStyle.Render("❯ ")
		}
		line := marker + content
		if i == m.cursor {
			line = selectedStyle.Render(line)
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	if len(items) > visible {
		body.WriteString(dimStyle.Render(fmt.Sprintf("  %d–%d of %d", m.offset+1, end, len(items))))
		body.WriteString("\n")
	}
	return body.String()
}

func (m model) renderInspector(width, height int) string {
	lines := []string{headerStyle.Render(pad("SELECTED SITE", width))}
	it := m.selectedItem()
	if it.site == nil {
		if it.rootName == "" {
			lines = append(lines, "", dimStyle.Render("No site selected."))
		} else {
			lines = append(lines,
				"",
				chipStyle.Render(it.rootName+".test"),
				dimStyle.Render("Children are visible because they match the current filters."),
			)
		}
		return padLines(lines, width, height)
	}

	s := *it.site
	lines = append(lines,
		"",
		chipStyle.Render(truncate(s.Name+".test", width)),
		m.inspectorKV("url", siteURLLabel(s.Name), width),
		m.inspectorKV("kind", string(s.Kind), width),
		m.inspectorKV("https", yesNo(s.Secure), width),
	)
	if s.Kind == site.KindPHP {
		lines = append(lines, m.inspectorKV("php", blankDash(s.PHP), width))
	}
	if s.Kind == site.KindProxy {
		lines = append(lines, m.inspectorKV("target", s.Target, width))
	} else {
		lines = append(lines,
			m.inspectorKV("path", blankDash(s.Path), width),
			m.inspectorKV("docroot", s.Docroot, width),
			m.inspectorKV("docroot ok", yesNo(m.docExists[s.Name]), width),
		)
	}
	lines = append(lines,
		m.inspectorKV("probe", m.probeLabel(s.Name), width),
		m.inspectorKV("fragment", filepath.Join(paths.DataDir(), "sites", s.Name+".caddy"), width),
	)
	if reasons := m.problemReasons(s); len(reasons) > 0 {
		lines = append(lines, m.inspectorKV("problem", strings.Join(reasons, ", "), width))
	}
	if logLines := m.logs[s.Name]; len(logLines) > 0 {
		lines = append(lines, "", headerStyle.Render(pad("LOGS", width)))
		for _, line := range logLines {
			lines = append(lines, dimStyle.Render(truncate(line, width)))
		}
	}
	lines = append(lines,
		"",
		headerStyle.Render(pad("ACTIONS", width)),
		actionLine("o", "open in browser", width),
		actionLine("l", "tail logs", width),
		actionLine("r", "refresh probes", width),
		actionLine("a", "toggle auto-refresh", width),
		actionLine("z", "sort by "+m.sort.next().label(), width),
		actionLine("space", "collapse group", width),
		actionLine("!", "problems only", width),
		actionLine("/", "search sites", width),
		actionLineEnabled("u", "unlink", width, m.isExplicitLink(s.Name)),
		actionLineEnabled("S", "toggle HTTPS", width, m.isExplicitLink(s.Name)),
		actionLineEnabled("R", "change root", width, m.canChangeRoot(s)),
	)
	if !m.docExists[s.Name] && s.Kind != site.KindProxy {
		lines = append(lines, actionLine("m", "show missing docroots", width))
	}
	return padLines(lines, width, height)
}

func (m model) selectedItem() displayItem {
	items := m.displayItems()
	if m.cursor < 0 || m.cursor >= len(items) {
		return displayItem{}
	}
	return items[m.cursor]
}

// fitJoin packs as many parts as fit within width, joined by sep.
func fitJoin(width int, sep string, parts ...string) string {
	if width < 1 {
		return ""
	}
	out := ""
	for i, p := range parts {
		candidate := out
		if i > 0 {
			candidate += sep
		}
		candidate += p
		if lipgloss.Width(candidate) > width {
			break
		}
		out = candidate
	}
	return out
}

func (m model) filterLine() string {
	if m.searching {
		return " " + searchStyle.Render("/ ") + m.filt.search + searchStyle.Render("▏")
	}
	parts := []string{}
	if l := m.filt.secure.label(); l != "" {
		parts = append(parts, "https="+l)
	}
	if l := m.filt.kind.label(); l != "" {
		parts = append(parts, "kind="+l)
	}
	if l := m.filt.code.label(); l != "" {
		parts = append(parts, "code="+l)
	}
	if m.filt.missingOnly {
		parts = append(parts, "missing-docroot")
	}
	if m.filt.problemOnly {
		parts = append(parts, "problems")
	}
	if m.sort != sortName {
		parts = append(parts, "sort="+m.sort.label())
	}
	if m.filt.search != "" {
		parts = append(parts, "search="+strconv.Quote(m.filt.search))
	}
	if len(parts) == 0 {
		return dimStyle.Render(" filters: (none)")
	}
	return " " + chipStyle.Render("filters:") + " " + strings.Join(parts, "  ")
}

func (m model) renderSynthetic(rootName string, collapsed bool, childCount int, lay layout) string {
	prefix := groupMarker(collapsed, childCount)
	nameText := prefix + truncate(rootName+".test", lay.nameW-lipgloss.Width(prefix)-1)
	name := dimStyle.Render(pad(nameText, lay.nameW))
	restW := lay.httpsW + lay.statW
	if lay.showKind {
		restW += lay.kindW
	}
	if lay.showPHP {
		restW += lay.phpW
	}
	if lay.showLat {
		restW += lay.latW
	}
	if lay.showDoc {
		restW += lay.docW
	}
	rest := dimStyle.Render(pad("(no parent site — children only)", restW))
	return name + rest
}

func groupMarker(collapsed bool, childCount int) string {
	if childCount == 0 {
		return ""
	}
	if collapsed {
		return "▸ "
	}
	return "▾ "
}

func (m model) renderRow(s site.Resolved, isChild, isLast, collapsed bool, childCount int, lay layout) string {
	displayName := s.Name + ".test"
	prefix := ""
	prefixLen := 0
	if isChild {
		corner := "├─"
		if isLast {
			corner = "└─"
		}
		prefix = "  " + corner + " "
		prefixLen = 5 // "  X─ " is 5 visible cols
	} else {
		prefix = groupMarker(collapsed, childCount)
		prefixLen = lipgloss.Width(prefix)
	}
	truncated := truncate(displayName, lay.nameW-prefixLen-1)
	plain := prefix + truncated
	styled := dimStyle.Render(prefix) + truncated
	name := padStyled(styled, plain, lay.nameW)

	httpsText := "no"
	httpsStyle := warnStyle
	if s.Secure {
		httpsText = "yes"
		httpsStyle = okStyle
	}
	httpsCell := padStyled(httpsStyle.Render(httpsText), httpsText, lay.httpsW)

	php := s.PHP
	if php == "" {
		php = "-"
	}
	phpCell := pad(php, lay.phpW)

	res, has := m.results[s.Name]
	var statText, statStyled, latText string
	switch {
	case !has:
		statText = "-"
		statStyled = dimStyle.Render(statText)
		latText = "-"
	case res.code == -1:
		statText = "ERR"
		statStyled = errStyle.Render(statText)
		latText = formatDur(res.latency)
	default:
		statText = strconv.Itoa(res.code)
		switch {
		case res.code >= 500:
			statStyled = errStyle.Render(statText)
		case res.code >= 400:
			statStyled = warnStyle.Render(statText)
		case res.code >= 200:
			statStyled = okStyle.Render(statText)
		default:
			statStyled = statText
		}
		latText = formatDur(res.latency)
	}
	statCell := padStyled(statStyled, statText, lay.statW)
	latCell := pad(latText, lay.latW)

	var docPlain, docStyled string
	if s.Kind == site.KindProxy {
		docPlain = "→ " + s.Target
		docStyled = chipStyle.Render("→ ") + s.Target
	} else {
		docPlain = truncate(s.Docroot, lay.docW-2)
		docStyled = docPlain
		if !m.docExists[s.Name] {
			docStyled = errStyle.Render("✗ " + docPlain)
			docPlain = "✗ " + docPlain
		}
	}
	docCell := padStyled(docStyled, docPlain, lay.docW)

	parts := []string{name, httpsCell}
	if lay.showKind {
		parts = append(parts, pad(string(s.Kind), lay.kindW))
	}
	if lay.showPHP {
		parts = append(parts, phpCell)
	}
	parts = append(parts, statCell)
	if lay.showLat {
		parts = append(parts, latCell)
	}
	if lay.showDoc {
		parts = append(parts, docCell)
	}
	return strings.Join(parts, "")
}

func (m model) inspectorKV(label, value string, width int) string {
	const labelW = 11
	if value == "" {
		value = "-"
	}
	valueW := width - labelW
	if valueW < 1 {
		valueW = 1
	}
	return dimStyle.Render(pad(label, labelW)) + truncate(value, valueW)
}

func (m model) probeLabel(name string) string {
	res, has := m.results[name]
	if !has {
		return "pending"
	}
	if res.code == -1 {
		return "ERR " + formatDur(res.latency)
	}
	return strconv.Itoa(res.code) + " " + formatDur(res.latency)
}

func siteURLLabel(name string) string {
	return fmt.Sprintf("https://%s.test", name)
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}

func blankDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func actionLine(key, label string, width int) string {
	keyText := chipStyle.Render(key)
	plain := key + " " + label
	if lipgloss.Width(plain) > width {
		label = truncate(label, width-2)
	}
	return keyText + " " + label
}

func actionLineEnabled(key, label string, width int, enabled bool) string {
	if enabled {
		return actionLine(key, label, width)
	}
	plain := key + " " + label
	if lipgloss.Width(plain) > width {
		label = truncate(label, width-2)
	}
	return dimStyle.Render(plain)
}

func padLines(lines []string, width, height int) string {
	if height < 1 {
		height = 1
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = padVisible(line, width)
	}
	return strings.Join(lines, "\n")
}

func modalBox(title string, lines []string, termWidth int) string {
	contentW := 0
	for _, line := range lines {
		if w := lipgloss.Width(line); w > contentW {
			contentW = w
		}
	}
	if titleW := lipgloss.Width(title) + 2; titleW > contentW {
		contentW = titleW
	}
	maxW := termWidth - 6
	if maxW < 24 {
		maxW = 24
	}
	if contentW > maxW {
		contentW = maxW
	}
	topLabel := " " + title + " "
	topFill := contentW - lipgloss.Width(topLabel)
	if topFill < 0 {
		topFill = 0
	}
	out := []string{
		ruleStyle.Render("┌" + topLabel + strings.Repeat("─", topFill) + "┐"),
	}
	for _, line := range lines {
		out = append(out, ruleStyle.Render("│")+padVisible(truncate(line, contentW), contentW)+ruleStyle.Render("│"))
	}
	out = append(out, ruleStyle.Render("└"+strings.Repeat("─", contentW)+"┘"))
	box := strings.Join(out, "\n")
	if termWidth <= contentW+2 {
		return box
	}
	padLeft := (termWidth - contentW - 2) / 2
	prefix := strings.Repeat(" ", padLeft)
	for i, line := range out {
		out[i] = prefix + line
	}
	return strings.Join(out, "\n")
}

func joinColumns(left, right string, leftWidth int, sep string) string {
	lLines := strings.Split(strings.TrimRight(left, "\n"), "\n")
	rLines := strings.Split(strings.TrimRight(right, "\n"), "\n")
	n := len(lLines)
	if len(rLines) > n {
		n = len(rLines)
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(lLines) {
			l = lLines[i]
		}
		if i < len(rLines) {
			r = rLines[i]
		}
		out[i] = padVisible(l, leftWidth) + sep + r
	}
	return strings.Join(out, "\n")
}

// --- helpers -------------------------------------------------------------

func pad(s string, w int) string {
	visible := lipgloss.Width(s)
	if visible >= w {
		return s
	}
	return s + strings.Repeat(" ", w-visible)
}

func padVisible(s string, w int) string {
	visible := lipgloss.Width(s)
	if visible >= w {
		return s
	}
	return s + strings.Repeat(" ", w-visible)
}

func padStyled(styled, plain string, w int) string {
	visible := len([]rune(plain))
	if visible >= w {
		return styled
	}
	return styled + strings.Repeat(" ", w-visible)
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w < 2 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

func formatDur(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "<1ms"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	default:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}

func checkDocs(sites []site.Resolved) map[string]bool {
	out := make(map[string]bool, len(sites))
	for _, s := range sites {
		// Proxies don't have a docroot; mark as "exists" so they're never flagged missing.
		if s.Kind == site.KindProxy {
			out[s.Name] = true
			continue
		}
		_, err := os.Stat(s.Docroot)
		out[s.Name] = err == nil
	}
	return out
}

// --- entrypoint ----------------------------------------------------------

func Run() error {
	s, err := site.Load()
	if err != nil {
		return err
	}
	sites := s.Resolve()
	if len(sites) == 0 {
		return fmt.Errorf("no sites configured. Run `routa track <dir>` or `routa link [name]` first")
	}
	m := model{
		sites:     sites,
		links:     linkMap(s),
		results:   map[string]probeResult{},
		logs:      map[string][]string{},
		health:    collectHealth(),
		collapsed: map[string]bool{},
		docExists: checkDocs(sites),
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// DebugRender returns one rendered View frame (no event loop) at the given width.
// Used by `routa tui-render` to inspect output non-interactively.
func DebugRender(width int) string {
	s, err := site.Load()
	if err != nil {
		return "load error: " + err.Error()
	}
	sites := s.Resolve()
	m := model{
		sites:     sites,
		links:     linkMap(s),
		results:   map[string]probeResult{},
		logs:      map[string][]string{},
		health:    collectHealth(),
		collapsed: map[string]bool{},
		docExists: checkDocs(sites),
		width:     width,
		height:    40,
	}
	return m.View()
}

func siteURL(name string) string {
	if portBound("127.0.0.1:443") || portBound(":443") {
		return fmt.Sprintf("https://%s.test", name)
	}
	return fmt.Sprintf("https://%s.test:8443", name)
}

func portBound(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
