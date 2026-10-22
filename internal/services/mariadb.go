package services

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/systemd"
)

const (
	MariaDBBinaryName  = "mariadbd"
	MariaDBDefaultPort = "3306"
)

const mariadbUnitTmpl = `[Unit]
Description=routa MariaDB {{.Version}}
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

const mariadbConfigTmpl = `[mysqld]
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

type mariadbUnitData struct {
	Version    string
	Binary     string
	ConfigPath string
	StartLimit string
	RepairExec string
}

type mariadbConfigData struct {
	DataDir    string
	SocketPath string
	PIDFile    string
	LogPath    string
	Port       string
}

type MariaDBInstance struct {
	Version  string
	Instance string
	Unit     string
	DataDir  string
	Port     string
}

func MariaDB(version string) Definition {
	return MariaDBWithPort(version, MariaDBDefaultPort)
}

func MariaDBWithPort(version, port string) Definition {
	return MariaDBInstanceWithPort(version, "", port)
}

func MariaDBInstanceWithPort(version, instance, port string) Definition {
	return Definition{
		Name:        "mariadb",
		UnitName:    MariaDBUnitNameForInstance(version, instance),
		BinaryName:  MariaDBBinaryName,
		ConfigPath:  MariaDBConfigPathForInstance(version, instance),
		DataDir:     MariaDBDataDirForInstance(version, instance),
		RenderUnit:  func(binary string) (string, error) { return RenderMariaDBUnitForInstance(version, instance, binary) },
		WriteConfig: func() error { return WriteMariaDBConfigForInstanceWithPort(version, instance, port) },
	}
}

func ValidateMariaDBVersion(version string) error {
	return validateVersionLabel("MariaDB", version)
}

func ValidateMariaDBInstance(instance string) error {
	return validateInstanceLabel("MariaDB", instance)
}

func MariaDBUnitName(version string) string {
	return MariaDBUnitNameForInstance(version, "")
}

func MariaDBUnitNameForInstance(version, instance string) string {
	return "routa-mariadb@" + databaseInstanceToken(version, instance) + ".service"
}

func MariaDBDataDir(version string) string {
	return MariaDBDataDirForInstance(version, "")
}

func MariaDBDataDirForInstance(version, instance string) string {
	return databaseInstanceDir(paths.DataDir(), "mariadb", version, instance)
}

func MariaDBConfigPath(version string) string {
	return MariaDBConfigPathForInstance(version, "")
}

func MariaDBConfigPathForInstance(version, instance string) string {
	return filepath.Join(databaseInstanceDir(paths.ConfigDir(), "mariadb", version, instance), "my.cnf")
}

func MariaDBSocketPath(version string) string {
	return MariaDBSocketPathForInstance(version, "")
}

func MariaDBSocketPathForInstance(version, instance string) string {
	return filepath.Join(paths.RunDir(), "mariadb-"+databaseInstanceToken(version, instance)+".sock")
}

func MariaDBPIDFile(version string) string {
	return MariaDBPIDFileForInstance(version, "")
}

func MariaDBPIDFileForInstance(version, instance string) string {
	return filepath.Join(paths.RunDir(), "mariadb-"+databaseInstanceToken(version, instance)+".pid")
}

func MariaDBLogPath(version string) string {
	return MariaDBLogPathForInstance(version, "")
}

func MariaDBLogPathForInstance(version, instance string) string {
	return filepath.Join(paths.LogDir(), "mariadb-"+databaseInstanceToken(version, instance)+".log")
}

func RenderMariaDBUnit(version, binary string) (string, error) {
	return RenderMariaDBUnitForInstance(version, "", binary)
}

func RenderMariaDBUnitForInstance(version, instance, binary string) (string, error) {
	if err := ValidateMariaDBVersion(version); err != nil {
		return "", err
	}
	if err := ValidateMariaDBInstance(instance); err != nil {
		return "", err
	}
	if binary == "" {
		return "", fmt.Errorf("mariadb binary path cannot be empty")
	}
	return render("mariadb-unit", mariadbUnitTmpl, mariadbUnitData{
		Version:    version,
		Binary:     binary,
		ConfigPath: MariaDBConfigPathForInstance(version, instance),
		StartLimit: unitStartLimit,
		RepairExec: databaseRepairExec("mariadb", version, instance),
	})
}

