package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/hostr/internal/diag"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Diagnose the host (resolver, valet status, required binaries)",
	Long:  `init runs read-only prerequisite checks. To provision, use "hostr install".`,
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func runInit(_ *cobra.Command, _ []string) error {
	checks := diag.Run()
	blocking := 0
	for _, c := range checks {
		mark := statusMark(c.Status)
		fmt.Printf("  %s  %-22s %s\n", mark, c.Name, c.Detail)
		if c.Hint != "" {
			fmt.Printf("       └─ %s\n", c.Hint)
		}
		if c.Status == diag.Fail {
			blocking++
		}
	}
	fmt.Println()
	if blocking > 0 {
		return fmt.Errorf("%d blocking issue(s) — resolve and re-run", blocking)
	}
	fmt.Println("Checks pass. Next: `hostr install` (alongside valet, alt ports). Then `hostr cutover` when ready.")
	return nil
}

func statusMark(s diag.Status) string {
	switch s {
	case diag.OK:
		return "OK  "
	case diag.Warn:
		return "WARN"
	case diag.Fail:
		return "FAIL"
	case diag.Absent:
		return "MISS"
	}
	return "?   "
}
