package cmd

import (
	"encoding/json"
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

	"github.com/scottzirkel/routa/internal/cutover"
	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/php"
	"github.com/scottzirkel/routa/internal/site"
	"github.com/scottzirkel/routa/internal/systemd"
	"github.com/scottzirkel/routa/internal/tui"
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
		sites := s.Resolve()
		if err := php.RefreshFPMConfigsForSites(sites); err != nil {
			return err
		}
		if err := site.WriteFragments(sites); err != nil {
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
	Short: "Restart routa services (no arg = all: dns + caddy + all php-fpm)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		var units []string
		if len(args) == 1 {
			units = []string{normalizeUnit(args[0])}
		} else {
			units = []string{"routa-dns.service", "routa-caddy.service"}
			units = append(units, runningPHPUnits()...)
		}
		for _, u := range units {
			if err := prepareRestartUnit(u); err != nil {
				return fmt.Errorf("prepare %s: %w", u, err)
			}
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

func prepareRestartUnit(unit string) error {
	spec, ok := phpSpecFromUnit(unit)
	if !ok {
		return nil
	}
	return php.WriteFPMConfig(spec)
}

func phpSpecFromUnit(unit string) (string, bool) {
	if !strings.HasPrefix(unit, "routa-php@") || !strings.HasSuffix(unit, ".service") {
		return "", false
	}
	spec := strings.TrimSuffix(strings.TrimPrefix(unit, "routa-php@"), ".service")
	return spec, spec != ""
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
			fmt.Println("no sites configured. Run `routa track <dir>` or `routa link [name]`.")
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
				"\n! default PHP %q is not installed. Run: routa php install %s\n",
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
		return normalizeSiteName(args[0])
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return normalizeSiteName(filepath.Base(cwd))
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
	Short: "Tail logs for a site (Caddy access + PHP errors). No name = all routa-caddy/dns.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			c := exec.Command("journalctl", "--user", "-fu", "routa-caddy.service", "-u", "routa-dns.service", "-n", fmt.Sprintf("%d", logsLines))
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			c.Stdin = os.Stdin
			return c.Run()
		}
		name, err := normalizeSiteName(args[0])
		if err != nil {
			return err
		}
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

var (
	doctorProbe bool
	doctorJSON  bool
)

type doctorReport struct {
	Services   []doctorService     `json:"services"`
	Network    doctorNetwork       `json:"network"`
	DNS        doctorDNS           `json:"dns"`
	Cutover    doctorCutover       `json:"cutover"`
	SiteProbes []doctorProbeResult `json:"site_probes,omitempty"`
}

type doctorService struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Status string `json:"status"`
}

type doctorEndpoint struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type doctorNetwork struct {
	CaddyAdmin doctorEndpoint `json:"caddy_admin"`
	CaddyHTTPS doctorEndpoint `json:"caddy_https"`
	RoutaDNS   doctorEndpoint `json:"routa_dns"`
}

type doctorDNS struct {
	OK       bool   `json:"ok"`
	Name     string `json:"name"`
	Answer   string `json:"answer"`
	Expected string `json:"expected"`
	Detail   string `json:"detail,omitempty"`
}

type doctorCutover struct {
	Phase string `json:"phase"`
	Label string `json:"label"`
}

type doctorProbeResult struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	OK         bool   `json:"ok"`
	Status     string `json:"status"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "End-to-end health check: services, ports, DNS, cutover phase (--probe also HEADs each site)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		report, err := collectDoctorReport(doctorProbe)
		if err != nil {
			return err
		}
		if doctorJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(report)
		}
		return renderDoctorText(cmd, report)
	},
}

func collectDoctorReport(withProbes bool) (doctorReport, error) {
	report := doctorReport{}

	units := []string{"routa-dns.service", "routa-caddy.service"}
	units = append(units, runningPHPUnits()...)
	for _, u := range units {
		report.Services = append(report.Services, doctorServiceStatus(u, systemctlUserIsActive))
	}

	caddyActive := serviceActive(report.Services, "routa-caddy.service")
	caddyAdminOK := httpOK("http://127.0.0.1:2019/config/")
	std := portBound(":443") || portBound("127.0.0.1:443")
	alt := portBound("127.0.0.1:8443")
	routaDNSOK := portBound("127.0.0.1:1053")
	report.Network = doctorNetwork{
		CaddyAdmin: doctorEndpoint{Name: "caddy admin", OK: caddyAdminOK, Detail: "127.0.0.1:2019 (" + upDown(caddyAdminOK) + ")"},
		CaddyHTTPS: doctorEndpoint{Name: "caddy https", OK: std || alt, Detail: caddyAddrLabel(std, alt, caddyActive)},
		RoutaDNS:   doctorEndpoint{Name: "routa-dns", OK: routaDNSOK, Detail: "127.0.0.1:1053 (" + upDown(routaDNSOK) + ")"},
	}

	const dnsName = "doctor.routa.test"
	const expectedDNS = "127.0.0.1"
	dnsResult := queryRoutaDNS(dnsName)
	report.DNS = doctorDNS{
		OK:       dnsResult.Answer == expectedDNS,
		Name:     dnsName,
		Answer:   dnsResult.Answer,
		Expected: expectedDNS,
		Detail:   dnsResult.Detail,
	}

	phase := cutover.Detect()
	report.Cutover = doctorCutover{
		Phase: cutoverPhaseName(phase),
		Label: phaseLabel(phase),
	}

	if withProbes {
		s, err := site.Load()
		if err != nil {
			return report, err
		}
		for _, r := range s.Resolve() {
			url := siteURL(r.Name)
			probe := doctorProbeResult{Name: r.Name, URL: url}
			code, err := probeSite(r.Name)
			switch {
			case err != nil:
				probe.OK = false
				probe.Status = "error"
				probe.Error = err.Error()
			case code >= 200 && code < 400:
				probe.OK = true
				probe.Status = fmt.Sprintf("HTTP %d", code)
				probe.StatusCode = code
			default:
				probe.OK = false
				probe.Status = fmt.Sprintf("HTTP %d", code)
				probe.StatusCode = code
			}
			report.SiteProbes = append(report.SiteProbes, probe)
		}
	}

	return report, nil
}

func doctorServiceStatus(unit string, isActive func(string) ([]byte, error)) doctorService {
	out, err := isActive(unit)
	state := strings.TrimSpace(string(out))
	if state == "" && err != nil {
		state = err.Error()
	}
	if state == "" {
		state = "unknown"
	}
	return doctorService{
		Name:   unit,
		OK:     state == "active",
		Status: state,
	}
}

func serviceActive(services []doctorService, name string) bool {
	for _, service := range services {
		if service.Name == name {
			return service.OK
		}
	}
	return false
}

func systemctlUserIsActive(unit string) ([]byte, error) {
	return exec.Command("systemctl", "--user", "is-active", unit).CombinedOutput()
}

func renderDoctorText(cmd *cobra.Command, report doctorReport) error {
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "Services")
	for _, service := range report.Services {
		fmt.Fprintf(out, "  %s  %-30s %s\n", mark(service.OK), service.Name, service.Status)
	}

	fmt.Fprintln(out, "\nNetwork")
	fmt.Fprintf(out, "  %s  %-17s %s\n", mark(report.Network.CaddyAdmin.OK), report.Network.CaddyAdmin.Name, report.Network.CaddyAdmin.Detail)
	fmt.Fprintf(out, "  %s  %-17s %s\n", mark(report.Network.CaddyHTTPS.OK), report.Network.CaddyHTTPS.Name, report.Network.CaddyHTTPS.Detail)
	fmt.Fprintf(out, "  %s  %-17s %s\n", mark(report.Network.RoutaDNS.OK), report.Network.RoutaDNS.Name, report.Network.RoutaDNS.Detail)

	fmt.Fprintln(out, "\nDNS")
	fmt.Fprintf(out, "  %s  routa-dns answers %s -> %s (expected %s)\n", mark(report.DNS.OK), report.DNS.Name, report.DNS.Answer, report.DNS.Expected)
	if report.DNS.Detail != "" {
		fmt.Fprintf(out, "     %s\n", report.DNS.Detail)
	}

	fmt.Fprintln(out, "\nCutover")
	fmt.Fprintf(out, "  %s\n", report.Cutover.Label)

	if len(report.SiteProbes) > 0 {
		fmt.Fprintln(out, "\nSite probes")
		w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		for _, probe := range report.SiteProbes {
			name := strings.TrimSuffix(probe.Name, ".test") + ".test"
			switch {
			case probe.Error != "":
				fmt.Fprintf(w, "  ✗\t%s\t%s\n", name, probe.Error)
			case probe.OK:
				fmt.Fprintf(w, "  ✓\t%s\t%s\n", name, probe.Status)
			default:
				fmt.Fprintf(w, "  !\t%s\t%s\n", name, probe.Status)
			}
		}
		return w.Flush()
	}

	return nil
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
		out = append(out, "routa-php@"+spec+".service")
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

func caddyAddrLabel(std, alt bool, caddyActive bool) string {
	switch {
	case std && alt:
		if !caddyActive {
			return "127.0.0.1:443 + 127.0.0.1:8443  (bound while routa-caddy is not active; check for another owner)"
		}
		return "127.0.0.1:443 + 127.0.0.1:8443  (both; rollback may not have released alt)"
	case std:
		if !caddyActive {
			return "127.0.0.1:443  (bound while routa-caddy is not active; another process may own standard HTTPS)"
		}
		return "127.0.0.1:443  (Phase 2)"
	case alt:
		if !caddyActive {
			return "127.0.0.1:8443  (bound while routa-caddy is not active; another process may own routa's alt HTTPS)"
		}
		return "127.0.0.1:8443  (Phase 1)"
	}
	if caddyActive {
		return "(not bound; routa-caddy is active, check Caddy logs)"
	}
	return "(not bound; routa-caddy is not active)"
}

func phaseLabel(p cutover.Phase) string {
	switch p {
	case cutover.PhaseOne:
		return "Phase 1 — routa on alt ports (run `routa cutover` to swap)"
	case cutover.PhaseTwo:
		return "Phase 2 — routa owns standard ports + DNS routing"
	}
	return "Partial — system in mixed state; re-run `routa cutover` or `--rollback` to converge"
}

func cutoverPhaseName(p cutover.Phase) string {
	switch p {
	case cutover.PhaseOne:
		return "phase_one"
	case cutover.PhaseTwo:
		return "phase_two"
	}
	return "partial"
}

type dnsQueryResult struct {
	Answer string
	Detail string
}

func queryRoutaDNS(name string) dnsQueryResult {
	out, err := exec.Command(os.Args[0], "query", name).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return dnsQueryResult{Answer: "(error)", Detail: detail}
	}
	return parseRoutaDNSOutput(string(out))
}

func parseRoutaDNSOutput(out string) dnsQueryResult {
	for _, line := range strings.Split(string(out), "\n") {
		// `routa query` prints lines like: name.\t60\tIN\tA\t127.0.0.1
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "A" && i < len(fields)-1 {
				return dnsQueryResult{Answer: fields[i+1]}
			}
		}
	}
	return dnsQueryResult{Answer: "(no answer)", Detail: strings.TrimSpace(out)}
}

// --- tui (still stub) -----------------------------------------------------

var tuiCmd = &cobra.Command{
	Use:    "tui",
	Short:  "Interactive dashboard — site list, health, logs, filters, and inline actions",
	Hidden: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		return tui.Run()
	},
}

var tuiRenderWidth int

var tuiRenderCmd = &cobra.Command{
	Use:    "tui-render",
	Short:  "Render one TUI frame to stdout (debug — no event loop, no alt screen)",
	Hidden: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		fmt.Print(tui.DebugRender(tuiRenderWidth))
		fmt.Println()
		return nil
	},
}

func init() {
	logsCmd.Flags().IntVarP(&logsLines, "lines", "n", 50, "show this many trailing lines before following")
	logsCmd.Flags().BoolVar(&logsPHP, "no-php", false, "exclude php-fpm error log")
	doctorCmd.Flags().BoolVar(&doctorProbe, "probe", false, "also issue a HEAD against every site")
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "emit machine-readable JSON")
	tuiRenderCmd.Flags().IntVar(&tuiRenderWidth, "width", 120, "terminal width to render")
	rootCmd.AddCommand(reloadCmd, restartCmd, statusCmd, openCmd, logsCmd, doctorCmd, tuiCmd, tuiRenderCmd)
}
