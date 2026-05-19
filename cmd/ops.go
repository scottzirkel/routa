package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/caddyconf"
	"github.com/scottzirkel/routa/internal/cutover"
	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/php"
	"github.com/scottzirkel/routa/internal/services"
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
		if err := reloadCaddyWithCurrentRootConfig(); err != nil {
			return err
		}
		fmt.Println("reloaded")
		return nil
	},
}

// --- restart --------------------------------------------------------------

var restartCmd = &cobra.Command{
	Use:   "restart [unit|php [version]]",
	Short: "Restart routa services (no arg = dns + caddy + php-fpm + active optional services)",
	Args:  cobra.MaximumNArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		units, err := restartUnits(args)
		if err != nil {
			return err
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

func restartUnits(args []string) ([]string, error) {
	switch len(args) {
	case 0:
		units := []string{"routa-dns.service", "routa-caddy.service"}
		units = append(units, runningPHPUnits()...)
		units = append(units, activeOptionalServiceUnits()...)
		return units, nil
	case 1:
		if args[0] == "php" {
			units := runningPHPUnits()
			if len(units) == 0 {
				return nil, fmt.Errorf("no running routa PHP-FPM units found")
			}
			return units, nil
		}
		if strings.HasPrefix(args[0], "php@") {
			return []string{"routa-" + normalizeUnit(args[0])}, nil
		}
		return []string{normalizeUnit(args[0])}, nil
	case 2:
		if args[0] != "php" {
			return nil, fmt.Errorf("usage: routa restart [unit|php [version]]")
		}
		if err := requirePHP(args[1]); err != nil {
			return nil, err
		}
		return []string{phpUnitName(args[1])}, nil
	default:
		return nil, fmt.Errorf("usage: routa restart [unit|php [version]]")
	}
}

func phpUnitName(spec string) string {
	return fmt.Sprintf("routa-php@%s.service", spec)
}

func normalizeUnit(s string) string {
	if strings.HasSuffix(s, ".service") {
		return s
	}
	return s + ".service"
}

func prepareRestartUnit(unit string) error {
	if unit == "routa-caddy.service" {
		return writeCaddyfileForCurrentPhase()
	}
	spec, ok := phpSpecFromUnit(unit)
	if !ok {
		return nil
	}
	return php.WriteFPMConfig(spec)
}

func reloadCaddyWithCurrentRootConfig() error {
	if err := writeCaddyfileForCurrentPhase(); err != nil {
		return err
	}
	return site.ReloadCaddy()
}

func writeCaddyfileForCurrentPhase() error {
	if currentCaddyfileUsesStandardHTTPSPort() {
		return caddyconf.Write(caddyconf.PhaseTwo())
	}
	return caddyconf.Write(caddyconf.PhaseOne())
}

func currentCaddyfileUsesStandardHTTPSPort() bool {
	data, err := os.ReadFile(caddyconf.Path())
	if err != nil {
		return cutover.Detect() == cutover.PhaseTwo
	}
	return regexp.MustCompile(`(?m)^\s*https_port\s+443\s*$`).Match(data)
}

func phpSpecFromUnit(unit string) (string, bool) {
	if !strings.HasPrefix(unit, "routa-php@") || !strings.HasSuffix(unit, ".service") {
		return "", false
	}
	spec := strings.TrimSuffix(strings.TrimPrefix(unit, "routa-php@"), ".service")
	return spec, spec != ""
}

// --- status ---------------------------------------------------------------

var statusJSON bool

type statusSite struct {
	Name          string    `json:"name"`
	URL           string    `json:"url"`
	Source        string    `json:"source"`
	Kind          site.Kind `json:"kind"`
	PHP           string    `json:"php"`
	PHPMissing    bool      `json:"php_missing"`
	Secure        bool      `json:"secure"`
	Path          string    `json:"path"`
	Docroot       string    `json:"docroot"`
	Target        string    `json:"target"`
	AliasOf       string    `json:"alias_of"`
	EnvFile       string    `json:"env_file,omitempty"`
	Driver        string    `json:"driver,omitempty"`
	ServerEnvFile string    `json:"server_env_file,omitempty"`
}

var statusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"list"},
	Short:   "Show all configured sites and their resolved settings",
	RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := site.Load()
		if err != nil {
			return err
		}
		sites := s.Resolve()
		installed := installedPHPSet()
		if statusJSON {
			return renderStatusJSON(cmd, s, sites, installed)
		}
		if len(sites) == 0 {
			fmt.Println("no sites configured. Run `routa track <dir>` or `routa link [name]`.")
			return nil
		}

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

func renderStatusJSON(cmd *cobra.Command, s *site.State, sites []site.Resolved, installed map[string]bool) error {
	out := make([]statusSite, 0, len(sites))
	for _, r := range sites {
		out = append(out, statusSite{
			Name:          r.Name,
			URL:           resolvedSiteURL(r),
			Source:        resolvedSiteSource(s, r),
			Kind:          r.Kind,
			PHP:           r.PHP,
			PHPMissing:    r.PHP != "" && !installed[r.PHP],
			Secure:        r.Secure,
			Path:          r.Path,
			Docroot:       r.Docroot,
			Target:        r.Target,
			AliasOf:       r.AliasOf,
			EnvFile:       r.EnvFile,
			Driver:        r.Driver,
			ServerEnvFile: r.ServerEnvFile,
		})
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func resolvedSiteSource(s *site.State, r site.Resolved) string {
	if r.AliasOf != "" {
		return "alias"
	}
	for _, l := range s.Links {
		if l.Name == r.Name {
			return "linked"
		}
	}
	return "parked"
}

func resolvedSiteURL(r site.Resolved) string {
	scheme := "https"
	if !r.Secure {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s.test", scheme, r.Name)
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
	Detail string `json:"detail,omitempty"`
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
	Short: "End-to-end health check: services, ports, DNS, cutover state (--probe also HEADs each site)",
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
	optionalUnits := installedOptionalServiceUnits()
	units = append(units, optionalUnits...)
	optional := map[string]bool{}
	for _, unit := range optionalUnits {
		optional[unit] = true
	}
	for _, u := range units {
		service := doctorServiceStatus(u, systemctlUserIsActive)
		if optional[u] {
			service.Detail = optionalServiceDoctorDetail(u, service.OK, service.Status)
		}
		report.Services = append(report.Services, service)
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

type optionalServiceDoctorSpec struct {
	Kind    string
	Version string
	Binary  string
	Ports   []optionalServiceDoctorPort
}

type optionalServiceDoctorPort struct {
	Label string
	Port  string
}

var (
	doctorSharedLibraryRE = regexp.MustCompile(`error while loading shared libraries: ([^:]+):`)
	doctorVersionRE       = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)*`)
	phpFPMUnitSocketRE    = regexp.MustCompile(`^php-fpm-([0-9]+\.[0-9]+(?:\.[0-9]+)?)\.sock$`)
)

func optionalServiceDoctorDetail(unit string, active bool, status string) string {
	spec, details := optionalServiceDoctorSpecForUnit(unit)
	if strings.TrimSpace(status) == "failed" {
		details = append(details, "inspect logs with `journalctl --user -u "+unit+" -n 100 --no-pager`")
	}
	if spec.Binary != "" {
		if detail := optionalServiceBinaryDetail(spec); detail != "" {
			details = append(details, detail)
		}
	}
	if !active {
		for _, port := range spec.Ports {
			if port.Port == "" {
				continue
			}
			addr := "127.0.0.1:" + port.Port
			if portBound(addr) {
				details = append(details, port.Label+" port "+addr+" is already bound while "+unit+" is not active")
			}
		}
	}
	return strings.Join(details, "; ")
}

func optionalServiceDoctorSpecForUnit(unit string) (optionalServiceDoctorSpec, []string) {
	content, err := readRoutaUnit(unit)
	details := []string{}
	if err != nil {
		details = append(details, "could not read unit file: "+err.Error())
	}
	spec := optionalServiceDoctorSpec{
		Kind:   optionalServiceKind(unit),
		Binary: execStartBinary(content),
	}

	addPort := func(label, port string) {
		if err := services.ValidateTCPPort(label, port); err != nil {
			details = append(details, err.Error())
			return
		}
		spec.Ports = append(spec.Ports, optionalServiceDoctorPort{Label: label, Port: port})
	}
	addConfigPort := func(label, path, fallback string) {
		port, err := doctorConfigPort(path, fallback)
		if err != nil {
			details = append(details, "could not read "+label+" config port: "+err.Error())
		}
		addPort(label, port)
	}
	addUnitFlagPort := func(label, flag, fallback string) {
		addPort(label, routaUnitFlagPort(content, flag, fallback))
	}

	switch {
	case unit == services.RedisUnitName:
		port, err := services.RedisConfiguredPort()
		if err != nil {
			details = append(details, "could not read Redis config port: "+err.Error())
			port = services.RedisDefaultPort
		}
		addPort("Redis", port)
	case unit == services.MailpitUnitName:
		addUnitFlagPort("Mailpit web", "--listen", services.MailpitWebPort)
		addUnitFlagPort("Mailpit SMTP", "--smtp", services.MailpitSMTPPort)
	case strings.HasPrefix(unit, "routa-mariadb@"):
		version, instance := doctorDatabaseVersionInstance(unit, "routa-mariadb@")
		spec.Version = version
		addConfigPort("MariaDB", services.MariaDBConfigPathForInstance(version, instance), services.MariaDBDefaultPort)
	case strings.HasPrefix(unit, "routa-mysql@"):
		version, instance := doctorDatabaseVersionInstance(unit, "routa-mysql@")
		spec.Version = version
		addConfigPort("MySQL", services.MySQLConfigPathForInstance(version, instance), services.MySQLDefaultPort)
	case strings.HasPrefix(unit, "routa-postgres@"):
		version, instance := doctorDatabaseVersionInstance(unit, "routa-postgres@")
		spec.Version = version
		addConfigPort("Postgres", services.PostgresConfigPathForInstance(version, instance), services.PostgresDefaultPort)
	case strings.HasPrefix(unit, "routa-meilisearch@"):
		spec.Version = strings.TrimSuffix(strings.TrimPrefix(unit, "routa-meilisearch@"), ".service")
		addUnitFlagPort("Meilisearch", "--http-addr", services.MeilisearchDefaultPort)
	case strings.HasPrefix(unit, "routa-typesense@"):
		spec.Version = strings.TrimSuffix(strings.TrimPrefix(unit, "routa-typesense@"), ".service")
		addUnitFlagPort("Typesense", "--api-port", services.TypesenseDefaultPort)
	case strings.HasPrefix(unit, "routa-minio@"):
		spec.Version = strings.TrimSuffix(strings.TrimPrefix(unit, "routa-minio@"), ".service")
		addUnitFlagPort("MinIO", "--address", services.MinIODefaultPort)
		addUnitFlagPort("MinIO console", "--console-address", services.MinIODefaultConsolePort)
	}

	return spec, details
}

func optionalServiceKind(unit string) string {
	switch {
	case unit == services.RedisUnitName:
		return "redis"
	case unit == services.MailpitUnitName:
		return "mailpit"
	case strings.HasPrefix(unit, "routa-mariadb@"):
		return "mariadb"
	case strings.HasPrefix(unit, "routa-mysql@"):
		return "mysql"
	case strings.HasPrefix(unit, "routa-postgres@"):
		return "postgres"
	case strings.HasPrefix(unit, "routa-meilisearch@"):
		return "meilisearch"
	case strings.HasPrefix(unit, "routa-typesense@"):
		return "typesense"
	case strings.HasPrefix(unit, "routa-minio@"):
		return "minio"
	}
	return ""
}

func readRoutaUnit(unit string) (string, error) {
	for _, path := range []string{
		filepath.Join(paths.SystemdUserDir(), unit),
		filepath.Join(paths.SystemdUserDir(), "default.target.wants", unit),
	} {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", os.ErrNotExist
}

func execStartBinary(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "ExecStart="))
		if len(fields) == 0 {
			return ""
		}
		binary := strings.Trim(fields[0], `"'`)
		return strings.TrimLeft(binary, "-+!@")
	}
	return ""
}

func optionalServiceBinaryDetail(spec optionalServiceDoctorSpec) string {
	binary := spec.Binary
	if !filepath.IsAbs(binary) {
		resolved, err := exec.LookPath(binary)
		if err != nil {
			return "missing binary " + binary
		}
		binary = resolved
	} else if info, err := os.Stat(binary); err != nil {
		if os.IsNotExist(err) {
			return "missing binary " + binary
		}
		return "could not stat binary " + binary + ": " + err.Error()
	} else if info.IsDir() {
		return "binary path is a directory: " + binary
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	output := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return binary + " --version timed out"
	}
	if err != nil {
		if lib := missingRuntimeLibrary(output); lib != "" {
			return binary + " is missing runtime library " + lib
		}
		return ""
	}
	if detail := databaseRuntimeMismatchDetail(spec, binary, output); detail != "" {
		return detail
	}
	return ""
}

func missingRuntimeLibrary(output string) string {
	match := doctorSharedLibraryRE.FindStringSubmatch(output)
	if match == nil {
		return ""
	}
	return match[1]
}

func databaseRuntimeMismatchDetail(spec optionalServiceDoctorSpec, binary, output string) string {
	lower := strings.ToLower(output)
	switch spec.Kind {
	case "mysql":
		if strings.Contains(lower, "mariadb") {
			return "MySQL unit points at MariaDB binary " + binary
		}
	case "mariadb":
		if !strings.Contains(lower, "mariadb") {
			return "MariaDB unit points at non-MariaDB binary " + binary
		}
	case "postgres":
		// Version-only check below.
	default:
		return ""
	}
	if spec.Version != "" && !doctorOutputHasVersion(output, spec.Version) {
		versions := doctorVersionRE.FindAllString(output, -1)
		if len(versions) == 0 {
			return spec.Kind + " binary version does not match requested " + spec.Version
		}
		return spec.Kind + " binary version " + versions[0] + " does not match requested " + spec.Version
	}
	return ""
}

func doctorOutputHasVersion(output, requested string) bool {
	if !doctorVersionRE.MatchString(requested) {
		return strings.Contains(output, requested)
	}
	for _, version := range doctorVersionRE.FindAllString(output, -1) {
		if version == requested || strings.HasPrefix(version, requested+".") {
			return true
		}
	}
	return false
}

func doctorDatabaseVersionInstance(unit, prefix string) (string, string) {
	token := strings.TrimSuffix(strings.TrimPrefix(unit, prefix), ".service")
	version, instance, _ := strings.Cut(token, "_")
	return version, instance
}

func doctorConfigPort(path, fallback string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "port" {
				return strings.Trim(fields[1], `"'`), nil
			}
			continue
		}
		if strings.TrimSpace(key) == "port" {
			return strings.Trim(strings.TrimSpace(value), `"'`), nil
		}
	}
	return fallback, fmt.Errorf("port not found in %s", path)
}

func routaUnitFlagPort(content, flag, fallback string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ExecStart=") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "ExecStart="))
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] == flag {
				return portFromAddr(fields[i+1], fallback)
			}
		}
	}
	return fallback
}

