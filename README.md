# hostr

Local web dev server for Linux. PHP + static sites, per-site PHP versions, automatic HTTPS via a local CA. A valet-linux replacement that doesn't fight the resolver.

## Install

```bash
bash install.sh
```

Builds and symlinks `./hostr` into `~/.local/bin/`. Run it as yourself — no `sudo`. Re-running picks up the latest build because the symlink doesn't move. After the first run, `hostr <command>` works from any directory.

If `~/.local/bin` isn't on your `$PATH`, the script tells you and prints the line to add to your shell rc.

## Quick start

```bash
hostr init                      # diagnose host (resolver, valet, required binaries)
hostr install                   # provision alongside valet on alt ports (DNS :1053, :8080/:8443)
hostr php install 8.4           # fetch a static PHP build (Ollama-style)
hostr park ~/code               # any subdir of ~/code becomes <subdir>.test
hostr link                      # link the current dir as <basename>.test
hostr migrate-from-valet        # import your existing valet config

# When ready:
hostr cutover                   # swap onto :80/:443 + route *.test through hostr
hostr cutover --rollback        # reverse it
```

## Daily commands

```
hostr tui                       # interactive dashboard (see Keys below)
hostr status                    # flat table — all sites + resolved settings
hostr open [name]               # xdg-open https://<name>.test (port-aware)
hostr logs <name>               # tail Caddy access + PHP errors for one site
hostr doctor [--probe]          # health check; --probe also HEADs every site
hostr reload                    # re-detect docroots, regen fragments, reload Caddy
hostr restart [unit]            # restart all hostr services or one named unit

hostr php -v                    # run selected hostr PHP for this directory/site
hostr composer install          # run Composer using selected hostr PHP
hostr which-php                 # print selected hostr PHP binary
hostr php list / use / rm
hostr php ini set 8.4 memory_limit 512M
hostr php ini set 8.4 upload_max_filesize 128M
hostr php ini set 8.4 post_max_size 128M
hostr php ext list 8.4
hostr park / unpark / link / unlink / isolate / secure
hostr proxy <name> <port>       # reverse-proxy <name>.test → 127.0.0.1:<port>
hostr version                   # print version, commit, build date
```

## PHP CLI proxies

Like Valet and Herd, hostr keeps browser PHP isolation and shell PHP selection
separate. `hostr isolate <site> <version>` controls PHP-FPM for browser
requests. For terminal commands, use hostr's proxies from inside a project:

```bash
hostr which-php
hostr php artisan test
hostr composer install
```

The proxy resolves the current directory to a hostr site, uses that site's
isolated PHP version when present, and otherwise falls back to `hostr php use`.
If multiple sites point at the same directory with different PHP versions, hostr
fails instead of guessing.

## PHP ini settings

Each installed PHP version can have local ini overrides. Settings are stored in
`~/.config/hostr/php/<version>/php.ini`, rendered into that version's PHP-FPM
pool, and applied by restarting only that PHP-FPM service.

hostr applies Laravel-friendly FPM defaults before user overrides: larger upload
limits, higher `max_input_vars`, realpath cache tuning, and OPcache sized for
framework apps while still validating timestamps for local development.

```bash
hostr php ini set 8.4 memory_limit 512M
hostr php ini set 8.4 upload_max_filesize 128M
hostr php ini set 8.4 post_max_size 128M
hostr php ini show 8.4
hostr php ini edit 8.4
hostr php ini unset 8.4 memory_limit
```

## PHP extensions

The bundled PHP builds use the upstream static-php-cli bulk profile. Extensions
are compiled into the PHP binary, so hostr lists what is available rather than
installing shared modules at runtime:

```bash
hostr php ext list 8.4
```

## Custom docroot

Auto-detection picks Laravel's `public/`, then `dist/`/`out/`/`build/`/`_site/`,
then the dir itself. Override when the heuristic gets it wrong:

```bash
cd ~/code/some-vite-app
hostr link --root dist          # serves dist/ instead of the autodetect's choice
```

## Shell completion

Cobra ships completion for bash/zsh/fish/powershell. Generate and source:

