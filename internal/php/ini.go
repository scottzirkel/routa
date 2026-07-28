package php

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/scottzirkel/routa/internal/paths"
)

var iniKeyRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type INISetting struct {
	Key   string
	Value string
}

const (
	ExtensionKey              = "extension"
	ZendExtensionKey          = "zend_extension"
	XdebugModeKey             = "xdebug.mode"
	XdebugStartWithRequestKey = "xdebug.start_with_request"
	XdebugClientHostKey       = "xdebug.client_host"
	XdebugClientPortKey       = "xdebug.client_port"
	PcovEnabledKey            = "pcov.enabled"
)

// SAPI distinguishes the two php.ini files routa renders from one per-version
// settings file: the CLI file referenced by PHPRC, and the FPM file passed to
// php-fpm --php-ini.
type SAPI int

const (
	SAPICLI SAPI = iota
	SAPIFPM
)

type XdebugOptions struct {
	Mode             string
	StartWithRequest string
	ClientHost       string
	ClientPort       string
}

type XdebugStatus struct {
	Available        bool
	Enabled          bool
	Mode             string
	StartWithRequest string
	ClientHost       string
	ClientPort       string
	ZendExtension    string
}

type PcovOptions struct {
	// IncludeFPM pins pcov.enabled=1 for every SAPI instead of leaving the
	// CLI-only default in place.
	IncludeFPM bool
}

type PcovStatus struct {
	Available bool
	Enabled   bool
	Extension string
	// CLIEnabled and FPMEnabled are the rendered pcov.enabled values each SAPI
	// actually receives.
	CLIEnabled string
	FPMEnabled string
	// Pinned is true when pcov.enabled is set explicitly rather than left to
	// the per-SAPI default.
	Pinned bool
	// Loadable is false only when the selected CLI provably cannot dlopen a
	// shared extension, which makes every other field here moot.
	Loadable bool
}

func LaravelINISettings() []INISetting {
	return []INISetting{
		{Key: "memory_limit", Value: "512M"},
		{Key: "upload_max_filesize", Value: "128M"},
		{Key: "post_max_size", Value: "128M"},
		{Key: "max_input_vars", Value: "5000"},
		{Key: "realpath_cache_size", Value: "4096K"},
		{Key: "realpath_cache_ttl", Value: "600"},
		{Key: "opcache.enable", Value: "1"},
		{Key: "opcache.memory_consumption", Value: "256"},
		{Key: "opcache.interned_strings_buffer", Value: "16"},
		{Key: "opcache.max_accelerated_files", Value: "20000"},
		{Key: "opcache.validate_timestamps", Value: "1"},
		{Key: "opcache.revalidate_freq", Value: "0"},
		{Key: "opcache.save_comments", Value: "1"},
	}
}

func INIPath(spec string) string {
	return filepath.Join(paths.PHPConfigDir(), spec, "php.ini")
}

func EffectiveINISettings(spec string) ([]INISetting, error) {
	userSettings, err := LoadINISettings(spec)
	if err != nil {
		return nil, err
	}

	settings := LaravelINISettings()
	index := map[string]int{}
	for i, setting := range settings {
		index[setting.Key] = i
	}
	for _, setting := range userSettings {
		if i, ok := index[setting.Key]; ok {
			settings[i] = setting
			continue
		}
		index[setting.Key] = len(settings)
		settings = append(settings, setting)
	}
	return settings, nil
}

// RenderedINISettings is EffectiveINISettings plus the per-SAPI pcov default.
// Coverage collection earns its memory in the CLI, where test runners live, and
// never in FPM, where pcov would index every request's files for nothing — so
// the same loaded pcov.so is enabled for one and inert for the other. An
// explicit pcov.enabled in the per-version php.ini wins over both.
func RenderedINISettings(spec string, sapi SAPI) ([]INISetting, error) {
	settings, err := EffectiveINISettings(spec)
	if err != nil {
		return nil, err
	}
	loaded := false
	for _, setting := range settings {
		if strings.EqualFold(setting.Key, PcovEnabledKey) {
			return settings, nil
		}
		if setting.Key == ExtensionKey && PcovExtension.loadedBy(setting.Value) {
			loaded = true
		}
	}
	if !loaded {
		return settings, nil
	}
	value := "0"
	if sapi == SAPICLI {
		value = "1"
	}
	return append(settings, INISetting{Key: PcovEnabledKey, Value: value}), nil
}

func DefaultXdebugOptions() XdebugOptions {
	return XdebugOptions{
		Mode:             "debug,develop",
		StartWithRequest: "yes",
		ClientHost:       "127.0.0.1",
		ClientPort:       "9003",
	}
}

// ModeIncludesCoverage reports whether an xdebug.mode value asks Xdebug to
// collect code coverage — the one job routa hands to pcov instead.
func ModeIncludesCoverage(mode string) bool {
	for _, part := range strings.Split(mode, ",") {
		if strings.EqualFold(strings.TrimSpace(part), "coverage") {
			return true
		}
	}
	return false
}

