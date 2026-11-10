package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Scripts parse --json output, so an empty list must still be a JSON array
// rather than the human hint about installing something.
func TestListJSONPrintsEmptyArrays(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	cases := []struct {
		name string
		cmd  *cobra.Command
		flag *bool
	}{
		{"php list", phpListCmd, &phpListJSON},
		{"db list", dbListCmd, &dbListJSON},
		{"search list", searchListCmd, &searchListJSON},
		{"storage list", storageListCmd, &storageListJSON},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			*c.flag = true
			defer func() { *c.flag = false }()
			var out bytes.Buffer
			c.cmd.SetOut(&out)
			defer c.cmd.SetOut(nil)
			if err := c.cmd.RunE(c.cmd, nil); err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(out.String()); got != "[]" {
				t.Fatalf("output = %q, want []", got)
			}
		})
	}
}
