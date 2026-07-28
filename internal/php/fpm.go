package php

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/template"

	"github.com/scottzirkel/routa/internal/paths"
	"github.com/scottzirkel/routa/internal/site"
	"github.com/scottzirkel/routa/internal/systemd"
)

// fpm config — one per spec (the spec is what the user wrote: "8.3" or "8.3.30")
// so socket/log/pid paths don't collide between the major.minor instance and
// the patch instance, even though the binary is shared via symlink.
const fpmConfTmpl = `[global]
pid = {{.RunDir}}/php-fpm-{{.Spec}}.pid
error_log = {{.LogDir}}/php-fpm-{{.Spec}}.log
daemonize = no

[www]
listen = {{.RunDir}}/php-fpm-{{.Spec}}.sock
listen.mode = 0666
pm = ondemand
pm.max_children = 16
pm.process_idle_timeout = 60s
pm.max_requests = 500
clear_env = no
catch_workers_output = yes
decorate_workers_output = no
{{range .PoolINISettings}}php_admin_value[{{.Key}}] = {{.Value}}
{{end}}
{{range .SitePools}}
[{{.PoolName}}]
listen = {{.SocketPath}}
listen.mode = 0666
pm = ondemand
pm.max_children = 16
pm.process_idle_timeout = 60s
pm.max_requests = 500
clear_env = no
catch_workers_output = yes
decorate_workers_output = no
{{range $.PoolINISettings}}php_admin_value[{{.Key}}] = {{.Value}}
{{end}}
{{end}}
`

// systemd template — %i is the version spec.
const fpmUnitTmpl = `[Unit]
Description=routa PHP-FPM (%i)
After=network.target

[Service]
Type=simple
ExecStart={{.PHPDir}}/%i/bin/php-fpm --nodaemonize --php-ini {{.RunDir}}/php-fpm-%i.ini --fpm-config {{.RunDir}}/php-fpm-%i.conf
ExecReload=/bin/kill -USR2 $MAINPID
Restart=on-failure
RestartSec=2
KillSignal=SIGQUIT
TimeoutStopSec=5

[Install]
WantedBy=default.target
`

type fpmTmplData struct {
	RunDir          string
	LogDir          string
	Spec            string
	PoolINISettings []INISetting
	SitePools       []SitePool
}

type SitePool struct {
	PoolName   string
	SocketPath string
}

type unitTmplData struct {
	PHPDir string
	RunDir string
}

func WriteFPMConfig(spec string) error {
	if err := os.MkdirAll(paths.RunDir(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.LogDir(), 0o755); err != nil {
		return err
	}
	settings, err := RenderedINISettings(spec, SAPIFPM)
	if err != nil {
		return err
	}
	iniPath := filepath.Join(paths.RunDir(), fmt.Sprintf("php-fpm-%s.ini", spec))
	if err := writeINISettingsFile(iniPath, settings); err != nil {
		return err
	}
	sitePools, err := sitePoolsForSpec(spec)
	if err != nil {
		return err
	}
	t := template.Must(template.New("fpm").Parse(fpmConfTmpl))
	dest := filepath.Join(paths.RunDir(), fmt.Sprintf("php-fpm-%s.conf", spec))
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	return t.Execute(f, fpmTmplData{
		RunDir:          paths.RunDir(),
		LogDir:          paths.LogDir(),
		Spec:            spec,
		PoolINISettings: poolINISettings(settings),
		SitePools:       sitePools,
	})
}

// poolINISettings drops the extension-loading keys. FPM pools apply these as
// php_admin_value, which runs long after module startup, so extension= and
// zend_extension= only work from the master php.ini WriteFPMConfig renders.
func poolINISettings(settings []INISetting) []INISetting {
	out := make([]INISetting, 0, len(settings))
	for _, setting := range settings {
		if setting.Key == ZendExtensionKey || setting.Key == ExtensionKey {
			continue
		}
		out = append(out, setting)
	}
	return out
}

func RefreshFPMConfigsForSites(sites []site.Resolved) error {
	specs := map[string]bool{}
	for _, resolved := range sites {
		if resolved.Kind == site.KindPHP && resolved.PHP != "" {
			specs[resolved.PHP] = true
		}
	}
	for spec := range specs {
		if err := WriteFPMConfig(spec); err != nil {
			return err
		}
		unit := fmt.Sprintf("routa-php@%s.service", spec)
		if systemd.IsActive(unit) {
			if err := systemd.RunSystemctl("--user", "reload", unit); err != nil {
				return fmt.Errorf("reload %s: %w", unit, err)
			}
		}
	}
	return nil
}

func sitePoolsForSpec(spec string) ([]SitePool, error) {
	st, err := site.Load()
	if err != nil {
		return nil, err
	}
	var pools []SitePool
	for _, resolved := range st.Resolve() {
		if resolved.Kind != site.KindPHP || resolved.PHP != spec {
			continue
		}
		pools = append(pools, SitePool{
			PoolName:   "routa-" + resolved.Name,
			SocketPath: site.FPMSocketPath(resolved),
		})
	}
	sort.Slice(pools, func(i, j int) bool { return pools[i].PoolName < pools[j].PoolName })
	return pools, nil
}

func EnsureSystemdTemplate() error {
	if err := os.MkdirAll(paths.SystemdUserDir(), 0o755); err != nil {
		return err
	}
	t := template.Must(template.New("u").Parse(fpmUnitTmpl))
	dest := filepath.Join(paths.SystemdUserDir(), "routa-php@.service")
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	return t.Execute(f, unitTmplData{
		PHPDir: paths.PHPDir(),
		RunDir: paths.RunDir(),
	})
}

func SocketPath(spec string) string {
	return filepath.Join(paths.RunDir(), fmt.Sprintf("php-fpm-%s.sock", spec))
}