func RenderMariaDBConfig(version string) (string, error) {
	return RenderMariaDBConfigWithPort(version, MariaDBDefaultPort)
}

func RenderMariaDBConfigWithPort(version, port string) (string, error) {
	return RenderMariaDBConfigForInstanceWithPort(version, "", port)
}

func RenderMariaDBConfigForInstanceWithPort(version, instance, port string) (string, error) {
	if err := ValidateMariaDBVersion(version); err != nil {
		return "", err
	}
	if err := ValidateMariaDBInstance(instance); err != nil {
		return "", err
	}
	if err := ValidateTCPPort("MariaDB", port); err != nil {
		return "", err
	}
	return render("mariadb-config", mariadbConfigTmpl, mariadbConfigData{
		DataDir:    MariaDBDataDirForInstance(version, instance),
		SocketPath: MariaDBSocketPathForInstance(version, instance),
		PIDFile:    MariaDBPIDFileForInstance(version, instance),
		LogPath:    MariaDBLogPathForInstance(version, instance),
		Port:       port,
	})
}

func WriteMariaDBConfig(version string) error {
	return WriteMariaDBConfigWithPort(version, MariaDBDefaultPort)
}

func WriteMariaDBConfigWithPort(version, port string) error {
	return WriteMariaDBConfigForInstanceWithPort(version, "", port)
}

func WriteMariaDBConfigForInstanceWithPort(version, instance, port string) error {
	if err := ValidateMariaDBVersion(version); err != nil {
		return err
	}
	if err := ValidateMariaDBInstance(instance); err != nil {
		return err
	}
	if err := ValidateTCPPort("MariaDB", port); err != nil {
		return err
	}
	for _, dir := range []string{
		MariaDBDataDirForInstance(version, instance),
		filepath.Dir(MariaDBConfigPathForInstance(version, instance)),
		paths.RunDir(),
		paths.LogDir(),
	} {
		if err := ensureDir(dir); err != nil {
			return err
		}
	}
	content, err := RenderMariaDBConfigForInstanceWithPort(version, instance, port)
	if err != nil {
		return err
	}
	return os.WriteFile(MariaDBConfigPathForInstance(version, instance), []byte(content), 0o644)
}

func MariaDBConfiguredPortForInstance(version, instance string) (string, error) {
	return databaseConfiguredPort(MariaDBConfigPathForInstance(version, instance), "MariaDB", MariaDBDefaultPort)
}

func EnsureMariaDB(version string) error {
	return EnsureMariaDBWithPort(version, MariaDBDefaultPort)
}

func EnsureMariaDBWithPort(version, port string) error {
	return EnsureMariaDBInstanceWithPort(version, "", port)
}

func EnsureMariaDBInstanceWithPort(version, instance, port string) error {
	if err := ValidateMariaDBVersion(version); err != nil {
		return err
	}
	if err := ValidateMariaDBInstance(instance); err != nil {
		return err
	}
	if err := ValidateTCPPort("MariaDB", port); err != nil {
		return err
	}
	bin, err := FindMariaDBBinary(version)
	if err != nil {
		return err
	}
	if err := WriteFiles(MariaDBInstanceWithPort(version, instance, port), bin); err != nil {
		return err
	}
	if err := initializeMariaDBDataDir(version, instance, bin); err != nil {
		return err
	}
	return systemd.DaemonReload()
}

func InitializeMariaDBDataDir(version string) error {
	bin, err := FindMariaDBBinary(version)
	if err != nil {
		return err
	}
	return initializeMariaDBDataDir(version, "", bin)
}

