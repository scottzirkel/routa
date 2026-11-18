package workspace

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os/exec"
	"strings"
	"time"
)

// SetupCommand is one setup step: a shell command and where it came from.
type SetupCommand struct {
	Command string
	From    string // "site" for ProjectFile, "flag" for --run
}

// CommandResult is what happened to one setup command.
type CommandResult struct {
	Command  string  `json:"command"`
	From     string  `json:"from"`
	Status   string  `json:"status"` // ok, failed, or skipped
	ExitCode *int    `json:"exit_code,omitempty"`
	Seconds  float64 `json:"seconds"`
	// Tail is the end of a failed command's output.
	Tail string `json:"output_tail,omitempty"`
}

// tailLines is how much of a failed command's output RunSetup keeps.
const tailLines = 20

// RunSetup runs commands in dir, in order, with sh -c and env, sending their
// output to out. Commands get no input, so one that asks a question fails
// rather than waiting for an answer nobody will give. It stops at the first
// command that fails and marks the rest skipped. failed is the index of the
// failed command, or -1.
func RunSetup(dir string, env []string, commands []SetupCommand, out io.Writer, progress func(i int, c SetupCommand)) (results []CommandResult, failed int) {
	failed = -1
	for i, c := range commands {
		r := CommandResult{Command: c.Command, From: c.From}
		if failed >= 0 {
			r.Status = "skipped"
			results = append(results, r)
			continue
		}
		if progress != nil {
			progress(i, c)
		}
		tail := &tailBuffer{}
		cmd := exec.Command("/bin/sh", "-c", c.Command)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stdout = io.MultiWriter(out, tail)
		cmd.Stderr = cmd.Stdout
		start := time.Now()
		err := cmd.Run()
		r.Seconds = math.Round(time.Since(start).Seconds()*10) / 10
		code := 0
		if err != nil {
			code = -1
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else {
				tail.Write([]byte(err.Error() + "\n"))
			}
		}
		r.ExitCode = &code
		r.Status = "ok"
		if code != 0 {
			r.Status = "failed"
			r.Tail = tail.lastLines(tailLines)
			failed = i
		}
		results = append(results, r)
	}
	return results, failed
}

// tailBuffer keeps the end of a command's output.
type tailBuffer struct {
	buf []byte
}

const tailBytes = 16 << 10

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*tailBytes {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-tailBytes:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) lastLines(n int) string {
	text := strings.TrimRight(string(bytes.ToValidUTF8(t.buf, nil)), "\n")
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ShellQuote quotes s for a POSIX shell, so a printed command can be pasted.
func ShellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
