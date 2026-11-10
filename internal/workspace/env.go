// Package workspace creates and removes git worktrees of a routa site, with
// the untracked files, .env settings, and database a working copy needs.
package workspace

import (
	"regexp"
	"sort"
	"strings"
)

var envLineRE = regexp.MustCompile(`^(\s*(?:export\s+)?)([A-Za-z_][A-Za-z0-9_.]*)(\s*=\s*)(.*)$`)

// ParseEnv reads KEY=VALUE pairs the way PHP dotenv does for common files:
// it skips blank lines and comments, accepts `export`, strips single or
// double quotes, and drops an unquoted value's trailing " # comment".
// Variable interpolation is not expanded.
func ParseEnv(content string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		m := envLineRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		out[m[2]] = envValue(m[4])
	}
	return out
}

func envValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '"' {
		if end := strings.LastIndex(raw, `"`); end > 0 {
			return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(raw[1:end])
		}
	}
	if len(raw) >= 2 && raw[0] == '\'' {
		if end := strings.LastIndex(raw, "'"); end > 0 {
			return raw[1:end]
		}
	}
	if i := strings.Index(raw, " #"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}

// RewriteEnv renames the site host fromName.test, and every subdomain of it,
// to toName.test in each value, then sets the given keys. Comment lines and
// untouched lines keep their exact text. Keys that are not present yet are
// appended.
func RewriteEnv(content, fromName, toName string, set map[string]string) string {
	lines := strings.Split(content, "\n")
	done := map[string]bool{}
	for i, line := range lines {
		m := envLineRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		if value, ok := set[m[2]]; ok {
			lines[i] = m[1] + m[2] + "=" + quoteEnv(value)
			done[m[2]] = true
			continue
		}
		if fromName != "" && fromName != toName {
			lines[i] = m[1] + m[2] + m[3] + ReplaceHost(m[4], fromName, toName)
		}
	}
	var missing []string
	for key := range set {
		if !done[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		if n := len(lines); n > 0 && lines[n-1] == "" {
			lines = lines[:n-1]
		}
		for _, key := range missing {
			lines = append(lines, key+"="+quoteEnv(set[key]))
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// ReplaceHost renames host from.test and its subdomains to to.test inside s.
// It only matches whole hosts: my-from.test and from.testing stay as they are.
func ReplaceHost(s, from, to string) string {
	needle := from + ".test"
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(s[i:], needle)
		if j < 0 {
			b.WriteString(s[i:])
			return b.String()
		}
		j += i
		end := j + len(needle)
		if (j == 0 || !hostChar(s[j-1])) && (end == len(s) || !hostChar(s[end])) {
			b.WriteString(s[i:j])
			b.WriteString(to + ".test")
		} else {
			b.WriteString(s[i:end])
		}
		i = end
	}
}

func hostChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-'
}

func quoteEnv(value string) string {
	if value == "" || strings.ContainsAny(value, " \t#\"'$\\") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
	}
	return value
}
