package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/services"
	"github.com/scottzirkel/routa/internal/systemd"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Manage routa database services",
}

var dbInstallPort string
var dbStartPort string
var dbInstallUser string
var dbInstallPassword string
var dbStartUser string
var dbStartPassword string
var dbCredentialsUser string
var dbCredentialsPassword string

var dbInstallCmd = &cobra.Command{
	Use:   "install <engine> <version> [instance] [on <port>]",
	Short: "Write database config/unit and initialize its data directory",
	Args:  dbEngineVersionInstancePortArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, portArgs, err := databaseTargetFromArgs(cmd, args, true)
		if err != nil {
			return err
		}
		port, err := databasePortFromCommand(cmd, portArgs, target.Engine, dbInstallPort)
		if err != nil {
			return err
		}
		if err := ensureDatabase(cmd, target.Engine, target.Version, target.Instance, port); err != nil {
			return err
		}
		if creds, ok, err := databaseCredentialsFromFlags(target.Engine, dbInstallUser, dbInstallPassword); err != nil {
			return err
		} else if ok {
			if err := saveDatabaseCredentials(target.Engine, target.Version, target.Instance, creds); err != nil {
				return err
			}
			unit := databaseUnitName(target.Engine, target.Version, target.Instance)
			if systemd.IsActive(unit) {
				if err := applyDatabaseCredentials(target.Engine, target.Version, target.Instance, creds); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "saved credentials for %s (applied on next start)\n", unit)
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "installed %s\n", databaseUnitName(target.Engine, target.Version, target.Instance))
		return nil
	},
}

var dbStartCmd = &cobra.Command{
	Use:   "start <engine> <version> [instance] [on <port>]",
	Short: "Write database config/unit and start it",
	Args:  dbEngineVersionInstancePortArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, portArgs, err := databaseTargetFromArgs(cmd, args, true)
		if err != nil {
			return err
		}
		unit := databaseUnitName(target.Engine, target.Version, target.Instance)
		port, err := databasePortFromCommand(cmd, portArgs, target.Engine, dbStartPort)
		if err != nil {
			return err
		}
		if portBound(localhostAddr(port)) && !systemd.IsActive(unit) {
			return portInUseError(localhostAddr(port), target.Engine)
		}
		if err := ensureDatabase(cmd, target.Engine, target.Version, target.Instance, port); err != nil {
			return err
		}
		if err := repairDatabaseRuntime(cmd, target.Engine, target.Version, target.Instance, false); err != nil {
			return err
		}
		if err := systemd.EnableNow(unit); err != nil {
			return fmt.Errorf("start %s: %w", unit, err)
		}
		if err := applyDatabaseCredentialsAfterStart(cmd, target.Engine, target.Version, target.Instance, dbStartUser, dbStartPassword); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "started %s\n", unit)
		return nil
	},
}

var dbRepairCmd = &cobra.Command{
	Use:   "repair <engine> <version> [instance]",
	Short: "Clear stale socket, lock, and pid files left behind by a crashed database server",
	Args:  dbEngineVersionInstanceArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, _, err := databaseTargetFromArgs(cmd, args, false)
		if err != nil {
			return err
		}
		return repairDatabaseRuntime(cmd, target.Engine, target.Version, target.Instance, true)
	},
}

// repairDatabaseRuntime clears the runtime files of a database instance whose
// server is gone. A server killed without cleanup leaves a socket lock naming
// its pid; once that pid is recycled the lock looks live and the server refuses
// to start against it forever. Each unit runs this from ExecStartPre, so it has
// to succeed quietly when there is nothing to clear.
func repairDatabaseRuntime(cmd *cobra.Command, engine, version, instance string, verbose bool) error {
	state, err := services.InspectDatabaseRuntime(engine, version, instance)
	if err != nil {
		return err
	}
	unit := databaseUnitName(engine, version, instance)
	out := cmd.OutOrStdout()
	if state.OwnerPID != 0 {
		if verbose {
			fmt.Fprintf(out, "%s is already running as pid %d; left its runtime files alone\n", unit, state.OwnerPID)
		}
		return nil
	}
	for _, file := range state.Unverified {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: pid %d %s; leaving %s in place\n", file.PID, file.Reason, file.Path)
	}
	removed, err := state.Clear()
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		if verbose {
			fmt.Fprintf(out, "no stale runtime files for %s\n", unit)
		}
		return nil
	}
	for _, file := range removed {
		fmt.Fprintf(out, "cleared stale %s\n", file)
	}
	return nil
}

var dbStopCmd = &cobra.Command{
	Use:   "stop <engine> <version> [instance]",
	Short: "Stop and disable a routa database service",
	Args:  dbEngineVersionInstanceArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, _, err := databaseTargetFromArgs(cmd, args, false)
		if err != nil {
			return err
		}
		unit := databaseUnitName(target.Engine, target.Version, target.Instance)
		if err := systemd.DisableNow(unit); err != nil {
			return fmt.Errorf("stop %s: %w", unit, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "stopped %s\n", unit)
		return nil
	},
}

