package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ProjectFile holds a site's workspace settings. It sits in the main
// checkout, next to .routa-env.php.
const ProjectFile = ".routa.json"

// Project is what ProjectFile says.
type Project struct {
	Workspace Settings `json:"workspace"`
}

// Settings is what every new workspace of the site gets.
type Settings struct {
	// Services are routa services that must be running before Run starts.
	Services []string `json:"services,omitempty"`
	// Run lists setup commands that finish and exit, run in order.
	Run []string `json:"run,omitempty"`
	// Actions are commands that keep running for the workspace, by name.
	Actions map[string]string `json:"actions,omitempty"`
}

// LoadProject reads ProjectFile in dir. A missing file is an empty project.
// Unknown keys are refused, so a typo cannot silently drop a setting.
func LoadProject(dir string) (Project, error) {
	path := filepath.Join(dir, ProjectFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Project{}, nil
	}
	if err != nil {
		return Project{}, err
	}
	var p Project
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Project{}, fmt.Errorf("read %s: %w", path, err)
	}
	if err := p.Workspace.validate(); err != nil {
		return Project{}, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

func (s Settings) validate() error {
	for _, svc := range s.Services {
		if strings.TrimSpace(svc) == "" {
			return fmt.Errorf("workspace.services has an empty entry")
		}
	}
	for _, cmd := range s.Run {
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("workspace.run has an empty command")
		}
	}
	for name, cmd := range s.Actions {
		if err := ValidateActionName(name); err != nil {
			return fmt.Errorf("workspace.actions: %w", err)
		}
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("workspace.actions.%s has an empty command", name)
		}
	}
	return nil
}

// ActionNames lists the project's actions in name order.
func (s Settings) ActionNames() []string {
	names := make([]string, 0, len(s.Actions))
	for name := range s.Actions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var actionNameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

// ValidateActionName accepts lowercase letters, digits, and inner dashes, so
// a name fits in a systemd unit name and never contains the "_" that
// separates it from the site name there.
func ValidateActionName(name string) error {
	if !actionNameRE.MatchString(name) {
		return fmt.Errorf("invalid action name %q: use lowercase letters, digits, and dashes, up to 40 characters", name)
	}
	return nil
}
