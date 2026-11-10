package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/services"
	"github.com/scottzirkel/routa/internal/site"
	"github.com/scottzirkel/routa/internal/systemd"
)

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Manage routa search services",
}

var searchInstallPort string
var searchStartPort string

var searchInstallCmd = &cobra.Command{
	Use:   "install <engine> <version> [on <port>]",
	Short: "Write search service unit and prepare its data directory",
	Args:  searchEngineVersionPortArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		engine, version := args[0], args[1]
		port, err := searchPortFromCommand(cmd, args[2:], engine, searchInstallPort)
		if err != nil {
			return err
		}
		if err := ensureSearchService(engine, version, port); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "installed %s on %s\n", searchUnitName(engine, version), localhostAddr(port))
		return nil
	},
}

var searchStartCmd = &cobra.Command{
	Use:   "start <engine> <version> [on <port>]",
	Short: "Write search service unit and start it",
	Args:  searchEngineVersionPortArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		engine, version := args[0], args[1]
		unit := searchUnitName(engine, version)
		port, err := searchPortFromCommand(cmd, args[2:], engine, searchStartPort)
		if err != nil {
			return err
		}
		if portBound(localhostAddr(port)) && !systemd.IsActive(unit) {
			return portInUseError(localhostAddr(port), engine)
		}
		if err := ensureSearchService(engine, version, port); err != nil {
			return err
		}
		if err := systemd.EnableNow(unit); err != nil {
			return fmt.Errorf("start %s: %w", unit, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "started %s on %s\n", unit, localhostAddr(port))
		return nil
	},
}

var searchStopCmd = &cobra.Command{
	Use:   "stop <engine> <version>",
	Short: "Stop and disable a routa search service",
	Args:  searchEngineVersionArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		engine, version := args[0], args[1]
		unit := searchUnitName(engine, version)
		if err := systemd.DisableNow(unit); err != nil {
			return fmt.Errorf("stop %s: %w", unit, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "stopped %s\n", unit)
		return nil
	},
}

var searchStatusCmd = &cobra.Command{
	Use:   "status <engine> <version>",
	Short: "Show routa search service systemd status",
	Args:  searchEngineVersionArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		engine, version := args[0], args[1]
		port, err := searchConfiguredPort(engine, version)
		if err != nil {
			return err
		}
		unit := searchUnitName(engine, version)
		fmt.Fprintln(cmd.OutOrStdout(), searchStatusHeader(unit, port))
		return systemd.RunSystemctl("--user", "status", unit)
	},
}

var searchProxyCmd = &cobra.Command{
	Use:   "proxy <engine> <version> [name]",
	Short: "Proxy a search dashboard/API as <name>.test",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 && len(args) != 3 {
			return fmt.Errorf("usage: %s", cmd.UseLine())
		}
		return searchEngineVersionArgs(cmd, args[:2])
	},
	RunE: func(_ *cobra.Command, args []string) error {
		link, err := searchProxyLink(args)
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

var searchListCmd = &cobra.Command{
	Use:   "list",
	Short: "List routa search services",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		entries, err := searchListEntries()
		if err != nil {
			return err
		}
		if searchListJSON {
			for i := range entries {
				entries[i].Active = systemd.IsActive(entries[i].Unit)
			}
			return writeJSON(cmd.OutOrStdout(), entries)
		}
		if len(entries) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no search services installed. `routa search install meilisearch <version>` or `routa search install typesense <version>`")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "ENGINE\tVERSION\tPORT\tUNIT\tDATA_DIR")
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", e.Engine, e.Version, e.Port, e.Unit, e.DataDir)
		}
		return w.Flush()
	},
}

var searchListJSON bool

