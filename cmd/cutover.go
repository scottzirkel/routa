package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/hostr/internal/cutover"
)

func perLinkDropinExists() bool {
	matches, _ := filepath.Glob("/etc/systemd/network/*.network.d/hostr.conf")
	return len(matches) > 0
}

var (
	cutoverPlanOnly bool
	cutoverRollback bool
)

var cutoverCmd = &cobra.Command{
	Use:   "cutover",
	Short: "Swap hostr onto :80/:443 and route *.test through it (replaces valet)",
	Long: `cutover atomically swaps from "valet on standard ports" to
"hostr on standard ports". The destructive system changes (resolv.conf swap,
systemd-resolved drop-in, port range, valet shutdown) are emitted as a single
sudo-able shell block — copy and run it. Then re-run "hostr cutover" and it
will detect the changed state and finish the user-side swap (Caddy → :80/:443).

  hostr cutover                # show plan + sudo block, or finalize if sudo done
  hostr cutover --plan         # only show the plan, never finalize
  hostr cutover --rollback     # show the reverse sudo block + revert Caddy
`,
	RunE: runCutover,
}

func init() {
	cutoverCmd.Flags().BoolVar(&cutoverPlanOnly, "plan", false, "only show the plan; do not finalize")
	cutoverCmd.Flags().BoolVar(&cutoverRollback, "rollback", false, "reverse a previous cutover")
	rootCmd.AddCommand(cutoverCmd)
}

func runCutover(_ *cobra.Command, _ []string) error {
	if cutoverRollback {
		return runRollback()
	}

	phase := cutover.Detect()
	switch phase {
	case cutover.PhaseTwo:
		fmt.Println("Already in Phase 2 (hostr on :80/:443). Nothing to do.")
		fmt.Println("To revert, run: hostr cutover --rollback")
		return nil
	case cutover.PhasePartial:
		fmt.Println("⚠  System is in a partial state (DNS swapped but Caddy still on :8443, or vice versa).")
		fmt.Println("   Continuing will attempt to complete the cutover.")
	}

	checks := cutover.Preflight()
	fmt.Println("Pre-flight:")
	failed := 0
	for _, c := range checks {
		mark := "✓"
		if !c.OK {
			mark = "✗"
			failed++
		}
		fmt.Printf("  %s %s — %s\n", mark, c.Name, c.Detail)
	}
	if failed > 0 {
		return fmt.Errorf("%d preflight check(s) failed", failed)
	}

	// Detect what's already done so we know whether the user still needs the sudo block.
	resolvDone := resolvIsSystemdStub()
	perLinkDone := perLinkDropinExists()
	sysctlDone := fileExists("/etc/sysctl.d/50-hostr.conf")
	sudoDone := resolvDone && perLinkDone && sysctlDone

	fmt.Println()
	if !sudoDone {
		fmt.Println("Step 1 — run this as root (single block):")
		fmt.Println(divider)
		fmt.Println(cutover.SudoBlock())
		fmt.Println(divider)
		fmt.Println()
		if cutoverPlanOnly {
			fmt.Println("(plan only — re-run `hostr cutover` after the sudo block to finalize)")
			return nil
		}
		fmt.Println("After the block runs, re-invoke: hostr cutover")
		return nil
	}

	if cutoverPlanOnly {
		fmt.Println("Sudo step already applied. Re-run without --plan to finalize the user side.")
		return nil
	}

	fmt.Println("Sudo step detected as applied. Finalizing user side:")
	fmt.Println("→ stop hostr-caddy, swap to Phase 2 Caddyfile (ports 80/443), restart")
	if err := cutover.SwapToPhaseTwo(); err != nil {
		return err
	}
	fmt.Println("✓ hostr-caddy bound :443")
	fmt.Println()
	fmt.Println("Cutover complete. Verify in a browser at https://<any>.test (no port).")
	fmt.Println("Rollback: hostr cutover --rollback")
	return nil
}

func runRollback() error {
	resolvSystemd := resolvIsSystemdStub()
	perLink := perLinkDropinExists()
	sysctl := fileExists("/etc/sysctl.d/50-hostr.conf")
	systemSideStillApplied := resolvSystemd || perLink || sysctl

	if systemSideStillApplied {
		fmt.Println("Step 1 — run this as root to revert the system changes:")
		fmt.Println(divider)
		fmt.Println(cutover.SudoRollbackBlock())
		fmt.Println(divider)
		fmt.Println()
		fmt.Println("After the block runs, re-invoke: hostr cutover --rollback")
		return nil
	}

	fmt.Println("System changes already reverted. Swapping hostr-caddy back to alt ports.")
	if err := cutover.SwapToPhaseOne(); err != nil {
		return err
	}
	fmt.Println("✓ hostr-caddy back on :8080/:8443. valet-dns + nginx are owning the standard ports again.")
	return nil
}

func resolvIsSystemdStub() bool {
	t, err := os.Readlink("/etc/resolv.conf")
	if err != nil {
		return false
	}
	return contains(t, "systemd/resolve")
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

const divider = "─────────────────────────────────────────────────────────────"
