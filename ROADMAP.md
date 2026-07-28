# Roadmap

Tracking release status and future work. Order within sections is rough
priority, not commitment.

## Pending release

- **Bundled pcov coverage** — `routa php install` now installs a Routa-managed
  `pcov.so` alongside `xdebug.so`, and `routa php pcov install/on/off/status`
  toggles it. pcov defaults to on, enabled for the CLI and pinned off for
  PHP-FPM, so PHPUnit picks the fast coverage driver without FPM paying for it.
  Xdebug keeps `debug,develop` and hands coverage over; enabling both for
  coverage now warns.
- **Generalized extension pipeline** — the Xdebug build script and workflow are
  now extension-agnostic, publishing every managed shared extension to the
  `php-extensions` release while mirroring Xdebug to the legacy `php-xdebug`
  tag for older installs.
- **Wildcard subdomains** — site fragments include wildcard hosts such as
  `*.app.test`, while explicit subdomain links and aliases keep their own exact
  host fragments.
- **Stable PHP env behavior** — PHP sites use per-site FPM sockets without
  copying project `.env` values into generated FPM config.
- **Valet-compatible drivers** — project-local `LocalValetDriver.php` and
  global `*ValetDriver.php` files can serve unusual PHP layouts through Valet's
  `serves`, `isStaticFile`, and `frontControllerPath` method contract.
- **Site-specific server variables** — `.routa-env.php` injects request-time
  `$_SERVER` values using Valet's site-name and `*` map convention, with
  `.valet-env.php` supported as a migration fallback.

## Released

### v1.11.0 — Managed Xdebug-capable PHP installs

- **Xdebug-capable PHP installs** — PHP downloads now use the upstream
  static-php-cli `gnu-bulk` channel so shared extensions can load on glibc
  systems, `routa php install <version>` attempts to install a matching
  Routa-managed `xdebug.so`, and `routa php xdebug install/on/off/status`
  can add, toggle, and inspect Xdebug later without reinstalling PHP.
- **Xdebug artifact pipeline** — added and verified a GitHub Actions workflow
  and build script for publishing per-PHP-version Linux `amd64`/`arm64`
  Xdebug shared-extension archives to the `php-xdebug` release used by the
  installer.

### v1.10.0 — PHP workflow and optional service polish

- **Current-site PHP tooling** — `routa php xdebug on/off/status`,
  `routa php ext list`, and `routa php ini show/path/edit` can now infer the
  PHP version from the current site or default PHP when no version is provided.
- **Optional service port output** — Redis, Mailpit, databases, search, and
  storage expose configured ports in the relevant lifecycle, status, or list
  output, including MinIO API/console ports and Mailpit web/SMTP ports.
- **Mailpit proxy polish** — `routa mail proxy` now points at the configured
  Mailpit web port instead of assuming the default `8025`.
- **Stateful service guidance** — README now documents routa-owned service data
  and config paths, safer live database export options, and Mailpit tag and
  plus-address workflows for project-specific inboxes.
- **Routing and TLS polish** — added focused coverage for dotted tracked,
  linked, and aliased site names, cleaned link-path matching, and configured
  Caddy-issued `.test` leaf certificates for a 396-day lifetime backed by a
  730-day local intermediate CA.

### v1.9.0 — optional service list/status polish

- **Database list ports** — `routa db list` now exposes each database
  instance's configured port in a compact table.
- **Doctor cutover wording** — `routa doctor` now reports cutover state using
  user-facing descriptions instead of internal phase labels.
- **Dev-server port parsing** — `routa dev` now avoids mistaking URL scheme
  prefixes for ports, with coverage for common localhost URLs, wildcard bind
  addresses, and ignored out-of-range ports.
- **Optional service status output** — `routa mail status` and
  `routa search status` now print configured listen addresses before the
  underlying systemd status.
- **Routing and rendering coverage** — focused tests now cover linked-path
  precedence, absolute link roots, proxy overrides, proxy target normalization,
  proxy aliases, alias chains, static docroot priority, build-output priority,
  PHP public docroot priority, and mixed dev-server detection.

### v1.8.1 — PHP public docroot detection

- **PHP public front controllers** — site auto-detection now treats
  `public/index.php` as a PHP docroot without requiring `composer.json`,
  covering non-Composer PHP apps and custom front-controller layouts.

### v1.8.0 — diagnostics, PHP tooling, and Arch packaging

- **Optional service diagnostics** — `routa doctor` adds detail for optional
  services with missing binaries, occupied ports, failed units, runtime library
  failures, and mismatched database runtimes.
- **Arch packaging path** — release builds can produce Linux `amd64`/`arm64`
  archives, GitHub releases can attach those artifacts, and AUR metadata for
  `routa-bin` lives under `packaging/aur/routa-bin/`.