func EnableXdebug(spec string, opts XdebugOptions) error {
	if opts.Mode == "" {
		opts.Mode = DefaultXdebugOptions().Mode
	}
	if opts.StartWithRequest == "" {
		opts.StartWithRequest = DefaultXdebugOptions().StartWithRequest
	}
	if opts.ClientHost == "" {
		opts.ClientHost = DefaultXdebugOptions().ClientHost
	}
	if opts.ClientPort == "" {
		opts.ClientPort = DefaultXdebugOptions().ClientPort
	}
	settings := []INISetting{}
	if XdebugExtension.Available(spec) {
		settings = append(settings, INISetting{Key: ZendExtensionKey, Value: XdebugExtension.Path(spec)})
	}
	settings = append(settings, []INISetting{
		{Key: XdebugModeKey, Value: opts.Mode},
		{Key: XdebugStartWithRequestKey, Value: opts.StartWithRequest},
		{Key: XdebugClientHostKey, Value: opts.ClientHost},
		{Key: XdebugClientPortKey, Value: opts.ClientPort},
	}...)
	for _, setting := range settings {
		if err := SetINISetting(spec, setting.Key, setting.Value); err != nil {
			return err
		}
	}
	return nil
}

func DisableXdebug(spec string) error {
	settings, err := LoadINISettings(spec)
	if err != nil {
		return err
	}
	for _, setting := range settings {
		if setting.Key == ZendExtensionKey && XdebugExtension.loadedBy(setting.Value) {
			if err := UnsetINISetting(spec, ZendExtensionKey); err != nil {
				return err
			}
			break
		}
	}
	for _, setting := range []INISetting{
		{Key: XdebugModeKey, Value: "off"},
		{Key: XdebugStartWithRequestKey, Value: "default"},
	} {
		if err := SetINISetting(spec, setting.Key, setting.Value); err != nil {
			return err
		}
	}
	return nil
}

// EnablePcov loads pcov.so. It deliberately leaves pcov.enabled unset unless
// IncludeFPM is requested, so RenderedINISettings can supply the per-SAPI
// default and users keep `routa php ini set <v> pcov.enabled` as an override.
func EnablePcov(spec string, opts PcovOptions) error {
	if PcovExtension.Available(spec) {
		if err := SetINISetting(spec, ExtensionKey, PcovExtension.Path(spec)); err != nil {
			return err
		}
	}
	if opts.IncludeFPM {
		return SetINISetting(spec, PcovEnabledKey, "1")
	}
	return UnsetINISetting(spec, PcovEnabledKey)
}

func DisablePcov(spec string) error {
	settings, err := LoadINISettings(spec)
	if err != nil {
		return err
	}
	for _, setting := range settings {
		if setting.Key == ExtensionKey && PcovExtension.loadedBy(setting.Value) {
			if err := UnsetINISetting(spec, ExtensionKey); err != nil {
				return err
			}
			break
		}
	}
	return UnsetINISetting(spec, PcovEnabledKey)
}

// EnsurePcovEnabledIfAvailable turns pcov on for a freshly installed build.
// Unlike Xdebug, which defaults to off because it hooks every request, pcov
// stays loaded and costs nothing until a coverage run starts it.
func EnsurePcovEnabledIfAvailable(spec string) (bool, error) {
	if !PcovExtension.Available(spec) {
		return false, nil
	}
	return true, EnablePcov(spec, PcovOptions{})
}

func PcovINIStatus(spec string, modules []string) (PcovStatus, error) {
	loaded := moduleLoaded(modules, "pcov")
	status := PcovStatus{
		Available: loaded || PcovExtension.Available(spec),
		Loadable:  loaded || !CLIRejectsSharedExtensions(spec),
	}
	settings, err := EffectiveINISettings(spec)
	if err != nil {
		return status, err
	}
	for _, setting := range settings {
		switch {
		case setting.Key == ExtensionKey && PcovExtension.loadedBy(setting.Value):
			status.Extension = setting.Value
		case strings.EqualFold(setting.Key, PcovEnabledKey):
			status.Pinned = true
		}
	}
	for _, sapi := range []SAPI{SAPICLI, SAPIFPM} {
		rendered, err := RenderedINISettings(spec, sapi)
		if err != nil {
			return status, err
		}
		value := ""
		for _, setting := range rendered {
			if strings.EqualFold(setting.Key, PcovEnabledKey) {
				value = setting.Value
			}
		}
		if sapi == SAPICLI {
			status.CLIEnabled = value
		} else {
			status.FPMEnabled = value
		}
	}
	status.Enabled = status.Available && status.Loadable && (loaded || status.Extension != "") && status.CLIEnabled != "0"
	return status, nil
}

func EnsureXdebugDisabledIfAvailable(spec string) (bool, error) {
	available := XdebugExtension.Available(spec)
	modules, err := Modules(spec)
	if err != nil {
		if !available {
			return false, err
		}
	} else if moduleLoaded(modules, "xdebug") {
		available = true
	}
	if !available {
		return false, nil
	}
	return true, DisableXdebug(spec)
}

