# routa

![routa local web dev server for Linux](assets/media/routa-header.webp)

routa is a Linux local-development server for PHP, static, and proxied
dev-server projects under `.test` domains with local HTTPS.

It gives you a fast terminal workflow for local sites: track a directory, link
a project, ignore stale tracked sites, pin PHP versions per site, proxy
frontend dev servers, and inspect the
whole stack with one doctor command. routa uses Caddy, systemd user services,
systemd-resolved, and static PHP builds instead of running its own long-lived
daemon.

## What routa manages

- `.test` DNS through a local DNS responder
- HTTPS through Caddy's local CA
- PHP-FPM per installed PHP version
- Per-site PHP isolation for browser requests
- PHP and Composer CLI proxies that use the right project PHP
- Static sites and reverse proxies for frontend dev servers
- systemd user services for Caddy, DNS, and PHP-FPM

## Platform Support

routa targets Linux desktops with systemd user services and systemd-resolved.
It serves local sites under `.test`, binds Caddy to localhost, and manages PHP
through static builds under `~/.local/share/routa/php/`.

Intentionally out of scope: a GUI app, automatic in-place binary updates, macOS
support, non-systemd init systems, and arbitrary local TLDs. GitHub releases are
currently source/tag releases only; build locally with `git pull && bash
install.sh` until a binary artifact policy is chosen.

## Install

Install from a cloned checkout:

```bash
git clone https://github.com/scottzirkel/routa.git
cd routa
bash install.sh
```

The script builds routa and symlinks `./routa` into `~/.local/bin/`. Run it as
yourself, not with `sudo`. Re-running picks up the latest local build because
the symlink does not move. After the first run, `routa <command>` works from any
directory.

If `~/.local/bin` is not on your `$PATH`, the script tells you and prints the
line to add to your shell rc.

## Quick start

```bash
routa init                      # diagnose host resolver and required binaries
routa install                   # provision services on alt ports (DNS :1053, :8080/:8443)
routa php install 8.4           # fetch a static PHP build
routa track ~/code              # any subdir of ~/code becomes <subdir>.test
routa track ~/apps --root dist  # every child serves its own dist/ dir
routa link                      # link the current dir as <basename>.test

# When ready:
routa cutover                   # swap onto :80/:443 + route *.test through routa
routa cutover --rollback        # reverse it
```

## Daily commands

```
routa                           # interactive dashboard (see TUI below)
routa status                    # flat table — all sites + resolved settings
routa open [name]               # xdg-open https://<name>.test (port-aware)
routa logs <name>               # tail Caddy access + PHP errors for one site
routa doctor [--probe] [--json] # health check; --probe also HEADs every site
routa reload                    # re-detect docroots, regen fragments, reload Caddy
routa restart [unit]            # restart all routa services or one named unit

routa php -v                    # run selected routa PHP for this directory/site
routa composer install          # run Composer using selected routa PHP
routa which-php                 # print selected routa PHP binary
routa php list / use / rm
routa php ini set 8.4 memory_limit 512M
routa php ini set 8.4 upload_max_filesize 128M
routa php ini set 8.4 post_max_size 128M
routa php ext list 8.4
routa track / untrack / ignore / unignore / link / unlink / alias / unalias / isolate / secure
routa proxy <name> <port>       # reverse-proxy <name>.test → 127.0.0.1:<port>
routa dev [name]                # run a detected dev server and proxy it
routa version                   # print version, commit, build date
```

`park` and `unpark` are aliases for `track` and `untrack` for Valet users.

## Health checks

`routa doctor` checks user services, Caddy ports, routa DNS, and the detected
cutover phase. Add `--probe` to send a HEAD request to every configured site.

For scripts and bug reports, `routa doctor --json` emits a stable top-level
shape:

```json
{
  "services": [],
  "network": {},
  "dns": {},
  "cutover": {},
  "site_probes": []
}
```

`site_probes` is omitted unless `--probe` is used.

## PHP CLI proxies

routa keeps browser PHP isolation and shell PHP selection separate.
`routa isolate <site> <version>` controls PHP-FPM for browser
requests. For terminal commands, use routa's proxies from inside a project:

```bash
routa which-php
routa php artisan test
routa composer install
```

The proxy resolves the current directory to a routa site, uses that site's
isolated PHP version when present, and otherwise falls back to `routa php use`.
If multiple sites point at the same directory with different PHP versions, routa
fails instead of guessing.

## PHP ini settings

