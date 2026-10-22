package services

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/systemd"
)

const (
	MySQLBinaryName  = "mysqld"
	MySQLDefaultPort = "3306"
)

const mysqlUnitTmpl = `[Unit]
Description=routa MySQL {{.Version}}
After=network.target
{{.StartLimit}}

[Service]
Type=simple
{{if .RepairExec}}{{.RepairExec}}
{{end}}ExecStart={{.Binary}} --defaults-file={{.ConfigPath}}
Restart=on-failure
RestartSec=2
TimeoutStopSec=30

[Install]
WantedBy=default.target
`

const mysqlConfigTmpl = `[mysqld]
bind-address=127.0.0.1
port={{.Port}}
datadir={{.DataDir}}
socket={{.SocketPath}}
pid-file={{.PIDFile}}
log-error={{.LogPath}}
skip-networking=0

[client]
socket={{.SocketPath}}
`

var (
	sharedLibraryErrorRE = regexp.MustCompile(`error while loading shared libraries: ([^:]+):`)
	lddMissingLibraryRE  = regexp.MustCompile(`(?m)^\s*(\S+)\s+=>\s+not found$`)
)

type mysqlUnitData struct {
	Version    string
	Binary     string
	ConfigPath string
	StartLimit string
	RepairExec string
}

type mysqlConfigData struct {
	DataDir    string
	SocketPath string
	PIDFile    string
	LogPath    string
	Port       string
}

type MySQLInstance struct {
	Version  string
	Instance string
	Unit     string
	DataDir  string
	Port     string
}

type MySQLCredentials struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

func MySQL(version string) Definition {
	return MySQLWithPort(version, MySQLDefaultPort)
}

func MySQLWithPort(version, port string) Definition {
	return MySQLInstanceWithPort(version, "", port)
}

func MySQLInstanceWithPort(version, instance, port string) Definition {
	return Definition{
		Name:        "mysql",
		UnitName:    MySQLUnitNameForInstance(version, instance),
		BinaryName:  MySQLBinaryName,
		ConfigPath:  MySQLConfigPathForInstance(version, instance),
		DataDir:     MySQLDataDirForInstance(version, instance),
		RenderUnit:  func(binary string) (string, error) { return RenderMySQLUnitForInstance(version, instance, binary) },
		WriteConfig: func() error { return WriteMySQLConfigForInstanceWithPort(version, instance, port) },
	}
}

func ValidateMySQLVersion(version string) error {
	return validateVersionLabel("MySQL", version)
}

func ValidateMySQLInstance(instance string) error {
	return validateInstanceLabel("MySQL", instance)
}

func MySQLUnitName(version string) string {
	return MySQLUnitNameForInstance(version, "")
}

func MySQLUnitNameForInstance(version, instance string) string {
	return "routa-mysql@" + databaseInstanceToken(version, instance) + ".service"
}

func MySQLDataDir(version string) string {
	return MySQLDataDirForInstance(version, "")
}

func MySQLDataDirForInstance(version, instance string) string {
	return databaseInstanceDir(paths.DataDir(), "mysql", version, instance)
}

func MySQLConfigPath(version string) string {
	return MySQLConfigPathForInstance(version, "")
}

func MySQLConfigPathForInstance(version, instance string) string {
	return filepath.Join(databaseInstanceDir(paths.ConfigDir(), "mysql", version, instance), "my.cnf")
}

func MySQLCredentialsPathForInstance(version, instance string) string {
	return filepath.Join(databaseInstanceDir(paths.ConfigDir(), "mysql", version, instance), "credentials.json")
}

func MySQLSocketPath(version string) string {
	return MySQLSocketPathForInstance(version, "")
}

func MySQLSocketPathForInstance(version, instance string) string {
	return filepath.Join(paths.RunDir(), "mysql-"+databaseInstanceToken(version, instance)+".sock")
}

func MySQLPIDFile(version string) string {
	return MySQLPIDFileForInstance(version, "")
}

func MySQLPIDFileForInstance(version, instance string) string {
	return filepath.Join(paths.RunDir(), "mysql-"+databaseInstanceToken(version, instance)+".pid")
}

