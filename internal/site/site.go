// Package site holds hostr's site model: parked directories, explicit links,
// state persistence (JSON in $XDG_CONFIG_HOME/hostr/state.json), Caddy
// fragment rendering, and a reload trigger.
package site

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/scottzirkel/hostr/internal/paths"
	"github.com/scottzirkel/hostr/internal/systemd"
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

type State struct {
	Parked     []string `json:"parked"`
	Links      []Link   `json:"links"`
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
}

func statePath() string { return filepath.Join(paths.ConfigDir(), "state.json") }

func Load() (*State, error) {
	b, err := os.ReadFile(statePath())
	if os.IsNotExist(err) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", statePath(), err)
	}
	return &s, nil
}

func Save(s *State) error {
	if err := os.MkdirAll(paths.ConfigDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), b, 0o644)
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
			p := filepath.Join(dir, e.Name())
			r := build(e.Name(), p, "", "", "", true, s.DefaultPHP)
			seen[r.Name] = r
		}
	}
	for _, l := range s.Links {
		r := build(l.Name, l.Path, l.Root, l.Target, l.PHP, l.Secure, s.DefaultPHP)
		seen[r.Name] = r
	}

	out := make([]Resolved, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
//  3. Built static output: dist/ | out/ | build/ | _site/ with index.html → static
//  4. Static at root: index.html → static, docroot = root
//  5. Fallback: serve the directory itself (file_server may 404 if empty)
func detect(path string) (Kind, string) {
	if exists(filepath.Join(path, "composer.json")) &&
		exists(filepath.Join(path, "public", "index.php")) {
		return KindPHP, filepath.Join(path, "public")
	}
	if exists(filepath.Join(path, "index.php")) {
		return KindPHP, path
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

const fragmentTmpl = `{{.Name}}.test {
	bind 127.0.0.1
{{- if .Secure}}
	tls internal
{{- else}}
	# secure=false: HTTP only
{{- end}}
{{- if eq (printf "%s" .Kind) "proxy"}}
	reverse_proxy {{.Target}}
{{- else}}
	root * {{.Docroot}}
	encode zstd gzip
{{- if eq (printf "%s" .Kind) "php"}}
{{- if .PHP}}
	php_fastcgi unix/{{.SockPath}}
{{- else}}
	respond "hostr: {{.Name}} is a PHP site but no PHP version is installed. Run 'hostr php install <ver>'." 503
{{- end}}
{{- end}}
	file_server
{{- end}}
	log {
		output file {{.LogDir}}/{{.Name}}.log
	}
}
`

type fragData struct {
	Resolved
	SockPath string
	LogDir   string
}

func WriteFragments(sites []Resolved) error {
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
			Resolved: s,
			SockPath: filepath.Join(paths.RunDir(), fmt.Sprintf("php-fpm-%s.sock", s.PHP)),
			LogDir:   paths.LogDir(),
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

// Reload regenerates fragments and asks Caddy to pick them up.
// systemctl --user reload calls `caddy reload --config <path>`.
func ReloadCaddy() error {
	cmd := []string{"reload", "hostr-caddy.service"}
	return systemctlUser(cmd...)
}

func systemctlUser(args ...string) error {
	full := append([]string{"--user"}, args...)
	return systemd.RunSystemctl(full...)
}