Each installed PHP version can have local ini overrides. Settings are stored in
`~/.config/routa/php/<version>/php.ini`, rendered into that version's PHP-FPM
pool, and applied by restarting only that PHP-FPM service.

routa applies Laravel-friendly FPM defaults before user overrides: larger upload
limits, higher `max_input_vars`, realpath cache tuning, and OPcache sized for
framework apps while still validating timestamps for local development.

```bash
routa php ini set 8.4 memory_limit 512M
routa php ini set 8.4 upload_max_filesize 128M
routa php ini set 8.4 post_max_size 128M
routa php ini show 8.4
routa php ini edit 8.4
routa php ini unset 8.4 memory_limit
```

## PHP extensions

The bundled PHP builds use the upstream static-php-cli bulk profile. Extensions
are compiled into the PHP binary, so routa lists what is available rather than
installing shared modules at runtime:

```bash
routa php ext list 8.4
```

## Custom docroot

Auto-detection picks Laravel's `public/`, static `public/`, then
`dist/`/`out/`/`build/`/`_site/`, then the dir itself. Override when the
heuristic gets it wrong:

```bash
cd ~/code/some-vite-app
routa link --root dist          # serves dist/ instead of the autodetect's choice
```

Tracked directories can also apply the same root override to every immediate
child:

```bash
routa track ~/apps --root dist  # app.test serves ~/apps/app/dist
```

## Site aliases

Use aliases when several `.test` names should serve the same site config:

```bash
routa alias app api             # api.test uses app.test's source/proxy/PHP config
routa unalias api
```

Aliases follow the target site when its root, proxy target, PHP version, or
HTTPS setting changes.

## Shell completion

Cobra ships completion for bash/zsh/fish/powershell. Generate and source:

```bash
# zsh — drop into your fpath
mkdir -p ~/.zsh/completion
routa completion zsh > ~/.zsh/completion/_routa
# add to ~/.zshrc once: fpath+=~/.zsh/completion && autoload -U compinit && compinit

# bash
routa completion bash > ~/.local/share/bash-completion/completions/routa

# fish
routa completion fish > ~/.config/fish/completions/routa.fish
```

## Proxying dev servers

For Vite, Next, Astro, Rails, etc. — anything you'd normally hit at `localhost:<port>`:

```bash
npm run dev                     # Vite on :5173
routa proxy myapp 5173          # myapp.test → 127.0.0.1:5173, with HTTPS + WebSockets
```

Targets accept `5173` (assumed `127.0.0.1:5173`), `:5173`, or `host:5173`. Caddy auto-handles
WebSocket upgrades, so HMR works.

## Running dev servers

For process-backed apps, `routa dev` starts the app's normal dev server, waits
for the port, and registers the same WebSocket-friendly reverse proxy:

```bash
cd ~/code/myapp
routa dev                       # package.json dev, Rails, Phoenix, or Django
routa dev api                   # serve as api.test instead of the directory name
routa dev reverb --port 8080 -- php artisan reverb:start --host=127.0.0.1 --port=8080
routa dev --name custom --port 3000 -- ./scripts/start-web
```

Detected defaults include package manager `dev` scripts, Rails on :3000,
Phoenix on :4000, and Django on :8000. Pass `--port` for commands that do not
print or bind a predictable port.

## TUI

`routa` opens a Bubble Tea dashboard with subdomain grouping, live HTTP probes,
health status, log previews, filters, sorting, collapsible groups, and per-site
actions. `routa tui` remains as a hidden compatibility alias.

| key | action |
|---|---|
| `j`/`k` or ↑/↓ | navigate |
| `g` / `G` | top / bottom |
| `pgup` / `pgdn` | page |
| `o` / Enter | open the highlighted site in the browser |
| `l` | tail logs for the highlighted site (Caddy access + PHP errors) |
| `r` | reload state and re-probe all sites |
| `a` | toggle auto-refresh |
| `z` | cycle sort: name → problems → latency → kind |
| Space | collapse / expand the selected parent group |
| `/` | name search; type, Enter to lock, Esc to clear |
| `s` | cycle HTTPS filter: all → secure → insecure |
| `t` | cycle kind filter: all → php → static → proxy |
| `c` | cycle status filter: all → 2xx → 3xx → 4xx → 5xx → err → pending |
| `m` | toggle missing-docroot only |
| `!` | toggle problems-only view |
| `u` | unlink the highlighted explicit link after confirmation |
| `S` | toggle HTTPS for the highlighted explicit link after confirmation |
| `R` | change or clear the highlighted site's root override |
| `?` | show the full keymap |
| `x` | clear all filters |
| `q` / Ctrl-C | quit |

