package caddyconf

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWriteRendersRotatingLogs(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if err := Write(PhaseOne()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"http_port  8080",
		"https_port 8443",
		"output file " + strconv.Quote(filepath.Join(os.Getenv("XDG_STATE_HOME"), "hostr", "log", "caddy.log")) + " {",
		"roll_size 10MiB",
		"roll_keep 5",
		"roll_keep_for 720h",
		"import " + filepath.Join(os.Getenv("XDG_DATA_HOME"), "hostr", "sites") + "/*.caddy",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rendered Caddyfile missing %q:\n%s", want, content)
		}
	}
}