- **PHP debugging toggle** — `routa php xdebug on/off/status <version>` manages
  per-version Xdebug ini settings when the installed PHP build includes Xdebug,
  and Xdebug-capable installs default to off.
- **Optional service proxy helpers** — search services and MinIO console can be
  registered as `.test` proxies with service-aware default ports.
- **PHP-FPM reliability** — PHP downloads retry interrupted transfers,
  `routa restart php [version]` targets PHP-FPM directly, generated FPM env
  values are quoted safely, and `.env` references such as
  `${FORWARD_REDIS_PORT}` expand before pool config rendering.

### v1.7.0 — optional service dashboard actions

- **TUI service visibility** — the dashboard inspector shows installed optional
  services with configured ports, data directories, and active/inactive state.
- **TUI service actions** — optional services can be selected in the dashboard
  and started/stopped or restarted after confirmation.

### v1.6.0 — managed MySQL services and DB instances

- **Managed MySQL runtime** — MySQL uses routa-owned Oracle MySQL archives under
  `~/.local/share/routa/binaries/mysql/` and rejects MariaDB-compatible
  `mysqld` binaries for `routa db ... mysql`.
- **Named MySQL instances** — MySQL instances are isolated by version and
  optional project name with their own data, config, sockets, and ports.
- **Database credentials** — MySQL application credentials can be saved and
  applied for installed or running instances.
- **Service restart coverage** — `routa restart` includes active optional
  services alongside DNS, Caddy, and PHP-FPM.

### v1.5.0 — versioned optional services

- **Versioned databases** — MariaDB and Postgres can be installed, started,
  stopped, listed, and inspected as systemd user services.
- **Search services** — Meilisearch and Typesense run as version-isolated
  systemd user services with install/start/stop/status/list commands.
- **Object storage** — MinIO runs as a version-isolated local S3-compatible
  service with configurable API and console ports.
- **Redis and Mailpit** — Redis and Mailpit user services remain the simple
  single-instance service slices, with Mailpit optionally proxied as `.test`.

### v1.4.0 — aliases, tracked roots, and PHP env pools

- **`routa alias <existing> <new>`** — registers additional `.test` hostnames
  that resolve through the target site's source, proxy, PHP, root, and HTTPS
  config. `routa unalias <name>` removes them.
- **Tracked-dir default root** — `routa track --root <path>` applies a shared
  docroot override to every immediate child of a tracked dir.
- **Per-site env file passthrough** — PHP sites with a project `.env` get a
  generated PHP-FPM pool and per-site socket with `env[FOO] = bar` entries.
- **Routing edge coverage** — added focused coverage for tracked-root overrides,
  explicit-link precedence, and alias chains.

### v1.3.0 — process-backed dev apps

- **`routa dev`** — starts a detected project dev server, waits for the port,
  and registers a reverse proxy under `.test`.
- **Dev-server detection** — supports package manager `dev` scripts, Rails,
  Phoenix, and Django defaults.
- **Manual process support** — accepts explicit command, name, and port options
  for apps that do not fit a built-in detector.
- **Proxy behavior** — reverse proxy rendering now includes WebSocket-friendly
  forwarding headers for HMR and other upgraded connections.

### v1.2.0 — routa rename and site tracking polish

- **Project rename** — completed the hostr-to-routa command, path, service, and
  documentation rename.
- **Track/untrack language** — `routa track` and `routa untrack` are now the
  primary commands, with `park` and `unpark` kept as Valet-compatible aliases.
- **Ignored tracked sites** — `routa ignore` and `routa unignore` hide or
  restore auto-discovered tracked subdirectories.
- **Static site detection** — static `public/` directories are detected, and
  static SPA routing falls back to `index.html`.

### v1.1.0 — interactive dashboard

- **Bare `routa` opens the TUI** — the dashboard is now the default entrypoint.
- **Site inspection** — the TUI has a split inspector, health strip, live probe
  status, and log previews.
- **Navigation controls** — filters, sorting, collapsible subdomain groups,
  help prompts, and selected-site actions are available inline.
- **Compatibility** — `routa tui` remains available as a hidden alias.

### v1.0.0 — stable Linux local dev server

Goal: make the Linux-focused workflow stable, recoverable, and supportable enough
to treat the CLI and config shape as a real contract. This milestone was not
trying to become a full-stack desktop dev suite.

