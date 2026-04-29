package php

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/scottzirkel/hostr/internal/paths"
)

var iniKeyRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type INISetting struct {
	Key   string
	Value string
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

func WriteCLIConfig(spec string) (string, error) {
	settings, err := EffectiveINISettings(spec)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(paths.RunDir(), "php-cli-"+spec)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var lines []string
	for _, setting := range settings {
		lines = append(lines, fmt.Sprintf("%s = %s", setting.Key, setting.Value))
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	return dir, os.WriteFile(filepath.Join(dir, "php.ini"), []byte(content), 0o644)
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
	return os.WriteFile(path, []byte("; hostr PHP settings\n"), 0o644)
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
