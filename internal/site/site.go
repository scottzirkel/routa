// Package site holds routa's site model: parked directories, explicit links,
// state persistence (JSON in $XDG_CONFIG_HOME/routa/state.json), Caddy
// fragment rendering, and a reload trigger.
package site

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/systemd"
)

type Kind string

const (
	KindStatic Kind = "static"
	KindPHP    Kind = "php"
	KindProxy  Kind = "proxy"
)

type Link struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`   // for static/php; empty for proxy
	Root   string `json:"root,omitempty"`   // optional docroot override (rel to Path or absolute)
	Target string `json:"target,omitempty"` // for proxy: "host:port"
	PHP    string `json:"php,omitempty"`    // pinned version; empty = use default
	Secure bool   `json:"secure"`
}

type Alias struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

const CurrentStateVersion = 3

type State struct {
	Version    int      `json:"version"`
	Parked     []string `json:"parked"`
	Ignored    []string `json:"ignored,omitempty"`
	Links      []Link   `json:"links"`
	Aliases    []Alias  `json:"aliases,omitempty"`
	DefaultPHP string   `json:"default_php,omitempty"`
}

type Resolved struct {
	Name    string
	Path    string
	Docroot string
	Target  string // for proxy
	Kind    Kind
	PHP     string // resolved version (Link.PHP or DefaultPHP)
	Secure  bool
	AliasOf string
}

var siteNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func statePath() string { return filepath.Join(paths.ConfigDir(), "state.json") }

func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("site name cannot be empty")
	}
	if strings.HasSuffix(name, ".test") {
		return fmt.Errorf("site name should not include .test")
	}
	if len(name) > 253 || !siteNameRE.MatchString(name) {
		return fmt.Errorf("invalid site name %q", name)
	}
	return nil
}

func ValidateProxyTarget(target string) error {
	if strings.TrimSpace(target) != target || target == "" {
		return fmt.Errorf("invalid proxy target %q", target)
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("invalid proxy target %q: expected host:port", target)
	}
	if host == "" {
		return fmt.Errorf("invalid proxy target %q: host cannot be empty", target)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid proxy target %q: port must be 1-65535", target)
	}
	return nil
}

func Load() (*State, error) {
	b, err := os.ReadFile(statePath())
	if os.IsNotExist(err) {
		return &State{Version: CurrentStateVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", statePath(), err)
	}
	if err := normalizeLoadedState(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

func Save(s *State) error {
	if err := os.MkdirAll(paths.ConfigDir(), 0o755); err != nil {
		return err
	}
	s.Version = CurrentStateVersion
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), b, 0o644)
}

func normalizeLoadedState(s *State) error {
	switch {
	case s.Version == 0:
		// Legacy pre-version state files are the v1 shape.
		s.Version = CurrentStateVersion
	case s.Version > CurrentStateVersion:
		return fmt.Errorf("state file %s has unsupported version %d; this routa supports up to version %d", statePath(), s.Version, CurrentStateVersion)
	case s.Version < 0:
		return fmt.Errorf("state file %s has invalid version %d", statePath(), s.Version)
	}
	return nil
}

// Resolve walks parked dirs and merges explicit links. Links override.
func (s *State) Resolve() []Resolved {
	seen := map[string]Resolved{}

	for _, dir := range s.Parked {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			name := strings.ToLower(e.Name())
			if ValidateName(name) != nil {
				continue
			}
			if s.Ignores(name) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			r := build(name, p, "", "", "", true, s.DefaultPHP)
			seen[r.Name] = r
		}
	}
	for _, l := range s.Links {
		r := build(l.Name, l.Path, l.Root, l.Target, l.PHP, l.Secure, s.DefaultPHP)
		seen[r.Name] = r
	}
	s.resolveAliases(seen)

	out := make([]Resolved, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *State) resolveAliases(seen map[string]Resolved) {
	aliases := map[string]string{}
	for _, a := range s.Aliases {
		if a.Name == "" || a.Target == "" {
			continue
		}
		if _, exists := seen[a.Name]; exists {
			continue
		}
		aliases[a.Name] = a.Target
	}
	var resolveOne func(name string, stack map[string]bool) (Resolved, bool)
	resolveOne = func(name string, stack map[string]bool) (Resolved, bool) {
		if r, ok := seen[name]; ok {
			return r, true
		}
		target, ok := aliases[name]
		if !ok || stack[name] {
			return Resolved{}, false
		}
		stack[name] = true
		r, ok := resolveOne(target, stack)
		delete(stack, name)
		if !ok {
			return Resolved{}, false
		}
		r.Name = name
		r.AliasOf = target
		seen[name] = r
		return r, true
	}
	for name := range aliases {
		_, _ = resolveOne(name, map[string]bool{})
	}
}

func (s *State) ResolvePath(path string) []Resolved {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = filepath.Clean(path)
	}
	var matches []Resolved
	longest := -1
	for _, r := range s.Resolve() {
		if r.Path == "" {
			continue
		}
		sitePath, err := filepath.Abs(r.Path)
		if err != nil {
			sitePath = filepath.Clean(r.Path)
		}
		if !pathContains(sitePath, abs) {
			continue
		}
		if len(sitePath) > longest {
			longest = len(sitePath)
			matches = matches[:0]
		}
		if len(sitePath) == longest {
			matches = append(matches, r)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	return matches
}

func pathContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func build(name, path, root, target, php string, secure bool, defaultPHP string) Resolved {
	if target != "" {
		return Resolved{
			Name:   name,
			Target: target,
			Kind:   KindProxy,
			Secure: secure,
		}
	}
	var kind Kind
	var docroot string
	if root != "" {
		docroot = root
		if !filepath.IsAbs(docroot) {
			docroot = filepath.Join(path, root)
		}
		// Determine kind from the override docroot's contents.
		switch {
		case exists(filepath.Join(docroot, "index.php")):
			kind = KindPHP
		default:
			kind = KindStatic
		}
	} else {
		kind, docroot = detect(path)
	}
	resolvedPHP := php
	if resolvedPHP == "" && kind == KindPHP {
		resolvedPHP = defaultPHP
	}
	return Resolved{
		Name:    name,
		Path:    path,
		Docroot: docroot,
		Kind:    kind,
		PHP:     resolvedPHP,
		Secure:  secure,
	}
}

// detect picks the docroot and kind for a site directory.
// Heuristic order, from most specific to least:
//  1. Laravel: composer.json + public/index.php → PHP, docroot = public
//  2. Plain PHP at root: index.php → PHP, docroot = root
//  3. Public static output: public/index.html → static, docroot = public
//  4. Built static output: dist/ | out/ | build/ | _site/ with index.html → static
//  5. Static at root: index.html → static, docroot = root
//  6. Fallback: serve the directory itself (file_server may 404 if empty)
func detect(path string) (Kind, string) {
	if exists(filepath.Join(path, "composer.json")) &&
		exists(filepath.Join(path, "public", "index.php")) {
		return KindPHP, filepath.Join(path, "public")
	}
	if exists(filepath.Join(path, "index.php")) {
		return KindPHP, path
	}
	if exists(filepath.Join(path, "public", "index.html")) {
		return KindStatic, filepath.Join(path, "public")
	}
	for _, sub := range []string{"dist", "out", "build", "_site"} {
		if exists(filepath.Join(path, sub, "index.html")) {
			return KindStatic, filepath.Join(path, sub)
		}
	}
	if exists(filepath.Join(path, "index.html")) {
		return KindStatic, path
	}
	return KindStatic, path
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// --- mutations -----------------------------------------------------------

func AddParked(s *State, dir string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for _, p := range s.Parked {
		if p == abs {
			return
		}
	}
	s.Parked = append(s.Parked, abs)
}

func RemoveParked(s *State, dir string) {
	abs, _ := filepath.Abs(dir)
	out := s.Parked[:0]
	for _, p := range s.Parked {
		if p != abs && p != dir {
			out = append(out, p)
		}
	}
	s.Parked = out
}

func AddIgnored(s *State, name string) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".test")
	for _, ignored := range s.Ignored {
		if ignored == name {
			return
		}
	}
	s.Ignored = append(s.Ignored, name)
	sort.Strings(s.Ignored)
}

func RemoveIgnored(s *State, name string) bool {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".test")
	out := s.Ignored[:0]
	removed := false
	for _, ignored := range s.Ignored {
		if ignored == name {
			removed = true
			continue
		}
		out = append(out, ignored)
	}
	s.Ignored = out
	return removed
}

func (s *State) Ignores(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".test")
	for _, ignored := range s.Ignored {
		if ignored == name {
			return true
		}
	}
	return false
}

func AddAlias(s *State, target, name string) {
	out := s.Aliases[:0]
	for _, a := range s.Aliases {
		if a.Name != name {
			out = append(out, a)
		}
	}
	s.Aliases = append(out, Alias{Name: name, Target: target})
	sort.Slice(s.Aliases, func(i, j int) bool { return s.Aliases[i].Name < s.Aliases[j].Name })
}

func RemoveAlias(s *State, name string) bool {
	out := s.Aliases[:0]
	removed := false
	for _, a := range s.Aliases {
		if a.Name == name {
			removed = true
			continue
		}
		out = append(out, a)
	}
	s.Aliases = out
	return removed
}

func AddLink(s *State, l Link) {
	for i, existing := range s.Links {
		if existing.Name == l.Name {
			s.Links[i] = l
			return
		}
	}
	s.Links = append(s.Links, l)
}

func RemoveLink(s *State, name string) bool {
	out := s.Links[:0]
	removed := false
	for _, l := range s.Links {
		if l.Name == name {
			removed = true
			continue
		}
		out = append(out, l)
	}
	s.Links = out
	return removed
}

// --- caddy fragment rendering -------------------------------------------

const fragmentTmpl = `{{.SiteAddress}} {
	bind 127.0.0.1
{{- if .Secure}}
	tls internal
{{- else}}
	# secure=false: HTTP only
{{- end}}
{{- if eq (printf "%s" .Kind) "proxy"}}
	reverse_proxy {{.TargetCaddy}} {
		header_up Host {host}
		header_up X-Forwarded-Host {host}
		header_up X-Forwarded-Proto {scheme}
	}
{{- else}}
	root * {{.DocrootCaddy}}
	encode zstd gzip
{{- if eq (printf "%s" .Kind) "php"}}
{{- if .PHP}}
	php_fastcgi {{.SockPathCaddy}}
{{- else}}
	respond "routa: {{.Name}} is a PHP site but no PHP version is installed. Run 'routa php install <ver>'." 503
{{- end}}
{{- else}}
	try_files {path} {path}/ /index.html
{{- end}}
	file_server
{{- end}}
	log {
		output file {{.LogFileCaddy}} {
			roll_size 10MiB
			roll_keep 5
			roll_keep_for 720h
		}
	}
}
`

type fragData struct {
	Resolved
	SiteAddress   string
	TargetCaddy   string
	DocrootCaddy  string
	SockPathCaddy string
	LogFileCaddy  string
}

func WriteFragments(sites []Resolved) error {
	for _, s := range sites {
		if err := ValidateName(s.Name); err != nil {
			return err
		}
		if s.Kind == KindProxy {
			if err := ValidateProxyTarget(s.Target); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(paths.SitesDir(), 0o755); err != nil {
		return err
	}
	// Wipe stale fragments.
	old, _ := filepath.Glob(filepath.Join(paths.SitesDir(), "*.caddy"))
	keep := map[string]bool{}
	for _, s := range sites {
		keep[fragName(s.Name)] = true
	}
	for _, f := range old {
		if !keep[filepath.Base(f)] {
			_ = os.Remove(f)
		}
	}

	t := template.Must(template.New("frag").Parse(fragmentTmpl))
	for _, s := range sites {
		data := fragData{
			Resolved:      s,
			SiteAddress:   siteAddress(s),
			TargetCaddy:   caddyQuote(s.Target),
			DocrootCaddy:  caddyQuote(s.Docroot),
			SockPathCaddy: caddyQuote("unix/" + filepath.Join(paths.RunDir(), fmt.Sprintf("php-fpm-%s.sock", s.PHP))),
			LogFileCaddy:  caddyQuote(filepath.Join(paths.LogDir(), s.Name+".log")),
		}
		f, err := os.Create(filepath.Join(paths.SitesDir(), fragName(s.Name)))
		if err != nil {
			return err
		}
		if err := t.Execute(f, data); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	return nil
}

func fragName(siteName string) string { return siteName + ".caddy" }

func siteAddress(s Resolved) string {
	if s.Secure {
		return s.Name + ".test"
	}
	return "http://" + s.Name + ".test"
}

func caddyQuote(s string) string {
	return strconv.Quote(s)
}

// Reload regenerates fragments and asks Caddy to pick them up.
// systemctl --user reload calls `caddy reload --config <path>`.
func ReloadCaddy() error {
	cmd := []string{"reload", "routa-caddy.service"}
	return systemctlUser(cmd...)
}

func systemctlUser(args ...string) error {
	full := append([]string{"--user"}, args...)
	return systemd.RunSystemctl(full...)
}