var dbStatusCmd = &cobra.Command{
	Use:   "status <engine> <version> [instance]",
	Short: "Show routa database systemd status",
	Args:  dbEngineVersionInstanceArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, _, err := databaseTargetFromArgs(cmd, args, false)
		if err != nil {
			return err
		}
		return systemd.RunSystemctl("--user", "status", databaseUnitName(target.Engine, target.Version, target.Instance))
	},
}

var dbListCmd = &cobra.Command{
	Use:   "list",
	Short: "List routa database services",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		mariadbInstances, err := services.InstalledMariaDBInstances()
		if err != nil {
			return err
		}
		mysqlInstances, err := services.InstalledMySQLInstances()
		if err != nil {
			return err
		}
		postgresInstances, err := services.InstalledPostgresInstances()
		if err != nil {
			return err
		}
		if len(mariadbInstances) == 0 && len(mysqlInstances) == 0 && len(postgresInstances) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no database services installed. `routa db install mariadb <version>`, `routa db install mysql <version>`, or `routa db install postgres <version>`")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 4, ' ', 0)
		fmt.Fprintln(w, "ENGINE\tVERSION\tINSTANCE\tPORT")
		for _, instance := range mariadbInstances {
			fmt.Fprintf(w, "mariadb\t%s\t%s\t%s\n", instance.Version, databaseInstanceName(instance.Instance), instance.Port)
		}
		for _, instance := range mysqlInstances {
			fmt.Fprintf(w, "mysql\t%s\t%s\t%s\n", instance.Version, databaseInstanceName(instance.Instance), instance.Port)
		}
		for _, instance := range postgresInstances {
			fmt.Fprintf(w, "postgres\t%s\t%s\t%s\n", instance.Version, databaseInstanceName(instance.Instance), instance.Port)
		}
		return w.Flush()
	},
}

var dbCredentialsCmd = &cobra.Command{
	Use:   "credentials <engine> <version> [instance] --user <user> --password <password>",
	Short: "Create or update database application credentials",
	Args:  dbEngineVersionInstanceArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, _, err := databaseTargetFromArgs(cmd, args, false)
		if err != nil {
			return err
		}
		creds, ok, err := databaseCredentialsFromFlags(target.Engine, dbCredentialsUser, dbCredentialsPassword)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("requires --user and --password")
		}
		if err := saveDatabaseCredentials(target.Engine, target.Version, target.Instance, creds); err != nil {
			return err
		}
		unit := databaseUnitName(target.Engine, target.Version, target.Instance)
		if systemd.IsActive(unit) {
			if err := applyDatabaseCredentials(target.Engine, target.Version, target.Instance, creds); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "updated credentials for %s\n", unit)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "saved credentials for %s (applied on next start)\n", unit)
		return nil
	},
}

type databaseTarget struct {
	Engine   string
	Version  string
	Instance string
}

func dbEngineVersionArgs(_ *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("requires engine and version")
	}
	return validateDatabaseTarget(databaseTarget{Engine: args[0], Version: args[1]})
}

func dbEngineVersionInstanceArgs(cmd *cobra.Command, args []string) error {
	_, _, err := databaseTargetFromArgs(cmd, args, false)
	return err
}

func dbEngineVersionInstancePortArgs(cmd *cobra.Command, args []string) error {
	_, _, err := databaseTargetFromArgs(cmd, args, true)
	return err
}

func databaseTargetFromArgs(cmd *cobra.Command, args []string, allowPort bool) (databaseTarget, []string, error) {
	if len(args) < 2 {
		return databaseTarget{}, nil, fmt.Errorf("requires engine and version")
	}
	target := databaseTarget{Engine: args[0], Version: args[1]}
	tail := args[2:]
	if !allowPort {
		if len(tail) > 1 {
			return databaseTarget{}, nil, fmt.Errorf("usage: %s", cmd.UseLine())
		}
		if len(tail) == 1 {
			target.Instance = tail[0]
		}
		return target, nil, validateDatabaseTarget(target)
	}

	var portArgs []string
	switch len(tail) {
	case 0:
	case 1:
		if tail[0] == "on" {
			return databaseTarget{}, nil, fmt.Errorf("usage: %s", cmd.UseLine())
		}
		target.Instance = tail[0]
	case 2:
		portArgs = tail
	case 3:
		target.Instance = tail[0]
		portArgs = tail[1:]
	default:
		return databaseTarget{}, nil, fmt.Errorf("usage: %s", cmd.UseLine())
	}
	return target, portArgs, validateDatabaseTarget(target)
}

func validateDatabaseTarget(target databaseTarget) error {
	if target.Instance == "on" {
		return fmt.Errorf("database instance cannot be %q", target.Instance)
	}
	switch target.Engine {
	case "mariadb":
		if err := services.ValidateMariaDBVersion(target.Version); err != nil {
			return err
		}
		return services.ValidateMariaDBInstance(target.Instance)
	case "mysql":
		if err := services.ValidateMySQLVersion(target.Version); err != nil {
			return err
		}
		return services.ValidateMySQLInstance(target.Instance)
	case "postgres":
		if err := services.ValidatePostgresVersion(target.Version); err != nil {
			return err
		}
		return services.ValidatePostgresInstance(target.Instance)
	default:
		return fmt.Errorf("unsupported database %q (supported: mariadb, mysql, postgres)", target.Engine)
	}
}