func XdebugINIStatus(spec string, modules []string) (XdebugStatus, error) {
	loaded := moduleLoaded(modules, "xdebug")
	status := XdebugStatus{Available: loaded || XdebugExtension.Available(spec)}
	settings, err := EffectiveINISettings(spec)
	if err != nil {
		return status, err
	}
	values := map[string]string{}
	for _, setting := range settings {
		values[strings.ToLower(setting.Key)] = setting.Value
	}
	status.Mode = values[XdebugModeKey]
	status.StartWithRequest = values[XdebugStartWithRequestKey]
	status.ClientHost = values[XdebugClientHostKey]
	status.ClientPort = values[XdebugClientPortKey]
	status.ZendExtension = values[ZendExtensionKey]
	configured := XdebugExtension.loadedBy(status.ZendExtension)
	status.Enabled = status.Available && (loaded || configured) && !strings.EqualFold(status.Mode, "off")
	return status, nil
}

func moduleLoaded(modules []string, want string) bool {
	for _, module := range modules {
		if strings.EqualFold(module, want) {
			return true
		}
	}
	return false
}

// WriteCLIShim creates a directory whose only php is the CLI routa selected.
// Subprocesses resolve `php` from PATH — composer, vendor/bin shebangs, composer
// scripts — and putting the real bin/ on PATH hands them the stock php sitting
// next to the dynamic one. They would then read PHPRC, find a shared extension
// they cannot load, and fail on every invocation.
func WriteCLIShim(spec string) (string, error) {
	target := BinPath(spec)
	dir := filepath.Join(paths.RunDir(), "php-cli-"+spec, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	link := filepath.Join(dir, "php")
	if current, err := os.Readlink(link); err == nil && current == target {
		return dir, nil
	}
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Symlink(target, link); err != nil {
		return "", err
	}
	return dir, nil
}

func WriteCLIConfig(spec string) (string, error) {
	settings, err := RenderedINISettings(spec, SAPICLI)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(paths.RunDir(), "php-cli-"+spec)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, writeINISettingsFile(filepath.Join(dir, "php.ini"), settings)
}

func writeINISettingsFile(path string, settings []INISetting) error {
	var lines []string
	for _, setting := range settings {
		lines = append(lines, fmt.Sprintf("%s = %s", setting.Key, setting.Value))
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func LoadINISettings(spec string) ([]INISetting, error) {
	lines, err := readINILines(spec)
	if err != nil {
		return nil, err
	}
	var settings []INISetting
	for _, line := range lines {
		setting, ok, err := parseINISetting(line)
		if err != nil {
			return nil, err
		}
		if ok {
			settings = append(settings, setting)
		}
	}
	return settings, nil
}

func SetINISetting(spec, key, value string) error {
	if err := validateINISetting(key, value); err != nil {
		return err
	}
	lines, err := readINILines(spec)
	if err != nil {
		return err
	}

	next := fmt.Sprintf("%s = %s", key, value)
	updated := false
	var out []string
	for _, line := range lines {
		setting, ok, err := parseINISetting(line)
		if err != nil {
			return err
		}
		if ok && setting.Key == key {
			if !updated {
				out = append(out, next)
				updated = true
			}
			continue
		}
		out = append(out, line)
	}
	if !updated {
		out = append(out, next)
	}
	return writeINILines(spec, out)
}

func UnsetINISetting(spec, key string) error {
	if err := validateINIKey(key); err != nil {
		return err
	}
	lines, err := readINILines(spec)
	if err != nil {
		return err
	}

	var out []string
	for _, line := range lines {
		setting, ok, err := parseINISetting(line)
		if err != nil {
			return err
		}
		if ok && setting.Key == key {
			continue
		}
		out = append(out, line)
	}
	return writeINILines(spec, out)
}

func EnsureINIFile(spec string) error {
	path := INIPath(spec)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte("; routa PHP settings\n"), 0o644)
}

func validateINISetting(key, value string) error {
	if err := validateINIKey(key); err != nil {
		return err
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("ini value cannot contain newlines")
	}
	return nil
}

func validateINIKey(key string) error {
	if key == "" {
		return fmt.Errorf("ini key cannot be empty")
	}
	if !iniKeyRE.MatchString(key) {
		return fmt.Errorf("invalid ini key %q", key)
	}
	return nil
}

func readINILines(spec string) ([]string, error) {
	path := INIPath(spec)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func writeINILines(spec string, lines []string) error {
	path := INIPath(spec)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func parseINISetting(line string) (INISetting, bool, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
		return INISetting{}, false, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		return INISetting{}, false, fmt.Errorf("php.ini sections are not supported: %s", line)
	}

	key, value, ok := strings.Cut(trimmed, "=")
	if !ok {
		return INISetting{}, false, fmt.Errorf("invalid php.ini line: %s", line)
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if err := validateINISetting(key, value); err != nil {
		return INISetting{}, false, err
	}
	return INISetting{Key: key, Value: value}, true, nil
}
