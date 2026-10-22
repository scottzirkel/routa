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
	PostgresBinaryName  = "postgres"
	PostgresDefaultPort = "5432"
)

const postgresUnitTmpl = `[Unit]
Description=routa Postgres {{.Version}}
After=network.target
{{.StartLimit}}

[Service]
Type=simple
{{if .RepairExec}}{{.RepairExec}}
{{end}}ExecStart={{.Binary}} -D {{.DataDir}} -c config_file={{.ConfigPath}}
StandardOutput=append:{{.LogPath}}
StandardError=append:{{.LogPath}}
Restart=on-failure
RestartSec=2
TimeoutStopSec=30

[Install]
WantedBy=default.target
`

const postgresConfigTmpl = `listen_addresses = '127.0.0.1'
port = {{.Port}}
unix_socket_directories = '{{.RunDir}}'
external_pid_file = '{{.PIDFile}}'
logging_collector = off
log_destination = 'stderr'
`

type postgresUnitData struct {
	Version    string
	Binary     string
	DataDir    string
	ConfigPath string
	LogPath    string
	StartLimit string
	RepairExec string
}

type postgresConfigData struct {
	RunDir  string
	PIDFile string
	Port    string
}

type PostgresInstance struct {
	Version  string
	Instance string
	Unit     string
	DataDir  string
	Port     string
}

func Postgres(version string) Definition {
	return PostgresWithPort(version, PostgresDefaultPort)
}

func PostgresWithPort(version, port string) Definition {
	return PostgresInstanceWithPort(version, "", port)
}

func PostgresInstanceWithPort(version, instance, port string) Definition {
	return Definition{
		Name:        "postgres",
		UnitName:    PostgresUnitNameForInstance(version, instance),
		BinaryName:  PostgresBinaryName,
		ConfigPath:  PostgresConfigPathForInstance(version, instance),
		DataDir:     PostgresDataDirForInstance(version, instance),
		RenderUnit:  func(binary string) (string, error) { return RenderPostgresUnitForInstance(version, instance, binary) },
		WriteConfig: func() error { return WritePostgresConfigForInstanceWithPort(version, instance, port) },
	}
}

func ValidatePostgresVersion(version string) error {
	return validateVersionLabel("Postgres", version)
}

func ValidatePostgresInstance(instance string) error {
	return validateInstanceLabel("Postgres", instance)
}

func PostgresUnitName(version string) string {
	return PostgresUnitNameForInstance(version, "")
}

func PostgresUnitNameForInstance(version, instance string) string {
	return "routa-postgres@" + databaseInstanceToken(version, instance) + ".service"
}

func PostgresDataDir(version string) string {
	return PostgresDataDirForInstance(version, "")
}

func PostgresDataDirForInstance(version, instance string) string {
	return databaseInstanceDir(paths.DataDir(), "postgres", version, instance)
}

func PostgresConfigPath(version string) string {
	return PostgresConfigPathForInstance(version, "")
}

func PostgresConfigPathForInstance(version, instance string) string {
	return filepath.Join(databaseInstanceDir(paths.ConfigDir(), "postgres", version, instance), "postgresql.conf")
}

func PostgresPIDFile(version string) string {
	return PostgresPIDFileForInstance(version, "")
}

func PostgresPIDFileForInstance(version, instance string) string {
	return filepath.Join(paths.RunDir(), "postgres-"+databaseInstanceToken(version, instance)+".pid")
}

func PostgresLogPath(version string) string {
	return PostgresLogPathForInstance(version, "")
}

func PostgresLogPathForInstance(version, instance string) string {
	return filepath.Join(paths.LogDir(), "postgres-"+databaseInstanceToken(version, instance)+".log")
}

func RenderPostgresUnit(version, binary string) (string, error) {
	return RenderPostgresUnitForInstance(version, "", binary)
}