func ensureDatabase(cmd *cobra.Command, engine, version, instance, port string) error {
	switch engine {
	case "mariadb":
		return services.EnsureMariaDBInstanceWithPort(version, instance, port)
	case "mysql":
		if _, err := services.InstallMySQL(cmd.Context(), version, cmd.OutOrStdout()); err != nil {
			return err
		}
		return services.EnsureMySQLInstanceWithPort(version, instance, port)
	case "postgres":
		return services.EnsurePostgresInstanceWithPort(version, instance, port)
	default:
		return fmt.Errorf("unsupported database %q (supported: mariadb, mysql, postgres)", engine)
	}
}

func databaseCredentialsFromFlags(engine, user, password string) (services.MySQLCredentials, bool, error) {
	if user == "" && password == "" {
		return services.MySQLCredentials{}, false, nil
	}
	if engine != "mysql" {
		return services.MySQLCredentials{}, false, fmt.Errorf("credentials are currently supported for mysql")
	}
	if user == "" {
		return services.MySQLCredentials{}, false, fmt.Errorf("requires --user when --password is provided")
	}
	creds := services.MySQLCredentials{User: user, Password: password}
	if err := services.ValidateMySQLCredentials(creds); err != nil {
		return services.MySQLCredentials{}, false, err
	}
	return creds, true, nil
}

func saveDatabaseCredentials(engine, version, instance string, creds services.MySQLCredentials) error {
	switch engine {
	case "mysql":
		return services.WriteMySQLCredentialsForInstance(version, instance, creds)
	default:
		return fmt.Errorf("credentials are currently supported for mysql")
	}
}

func applyDatabaseCredentialsAfterStart(cmd *cobra.Command, engine, version, instance, user, password string) error {
	creds, ok, err := databaseCredentialsFromFlags(engine, user, password)
	if err != nil {
		return err
	}
	if ok {
		if err := saveDatabaseCredentials(engine, version, instance, creds); err != nil {
			return err
		}
		return applyDatabaseCredentials(engine, version, instance, creds)
	}
	if engine != "mysql" {
		return nil
	}
	creds, ok, err = services.ReadMySQLCredentialsForInstance(version, instance)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := applyDatabaseCredentials(engine, version, instance, creds); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "applied saved credentials for %s\n", databaseUnitName(engine, version, instance))
	return nil
}

func applyDatabaseCredentials(engine, version, instance string, creds services.MySQLCredentials) error {
	switch engine {
	case "mysql":
		return services.ApplyMySQLCredentialsForInstance(version, instance, creds)
	default:
		return fmt.Errorf("credentials are currently supported for mysql")
	}
}

func databasePort(engine, port string) (string, error) {
	if port == "" {
		switch engine {
		case "mariadb":
			port = services.MariaDBDefaultPort
		case "mysql":
			port = services.MySQLDefaultPort
		case "postgres":
			port = services.PostgresDefaultPort
		}
	}
	if err := services.ValidateTCPPort(engine, port); err != nil {
		return "", err
	}
	return port, nil
}

func databasePortFromCommand(cmd *cobra.Command, args []string, engine, flagPort string) (string, error) {
	fallback, err := databasePort(engine, "")
	if err != nil {
		return "", err
	}
	return portFromCommand(cmd, args, "port", flagPort, fallback, engine)
}

func databaseUnitName(engine, version, instance string) string {
	switch engine {
	case "mariadb":
		return services.MariaDBUnitNameForInstance(version, instance)
	case "mysql":
		return services.MySQLUnitNameForInstance(version, instance)
	case "postgres":
		return services.PostgresUnitNameForInstance(version, instance)
	default:
		return ""
	}
}

func databaseInstanceName(instance string) string {
	if instance == "" {
		return "default"
	}
	return instance
}

func init() {
	dbInstallCmd.Flags().StringVar(&dbInstallPort, "port", "", "database TCP port")
	dbInstallCmd.Flags().StringVar(&dbInstallUser, "user", "", "database application user")
	dbInstallCmd.Flags().StringVar(&dbInstallPassword, "password", "", "database application password")
	dbStartCmd.Flags().StringVar(&dbStartPort, "port", "", "database TCP port")
	dbStartCmd.Flags().StringVar(&dbStartUser, "user", "", "database application user")
	dbStartCmd.Flags().StringVar(&dbStartPassword, "password", "", "database application password")
	dbCredentialsCmd.Flags().StringVar(&dbCredentialsUser, "user", "", "database application user")
	dbCredentialsCmd.Flags().StringVar(&dbCredentialsPassword, "password", "", "database application password")
	dbCmd.AddCommand(dbInstallCmd, dbStartCmd, dbStopCmd, dbStatusCmd, dbListCmd, dbCredentialsCmd, dbRepairCmd)
	rootCmd.AddCommand(dbCmd)
}
