<h1 align="center">
  <img src="assets/media/logo-full.svg" alt="routa" width="520">
</h1>

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
- Optional Redis and Mailpit user services for local app dependencies
- systemd user services for Caddy, DNS, and PHP-FPM

## Platform Support

routa targets Linux desktops with systemd user services and systemd-resolved.
It serves local sites under `.test`, binds Caddy to localhost, and manages PHP
through static builds under `~/.local/share/routa/php/`.

Intentionally out of scope: a GUI app, automatic in-place binary updates, macOS
support, non-systemd init systems, and arbitrary local TLDs. Tagged GitHub
releases include Linux binary archives for package managers, and cloned
checkouts can still build locally with `git pull && bash install.sh`.

## Install

On Arch-based systems, install the binary package after it is published to AUR:

```bash
paru -S routa-bin
```

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
routa track ~/code              # any subdir becomes <subdir>.test and *.<subdir>.test
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
routa status --json             # agent-friendly JSON list of resolved sites
routa list                      # alias for routa status
routa open [name]               # xdg-open https://<name>.test (port-aware)
routa logs <name>               # tail Caddy access + PHP errors for one site
routa doctor [--probe] [--json] # health check; --probe also HEADs every site
routa reload                    # re-detect docroots, regen fragments, reload Caddy
routa restart [unit]            # restart all routa services or one named unit
routa restart php [version]     # restart all PHP-FPM units, or one PHP version

routa php -v                    # run selected routa PHP for this directory/site
routa composer install          # run Composer using selected routa PHP
routa which-php                 # print selected routa PHP binary
routa php list / use / rm
routa php ini show [8.4] / path [8.4] / edit [8.4]
routa php ini set 8.4 memory_limit 512M
routa php ini set 8.4 upload_max_filesize 128M
routa php ini set 8.4 post_max_size 128M
routa php ext list [8.4]
routa php xdebug install [8.4] / on [8.4] / off [8.4] / status [8.4]
routa php pcov install [8.4] / on [8.4] / off [8.4] / status [8.4]
routa php shim install / uninstall / status
routa track / untrack / ignore / unignore / link / unlink / alias / unalias / isolate / secure
routa proxy <name> <port>       # reverse-proxy <name>.test → 127.0.0.1:<port>
routa dev [name]                # run a detected dev server and proxy it
routa redis start [--port 6380] / stop / restart / status
routa mail start on 8026 --smtp-port 1026 / stop / restart / status / proxy
routa db start mariadb 11.4 on 3307
routa db start mysql 8.0 affiliate-platform on 3309 --user affiliate --password secret
routa db credentials mysql 8.0 affiliate-platform --user affiliate --password new-secret
routa db start postgres 16 on 5433
routa search start meilisearch 1.12 on 7701
routa search start typesense 28 on 8109
routa storage start minio RELEASE.2026-05-01T00-00-00Z on 9002 --console-port 9003
routa version                   # print version, commit, build date
```

`park` and `unpark` are aliases for `track` and `untrack` for Valet users.

## Health checks

`routa doctor` checks user services, Caddy ports, routa DNS, and the detected
cutover state. Add `--probe` to send a HEAD request to every configured site.

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
routa php ini show
routa php ini edit
routa php ini unset 8.4 memory_limit
```

`show`, `path`, and `edit` infer the current/default PHP version when no
version is provided. `set` and `unset` keep the version explicit so key/value
arguments stay unambiguous.

## Per-site environment

For PHP sites, routa keeps each site on its own PHP-FPM socket:

```bash
APP_ENV=local
DB_DATABASE=my_app
```

Routa does not copy project `.env` values into PHP-FPM config. Your framework
or application reads its own `.env`, so editing values such as `DB_DATABASE`
does not require `routa reload`.

For frameworks that need explicit server variables, add `.routa-env.php` to
the project root:

