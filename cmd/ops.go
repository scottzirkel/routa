package cmd

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/hostr/internal/cutover"
	"github.com/scottzirkel/hostr/internal/paths"
	"github.com/scottzirkel/hostr/internal/php"
	"github.com/scottzirkel/hostr/internal/site"
	"github.com/scottzirkel/hostr/internal/systemd"
)

// --- reload ---------------------------------------------------------------

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Re-detect docroots/kinds for all sites, regenerate fragments, reload Caddy",
	RunE: func(_ *cobra.Command, _ []string) error {
		s, err := site.Load()
		if err != nil {
			return err
		}
		if err := site.WriteFragments(s.Resolve()); err != nil {
			return err
		}
		if err := site.ReloadCaddy(); err != nil {
			return err
		}
		fmt.Println("reloaded")
		return nil
	},
}

// --- restart --------------------------------------------------------------

var restartCmd = &cobra.Command{
	Use:   "restart [unit]",
	Short: "Restart hostr services (no arg = all: dns + caddy + all php-fpm)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		var units []string
		if len(args) == 1 {
			units = []string{normalizeUnit(args[0])}
		} else {
			units = []string{"hostr-dns.service", "hostr-caddy.service"}
			units = append(units, runningPHPUnits()...)
		}
		for _, u := range units {
			if err := systemd.RunSystemctl("--user", "restart", u); err != nil {
				return fmt.Errorf("restart %s: %w", u, err)
			}
			fmt.Printf("✓ restarted %s\n", u)
		}
		return nil
	},
}

func normalizeUnit(s string) string {
	if strings.HasSuffix(s, ".service") {
		return s
	}
	return s + ".service"
}

// --- status ---------------------------------------------------------------

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show all configured sites and their resolved settings",
	RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := site.Load()
		if err != nil {
			return err
		}
		sites := s.Resolve()
		if len(sites) == 0 {
			fmt.Println("no sites configured. Run `hostr park <dir>` or `hostr link [name]`.")
			return nil
		}
		installed := installedPHPSet()

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tKIND\tPHP\tSECURE\tDOCROOT")
		for _, r := range sites {
			ver := r.PHP
			if ver == "" {
				ver = "-"
			} else if !installed[ver] {
				ver += " (missing!)"
			}
			sec := "yes"
			if !r.Secure {
				sec = "no"
			}
			fmt.Fprintf(w, "%s.test\t%s\t%s\t%s\t%s\n", r.Name, r.Kind, ver, sec, r.Docroot)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if s.DefaultPHP != "" && !installed[s.DefaultPHP] {
			fmt.Fprintf(cmd.OutOrStderr(),
				"\n! default PHP %q is not installed. Run: hostr php install %s\n",
				s.DefaultPHP, s.DefaultPHP)
		}
		return nil
	},
}

func installedPHPSet() map[string]bool {
	out := map[string]bool{}
	if v, _ := php.InstalledVersions(); v != nil {
		for _, i := range v {
			out[i.Version] = true
		}
	}
	if links, _ := php.Symlinks(); links != nil {
		for k := range links {
			out[k] = true
		}
	}
	return out
}

// --- open -----------------------------------------------------------------

var openCmd = &cobra.Command{
	Use:   "open [name]",
	Short: "Open a site in the default browser (defaults to current dir's site)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name, err := siteNameFromArgsOrCwd(args)
		if err != nil {
			return err
		}
		url := siteURL(name)
		fmt.Println(url)
		return exec.Command("xdg-open", url).Start()
	},
}

func siteNameFromArgsOrCwd(args []string) (string, error) {
	if len(args) == 1 {
		return strings.TrimSuffix(args[0], ".test"), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Base(cwd), nil
}

func siteURL(name string) string {
	if portBound("127.0.0.1:443") || portBound(":443") {
		return fmt.Sprintf("https://%s.test", name)
	}
	return fmt.Sprintf("https://%s.test:8443", name)
}

// --- logs -----------------------------------------------------------------

var (
	logsLines int
	logsPHP   bool
)

var logsCmd = &cobra.Command{
	Use:   "logs [name]",
	Short: "Tail logs for a site (Caddy access + PHP errors). No name = all hostr-caddy/dns.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			c := exec.Command("journalctl", "--user", "-fu", "hostr-caddy.service", "-u", "hostr-dns.service", "-n", fmt.Sprintf("%d", logsLines))
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			c.Stdin = os.Stdin
			return c.Run()
		}
		name := strings.TrimSuffix(args[0], ".test")
		s, err := site.Load()
		if err != nil {
			return err
		}
		var match *site.Resolved
		for _, r := range s.Resolve() {
			if r.Name == name {
				rr := r
				match = &rr
				break
			}
		}
		if match == nil {
			return fmt.Errorf("no site named %s", name)
		}
		files := []string{filepath.Join(paths.LogDir(), name+".log")}
		if !logsPHP && match.Kind == site.KindPHP && match.PHP != "" {
			files = append(files, filepath.Join(paths.LogDir(), "php-fpm-"+match.PHP+".log"))
		}
		args2 := []string{"-n", fmt.Sprintf("%d", logsLines), "-F"}
		args2 = append(args2, files...)
		c := exec.Command("tail", args2...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.Stdin = os.Stdin
		return c.Run()
	},
}

