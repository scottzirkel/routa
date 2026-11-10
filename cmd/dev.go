package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/scottzirkel/routa/internal/dev"
	"github.com/scottzirkel/routa/internal/site"
)

var (
	devName    string
	devPort    int
	devTimeout time.Duration
)

var devCmd = &cobra.Command{
	Use:   "dev [name] [-- command...]",
	Short: "Run a detected dev server and proxy <name>.test to it",
	Long: `Runs a local dev server, waits for its HTTP port, and registers a routa
reverse proxy so the app is available at https://<name>.test. WebSockets and
HMR flow through the same proxy. When the dev server stops, routa puts the
site back the way it was before.

Without a command, routa detects common project types such as package.json dev
scripts, Rails, Phoenix, and Django. For anything else, pass a command after --.

Inside a PHP site, PHP-FPM keeps serving the site and nothing is proxied.
routa runs 'php artisan dev' when the Laravel app has it, and otherwise the
package.json dev script (usually Vite), using the site's PHP version.`,
	Args: cobra.ArbitraryArgs,
	RunE: runDev,
}

func init() {
	devCmd.Flags().StringVar(&devName, "name", "", "site name to use when passing a custom command without a positional name")
	devCmd.Flags().IntVar(&devPort, "port", 0, "port to proxy instead of auto-detecting")
	devCmd.Flags().DurationVar(&devTimeout, "timeout", 30*time.Second, "how long to wait for the dev server port")
	rootCmd.AddCommand(devCmd)
}

func runDev(_ *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	name := filepath.Base(cwd)
	if devName != "" {
		name = devName
	}

	// Detected dev servers inside a PHP site run beside PHP-FPM. Proxying
	// them would replace the PHP app with Vite's asset server.
	if len(args) == 0 || (len(args) == 1 && devName == "") {
		sites, err := phpSitesForDir(cwd)
		if err != nil {
			return err
		}
		if len(sites) > 0 {
			return runDevBesidePHP(sites)
		}
	}

	var command []string
	var kind string
	defaultPort := 0

	switch {
	case len(args) == 0:
		spec, err := dev.Detect(cwd)
		if err != nil {
			return err
		}
		kind, command, defaultPort = spec.Kind, spec.Command, spec.DefaultPort
	case devName != "":
		command = args
		kind = "custom"
	case len(args) == 1:
		name = args[0]
		spec, err := dev.Detect(cwd)
		if err != nil {
			return err
		}
		kind, command, defaultPort = spec.Kind, spec.Command, spec.DefaultPort
	default:
		name = args[0]
		command = args[1:]
		kind = "custom"
	}

	name, err = normalizeSiteName(name)
	if err != nil {
		return err
	}
	if len(command) == 0 {
		return fmt.Errorf("empty dev command")
	}
	if devPort != 0 {
		defaultPort = devPort
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := devCommand(ctx, cwd, command, os.Environ())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	ports := make(chan int, 8)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", strings.Join(command, " "), err)
	}
	exited := make(chan struct{})
	var procErr error
	go func() {
		procErr = cmd.Wait()
		close(exited)
	}()
	go streamDevOutput(stdout, os.Stdout, ports)
	go streamDevOutput(stderr, os.Stderr, ports)

	port, err := waitForDevPort(ctx, defaultPort, ports, exited, &procErr, devTimeout)
	if err != nil {
		_ = terminateProcess(cmd.Process)
		<-exited
		return err
	}
	target := fmt.Sprintf("127.0.0.1:%d", port)
	previous, err := registerDevProxy(name, target)
	if err != nil {
		_ = terminateProcess(cmd.Process)
		<-exited
		return err
	}
	defer restoreAfterDev(name, target, previous)

	label := kind
	if label == "" {
		label = "dev"
	}
	fmt.Printf("dev %s: https://%s.test → %s (%s)\n", label, name, target, strings.Join(command, " "))
	<-exited
	err = procErr
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// devCommand builds the dev server process. Ctrl+C already reaches the child
// through the terminal, so cancelling interrupts it instead of killing it:
// dev servers clean up on interrupt, and Laravel's Vite plugin deletes
// public/hot then. A leftover hot file points the site at a dead server.
// The child is killed only if it ignores the interrupt.
func devCommand(ctx context.Context, dir string, command, env []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 10 * time.Second
	return cmd
}

// phpSitesForDir returns the PHP sites that serve dir, if any.
func phpSitesForDir(dir string) ([]site.Resolved, error) {
	s, err := site.Load()
	if err != nil {
		return nil, err
	}
	var out []site.Resolved
	for _, r := range s.ResolvePath(dir) {
		if r.Kind == site.KindPHP {
			out = append(out, r)
		}
	}
	return out, nil
}

// runDevBesidePHP runs a PHP site's dev command in the foreground. PHP-FPM
// keeps serving the site, so nothing is proxied and the site config is left
// alone.
func runDevBesidePHP(sites []site.Resolved) error {
	dir := sites[0].Path
	phpCtx, phpErr := currentPHPContext()
	spec, ok := dev.DetectForPHPSite(dir, func() bool {
		return phpCtx != nil && artisanHasCommand(phpCtx, dir, "dev")
	})
	if !ok {
		return fmt.Errorf("%s is a PHP site that routa already serves at %s; found no 'artisan dev' command or package.json dev script to run beside it", dir, siteURLs(sites))
	}

	command := append([]string(nil), spec.Command...)
	env := os.Environ()
	if phpCtx != nil {
		env = envWithRoutaPHP(phpCtx)
	}
	if command[0] == "php" {
		if phpCtx == nil {
			return phpErr
		}
		command[0] = phpCtx.Bin
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := devCommand(ctx, dir, command, env)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("dev %s: %s stays on PHP-FPM; running %s beside it\n", spec.Kind, siteURLs(sites), strings.Join(spec.Command, " "))
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return childExitError{code: exit.ExitCode()}
		}
		return err
	}
	return nil
}

