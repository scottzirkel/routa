package php

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/scottzirkel/hostr/internal/paths"
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
`

// systemd template — %i is the version spec.
const fpmUnitTmpl = `[Unit]
Description=hostr PHP-FPM (%i)
After=network.target

[Service]
Type=simple
ExecStart={{.PHPDir}}/%i/bin/php-fpm --nodaemonize --fpm-config {{.RunDir}}/php-fpm-%i.conf
ExecReload=/bin/kill -USR2 $MAINPID
Restart=on-failure
RestartSec=2
KillSignal=SIGQUIT
TimeoutStopSec=5

[Install]
WantedBy=default.target
`

type fpmTmplData struct {
	RunDir string
	LogDir string
	Spec   string
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
	t := template.Must(template.New("fpm").Parse(fpmConfTmpl))
	dest := filepath.Join(paths.RunDir(), fmt.Sprintf("php-fpm-%s.conf", spec))
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	return t.Execute(f, fpmTmplData{
		RunDir: paths.RunDir(),
		LogDir: paths.LogDir(),
		Spec:   spec,
	})
}

func EnsureSystemdTemplate() error {
	if err := os.MkdirAll(paths.SystemdUserDir(), 0o755); err != nil {
		return err
	}
	t := template.Must(template.New("u").Parse(fpmUnitTmpl))
	dest := filepath.Join(paths.SystemdUserDir(), "hostr-php@.service")
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