- **Installation and rollback confidence**
  - `routa install` now checks required commands before side effects and has
    pure unit-rendering coverage.
  - `routa uninstall --purge` has helper coverage for purge scope and PHP-FPM
    unit discovery.
  - Cutover/rollback now has partial-state helper coverage and sudo block
    ordering checks.
  - `routa init` now treats missing required dependencies as blocking
    diagnostics instead of reporting a pass before `routa install` fails.
  - `v0.7.0` tightened prerequisite diagnostics and `routa doctor` service
    failure output.
  - `v0.5.1` added baseline hardening for proxy target validation,
    PHP-FPM cleanup during uninstall, safer rollback resolver restoration,
    existing systemd-resolved detection, and cutover refusal when no
    systemd-networkd `.network` files are available.
  - Document the required host assumptions: systemd user services,
    systemd-resolved, systemd-networkd `.network` files for per-link routing,
    Caddy, and p11-kit trust store behavior.
- **Config/schema stability**
  - Treat `~/.config/routa/state.json` as a stable contract.
  - Current state files are versioned; future shape changes
    require explicit migrations instead of silent guessing.
- **Core routing correctness**
  - Custom roots, linked-site overrides, secure toggle rendering, and
    missing-docroot status output now have focused coverage.
  - Proxy targets now validate before state is saved or Caddy fragments render.
  - Site detection, parked directory resolution, proxy target validation, and
    Caddy fragment rendering have focused coverage for the v1 contract.
- **Migration reliability**
  - Missing/malformed config, relative symlinks, quoted Nginx roots, whitespace,
    custom roots, and isolated PHP versions now have focused coverage.
- **Supportability**
  - Service failure diagnostics now preserve `systemctl` error details in
    `routa doctor`.
  - DNS failures now preserve raw query details in `routa doctor`.
  - Cert trust errors now name the missing Caddy root or failed `trust anchor`
    action with a p11-kit/system trust store hint.
  - Port diagnostics now call out likely ownership conflicts when HTTPS ports
    are bound while `routa-caddy` is not active.
- **Distribution**
  - Current policy: GitHub releases are source/tag-only until a binary artifact
    policy is chosen.
  - Tagged releases with proper semver; `routa version` already prints
    `git describe`.
- **Docs pass**
  - README troubleshooting covers install, migration, rollback, DNS, port,
    certificate, and source/tag-only release behavior.
  - Command help covers the v1 workflows that should be usable without reading
    implementation details.

## Future Work Triage

### P0 — release and packaging

- **Release pending work** — cut the next release so wildcard subdomains,
  stable PHP env behavior, Linux release artifacts, and matching PHP Xdebug
  artifacts are attached.
- **AUR package publication** — publish and maintain `routa-bin` after release
  artifacts exist, then keep package metadata aligned with the release process.

### P2 — local workflow polish

- **FrankenPHP runtime option** — investigate FrankenPHP as a viable alternative
  or optional runtime alongside PHP-FPM. Evaluate compatibility with Routa's
  Caddy-based routing, per-site PHP versions, `.routa-env.php` server-variable
  injection, Valet-compatible drivers, Xdebug expectations, and static PHP
  distribution model before deciding whether to prototype.
- **Directory listing toggle** — expose a site/global setting for directory
  listing behavior, defaulting to off.
- **More routing edge coverage** — keep adding unusual tracked-dir, linked-site,
  proxy, dev-server, wildcard-host, and path-combination cases as they appear.
- **More TLD support** — `.test` is currently hardcoded. Allow `.localhost` or
  an arbitrary local TLD without weakening the Linux resolver assumptions.
- **Multi-host LAN sharing** — optionally bind `routa-caddy` to a LAN address
  and document how other devices can resolve local sites against the dev
  machine. Useful for phone/tablet testing.

### P3 — extensibility and larger UX work

- **PHP extension variants** — `routa php ext list <ver>` exists today for the
  compiled-in upstream bulk profile. Add finer-grained variant selection or
  custom static-php-cli builds for users who need a different extension set.
- **Caddy admin API integration** — drive site changes via the admin API instead
  of file fragments plus reloads for faster, more atomic updates.
- **Web dashboard** — add a small local web UI in addition to the TUI for users
  who prefer browser-based inspection and actions.

### Not planned

- **Nginx config compatibility** — Routa should not try to execute or translate
  arbitrary Valet/Herd Nginx snippets. A migration diagnostic can point out
  unsupported custom Nginx config, but Caddy remains the routing layer.
- **Herd manifest compatibility** — `herd.yml`, Forge integration, Expose
  sharing, and Herd Pro service workflows are Herd-specific product surfaces,
  not Routa compatibility targets.
- **Node version management** — Herd's `isolate-node`/NVM behavior is outside
  Routa's current site-serving scope. Dev-server proxying remains the supported
  Node workflow.
- **macOS support** — most of the stack is portable, but Routa's resolver,
  systemd user service, and trust-store flows are Linux-specific.
- **GUI app** — explicit project rejection from day one.
- **Auto-updating the binary in place** — leave to OS package managers
  (`routa-bin`, future packages) and `git pull && bash install.sh`.
