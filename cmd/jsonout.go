package cmd

import (
	"encoding/json"
	"io"
)

// writeJSON prints v as indented JSON. Read-only commands use it for --json
// so scripts and coding agents never have to parse tables.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// serviceListEntry is one row of `routa db|search|storage list --json`.
// Saved credentials are deliberately left out.
type serviceListEntry struct {
	Engine      string `json:"engine"`
	Version     string `json:"version"`
	Instance    string `json:"instance,omitempty"`
	Port        string `json:"port,omitempty"`
	ConsolePort string `json:"console_port,omitempty"`
	Unit        string `json:"unit"`
	Active      bool   `json:"active"`
	DataDir     string `json:"data_dir"`
}