// --- doctor ---------------------------------------------------------------

var doctorProbe bool

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "End-to-end health check: services, ports, DNS, cutover phase (--probe also GETs each site)",
	RunE: func(_ *cobra.Command, _ []string) error {
		// Services
		fmt.Println("Services")
		units := []string{"hostr-dns.service", "hostr-caddy.service"}
		units = append(units, runningPHPUnits()...)
		for _, u := range units {
			out, _ := exec.Command("systemctl", "--user", "is-active", u).Output()
			state := strings.TrimSpace(string(out))
			fmt.Printf("  %s  %-30s %s\n", mark(state == "active"), u, state)
		}

		// Network
		fmt.Println("\nNetwork")
		fmt.Printf("  %s  caddy admin       127.0.0.1:2019  (%s)\n",
			mark(httpOK("http://127.0.0.1:2019/config/")), upDown(httpOK("http://127.0.0.1:2019/config/")))
		std := portBound(":443") || portBound("127.0.0.1:443")
		alt := portBound("127.0.0.1:8443")
		fmt.Printf("  %s  caddy https       %s\n", mark(std || alt), caddyAddrLabel(std, alt))
		fmt.Printf("  %s  hostr-dns         127.0.0.1:1053  (%s)\n",
			mark(portBound("127.0.0.1:1053")), upDown(portBound("127.0.0.1:1053")))

		// DNS sanity
		fmt.Println("\nDNS")
		ans := queryHostrDNS("doctor.hostr.test")
		fmt.Printf("  %s  hostr-dns answers *.test → %s\n", mark(ans == "127.0.0.1"), ans)

		// Cutover phase
		fmt.Println("\nCutover")
		fmt.Printf("  %s\n", phaseLabel(cutover.Detect()))

		// Optional per-site probe
		if doctorProbe {
			fmt.Println("\nSite probes")
			s, err := site.Load()
			if err != nil {
				return err
			}
			sites := s.Resolve()
			w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			for _, r := range sites {
				code, err := probeSite(r.Name)
				switch {
				case err != nil:
					fmt.Fprintf(w, "  ✗\t%s.test\t%s\n", r.Name, err)
				case code >= 200 && code < 400:
					fmt.Fprintf(w, "  ✓\t%s.test\tHTTP %d\n", r.Name, code)
				default:
					fmt.Fprintf(w, "  !\t%s.test\tHTTP %d\n", r.Name, code)
				}
			}
			w.Flush()
		}
		return nil
	},
}

func probeSite(name string) (int, error) {
	url := siteURL(name)
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Head(url)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// --- helpers --------------------------------------------------------------

func runningPHPUnits() []string {
	socks, _ := filepath.Glob(filepath.Join(paths.RunDir(), "php-fpm-*.sock"))
	var out []string
	for _, s := range socks {
		base := filepath.Base(s)
		spec := strings.TrimSuffix(strings.TrimPrefix(base, "php-fpm-"), ".sock")
		out = append(out, "hostr-php@"+spec+".service")
	}
	return out
}

func portBound(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func httpOK(url string) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func upDown(ok bool) string {
	if ok {
		return "up"
	}
	return "down"
}

func caddyAddrLabel(std, alt bool) string {
	switch {
	case std && alt:
		return "127.0.0.1:443 + 127.0.0.1:8443  (both — rollback didn't release alt?)"
	case std:
		return "127.0.0.1:443  (Phase 2)"
	case alt:
		return "127.0.0.1:8443  (Phase 1)"
	}
	return "(not bound!)"
}

func phaseLabel(p cutover.Phase) string {
	switch p {
	case cutover.PhaseOne:
		return "Phase 1 — alongside valet on alt ports (run `hostr cutover` to swap)"
	case cutover.PhaseTwo:
		return "Phase 2 — hostr owns standard ports + DNS routing"
	}
	return "Partial — system in mixed state; re-run `hostr cutover` or `--rollback` to converge"
}

func queryHostrDNS(name string) string {
	out, err := exec.Command(os.Args[0], "query", name).CombinedOutput()
	if err != nil {
		return "(error)"
	}
	for _, line := range strings.Split(string(out), "\n") {
		// `hostr query` prints lines like: name.\t60\tIN\tA\t127.0.0.1
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "A" && i < len(fields)-1 {
				return fields[i+1]
			}
		}
	}
	return "(no answer)"
}

// --- tui (still stub) -----------------------------------------------------

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Interactive dashboard (not yet implemented)",
	RunE: func(_ *cobra.Command, _ []string) error {
		return fmt.Errorf("not yet implemented")
	},
}

func init() {
	logsCmd.Flags().IntVarP(&logsLines, "lines", "n", 50, "show this many trailing lines before following")
	logsCmd.Flags().BoolVar(&logsPHP, "no-php", false, "exclude php-fpm error log")
	doctorCmd.Flags().BoolVar(&doctorProbe, "probe", false, "also issue a HEAD against every site")
	rootCmd.AddCommand(reloadCmd, restartCmd, statusCmd, openCmd, logsCmd, doctorCmd, tuiCmd)
}