func RenderPostgresUnitForInstance(version, instance, binary string) (string, error) {
	if err := ValidatePostgresVersion(version); err != nil {
		return "", err
	}
	if err := ValidatePostgresInstance(instance); err != nil {
		return "", err
	}
	if binary == "" {
		return "", fmt.Errorf("postgres binary path cannot be empty")
	}
	return render("postgres-unit", postgresUnitTmpl, postgresUnitData{
		Version:    version,
		Binary:     binary,
		DataDir:    PostgresDataDirForInstance(version, instance),
		ConfigPath: PostgresConfigPathForInstance(version, instance),
		LogPath:    PostgresLogPathForInstance(version, instance),
		StartLimit: unitStartLimit,
		RepairExec: databaseRepairExec("postgres", version, instance),
	})
}

func RenderPostgresConfig(version string) (string, error) {
	return RenderPostgresConfigWithPort(version, PostgresDefaultPort)
}

func RenderPostgresConfigWithPort(version, port string) (string, error) {
	return RenderPostgresConfigForInstanceWithPort(version, "", port)
}

func RenderPostgresConfigForInstanceWithPort(version, instance, port string) (string, error) {
	if err := ValidatePostgresVersion(version); err != nil {
		return "", err
	}
	if err := ValidatePostgresInstance(instance); err != nil {
		return "", err
	}
	if err := ValidateTCPPort("Postgres", port); err != nil {
		return "", err
	}
	return render("postgres-config", postgresConfigTmpl, postgresConfigData{
		RunDir:  paths.RunDir(),
		PIDFile: PostgresPIDFileForInstance(version, instance),
		Port:    port,
	})
}

func WritePostgresConfig(version string) error {
	return WritePostgresConfigWithPort(version, PostgresDefaultPort)
}

func WritePostgresConfigWithPort(version, port string) error {
	return WritePostgresConfigForInstanceWithPort(version, "", port)
}

func WritePostgresConfigForInstanceWithPort(version, instance, port string) error {
	if err := ValidatePostgresVersion(version); err != nil {
		return err
	}
	if err := ValidatePostgresInstance(instance); err != nil {
		return err
	}
	if err := ValidateTCPPort("Postgres", port); err != nil {
		return err
	}
	for _, dir := range []string{
		PostgresDataDirForInstance(version, instance),
		filepath.Dir(PostgresConfigPathForInstance(version, instance)),
		paths.RunDir(),
		paths.LogDir(),
	} {
		if err := ensureDir(dir); err != nil {
			return err
		}
	}
	content, err := RenderPostgresConfigForInstanceWithPort(version, instance, port)
	if err != nil {
		return err
	}
	return os.WriteFile(PostgresConfigPathForInstance(version, instance), []byte(content), 0o644)
}

func PostgresConfiguredPortForInstance(version, instance string) (string, error) {
	return databaseConfiguredPort(PostgresConfigPathForInstance(version, instance), "Postgres", PostgresDefaultPort)
}

func EnsurePostgres(version string) error {
	return EnsurePostgresWithPort(version, PostgresDefaultPort)
}

func EnsurePostgresWithPort(version, port string) error {
	return EnsurePostgresInstanceWithPort(version, "", port)
}

func EnsurePostgresInstanceWithPort(version, instance, port string) error {
	if err := ValidatePostgresVersion(version); err != nil {
		return err
	}
	if err := ValidatePostgresInstance(instance); err != nil {
		return err
	}
	if err := ValidateTCPPort("Postgres", port); err != nil {
		return err
	}
	bin, err := FindPostgresBinary(version)
	if err != nil {
		return err
	}
	if err := WriteFiles(PostgresInstanceWithPort(version, instance, port), bin); err != nil {
		return err
	}
	if err := initializePostgresDataDir(version, instance, bin); err != nil {
		return err
	}
	return systemd.DaemonReload()
}

func InitializePostgresDataDir(version string) error {
	bin, err := FindPostgresBinary(version)
	if err != nil {
		return err
	}
	return initializePostgresDataDir(version, "", bin)
}