Layout reflows with the terminal — narrow widths drop KIND, LAT, DOCROOT in
priority order. Wide terminals split into a site table and selected-site
inspector.

Subdomains (`api.affiliate`, `app.affiliate`, …) group under their parent
(`affiliate.test`) with tree-style indentation. Missing docroots get a red `✗`
prefix.

## Layout

| | |
|---|---|
| `~/.local/share/routa/` | PHP builds, Caddyfile, site fragments, CA stash |
| `~/.local/state/routa/` | sockets, logs, fpm runtime config |
| `~/.config/routa/` | `state.json` (versioned tracked dirs, tracked roots, ignored sites, links, aliases, default PHP), PHP ini overrides |
| `~/.config/systemd/user/routa-*.service` | `routa-dns`, `routa-caddy`, `routa-php@<spec>` |

## State file compatibility

`~/.config/routa/state.json` is versioned. Current routa writes `version: 4`.
Pre-version state files are treated as the legacy v1 shape and migrated on the
next save. If a future routa writes a newer state version, older binaries fail
instead of guessing how to interpret it.

## Stack

- **DNS:** tiny Go responder for `*.test` on `127.0.0.1:1053` (`miekg/dns`). Zero upstream config — answers `127.0.0.1` for `*.test`, NXDOMAIN otherwise.
- **TLS:** Caddy issues from its built-in local CA. Root cert installed into the system trust store via p11-kit's `trust anchor` (so `curl` and Chromium-family browsers trust it).
- **PHP:** musl-static builds from [dl.static-php.dev](https://dl.static-php.dev/static-php-cli/bulk) — Laravel-ready extension set, no glibc dependency, plus routa's Laravel-friendly FPM ini defaults. Per-version socket via templated systemd unit `routa-php@<spec>.service`.
- **Routing:** Caddy's `php_fastcgi` for PHP sites (Caddy default `try_files` handles Laravel routing). `file_server` for static.
- **Process management:** systemd user units. routa itself is a stateless CLI — no daemon.

## How it routes `*.test` after cutover

`browser → systemd-resolved (127.0.0.53) → per-link routing for ~test → 127.0.0.1:1053 (routa-dns) → 127.0.0.1 → routa-caddy → site fragment`

The per-link config goes in `/etc/systemd/network/<file>.d/routa.conf` (one per existing `.network` file). Global routing via `/etc/systemd/resolved.conf.d/` doesn't pin queries to a specific server, so per-link is the only way to reliably route a single domain.

Cutover requires at least one `/etc/systemd/network/*.network` file. `routa
cutover` refuses to run its sudo block if no `.network` files exist, before
changing resolver or port settings. If your machine uses NetworkManager without
systemd-networkd `.network` files, stay on Phase 1 or add a networkd-managed
link before running cutover.

## Troubleshooting

- **A site does not resolve:** run `routa doctor`. In Phase 1, query routa DNS
  directly with `routa query app.test`; system-wide `.test` routing only happens
  after `routa cutover`. `routa doctor` shows the DNS answer, expected answer,
  and any raw query output when routa-dns does not return an A record.
- **Caddy is not on the expected port:** run `routa restart caddy` and then
  `routa doctor`. If the cutover phase is partial, re-run `routa cutover` or
  `routa cutover --rollback` to converge. If HTTPS ports are bound while
  `routa-caddy` is inactive, `routa doctor` calls that out as a likely port
  ownership conflict.
- **Rollback resolver behavior:** `routa cutover --rollback` removes routa's
  per-link routing. The sudo rollback block restores `/etc/resolv.conf` to a
  detected legacy local-dev resolver when one exists; otherwise it restores
  systemd-resolved's stub resolver.
- **A PHP site returns 503:** install or select a PHP version with
  `routa php install <ver>` and `routa php use <ver>`, or isolate the site with
  `routa isolate <site> <ver>`.
- **Certificates are not trusted:** re-run `routa install` to reinstall the
  local CA. If it fails, the error names the Caddy root path and the failed
  `trust anchor` action. Confirm p11-kit is installed and restart browsers that
  cache trust state.
## Uninstall

```bash
routa cutover --rollback        # if cutover was done
routa uninstall --purge         # remove services, untrust CA, wipe routa state/data/config
```

`--purge` deletes routa-owned XDG directories named `routa`
(`~/.local/share/routa`, `~/.local/state/routa`, and `~/.config/routa`). It does
not delete your website/project directories referenced by tracked dirs or links.