```php
<?php

return [
    '*' => [
        'WEBSITE_NAME' => 'Local',
    ],

    'app' => [
        'APP_CONTEXT' => 'primary',
    ],
];
```

Routa reads this file on each PHP request and copies matching entries into
`$_SERVER`. The `*` entry applies to every site served from the project, and
the site-name entry, such as `app` for `app.test`, overrides or extends it.
For Valet migration compatibility, `.valet-env.php` uses the same format when
`.routa-env.php` is absent.

## PHP extensions

The bundled PHP builds use the upstream static-php-cli `gnu-bulk` profile.
Most extensions are compiled into the PHP binary, so routa lists what is
available:

```bash
routa php ext list
```

When no version is provided, routa uses the PHP version selected for the current
directory or the default PHP version. Pass an explicit version, such as
`routa php ext list 8.4`, to inspect a different installation.

## Xdebug

`routa php install <version>` installs a Routa-managed Xdebug shared extension
next to the exact PHP version when an artifact is published for that PHP
release. Xdebug defaults to off, and toggling later only edits Routa's
per-version ini plus restarts that PHP-FPM service:

```bash
routa php xdebug install
routa php xdebug on
routa php xdebug status
routa php xdebug off
```

`on` defaults to Xdebug 3 settings: `xdebug.mode=debug,develop`,
`xdebug.start_with_request=yes`, `xdebug.client_host=127.0.0.1`, and
`xdebug.client_port=9003`. When no version is provided, routa uses the PHP
version selected for the current directory or the default PHP version. Pass an
explicit version, such as `routa php xdebug on 8.4`, to manage a different
installation. `status` reports the managed `zend_extension` path when Xdebug is
available. `routa php xdebug install [version]` can fetch the extension later
without reinstalling PHP if the artifact was not available during the original
PHP install.

Note that the default mode deliberately leaves out `coverage` — routa hands code
coverage to pcov instead. See below.

## pcov

`routa php install <version>` also installs a Routa-managed pcov shared
extension, the low-overhead code coverage driver PHPUnit prefers over Xdebug.
Unlike Xdebug it defaults to **on**, because a loaded pcov costs nothing until a
coverage run starts it:

```bash
routa php pcov status
routa php pcov off
routa php pcov on
```

pcov is enabled for the CLI and pinned off for PHP-FPM. Coverage is a test
runner concern, and in FPM pcov would index every request's files for nothing —
so `routa php` and `routa composer` get `pcov.enabled=1` while the FPM pools get
`pcov.enabled=0` from the same per-version ini. Pass `--fpm` to `routa php pcov
on` to enable it everywhere, or set the value by hand with `routa php ini set
8.4 pcov.enabled 1` to override both defaults.

Because PHPUnit selects pcov ahead of Xdebug whenever both are loaded, running
`routa php vendor/bin/phpunit --coverage-text` picks up pcov with no further
configuration. Turning pcov off hands coverage back to Xdebug, which then needs
`coverage` added to `xdebug.mode`. Restrict what pcov scans per project with
`pcov.directory` in `phpunit.xml` rather than routa's ini — routa's is shared by
every site on that PHP version.

### Shared extensions need a dynamically-linked CLI

The upstream `gnu-bulk` PHP builds link glibc **statically**, and Linux cannot
`dlopen` into such a binary. Those builds therefore cannot load pcov, Xdebug, or
any other shared extension no matter how php.ini is configured — the `.so` is
installed and simply never loads.

`routa php pcov status` reports this directly:

```
loadable   false
cli        ~/.local/share/routa/php/8.4/bin/php
```

To fix it, put a dynamically-linked CLI at `bin/php-dynamic` beside the stock
`bin/php` for that version. routa prefers `php-dynamic` for `routa php` and
`routa composer` whenever it exists, and `routa php install` never overwrites
it, so it survives reinstalls. `php-fpm` is untouched: it keeps serving sites
from the stock static binary, which is also why pcov defaults to CLI-only.

