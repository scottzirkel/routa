package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/services"
	"github.com/scottzirkel/routa/internal/site"
	"github.com/scottzirkel/routa/internal/systemd"
)

var storageCmd = &cobra.Command{
	Use:   "storage",
	Short: "Manage routa object storage services",
}

var storageInstallPort string
var storageInstallConsolePort string
var storageStartPort string
var storageStartConsolePort string

var storageInstallCmd = &cobra.Command{
	Use:   "install minio <version> [on <port>]",
	Short: "Write MinIO unit and prepare its data directory",
	Args:  storageMinIOVersionPortArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		version := args[1]
		port, consolePort, err := minIOPortsFromCommand(cmd, args[2:], storageInstallPort, storageInstallConsolePort)
		if err != nil {
			return err
		}
		if err := services.EnsureMinIOWithPorts(version, port, consolePort); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "installed %s on API %s and console %s\n", services.MinIOUnitName(version), localhostAddr(port), localhostAddr(consolePort))
		return nil
	},
}

var storageStartCmd = &cobra.Command{
	Use:   "start minio <version> [on <port>]",
	Short: "Write MinIO unit and start it",
	Args:  storageMinIOVersionPortArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		version := args[1]
		unit := services.MinIOUnitName(version)
		port, consolePort, err := minIOPortsFromCommand(cmd, args[2:], storageStartPort, storageStartConsolePort)
		if err != nil {
			return err
		}
		for label, p := range map[string]string{"minio": port, "minio console": consolePort} {
			if portBound(localhostAddr(p)) && !systemd.IsActive(unit) {
				return portInUseError(localhostAddr(p), label)
			}
		}
		if err := services.EnsureMinIOWithPorts(version, port, consolePort); err != nil {
			return err
		}
		if err := systemd.EnableNow(unit); err != nil {
			return fmt.Errorf("start %s: %w", unit, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "started %s on API %s and console %s\n", unit, localhostAddr(port), localhostAddr(consolePort))
		return nil
	},
}

var storageStopCmd = &cobra.Command{
	Use:   "stop minio <version>",
	Short: "Stop and disable a routa MinIO service",
	Args:  storageMinIOVersionArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		version := args[1]
		unit := services.MinIOUnitName(version)
		if err := systemd.DisableNow(unit); err != nil {
			return fmt.Errorf("stop %s: %w", unit, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "stopped %s\n", unit)
		return nil
	},
}

var storageStatusCmd = &cobra.Command{
	Use:   "status minio <version>",
	Short: "Show routa MinIO systemd status",
	Args:  storageMinIOVersionArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		version := args[1]
		apiPort, consolePort, err := minIOConfiguredPorts(version)
		if err != nil {
			return err
		}
		unit := services.MinIOUnitName(version)
		fmt.Fprintln(cmd.OutOrStdout(), minIOStatusHeader(unit, apiPort, consolePort))
		return systemd.RunSystemctl("--user", "status", unit)
	},
}

var storageProxyCmd = &cobra.Command{
	Use:   "proxy minio <version> [name]",
	Short: "Proxy the MinIO console as <name>.test",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 && len(args) != 3 {
			return fmt.Errorf("usage: %s", cmd.UseLine())
		}
		return storageMinIOVersionArgs(cmd, args[:2])
	},
	RunE: func(_ *cobra.Command, args []string) error {
		link, err := minIOProxyLink(args)
		if err != nil {
			return err
		}
		s, err := site.Load()
		if err != nil {
			return err
		}
		site.AddLink(s, link)
		return commitAndReload(s, fmt.Sprintf("proxy %s.test -> %s", link.Name, link.Target))
	},
}

