package workspace

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSetupRunsCommandsInOrderInTheWorktree(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	var ran []int
	results, failed := RunSetup(dir, []string{"PATH=" + os.Getenv("PATH"), "GREETING=hi"}, []SetupCommand{
		{Command: "echo one > order; echo $GREETING", From: "site"},
		{Command: "echo two >> order", From: "flag"},
	}, &out, func(i int, _ SetupCommand) { ran = append(ran, i) })
	if failed != -1 {
		t.Fatalf("failed = %d, results = %#v", failed, results)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "order")); string(data) != "one\ntwo\n" {
		t.Fatalf("commands ran as %q, want in order inside the worktree", data)
	}
	if out.String() != "hi\n" {
		t.Fatalf("output = %q, want the env passed through", out.String())
	}
	if len(ran) != 2 || len(results) != 2 {
		t.Fatalf("progress %v, results %#v", ran, results)
	}
	for _, r := range results {
		if r.Status != "ok" || r.ExitCode == nil || *r.ExitCode != 0 {
			t.Fatalf("result %#v", r)
		}
	}
	if results[0].From != "site" || results[1].From != "flag" {
		t.Fatalf("from = %q, %q", results[0].From, results[1].From)
	}
}

func TestRunSetupStopsAtTheFirstFailure(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	var script strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&script, "echo line %d; ", i)
	}
	script.WriteString("echo oops >&2; exit 3")
	results, failed := RunSetup(dir, os.Environ(), []SetupCommand{
		{Command: "true"},
		{Command: script.String()},
		{Command: "touch never"},
	}, &out, nil)
	if failed != 1 {
		t.Fatalf("failed = %d, want 1", failed)
	}
	r := results[1]
	if r.Status != "failed" || r.ExitCode == nil || *r.ExitCode != 3 {
		t.Fatalf("failed result = %#v", r)
	}
	lines := strings.Split(r.Tail, "\n")
	if len(lines) != tailLines || lines[len(lines)-1] != "oops" || lines[0] != "line 12" {
		t.Fatalf("tail = %q, want the last %d lines with stderr", r.Tail, tailLines)
	}
	if results[2].Status != "skipped" || results[2].ExitCode != nil {
		t.Fatalf("after a failure: %#v", results[2])
	}
	if _, err := os.Stat(filepath.Join(dir, "never")); err == nil {
		t.Fatal("a command after the failure ran")
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	var tb tailBuffer
	for i := 0; i < 10000; i++ {
		fmt.Fprintf(&tb, "line %d\n", i)
	}
	if len(tb.buf) > 2*tailBytes {
		t.Fatalf("kept %d bytes", len(tb.buf))
	}
	if got := tb.lastLines(2); got != "line 9998\nline 9999" {
		t.Fatalf("lastLines = %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"npm":                 "npm",
		"5292":                "5292",
		"npm run build":       "'npm run build'",
		"echo 'hi'":           `'echo '\''hi'\'''`,
		"":                    "''",
		"a&&b":                "'a&&b'",
		"--site=shop":         "--site=shop",
		"composer test:setup": "'composer test:setup'",
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