func MySQLLogPath(version string) string {
	return MySQLLogPathForInstance(version, "")
}

func MySQLLogPathForInstance(version, instance string) string {
	return filepath.Join(paths.LogDir(), "mysql-"+databaseInstanceToken(version, instance)+".log")
}

func RenderMySQLUnit(version, binary string) (string, error) {
	return RenderMySQLUnitForInstance(version, "", binary)
}

func RenderMySQLUnitForInstance(version, instance, binary string) (string, error) {
	if err := ValidateMySQLVersion(version); err != nil {
		return "", err
	}
	if err := ValidateMySQLInstance(instance); err != nil {
		return "", err
	}
	if binary == "" {
		return "", fmt.Errorf("mysql binary path cannot be empty")
	}
	return render("mysql-unit", mysqlUnitTmpl, mysqlUnitData{
		Version:    version,
		Binary:     binary,
		ConfigPath: MySQLConfigPathForInstance(version, instance),
		StartLimit: unitStartLimit,
		RepairExec: databaseRepairExec("mysql", version, instance),
	})
}

func RenderMySQLConfig(version string) (string, error) {
	return RenderMySQLConfigWithPort(version, MySQLDefaultPort)
}

func RenderMySQLConfigWithPort(version, port string) (string, error) {
	return RenderMySQLConfigForInstanceWithPort(version, "", port)
}

func RenderMySQLConfigForInstanceWithPort(version, instance, port string) (string, error) {
	if err := ValidateMySQLVersion(version); err != nil {
		return "", err
	}
	if err := ValidateMySQLInstance(instance); err != nil {
		return "", err
	}
	if err := ValidateTCPPort("MySQL", port); err != nil {
		return "", err
	}
	return render("mysql-config", mysqlConfigTmpl, mysqlConfigData{
		DataDir:    MySQLDataDirForInstance(version, instance),
		SocketPath: MySQLSocketPathForInstance(version, instance),
		PIDFile:    MySQLPIDFileForInstance(version, instance),
		LogPath:    MySQLLogPathForInstance(version, instance),
		Port:       port,
	})
}

func WriteMySQLConfig(version string) error {
	return WriteMySQLConfigWithPort(version, MySQLDefaultPort)
}

func WriteMySQLConfigWithPort(version, port string) error {
	return WriteMySQLConfigForInstanceWithPort(version, "", port)
}

func WriteMySQLConfigForInstanceWithPort(version, instance, port string) error {
	if err := ValidateMySQLVersion(version); err != nil {
		return err
	}
	if err := ValidateMySQLInstance(instance); err != nil {
		return err
	}
	if err := ValidateTCPPort("MySQL", port); err != nil {
		return err
	}
	for _, dir := range []string{
		MySQLDataDirForInstance(version, instance),
		filepath.Dir(MySQLConfigPathForInstance(version, instance)),
		paths.RunDir(),
		paths.LogDir(),
	} {
		if err := ensureDir(dir); err != nil {
			return err
		}
	}
	content, err := RenderMySQLConfigForInstanceWithPort(version, instance, port)
	if err != nil {
		return err
	}
	return os.WriteFile(MySQLConfigPathForInstance(version, instance), []byte(content), 0o644)
}

func WriteMySQLCredentialsForInstance(version, instance string, creds MySQLCredentials) error {
	if err := ValidateMySQLVersion(version); err != nil {
		return err
	}
	if err := ValidateMySQLInstance(instance); err != nil {
		return err
	}
	if err := ValidateMySQLCredentials(creds); err != nil {
		return err
	}
	if err := ensureDir(filepath.Dir(MySQLCredentialsPathForInstance(version, instance))); err != nil {
		return err
	}
	content, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return os.WriteFile(MySQLCredentialsPathForInstance(version, instance), content, 0o600)
}

func ReadMySQLCredentialsForInstance(version, instance string) (MySQLCredentials, bool, error) {
	data, err := os.ReadFile(MySQLCredentialsPathForInstance(version, instance))
	if err != nil {
		if os.IsNotExist(err) {
			return MySQLCredentials{}, false, nil
		}
		return MySQLCredentials{}, false, err
	}
	var creds MySQLCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return MySQLCredentials{}, false, err
	}
	if err := ValidateMySQLCredentials(creds); err != nil {
		return MySQLCredentials{}, false, err
	}
	return creds, true, nil
}