Build one with static-php-cli, matching the extension set in the channel's
`README.txt` so the CLI and FPM binaries agree:

```bash
git clone --depth=1 --branch 2.8.5 https://github.com/crazywhalecc/static-php-cli.git
cd static-php-cli
bin/spc-gnu-docker download --with-php=8.4.20 --for-extensions="<exts>,pcov" --prefer-pre-built
bin/spc-gnu-docker build "<exts>" --build-cli --build-shared=pcov
cp buildroot/bin/php ~/.local/share/routa/php/8.4.20/bin/php-dynamic
```

## Making scripts and editors use routa's PHP

`routa php` and `routa composer` are proxies: they only apply when you type
`routa` first. A shell alias such as `alias php="routa php"` looks like it fixes
that, but aliases exist only inside an interactive shell. Anything with a
`#!/usr/bin/env php` line — `vendor/bin/pest`, `vendor/bin/phpunit`, `composer`,
your editor's test runner — never sees it and silently runs whatever `php` your
`PATH` finds first, usually the system PHP with a different version and
extension set than the one serving your sites.

`routa php shim install` writes a real `php` executable into `~/.local/bin` that
forwards to `routa php`, so those scripts resolve to the same PHP routa runs:

```bash
routa php shim install
routa php shim status
```

Because it is a file rather than an alias, it applies everywhere — scripts,
editors, subprocesses. It still resolves the version per directory, so a site
pinned to 8.3 gets 8.3. Once installed, an `alias php="routa php"` is redundant.

`status` reports which `php` scripts will actually run and warns when something
earlier on your `PATH` shadows the shim. `uninstall` removes it, and neither
command touches a `php` that routa did not create unless you pass `--force`.

## Custom docroot

Auto-detection picks PHP `public/`, PHP at the root, static `public/`, then
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

Routa serves wildcard subdomains for each site. If `app.test` exists,
`api.app.test` and any other `*.app.test` host route to the same site unless
that subdomain is explicitly linked or aliased.

## Site aliases

Use aliases when several `.test` names should serve the same site config:

```bash
routa alias app api             # api.test uses app.test's source/proxy/PHP config
routa unalias api
```

Aliases follow the target site when its root, proxy target, PHP version, or
HTTPS setting changes.

## Valet-compatible drivers

Routa supports Valet-style PHP drivers for unusual project layouts. Put a
project-specific `LocalValetDriver.php` in the project root, or global drivers
named `*ValetDriver.php` in `~/.config/routa/Drivers/`. During migration,
Routa also checks Valet's `~/.config/valet/Drivers/` directory.

Drivers use Valet's method contract:

```php
serves($sitePath, $siteName, $uri)
isStaticFile($sitePath, $siteName, $uri)
frontControllerPath($sitePath, $siteName, $uri)
```

`LocalValetDriver.php` can make a project a PHP site even without a detected
`index.php`. Global drivers are used for sites Routa already detects as PHP.
Routa's built-in detector remains the fallback behavior.

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

### Vite TLS certificates

Vite integrations that auto-detect local TLS often look in Valet or Herd
certificate directories. routa uses Caddy's local CA instead, so point Vite at
the Caddy-issued certificate for your `.test` host:

```js
import fs from 'node:fs'
import { homedir } from 'node:os'
import { defineConfig } from 'vite'

const host = 'myapp.test'
const certDir = `${homedir()}/.local/share/caddy/certificates/local/${host}`

export default defineConfig({
  server: {
    host,
    hmr: { host },
    https: {
      key: fs.readFileSync(`${certDir}/${host}.key`),
      cert: fs.readFileSync(`${certDir}/${host}.crt`),
    },
  },
})
```

The Caddy root certificate lives at
`~/.local/share/caddy/pki/authorities/local/root.crt` and is installed into the
system trust store by `routa install`. routa asks Caddy's internal issuer for
per-site certificates that are valid for 396 days, with a 730-day local
intermediate certificate.

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

## Redis