func searchListEntries() ([]serviceListEntry, error) {
	entries := []serviceListEntry{}
	meiliInstances, err := services.InstalledMeilisearchInstances()
	if err != nil {
		return nil, err
	}
	for _, i := range meiliInstances {
		port, err := searchConfiguredPort("meilisearch", i.Version)
		if err != nil {
			return nil, err
		}
		entries = append(entries, serviceListEntry{Engine: "meilisearch", Version: i.Version, Port: port, Unit: i.Unit, DataDir: i.DataDir})
	}
	typesenseInstances, err := services.InstalledTypesenseInstances()
	if err != nil {
		return nil, err
	}
	for _, i := range typesenseInstances {
		port, err := searchConfiguredPort("typesense", i.Version)
		if err != nil {
			return nil, err
		}
		entries = append(entries, serviceListEntry{Engine: "typesense", Version: i.Version, Port: port, Unit: i.Unit, DataDir: i.DataDir})
	}
	return entries, nil
}

func searchProxyLink(args []string) (site.Link, error) {
	engine, version := args[0], args[1]
	name := engine
	if len(args) == 3 {
		name = args[2]
	}
	normalized, err := normalizeSiteName(name)
	if err != nil {
		return site.Link{}, err
	}
	port, err := searchConfiguredPort(engine, version)
	if err != nil {
		return site.Link{}, err
	}
	return site.Link{Name: normalized, Target: localhostAddr(port), Secure: true}, nil
}

func searchEngineVersionArgs(_ *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("requires engine and version")
	}
	switch args[0] {
	case "meilisearch":
		return services.ValidateMeilisearchVersion(args[1])
	case "typesense":
		return services.ValidateTypesenseVersion(args[1])
	default:
		return fmt.Errorf("unsupported search engine %q (supported: meilisearch, typesense)", args[0])
	}
}

func searchEngineVersionPortArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 2 && len(args) != 4 {
		return fmt.Errorf("usage: %s", cmd.UseLine())
	}
	return searchEngineVersionArgs(cmd, args[:2])
}

func ensureSearchService(engine, version, port string) error {
	switch engine {
	case "meilisearch":
		return services.EnsureMeilisearchWithPort(version, port)
	case "typesense":
		return services.EnsureTypesenseWithPort(version, port)
	default:
		return fmt.Errorf("unsupported search engine %q (supported: meilisearch, typesense)", engine)
	}
}

func searchPort(engine, port string) (string, error) {
	if port == "" {
		switch engine {
		case "meilisearch":
			port = services.MeilisearchDefaultPort
		case "typesense":
			port = services.TypesenseDefaultPort
		}
	}
	if err := services.ValidateTCPPort(engine, port); err != nil {
		return "", err
	}
	return port, nil
}

func searchPortFromCommand(cmd *cobra.Command, args []string, engine, flagPort string) (string, error) {
	fallback, err := searchPort(engine, "")
	if err != nil {
		return "", err
	}
	return portFromCommand(cmd, args, "port", flagPort, fallback, engine)
}

func searchUnitName(engine, version string) string {
	switch engine {
	case "meilisearch":
		return services.MeilisearchUnitName(version)
	case "typesense":
		return services.TypesenseUnitName(version)
	default:
		return ""
	}
}

func searchConfiguredPort(engine, version string) (string, error) {
	fallback, err := searchPort(engine, "")
	if err != nil {
		return "", err
	}
	unit := searchUnitName(engine, version)
	content, err := readRoutaUnit(unit)
	if err != nil {
		return fallback, nil
	}
	switch engine {
	case "meilisearch":
		return routaUnitFlagPort(content, "--http-addr", fallback), nil
	case "typesense":
		return routaUnitFlagPort(content, "--api-port", fallback), nil
	default:
		return "", fmt.Errorf("unsupported search engine %q (supported: meilisearch, typesense)", engine)
	}
}

func searchStatusHeader(unit, port string) string {
	return fmt.Sprintf("%s listens on %s", unit, localhostAddr(port))
}

func init() {
	searchListCmd.Flags().BoolVar(&searchListJSON, "json", false, "emit machine-readable JSON")
	searchInstallCmd.Flags().StringVar(&searchInstallPort, "port", "", "search service TCP port")
	searchStartCmd.Flags().StringVar(&searchStartPort, "port", "", "search service TCP port")
	searchCmd.AddCommand(searchInstallCmd, searchStartCmd, searchStopCmd, searchStatusCmd, searchProxyCmd, searchListCmd)
	rootCmd.AddCommand(searchCmd)
}
