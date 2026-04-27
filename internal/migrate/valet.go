// Package migrate imports a valet-linux configuration into hostr's state.
// Reads ~/.valet/{config.json,Sites,Certificates,Nginx} and produces a Plan
// the caller can preview or apply.
package migrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/scottzirkel/hostr/internal/site"
)

type ValetConfig struct {
	Paths  []string `json:"paths"`
	Domain string   `json:"domain"`
}

type Plan struct {
	Parked   []string
	Links    []site.Link
	Warnings []string
}

func valetPath(parts ...string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(append([]string{home, ".valet"}, parts...)...)
}

func ReadConfig() (*ValetConfig, error) {
	data, err := os.ReadFile(valetPath("config.json"))
	if err != nil {
		return nil, fmt.Errorf("read valet config: %w", err)
	}
	var c ValetConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse valet config: %w", err)
	}
	return &c, nil
}

func BuildPlan(cfg *ValetConfig) (*Plan, error) {
	p := &Plan{Parked: append([]string{}, cfg.Paths...)}

	if cfg.Domain != "" && cfg.Domain != "test" {
		p.Warnings = append(p.Warnings,
			fmt.Sprintf("valet domain is %q; hostr currently only handles .test — sites will be served under .test", cfg.Domain))
	}

	entries, err := os.ReadDir(valetPath("Sites"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(filepath.Join(valetPath("Sites"), e.Name()))
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(valetPath("Sites"), target)
		}
		l := site.Link{
			Name:   e.Name(),
			Path:   target,
			Secure: hasCert(e.Name()),
		}
		if v := isolatedPHP(e.Name()); v != "" {
			l.PHP = v
		}
		p.Links = append(p.Links, l)
	}
	return p, nil
}

func hasCert(name string) bool {
	_, err := os.Stat(valetPath("Certificates", name+".test.crt"))
	return err == nil
}

// Capture e.g. "8.3" from valet8.3.sock, or "84" from valet84.sock — caller normalizes.
var fpmSocketRE = regexp.MustCompile(`valet(\d+\.?\d+)\.sock`)

func isolatedPHP(name string) string {
	data, err := os.ReadFile(valetPath("Nginx", name+".test"))
	if err != nil {
		return ""
	}
	m := fpmSocketRE.FindStringSubmatch(string(data))
	if m == nil {
		return ""
	}
	v := m[1]
	// "84" → "8.4", "83" → "8.3"
	if len(v) == 2 {
		return string(v[0]) + "." + string(v[1])
	}
	return v
}