func portFromAddr(addr, fallback string) string {
	addr = strings.Trim(addr, `"'`)
	if addr == "" {
		return fallback
	}
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	if i := strings.LastIndex(addr, ":"); i >= 0 && i < len(addr)-1 {
		return addr[i+1:]
	}
	return addr
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
		if service.Detail != "" {
			fmt.Fprintf(out, "     %s\n", service.Detail)
		}
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
		match := phpFPMUnitSocketRE.FindStringSubmatch(base)
		if match == nil {
			continue
		}
		spec := match[1]
		out = append(out, "routa-php@"+spec+".service")
	}
	return out
}

func installedOptionalServiceUnits() []string {
	var out []string
	for _, unit := range []string{services.RedisUnitName, services.MailpitUnitName} {
		if routaUnitExists(unit) {
			out = append(out, unit)
		}
	}
	for _, unit := range services.MariaDBUnitNamesForUninstall() {
		if routaUnitExists(unit) {
			out = append(out, unit)
		}
	}
	for _, unit := range services.MySQLUnitNamesForUninstall() {
		if routaUnitExists(unit) {
			out = append(out, unit)
		}
	}
	for _, unit := range services.PostgresUnitNamesForUninstall() {
		if routaUnitExists(unit) {
			out = append(out, unit)
		}
	}
	for _, unit := range services.MeilisearchUnitNamesForUninstall() {
		if routaUnitExists(unit) {
			out = append(out, unit)
		}
	}
	for _, unit := range services.TypesenseUnitNamesForUninstall() {
		if routaUnitExists(unit) {
			out = append(out, unit)
		}
	}
	for _, unit := range services.MinIOUnitNamesForUninstall() {
		if routaUnitExists(unit) {
			out = append(out, unit)
		}
	}
	return out
}