```bash
# zsh — drop into your fpath
mkdir -p ~/.zsh/completion
hostr completion zsh > ~/.zsh/completion/_hostr
# add to ~/.zshrc once: fpath+=~/.zsh/completion && autoload -U compinit && compinit

# bash
hostr completion bash > ~/.local/share/bash-completion/completions/hostr

# fish
hostr completion fish > ~/.config/fish/completions/hostr.fish
```

## Proxying dev servers

For Vite, Next, Astro, Rails, etc. — anything you'd normally hit at `localhost:<port>`:

```bash
npm run dev                     # Vite on :5173
hostr proxy myapp 5173          # myapp.test → 127.0.0.1:5173, with HTTPS + WebSockets
```

Targets accept `5173` (assumed `127.0.0.1:5173`), `:5173`, or `host:5173`. Caddy auto-handles
WebSocket upgrades, so HMR works.

## TUI

`hostr tui` opens a Bubble Tea dashboard with subdomain grouping, live HTTP probes, filters, and per-site actions.

| key | action |
|---|---|
| `j`/`k` or ↑/↓ | navigate |
| `g` / `G` | top / bottom |
| `pgup` / `pgdn` | page |
| `o` / Enter | open the highlighted site in the browser |
| `l` | tail logs for the highlighted site (Caddy access + PHP errors) |
| `r` | re-probe all sites |
| `/` | name search; type, Enter to lock, Esc to clear |
| `s` | cycle HTTPS filter: all → secure → insecure |
| `t` | cycle kind filter: all → php → static → proxy |
| `c` | cycle status filter: all → 2xx → 3xx → 4xx → 5xx → err → pending |
| `m` | toggle missing-docroot only |
| `x` | clear all filters |
| `q` / Ctrl-C | quit |

Layout reflows with the terminal — narrow widths drop KIND, LAT, DOCROOT in priority order. Wide terminals expand NAME and DOCROOT.

Subdomains (`api.affiliate`, `app.affiliate`, …) group under their parent (`affiliate.test`) with tree-style indentation. Missing docroots get a red `✗` prefix.

## Layout

| | |
|---|---|
| `~/.local/share/hostr/` | PHP builds, Caddyfile, site fragments, CA stash |
| `~/.local/state/hostr/` | sockets, logs, fpm runtime config |
| `~/.config/hostr/` | `state.json` (parked dirs, links, default PHP), PHP ini overrides |
| `~/.config/systemd/user/hostr-*.service` | `hostr-dns`, `hostr-caddy`, `hostr-php@<spec>` |

## Stack

- **DNS:** tiny Go responder for `*.test` on `127.0.0.1:1053` (`miekg/dns`). Zero upstream config — answers `127.0.0.1` for `*.test`, NXDOMAIN otherwise.
- **TLS:** Caddy issues from its built-in local CA. Root cert installed into the system trust store via p11-kit's `trust anchor` (so `curl` and Chromium-family browsers trust it).
- **PHP:** musl-static builds from [dl.static-php.dev](https://dl.static-php.dev/static-php-cli/bulk) — Laravel-ready extension set, no glibc dependency, plus hostr's Laravel-friendly FPM ini defaults. Per-version socket via templated systemd unit `hostr-php@<spec>.service`.
- **Routing:** Caddy's `php_fastcgi` for PHP sites (Caddy default `try_files` handles Laravel routing). `file_server` for static.
- **Process management:** systemd user units. hostr itself is a stateless CLI — no daemon.

## How it routes `*.test` after cutover

`browser → systemd-resolved (127.0.0.53) → per-link routing for ~test → 127.0.0.1:1053 (hostr-dns) → 127.0.0.1 → hostr-caddy → site fragment`

The per-link config goes in `/etc/systemd/network/<file>.d/hostr.conf` (one per existing `.network` file). Global routing via `/etc/systemd/resolved.conf.d/` doesn't pin queries to a specific server, so per-link is the only way to reliably route a single domain.

## Uninstall

```bash
hostr cutover --rollback        # if cutover was done
hostr uninstall --purge         # remove services, untrust CA, wipe ~/.local/share/hostr ~/.config/hostr
```