func siteURLs(sites []site.Resolved) string {
	urls := make([]string, 0, len(sites))
	for _, r := range sites {
		scheme := "https"
		if !r.Secure {
			scheme = "http"
		}
		urls = append(urls, fmt.Sprintf("%s://%s.test", scheme, r.Name))
	}
	return strings.Join(urls, ", ")
}

// artisanHasCommand asks the app's artisan for its command list. Booting the
// app can fail (a broken .env, missing vendor/), which reads as "no".
func artisanHasCommand(phpCtx *phpCLIContext, dir, name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, phpCtx.Bin, "artisan", "list", "--raw")
	cmd.Dir = dir
	cmd.Env = envWithRoutaPHP(phpCtx)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return dev.ArtisanListHasCommand(string(out), name)
}

// registerDevProxy points name.test at the dev server and returns the link it
// replaced, if any, so the site can be restored when the server stops.
func registerDevProxy(name, target string) (*site.Link, error) {
	s, err := site.Load()
	if err != nil {
		return nil, err
	}
	previous := findLink(s, name)
	site.AddLink(s, site.Link{Name: name, Target: target, Secure: true})
	return previous, commitAndReload(s, fmt.Sprintf("proxy %s.test → %s", name, target))
}

func restoreAfterDev(name, target string, previous *site.Link) {
	s, err := site.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not restore %s.test: %v\n", name, err)
		return
	}
	msg, changed := restoreDevLink(s, name, target, previous)
	if !changed {
		return
	}
	if err := commitAndReload(s, msg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not restore %s.test: %v\n", name, err)
	}
}

// restoreDevLink undoes registerDevProxy. It leaves the site alone when
// something else changed it while the dev server ran.
func restoreDevLink(s *site.State, name, target string, previous *site.Link) (string, bool) {
	current := findLink(s, name)
	if current == nil || current.Target != target {
		return "", false
	}
	if previous != nil {
		site.AddLink(s, *previous)
		return fmt.Sprintf("restored %s.test to its previous config", name), true
	}
	site.RemoveLink(s, name)
	return fmt.Sprintf("removed dev proxy %s.test", name), true
}

func findLink(s *site.State, name string) *site.Link {
	for _, l := range s.Links {
		if l.Name == name {
			found := l
			return &found
		}
	}
	return nil
}

var portRE = regexp.MustCompile(`(?i)(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])(?:[^\d\n]*):([1-9][0-9]{1,4})|(?:^|\s):([1-9][0-9]{1,4})`)

func streamDevOutput(src io.Reader, dst *os.File, ports chan<- int) {
	scanner := bufio.NewScanner(src)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(dst, line)
		if m := portRE.FindStringSubmatch(line); len(m) == 3 {
			portText := m[1]
			if portText == "" {
				portText = m[2]
			}
			if p, err := strconv.Atoi(portText); err == nil && p <= 65535 {
				select {
				case ports <- p:
				default:
				}
			}
		}
	}
}

func waitForDevPort(ctx context.Context, defaultPort int, ports <-chan int, exited <-chan struct{}, procErr *error, timeout time.Duration) (int, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()

	candidates := []int{}
	if defaultPort != 0 {
		candidates = append(candidates, defaultPort)
	}
	seen := map[int]bool{}
	for _, p := range candidates {
		seen[p] = true
	}

	for {
		for _, port := range candidates {
			if canDialLocal(port) {
				return port, nil
			}
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-exited:
			if *procErr != nil {
				return 0, fmt.Errorf("dev command exited before opening a port: %w", *procErr)
			}
			return 0, fmt.Errorf("dev command exited before opening a port")
		case <-deadline.C:
			if defaultPort == 0 {
				return 0, fmt.Errorf("timed out waiting for a dev server port; pass --port when the command does not print one")
			}
			return 0, fmt.Errorf("timed out waiting for dev server on 127.0.0.1:%d", defaultPort)
		case port := <-ports:
			if !seen[port] {
				candidates = append(candidates, port)
				seen[port] = true
			}
		case <-tick.C:
		}
	}
}

func canDialLocal(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func terminateProcess(p *os.Process) error {
	if p == nil {
		return nil
	}
	if err := p.Signal(os.Interrupt); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	return p.Kill()
}