routa can manage a local Redis instance as a systemd user service:

```bash
routa redis start
routa redis start --port 6380
routa redis start on 6380
routa redis status
routa redis stop
```

`routa redis start` expects `redis-server` to be installed by your system
package manager. It writes `routa-redis.service`, stores Redis data under
`~/.local/share/routa/services/redis/`, and binds Redis to localhost on
`:6379` by default. Pass `--port` or `on <port>` to avoid an existing
Redis-compatible service such as Valkey.

## Mailpit

routa can manage Mailpit as a systemd user service:

```bash
routa mail start
routa mail start --port 8026 --smtp-port 1026
routa mail start on 8026 --smtp-port 1026
routa mail proxy        # mail.test -> 127.0.0.1:8025
routa mail proxy inbox  # inbox.test -> configured Mailpit web port
routa mail status
routa mail stop
```

`routa mail start` expects `mailpit` to be installed by your system package
manager. It writes `routa-mailpit.service`, stores Mailpit's persistent
database under `~/.local/share/routa/services/mailpit/`, binds the web UI to
`127.0.0.1:8025`, and binds SMTP to `127.0.0.1:1025` by default. `routa mail
start`, `restart`, and `status` print both configured listen addresses. `routa
mail proxy` reads the configured web UI port, so custom `--port` values are
reflected in the generated `.test` proxy.

For project-specific inboxes, use Mailpit tags. Mailpit applies tags from the
`X-Tags` message header and from plus-addresses such as
`app+checkout@example.test`; those tags can be filtered in the Mailpit UI. A
named proxy such as `routa mail proxy checkout-mail` gives the filtered Mailpit
workflow a stable `.test` URL while keeping a single Mailpit service and SMTP
port.

## Databases

routa can manage local MariaDB, MySQL, and Postgres instances as systemd user
services. MySQL installs a routa-owned server runtime on demand:

```bash
routa db install mariadb 11.4
routa db start mariadb 11.4 --port 3307
routa db start mariadb 11.4 on 3307
routa db status mariadb 11.4
routa db stop mariadb 11.4

routa db install mysql 8.0
routa db start mysql 8.0 on 3309
routa db start mysql 8.0 affiliate-platform on 3310 --user affiliate --password secret
routa db credentials mysql 8.0 affiliate-platform --user affiliate --password new-secret

routa db install postgres 16
routa db start postgres 16 on 5433
routa db start postgres 16 reporting on 5434
routa db list
```

Database services are version-isolated and run as systemd user services, so
they do not start, stop, or write into system database services such as
`mysql.service` or `/var/lib/mysql`. For MySQL, routa downloads Oracle's generic
Linux server archive into `~/.local/share/routa/binaries/mysql/` and uses that
routa-owned `mysqld` before considering anything from the system. If the MySQL
runtime needs OS shared libraries, routa prints the package command to install
them. For other engines, routa searches common versioned binary names and paths
first (`postgres-16`, `/usr/lib/postgresql/16/bin/postgres`, `mariadbd-11.4`,
and similar), then falls back to the unversioned binary only when its
`--version` output matches the requested version. MySQL must resolve to an
Oracle MySQL-compatible server; a MariaDB `mysqld` binary is rejected for
`routa db ... mysql`.

By default, each database service is isolated by engine and version:
`routa-mysql@8.0.service` stores data under
`~/.local/share/routa/services/mysql/8.0/`. Add an instance name before
`on <port>` to run separate databases for separate projects on the same engine
and version, such as `routa-mysql@8.0_affiliate-platform.service` with data
under `~/.local/share/routa/services/mysql/8.0/instances/affiliate-platform/`.
routa initializes missing data directories with the distro-provided init tools
and does not delete database data.

MySQL starts with an empty local `root` password for routa management. Pass
`--user` and `--password` to `install` or `start` to save application
credentials for an instance; when the service is running routa creates or
updates that local app user and grants it privileges. Use
`routa db credentials mysql <version> [instance] --user <user> --password <password>`
to rotate credentials later. Saved credentials are stored with file mode `0600`
under the instance config directory and are applied on the next start if the
service is currently stopped. routa does not manage the MySQL `root` account as
an application user.