func ValidateMySQLCredentials(creds MySQLCredentials) error {
	if strings.TrimSpace(creds.User) == "" {
		return fmt.Errorf("mysql user cannot be empty")
	}
	if creds.User != strings.TrimSpace(creds.User) || strings.ContainsAny(creds.User, "'\"`\\\x00\n\r\t") {
		return fmt.Errorf("invalid mysql user %q", creds.User)
	}
	if strings.EqualFold(creds.User, "root") {
		return fmt.Errorf("routa does not manage the MySQL root account; choose an application user")
	}
	if strings.ContainsRune(creds.Password, '\x00') {
		return fmt.Errorf("mysql password cannot contain NUL")
	}
	return nil
}

func EnsureMySQL(version string) error {
	return EnsureMySQLWithPort(version, MySQLDefaultPort)
}

func EnsureMySQLWithPort(version, port string) error {
	return EnsureMySQLInstanceWithPort(version, "", port)
}

func EnsureMySQLInstanceWithPort(version, instance, port string) error {
	if err := ValidateMySQLVersion(version); err != nil {
		return err
	}
	if err := ValidateMySQLInstance(instance); err != nil {
		return err
	}
	if err := ValidateTCPPort("MySQL", port); err != nil {
		return err
	}
	bin, err := FindMySQLBinary(version)
	if err != nil {
		return err
	}
	if err := WriteFiles(MySQLInstanceWithPort(version, instance, port), bin); err != nil {
		return err
	}
	if err := initializeMySQLDataDir(version, instance, bin); err != nil {
		return err
	}
	return systemd.DaemonReload()
}

func InitializeMySQLDataDir(version string) error {
	bin, err := FindMySQLBinary(version)
	if err != nil {
		return err
	}
	return initializeMySQLDataDir(version, "", bin)
}

