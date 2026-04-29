package php

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

func Modules(spec string) ([]string, error) {
	bin := BinPath(spec)
	out, err := exec.Command(bin, "-m").Output()
	if err != nil {
		return nil, fmt.Errorf("php -m: %w", err)
	}

	seen := map[string]bool{}
	var modules []string
	for _, line := range strings.Split(string(bytes.TrimSpace(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		modules = append(modules, line)
	}
	sort.Slice(modules, func(i, j int) bool {
		a := strings.ToLower(modules[i])
		b := strings.ToLower(modules[j])
		if a == b {
			return modules[i] < modules[j]
		}
		return a < b
	})
	return modules, nil
}