func initializeMariaDBDataDir(version, instance, serverBin string) error {
	initialized, err := MariaDBDataDirInitializedForInstance(version, instance)
	if err != nil {
		return err
	}
	if initialized {
		return nil
	}
	initBin, err := findMariaDBInitCommand(version, serverBin)
	if err != nil {
		return err
	}
	cmd := exec.Command(initBin,
		"--defaults-file="+MariaDBConfigPathForInstance(version, instance),
		"--datadir="+MariaDBDataDirForInstance(version, instance),
		"--skip-test-db",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func FindMariaDBBinary(version string) (string, error) {
	return findMariaDBBinaryWith(version, exec.LookPath, commandOutput)
}

func findMariaDBBinaryWith(version string, lookPath lookPathFunc, output commandOutputFunc) (string, error) {
	if err := ValidateMariaDBVersion(version); err != nil {
		return "", err
	}
	return versionedCommand("MariaDB binary", version, mariadbBinaryCandidates(version), []string{"--version"}, lookPath, output)
}

func mariadbBinaryCandidates(version string) []string {
	major := majorVersionLabel(version)
	return []string{
		"mariadbd-" + version,
		"mariadbd" + version,
		"mysqld-" + version,
		"mysqld" + version,
		filepath.Join("/usr/lib/mariadb", version, "bin", "mariadbd"),
		filepath.Join("/usr/local/mariadb", version, "bin", "mariadbd"),
		filepath.Join("/opt/mariadb", version, "bin", "mariadbd"),
		"mariadbd-" + major,
		"mariadbd" + major,
		"mysqld-" + major,
		"mysqld" + major,
		filepath.Join("/usr/lib/mariadb", major, "bin", "mariadbd"),
		filepath.Join("/usr/local/mariadb", major, "bin", "mariadbd"),
		filepath.Join("/opt/mariadb", major, "bin", "mariadbd"),
		MariaDBBinaryName,
		"mysqld",
	}
}

func MariaDBDataDirInitialized(version string) (bool, error) {
	return MariaDBDataDirInitializedForInstance(version, "")
}

func MariaDBDataDirInitializedForInstance(version, instance string) (bool, error) {
	mysqlDir := filepath.Join(MariaDBDataDirForInstance(version, instance), "mysql")
	info, err := os.Stat(mysqlDir)
	if err == nil {
		return info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func InstalledMariaDBInstances() ([]MariaDBInstance, error) {
	instances, err := discoverDatabaseInstances("mariadb", "routa-mariadb@", "my.cnf", ValidateMariaDBVersion, ValidateMariaDBInstance)
	if err != nil {
		return nil, err
	}
	out := make([]MariaDBInstance, 0, len(instances))
	for instance := range instances {
		port, err := MariaDBConfiguredPortForInstance(instance.Version, instance.Instance)
		if err != nil {
			return nil, err
		}
		out = append(out, MariaDBInstance{
			Version:  instance.Version,
			Instance: instance.Instance,
			Unit:     MariaDBUnitNameForInstance(instance.Version, instance.Instance),
			DataDir:  MariaDBDataDirForInstance(instance.Version, instance.Instance),
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

func MariaDBUnitNamesForUninstall() []string {
	return databaseUnitNamesForUninstall("routa-mariadb@", ValidateMariaDBVersion, ValidateMariaDBInstance)
}

func findMariaDBInitCommand(version, serverBin string) (string, error) {
	names := []string{"mariadb-install-db", "mysql_install_db"}
	if serverBin != "" {
		dir := filepath.Dir(serverBin)
		for _, name := range names {
			path := filepath.Join(dir, name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, nil
			}
		}
	}
	for _, name := range []string{
		"mariadb-install-db-" + version,
		"mysql_install_db-" + version,
		"mariadb-install-db" + version,
		"mysql_install_db" + version,
		"mariadb-install-db-" + majorVersionLabel(version),
		"mysql_install_db-" + majorVersionLabel(version),
		"mariadb-install-db" + majorVersionLabel(version),
		"mysql_install_db" + majorVersionLabel(version),
	} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("missing required command \"mariadb-install-db\" or \"mysql_install_db\". Install MariaDB with your system package manager")
}
