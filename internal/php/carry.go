package php

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/scottzirkel/routa/internal/paths"
)

// Settings are per PHP version, so a new minor version starts with none of the
// old one's. These helpers carry them over: routa php install runs it for a
// new minor version, and routa php ini copy runs it on request.

// HasUserSettings reports whether spec has settings someone chose: a conf.d
// file, or a php.ini setting other than the ones every install writes itself
// (the Xdebug and pcov load lines and Xdebug's off defaults).
func HasUserSettings(spec string) (bool, error) {
	confFiles, err := filepath.Glob(filepath.Join(ConfDir(spec), "*.ini"))
	if err != nil {
		return false, err
	}
	if len(confFiles) > 0 {
		return true, nil
	}
	settings, err := LoadINISettings(spec)
	if err != nil {
		return false, err
	}
	for _, s := range settings {
		if !installDefault(s) {
			return true, nil
		}
	}
	return false, nil
}

func installDefault(s INISetting) bool {
	switch {
	case s.Key == ExtensionKey || s.Key == ZendExtensionKey:
		return managedExtensionLine(s)
	case s.Key == XdebugModeKey:
		return s.Value == "off"
	case s.Key == XdebugStartWithRequestKey:
		return s.Value == "default"
	}
	return false
}

func managedExtensionLine(s INISetting) bool {
	for _, ext := range ManagedExtensions() {
		if ext.loadedBy(s.Value) {
			return true
		}
	}
	return false
}

var minorDirRE = regexp.MustCompile(`^(\d+)\.(\d+)$`)

// PreviousMinorWithSettings returns the newest PHP minor version below minor
// that has settings someone chose, or "" when there is none.
func PreviousMinorWithSettings(minor string) (string, error) {
	target, err := ParseVersion(minor + ".0")
	if err != nil {
		return "", fmt.Errorf("invalid PHP minor version %q", minor)
	}
	entries, err := os.ReadDir(paths.PHPConfigDir())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var candidates []Version
	for _, e := range entries {
		if !e.IsDir() || !minorDirRE.MatchString(e.Name()) {
			continue
		}
		v, err := ParseVersion(e.Name() + ".0")
		if err != nil || !v.Less(target) {
			continue
		}
		candidates = append(candidates, v)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[j].Less(candidates[i]) })
	for _, v := range candidates {
		has, err := HasUserSettings(v.MinorString())
		if err != nil {
			return "", err
		}
		if has {
			return v.MinorString(), nil
		}
	}
	return "", nil
}

// CopyReport says what CopySettings carried over.
type CopyReport struct {
	// Settings are the php.ini keys set on the new version.
	Settings []string
	// TurnedOff are extension lines copied commented out.
	TurnedOff []string
	// ConfFiles are the conf.d files copied.
	ConfFiles []string
	// SkippedConfFiles already existed for the new version and were left alone.
	SkippedConfFiles []string
}

// CopySettings copies from's php.ini settings and conf.d files to to. A copied
// setting replaces to's value for the same key. routa's own Xdebug and pcov
// load lines are left out, since every install sets up its own. Any other
// extension= or zend_extension= line is copied commented out: a compiled
// extension only loads in the PHP minor version it was built for, so it has
// to be rebuilt first. conf.d files that to already has are left alone.
func CopySettings(from, to string) (CopyReport, error) {
	var report CopyReport
	if from == to {
		return report, fmt.Errorf("cannot copy PHP %s settings onto themselves", from)
	}
	settings, err := LoadINISettings(from)
	if err != nil {
		return report, err
	}
	lines, err := readINILines(to)
	if err != nil {
		return report, err
	}
	for _, s := range settings {
		if s.Key == ExtensionKey || s.Key == ZendExtensionKey {
			if managedExtensionLine(s) {
				continue
			}
			report.TurnedOff = append(report.TurnedOff, fmt.Sprintf("%s = %s", s.Key, s.Value))
			continue
		}
		if lines, err = setINILine(lines, s.Key, s.Value); err != nil {
			return report, err
		}
		report.Settings = append(report.Settings, s.Key)
	}
	if len(report.TurnedOff) > 0 {
		lines = append(lines, turnedOffNote(from, to)...)
		for _, line := range report.TurnedOff {
			lines = append(lines, "; "+line)
		}
	}
	if len(report.Settings) > 0 || len(report.TurnedOff) > 0 {
		if err := writeINILines(to, lines); err != nil {
			return report, err
		}
	}

	if ConfDir(from) == ConfDir(to) {
		return report, nil
	}
	confFiles, err := filepath.Glob(filepath.Join(ConfDir(from), "*.ini"))
	if err != nil {
		return report, err
	}
	sort.Strings(confFiles)
	for _, src := range confFiles {
		name := filepath.Base(src)
		dest := filepath.Join(ConfDir(to), name)
		if _, err := os.Stat(dest); err == nil {
			report.SkippedConfFiles = append(report.SkippedConfFiles, name)
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return report, err
		}
		content, turnedOff := turnOffExtensions(string(data), from, to)
		for _, line := range turnedOff {
			report.TurnedOff = append(report.TurnedOff, name+": "+line)
		}
		if err := os.MkdirAll(ConfDir(to), 0o755); err != nil {
			return report, err
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			return report, err
		}
		report.ConfFiles = append(report.ConfFiles, name)
	}
	return report, nil
}

func turnedOffNote(from, to string) []string {
	return []string{
		fmt.Sprintf("; Copied from PHP %s and turned off: an extension only loads in the PHP", from),
		fmt.Sprintf("; version it was built for. Point these at builds for PHP %s, then uncomment them.", to),
	}
}

// turnOffExtensions comments out every extension= and zend_extension= line in
// an ini file's content, and returns the lines it turned off.
func turnOffExtensions(content, from, to string) (string, []string) {
	lines := strings.Split(content, "\n")
	var turnedOff []string
	for i, line := range lines {
		setting, ok, err := parseINISetting(line)
		if err != nil || !ok {
			continue
		}
		if setting.Key == ExtensionKey || setting.Key == ZendExtensionKey {
			turnedOff = append(turnedOff, strings.TrimSpace(line))
			lines[i] = "; " + strings.TrimSpace(line)
		}
	}
	if len(turnedOff) == 0 {
		return content, nil
	}
	return strings.Join(append(turnedOffNote(from, to), lines...), "\n"), turnedOff
}
