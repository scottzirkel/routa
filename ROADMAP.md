# Roadmap

Tracking release status and future work. Order within sections is rough
priority, not commitment.

## Shipped

### Pending release

- **`routa alias <existing> <new>`** — registers additional `.test` hostnames
  that resolve through the target site's source, proxy, PHP, root, and HTTPS
  config. `routa unalias <name>` removes them.

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
  - Current state files are versioned as `version: 1`; future shape changes
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

## Near-term (small, well-scoped)

- **Tracked-dir default root** — apply a default `--root` to every subdir of a
  tracked dir, e.g. all subdirs are Vite apps with `dist/` outputs.
- **Per-site env file passthrough** — let a site declare a `.env` whose vars
  routa-php-fpm exports into the worker (`env[FOO] = bar` lines in the pool
  config). Useful for sites that need different DB creds per env.
- **More routing edge coverage** — keep adding unusual tracked-dir, linked-site,
  proxy, dev-server, and path-combination cases as they appear.

## Mid-term

- **Distribution**
  - AUR package (`routa-bin`) so Arch users `paru -S routa-bin`.
- **Bundled services**
  - **MariaDB / Postgres** — managed user systemd unit per version, ports
    3306/5432, data under `~/.local/share/routa/db/`.
  - **Redis** — single user-space instance.
  - **Mailpit** — SMTP catcher on :1025, web UI on :8025, optionally proxied
    as `mail.test`.
  - CLI shape: `routa db install mariadb 11`, `routa db start/stop/list`,
    `routa mail start`. TUI panel for these.
- **PHP extension variants** — `routa php ext list <ver>` exists today for the
  compiled-in upstream bulk profile. Add finer-grained variant selection or
  custom static-php-cli builds for users who need a different extension set.
- **Xdebug toggle** — install xdebug-enabled PHP variant alongside,
  `routa php xdebug on/off <ver>` flips the loaded ini.

## Backlog / ideas

- **More TLD support** — currently hardcoded `.test`. Allow `.localhost` or arbitrary local TLDs.
- **Multi-host (LAN sharing)** — bind routa-caddy to LAN IP, have other
  devices on the network resolve `*.test` against your machine. Useful for
  testing on phones/tablets.
- **Caddy admin API integration** — drive site changes via the admin API instead
  of file fragments + reload (faster, atomic).
- **Plugin / driver system** — Laravel-style "drivers" for unusual project
  layouts so the auto-detect can be extended without touching core.
- **Web dashboard** — small local web UI (in addition to TUI) for users who prefer a browser.
- **macOS support** — most of the stack (Caddy, php-fpm, miekg/dns) is portable; the resolver bits are Linux-specific. Not a near-term priority.

## Won't do

- **GUI app** — explicit project rejection from day one.
- **Auto-updating the binary in place** — leave to OS package managers (AUR, brew, deb, rpm) and `git pull && bash install.sh`.