func initializePostgresDataDir(version, instance, serverBin string) error {
	initialized, err := PostgresDataDirInitializedForInstance(version, instance)
	if err != nil {
		return err
	}
	if initialized {
		return nil
	}
	initBin, err := findPostgresInitCommand(version, serverBin)
	if err != nil {
		return err
	}
	cmd := exec.Command(initBin, "-D", PostgresDataDirForInstance(version, instance))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func FindPostgresBinary(version string) (string, error) {
	return findPostgresBinaryWith(version, exec.LookPath, commandOutput)
}

func findPostgresBinaryWith(version string, lookPath lookPathFunc, output commandOutputFunc) (string, error) {
	if err := ValidatePostgresVersion(version); err != nil {
		return "", err
	}
	return versionedCommand("Postgres binary", version, postgresBinaryCandidates(version), []string{"--version"}, lookPath, output)
}

func postgresBinaryCandidates(version string) []string {
	major := majorVersionLabel(version)
	return []string{
		"postgres-" + version,
		"postgres" + version,
		filepath.Join("/usr/lib/postgresql", version, "bin", "postgres"),
		filepath.Join("/usr/pgsql-"+version, "bin", "postgres"),
		filepath.Join("/opt/postgresql", version, "bin", "postgres"),
		"postgres-" + major,
		"postgres" + major,
		filepath.Join("/usr/lib/postgresql", major, "bin", "postgres"),
		filepath.Join("/usr/pgsql-"+major, "bin", "postgres"),
		filepath.Join("/opt/postgresql", major, "bin", "postgres"),
		PostgresBinaryName,
	}
}

func findPostgresInitCommand(version, serverBin string) (string, error) {
	candidates := postgresInitCommandCandidates(version, serverBin)
	return versionedCommand("Postgres initdb", version, candidates, []string{"--version"}, exec.LookPath, commandOutput)
}

func postgresInitCommandCandidates(version, serverBin string) []string {
	major := majorVersionLabel(version)
	var out []string
	if serverBin != "" {
		out = append(out, filepath.Join(filepath.Dir(serverBin), "initdb"))
	}
	return append(out,
		"initdb-"+version,
		"initdb"+version,
		filepath.Join("/usr/lib/postgresql", version, "bin", "initdb"),
		filepath.Join("/usr/pgsql-"+version, "bin", "initdb"),
		filepath.Join("/opt/postgresql", version, "bin", "initdb"),
		"initdb-"+major,
		"initdb"+major,
		filepath.Join("/usr/lib/postgresql", major, "bin", "initdb"),
		filepath.Join("/usr/pgsql-"+major, "bin", "initdb"),
		filepath.Join("/opt/postgresql", major, "bin", "initdb"),
		"initdb",
	)
}

func PostgresDataDirInitialized(version string) (bool, error) {
	return PostgresDataDirInitializedForInstance(version, "")
}

func PostgresDataDirInitializedForInstance(version, instance string) (bool, error) {
	versionFile := filepath.Join(PostgresDataDirForInstance(version, instance), "PG_VERSION")
	info, err := os.Stat(versionFile)
	if err == nil {
		return !info.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func InstalledPostgresInstances() ([]PostgresInstance, error) {
	instances, err := discoverDatabaseInstances("postgres", "routa-postgres@", "postgresql.conf", ValidatePostgresVersion, ValidatePostgresInstance)
	if err != nil {
		return nil, err
	}
	out := make([]PostgresInstance, 0, len(instances))
	for instance := range instances {
		port, err := PostgresConfiguredPortForInstance(instance.Version, instance.Instance)
		if err != nil {
			return nil, err
		}
		out = append(out, PostgresInstance{
			Version:  instance.Version,
			Instance: instance.Instance,
			Unit:     PostgresUnitNameForInstance(instance.Version, instance.Instance),
			DataDir:  PostgresDataDirForInstance(instance.Version, instance.Instance),
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

func PostgresUnitNamesForUninstall() []string {
	return databaseUnitNamesForUninstall("routa-postgres@", ValidatePostgresVersion, ValidatePostgresInstance)
}
