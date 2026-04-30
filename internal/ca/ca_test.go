package ca

import (
	"errors"
	"strings"
	"testing"
)

func TestTrustCommandErrorIncludesActionCertAndHint(t *testing.T) {
	err := trustCommandError("store", "/tmp/root.crt", errors.New("exit status 1"))
	if err == nil {
		t.Fatal("expected error")
	}

	for _, want := range []string{
		"trust anchor --store failed",
		"/tmp/root.crt",
		"p11-kit",
		"system trust store",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}