Common MySQL paths:

| | |
|---|---|
| Runtime | `~/.local/share/routa/binaries/mysql/<version>/` |
| Default instance data | `~/.local/share/routa/services/mysql/<version>/` |
| Named instance data | `~/.local/share/routa/services/mysql/<version>/instances/<name>/` |
| Named instance config | `~/.config/routa/services/mysql/<version>/instances/<name>/my.cnf` |
| Named instance credentials | `~/.config/routa/services/mysql/<version>/instances/<name>/credentials.json` |

## Search

routa can manage Meilisearch and Typesense as version-isolated search services:

```bash
routa search install meilisearch 1.12
routa search start meilisearch 1.12 on 7701
routa search proxy meilisearch 1.12 search
routa search status meilisearch 1.12

routa search install typesense 28
routa search start typesense 28 on 8109
routa search proxy typesense 28 typesense
routa search list
```

Search services expect a matching system binary. Data is stored under
`~/.local/share/routa/services/meilisearch/<version>/` or
`~/.local/share/routa/services/typesense/<version>/`. `routa search install`,
`start`, `status`, and `list` include the configured listen port in their
output.

## Object storage

routa can manage MinIO as a version-isolated S3-compatible local service:

```bash
routa storage install minio RELEASE.2026-05-01T00-00-00Z
routa storage start minio RELEASE.2026-05-01T00-00-00Z on 9002 --console-port 9003
routa storage proxy minio RELEASE.2026-05-01T00-00-00Z minio
routa storage status minio RELEASE.2026-05-01T00-00-00Z
routa storage list
```

MinIO data is stored under `~/.local/share/routa/services/minio/<version>/`.
`routa storage install`, `start`, and `status` print the configured API and
console listen addresses, and `routa storage list` includes the configured ports
for each installed MinIO version.
The generated environment file sets local development credentials
`MINIO_ROOT_USER=routa` and `MINIO_ROOT_PASSWORD=routa-local-dev`.

## Backing up stateful services

Stateful optional services keep data under `~/.local/share/routa/services/`.
Configuration needed to recreate ports, credentials, and service settings lives
under `~/.config/routa/services/` and
`~/.config/systemd/user/routa-*.service`.

For file-level backups, stop the target service first, then copy its data and
matching config directories. Use `routa db list`, `routa search list`, and
`routa storage list` to confirm installed versions, instance names, ports, and
data paths before archiving. For live database exports, prefer the engine's
native dump tools (`mysqldump`, `mariadb-dump`, or `pg_dump`) against the
configured localhost port instead of copying a running data directory.

Important paths:

| Service | Data | Config |
|---|---|---|
| Redis | `~/.local/share/routa/services/redis/` | `~/.config/routa/services/redis/` |
| Mailpit | `~/.local/share/routa/services/mailpit/` | systemd unit only |
| MariaDB | `~/.local/share/routa/services/mariadb/<version>/[instances/<name>/]` | `~/.config/routa/services/mariadb/<version>/[instances/<name>/]` |
| MySQL | `~/.local/share/routa/services/mysql/<version>/[instances/<name>/]` | `~/.config/routa/services/mysql/<version>/[instances/<name>/]` |
| Postgres | `~/.local/share/routa/services/postgres/<version>/[instances/<name>/]` | `~/.config/routa/services/postgres/<version>/[instances/<name>/]` |
| Meilisearch | `~/.local/share/routa/services/meilisearch/<version>/` | systemd unit only |
| Typesense | `~/.local/share/routa/services/typesense/<version>/` | systemd unit only |
| MinIO | `~/.local/share/routa/services/minio/<version>/` | `~/.config/routa/services/minio/<version>/` |

## TUI