func initializeMySQLDataDir(version, instance, serverBin string) error {
	initialized, err := MySQLDataDirInitializedForInstance(version, instance)
	if err != nil {
		return err
	}
	if initialized {
		return nil
	}
	cmd := exec.Command(serverBin,
		"--defaults-file="+MySQLConfigPathForInstance(version, instance),
		"--initialize-insecure",
		"--datadir="+MySQLDataDirForInstance(version, instance),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func FindMySQLBinary(version string) (string, error) {
	return findMySQLBinaryWith(version, exec.LookPath, commandOutput)
}

func FindMySQLClientBinary(version string) (string, error) {
	return findMySQLClientBinaryWith(version, exec.LookPath, commandOutput)
}

func ApplyMySQLCredentialsForInstance(version, instance string, creds MySQLCredentials) error {
	if err := ValidateMySQLVersion(version); err != nil {
		return err
	}
	if err := ValidateMySQLInstance(instance); err != nil {
		return err
	}
	if err := ValidateMySQLCredentials(creds); err != nil {
		return err
	}
	port, err := MySQLConfiguredPortForInstance(version, instance)
	if err != nil {
		return err
	}
	client, err := FindMySQLClientBinary(version)
	if err != nil {
		return err
	}
	sql := mysqlCredentialsSQL(creds)
	var lastOut []byte
	var lastErr error
	for attempt := 0; attempt < 50; attempt++ {
		cmd := exec.Command(client,
			"--protocol=tcp",
			"-h127.0.0.1",
			"-P"+port,
			"-uroot",
			"-e",
			sql,
		)
		lastOut, lastErr = cmd.CombinedOutput()
		if lastErr == nil {
			return nil
		}
		if !mysqlConnectionNotReady(string(lastOut)) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("apply mysql credentials: %w\n%s", lastErr, strings.TrimSpace(string(lastOut)))
}

func MySQLConfiguredPortForInstance(version, instance string) (string, error) {
	return requiredDatabaseConfiguredPort(MySQLConfigPathForInstance(version, instance), "MySQL")
}

func mysqlCredentialsSQL(creds MySQLCredentials) string {
	user := sqlStringLiteral(creds.User)
	password := sqlStringLiteral(creds.Password)
	var stmts []string
	for _, host := range []string{"127.0.0.1", "localhost"} {
		hostLit := sqlStringLiteral(host)
		stmts = append(stmts,
			fmt.Sprintf("CREATE USER IF NOT EXISTS %s@%s IDENTIFIED BY %s", user, hostLit, password),
			fmt.Sprintf("ALTER USER %s@%s IDENTIFIED BY %s", user, hostLit, password),
			fmt.Sprintf("GRANT ALL PRIVILEGES ON *.* TO %s@%s", user, hostLit),
		)
	}
	stmts = append(stmts, "FLUSH PRIVILEGES")
	return strings.Join(stmts, "; ") + ";"
}

func sqlStringLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func mysqlConnectionNotReady(output string) bool {
	return strings.Contains(output, "Can't connect") || strings.Contains(output, "ERROR 2002")
}

func findMySQLBinaryWith(version string, lookPath lookPathFunc, output commandOutputFunc) (string, error) {
	if err := ValidateMySQLVersion(version); err != nil {
		return "", err
	}

	var mismatches []string
	var managedMissing []string
	var managedBinary string
	seen := map[string]bool{}
	for _, candidate := range mysqlBinaryCandidates(version) {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true

		path, ok := resolveCommandCandidate(candidate, lookPath)
		if !ok {
			continue
		}
		out, err := output(path, "--version")
		if err != nil {
			if missing := missingSharedLibrary(string(out)); missing != "" {
				mismatches = append(mismatches, fmt.Sprintf("%s (missing shared library %s)", path, missing))
				if isManagedMySQLBinary(path) {
					managedBinary = path
					for _, lib := range missingSharedLibraries(path, missing, output) {
						if !containsString(managedMissing, lib) {
							managedMissing = append(managedMissing, lib)
						}
					}
				}
			} else {
				mismatches = append(mismatches, fmt.Sprintf("%s (version check failed)", path))
			}
			continue
		}
		outputText := string(out)
		if strings.Contains(strings.ToLower(outputText), "mariadb") {
			mismatches = append(mismatches, fmt.Sprintf("%s (MariaDB)", path))
			continue
		}
		if !numericVersionPattern.MatchString(version) && strings.Contains(outputText, version) {
			return path, nil
		}
		actualVersions := versionsInOutput(outputText)
		for _, actual := range actualVersions {
			if versionLabelMatches(actual, version) {
				return path, nil
			}
		}
		if len(actualVersions) > 0 {
			mismatches = append(mismatches, fmt.Sprintf("%s (%s)", path, strings.Join(actualVersions, ", ")))
		} else {
			mismatches = append(mismatches, fmt.Sprintf("%s (unknown version)", path))
		}
	}

	if len(managedMissing) > 0 {
		sort.Strings(managedMissing)
		return "", &RuntimeDependencyError{
			Service:   "MySQL",
			Version:   version,
			Binary:    managedBinary,
			Libraries: managedMissing,
		}
	}

	detail := "no candidate binaries found"
	if len(mismatches) > 0 {
		detail = "found non-matching candidate(s): " + strings.Join(mismatches, ", ")
	}
	return "", fmt.Errorf("could not find MySQL binary matching version %s (%s)", version, detail)
}

func findMySQLClientBinaryWith(version string, lookPath lookPathFunc, output commandOutputFunc) (string, error) {
	if err := ValidateMySQLVersion(version); err != nil {
		return "", err
	}
	var mismatches []string
	seen := map[string]bool{}
	for _, candidate := range mysqlClientCandidates(version) {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		path, ok := resolveCommandCandidate(candidate, lookPath)
		if !ok {
			continue
		}
		if out, err := output(path, "--version"); err != nil {
			if missing := missingSharedLibrary(string(out)); missing != "" {
				mismatches = append(mismatches, fmt.Sprintf("%s (missing shared library %s)", path, missing))
			} else {
				mismatches = append(mismatches, fmt.Sprintf("%s (version check failed)", path))
			}
			continue
		}
		return path, nil
	}
	detail := "no candidate clients found"
	if len(mismatches) > 0 {
		detail = "found unusable candidate(s): " + strings.Join(mismatches, ", ")
	}
	return "", fmt.Errorf("could not find MySQL client (%s)", detail)
}

func missingSharedLibrary(output string) string {
	match := sharedLibraryErrorRE.FindStringSubmatch(output)
	if match == nil {
		return ""
	}
	return match[1]
}

func missingSharedLibraries(path, first string, output commandOutputFunc) []string {
	out := []string{}
	if first != "" {
		out = append(out, first)
	}
	lddOut, _ := output("ldd", path)
	for _, match := range lddMissingLibraryRE.FindAllStringSubmatch(string(lddOut), -1) {
		if !containsString(out, match[1]) {
			out = append(out, match[1])
		}
	}
	return out
}

func isManagedMySQLBinary(path string) bool {
	root := filepath.Join(paths.BinariesDir(), "mysql") + string(os.PathSeparator)
	return strings.HasPrefix(path, root)
}

func containsString(items []string, item string) bool {
	for _, existing := range items {
		if existing == item {
			return true
		}
	}
	return false
}

func mysqlBinaryCandidates(version string) []string {
	major := majorVersionLabel(version)
	return []string{
		ManagedMySQLBinaryPath(version),
		ManagedMySQLBinaryPath(major),
		"mysqld-" + version,
		"mysqld" + version,
		"mysql-server-" + version,
		filepath.Join("/usr/lib/mysql", version, "bin", "mysqld"),
		filepath.Join("/usr/local/mysql-"+version, "bin", "mysqld"),
		filepath.Join("/usr/local/mysql", version, "bin", "mysqld"),
		filepath.Join("/opt/mysql", version, "bin", "mysqld"),
		"mysqld-" + major,
		"mysqld" + major,
		"mysql-server-" + major,
		filepath.Join("/usr/lib/mysql", major, "bin", "mysqld"),
		filepath.Join("/usr/local/mysql-"+major, "bin", "mysqld"),
		filepath.Join("/usr/local/mysql", major, "bin", "mysqld"),
		filepath.Join("/opt/mysql", major, "bin", "mysqld"),
		MySQLBinaryName,
	}
}

func mysqlClientCandidates(version string) []string {
	major := majorVersionLabel(version)
	return []string{
		"mariadb",
		"mysql",
		"mysql-" + version,
		"mysql" + version,
		"mysql-" + major,
		"mysql" + major,
		managedBinaryPath("mysql", version, "mysql"),
		managedBinaryPath("mysql", major, "mysql"),
	}
}

func MySQLDataDirInitialized(version string) (bool, error) {
	return MySQLDataDirInitializedForInstance(version, "")
}

func MySQLDataDirInitializedForInstance(version, instance string) (bool, error) {
	mysqlDir := filepath.Join(MySQLDataDirForInstance(version, instance), "mysql")
	info, err := os.Stat(mysqlDir)
	if err == nil {
		return info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func InstalledMySQLInstances() ([]MySQLInstance, error) {
	instances, err := discoverDatabaseInstances("mysql", "routa-mysql@", "my.cnf", ValidateMySQLVersion, ValidateMySQLInstance)
	if err != nil {
		return nil, err
	}
	out := make([]MySQLInstance, 0, len(instances))
	for instance := range instances {
		port, err := databaseConfiguredPort(MySQLConfigPathForInstance(instance.Version, instance.Instance), "MySQL", MySQLDefaultPort)
		if err != nil {
			return nil, err
		}
		out = append(out, MySQLInstance{
			Version:  instance.Version,
			Instance: instance.Instance,
			Unit:     MySQLUnitNameForInstance(instance.Version, instance.Instance),
			DataDir:  MySQLDataDirForInstance(instance.Version, instance.Instance),
			Port:     port,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Version == out[j].Version {
			return out[i].Instance < out[j].Instance
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func MySQLUnitNamesForUninstall() []string {
	return databaseUnitNamesForUninstall("routa-mysql@", ValidateMySQLVersion, ValidateMySQLInstance)
}
