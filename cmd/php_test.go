package cmd

import (
	"errors"
	"testing"
)

func TestRunWithRoutaPHPPreservesChildExitCode(t *testing.T) {
	err := runWithRoutaPHP(&phpCLIContext{Bin: "/tmp/php"}, "sh", []string{"-c", "exit 7"})
	if err == nil {
		t.Fatal("expected child exit error")
	}

	var exit interface{ ExitCode() int }
	if !errors.As(err, &exit) {
		t.Fatalf("error does not expose ExitCode: %T %[1]v", err)
	}
	if exit.ExitCode() != 7 {
		t.Fatalf("exit code = %d, want 7", exit.ExitCode())
	}
}