`routa` opens a Bubble Tea dashboard with subdomain grouping, live HTTP probes,
health status, optional service status, log previews, filters, sorting,
collapsible groups, and per-site actions. `routa tui` remains as a hidden
compatibility alias.

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
| `[` / `]` | select an optional service in the inspector |
| `v` | start or stop the selected optional service after confirmation |
| `V` | restart the selected optional service after confirmation |
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
| `~/.local/share/routa/` | PHP builds, routa-managed service runtimes, optional service data, Caddyfile, site fragments, CA stash |
| `~/.local/state/routa/` | sockets, logs, fpm runtime config |
| `~/.config/routa/` | `state.json` (versioned tracked dirs, tracked roots, ignored sites, links, aliases, default PHP), PHP ini overrides, optional service config and saved credentials |
| `~/.config/systemd/user/routa-*.service` | `routa-dns`, `routa-caddy`, `routa-php@<spec>`, optional service units |

## State file compatibility

`~/.config/routa/state.json` is versioned. Current routa writes `version: 4`.
Pre-version state files are treated as the legacy v1 shape and migrated on the
next save. If a future routa writes a newer state version, older binaries fail
instead of guessing how to interpret it.

## Stack

- **DNS:** tiny Go responder for `*.test` on `127.0.0.1:1053` (`miekg/dns`). Zero upstream config — answers `127.0.0.1` for `*.test`, NXDOMAIN otherwise.
- **TLS:** Caddy issues from its built-in local CA. Root cert installed into the system trust store via p11-kit's `trust anchor` (so `curl` and Chromium-family browsers trust it).
- **PHP:** musl-static builds from [dl.static-php.dev](https://dl.static-php.dev/static-php-cli/bulk) — Laravel-ready extension set, no glibc dependency, plus routa's Laravel-friendly FPM ini defaults. Per-version socket via templated systemd unit `routa-php@<spec>.service`.
- **Optional services:** Redis, Mailpit, MariaDB, MySQL, Postgres, Meilisearch, Typesense, and MinIO run as systemd user services. MySQL uses routa-managed runtimes under `~/.local/share/routa/binaries/mysql/`; the remaining services currently use matching system binaries. Stateful services keep data under `~/.local/share/routa/services/`.
- **Routing:** Caddy's `php_fastcgi` for PHP sites (Caddy default `try_files` handles Laravel routing). `file_server` for static.
- **Process management:** systemd user units. routa itself is a stateless CLI — no daemon.

## How it routes `*.test` after cutover

`browser → systemd-resolved (127.0.0.53) → per-link routing for ~test → 127.0.0.1:1053 (routa-dns) → 127.0.0.1 → routa-caddy → site fragment`

The per-link config goes in `/etc/systemd/network/<file>.d/routa.conf` (one per existing `.network` file). Global routing via `/etc/systemd/resolved.conf.d/` doesn't pin queries to a specific server, so per-link is the only way to reliably route a single domain.

Cutover requires at least one `/etc/systemd/network/*.network` file. `routa
cutover` refuses to run its sudo block if no `.network` files exist, before
changing resolver or port settings. If your machine uses NetworkManager without
systemd-networkd `.network` files, keep routa on alternate ports or add a
networkd-managed link before running cutover.

## Troubleshooting

- **A site does not resolve:** run `routa doctor`. Before cutover, query routa
  DNS directly with `routa query app.test`; system-wide `.test` routing only
  happens after `routa cutover`. `routa doctor` shows the DNS answer, expected
  answer, and any raw query output when routa-dns does not return an A record.
- **Caddy is not on the expected port:** run `routa restart caddy` and then
  `routa doctor`. If the cutover state is partial, re-run `routa cutover` or
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

## Logo

The routa logo and brand marks were designed by
**[Scott Zirkel](https://github.com/scottzirkel)**, by hand — no AI involved.

No, it's not a turnip. It's a rutabaga. Get it? Routa... ruta... Shut up, it works.