var storageListCmd = &cobra.Command{
	Use:   "list",
	Short: "List routa object storage services",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		instances, err := services.InstalledMinIOInstances()
		if err != nil {
			return err
		}
		entries := []serviceListEntry{}
		for _, instance := range instances {
			apiPort, consolePort, err := minIOConfiguredPorts(instance.Version)
			if err != nil {
				return err
			}
			entries = append(entries, serviceListEntry{Engine: "minio", Version: instance.Version, Port: apiPort, ConsolePort: consolePort, Unit: instance.Unit, DataDir: instance.DataDir})
		}
		if storageListJSON {
			for i := range entries {
				entries[i].Active = systemd.IsActive(entries[i].Unit)
			}
			return writeJSON(cmd.OutOrStdout(), entries)
		}
		if len(entries) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no object storage services installed. `routa storage install minio <version>`")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "ENGINE\tVERSION\tAPI_PORT\tCONSOLE_PORT\tUNIT\tDATA_DIR")
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Engine, e.Version, e.Port, e.ConsolePort, e.Unit, e.DataDir)
		}
		return w.Flush()
	},
}

var storageListJSON bool

func minIOProxyLink(args []string) (site.Link, error) {
	version := args[1]
	name := "minio"
	if len(args) == 3 {
		name = args[2]
	}
	normalized, err := normalizeSiteName(name)
	if err != nil {
		return site.Link{}, err
	}
	_, consolePort, err := minIOConfiguredPorts(version)
	if err != nil {
		return site.Link{}, err
	}
	return site.Link{Name: normalized, Target: localhostAddr(consolePort), Secure: true}, nil
}

func storageMinIOVersionArgs(_ *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("requires minio and version")
	}
	if args[0] != "minio" {
		return fmt.Errorf("unsupported storage engine %q (supported: minio)", args[0])
	}
	return services.ValidateMinIOVersion(args[1])
}

func storageMinIOVersionPortArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 2 && len(args) != 4 {
		return fmt.Errorf("usage: %s", cmd.UseLine())
	}
	return storageMinIOVersionArgs(cmd, args[:2])
}

func minIOPorts(port, consolePort string) (string, string, error) {
	if port == "" {
		port = services.MinIODefaultPort
	}
	if consolePort == "" {
		consolePort = services.MinIODefaultConsolePort
	}
	if err := services.ValidateTCPPort("MinIO", port); err != nil {
		return "", "", err
	}
	if err := services.ValidateTCPPort("MinIO console", consolePort); err != nil {
		return "", "", err
	}
	return port, consolePort, nil
}

func minIOPortsFromCommand(cmd *cobra.Command, args []string, flagPort, consolePort string) (string, string, error) {
	_, fallbackConsolePort, err := minIOPorts("", consolePort)
	if err != nil {
		return "", "", err
	}
	port, err := portFromCommand(cmd, args, "port", flagPort, services.MinIODefaultPort, "MinIO")
	if err != nil {
		return "", "", err
	}
	return port, fallbackConsolePort, nil
}

func minIOConfiguredPorts(version string) (string, string, error) {
	apiPort, consolePort, err := minIOPorts("", "")
	if err != nil {
		return "", "", err
	}
	content, err := readRoutaUnit(services.MinIOUnitName(version))
	if err != nil {
		return apiPort, consolePort, nil
	}
	apiPort = routaUnitFlagPort(content, "--address", apiPort)
	consolePort = routaUnitFlagPort(content, "--console-address", consolePort)
	return apiPort, consolePort, nil
}

func minIOStatusHeader(unit, apiPort, consolePort string) string {
	return fmt.Sprintf("%s listens on API %s and console %s", unit, localhostAddr(apiPort), localhostAddr(consolePort))
}

func init() {
	storageListCmd.Flags().BoolVar(&storageListJSON, "json", false, "emit machine-readable JSON")
	storageInstallCmd.Flags().StringVar(&storageInstallPort, "port", "", "MinIO API TCP port")
	storageInstallCmd.Flags().StringVar(&storageInstallConsolePort, "console-port", "", "MinIO console TCP port")
	storageStartCmd.Flags().StringVar(&storageStartPort, "port", "", "MinIO API TCP port")
	storageStartCmd.Flags().StringVar(&storageStartConsolePort, "console-port", "", "MinIO console TCP port")
	storageCmd.AddCommand(storageInstallCmd, storageStartCmd, storageStopCmd, storageStatusCmd, storageProxyCmd, storageListCmd)
	rootCmd.AddCommand(storageCmd)
}