func activeOptionalServiceUnits() []string {
	var out []string
	for _, unit := range installedOptionalServiceUnits() {
		if systemd.IsActive(unit) {
			out = append(out, unit)
		}
	}
	return out
}

func routaUnitExists(unit string) bool {
	for _, path := range []string{
		filepath.Join(paths.SystemdUserDir(), unit),
		filepath.Join(paths.SystemdUserDir(), "default.target.wants", unit),
	} {
		if _, err := os.Lstat(path); err == nil {
			return true
		}
	}
	return false
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
		return "127.0.0.1:443  (standard HTTPS)"
	case alt:
		if !caddyActive {
			return "127.0.0.1:8443  (bound while routa-caddy is not active; another process may own routa's alt HTTPS)"
		}
		return "127.0.0.1:8443  (alternate HTTPS)"
	}
	if caddyActive {
		return "(not bound; routa-caddy is active, check Caddy logs)"
	}
	return "(not bound; routa-caddy is not active)"
}

func phaseLabel(p cutover.Phase) string {
	switch p {
	case cutover.PhaseOne:
		return "Installed — routa on alternate ports (run `routa cutover` to swap)"
	case cutover.PhaseTwo:
		return "Cut over — routa owns standard ports + DNS routing"
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
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "emit machine-readable JSON")
	tuiRenderCmd.Flags().IntVar(&tuiRenderWidth, "width", 120, "terminal width to render")
	rootCmd.AddCommand(reloadCmd, restartCmd, statusCmd, openCmd, logsCmd, doctorCmd, tuiCmd, tuiRenderCmd)
}
