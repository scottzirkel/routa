# Roadmap

Tracking what's not yet done. Order within sections is rough priority, not commitment.

## 1.0.0 — stable Linux Valet replacement

Goal: make the existing Linux-focused workflow stable, recoverable, and supportable
enough to treat the CLI and config shape as a real contract. This milestone is not
trying to become a full Herd replacement.

- **Installation and rollback confidence**
  - Harden `hostr install`, `hostr cutover`, `hostr cutover --rollback`, and
    `hostr uninstall --purge`.
  - Continue adding tests around cutover and rollback behavior.
  - Document the required host assumptions: systemd user services,
    systemd-resolved, Caddy, and p11-kit trust store behavior.
- **Config/schema stability**
  - Treat `~/.config/hostr/state.json` as a stable contract.
  - Document the state migration posture before 1.0.
  - Ensure older pre-1.0 state either loads cleanly or fails with actionable
    guidance.
- **Core routing correctness**
  - Continue expanding tests for site detection, custom roots, linked sites, parked dirs,
    proxies, secure toggle, and missing docroots.
- **Valet migration reliability**
  - Continue covering parked dirs, linked dirs, Nginx custom roots, isolated PHP versions,
    and missing/weird Valet config.
- **Supportability**
  - Review error messages for service failures, DNS failures, cert trust
    failures, and port conflicts.
- **Distribution**
  - GitHub Releases with prebuilt binaries via a `release.yml` workflow.
  - Release workflow should run `go test`, `go vet`, and build artifacts from
    tags.
  - Tagged releases with proper semver; `hostr version` already prints
    `git describe`.
- **Docs pass**
  - Keep README troubleshooting current as install, migration, rollback, and
    release packaging behavior changes.
  - Expand command help where workflows still require README context.

## Near-term after 1.0 (small, well-scoped)

- **TUI: help overlay** — `?` opens a full keymap modal so the footer can stay short.
- **TUI: auto-refresh** — re-probe every N seconds (off by default), toggle with a key.
- **TUI: inline actions** — `u` unlink, `s` toggle secure, `R` change `--root` for the highlighted site without dropping to the CLI.
- **TUI: filter by HTTPS column** — already by status/kind/secure; add a `proxy`-only quick filter (currently you can `t` cycle to it but the kind enum only has `php`/`static`).
- **`hostr alias <existing> <new>`** — register additional names that resolve to the same site (multiple `.test` hostnames → one source dir/proxy/php config).
- **`hostr park --root <path>`** — apply a default `--root` to every subdir of a parked dir (e.g. all subdirs are vite apps with `dist/` outputs).
- **Per-site env file passthrough** — let a site declare a `.env` whose vars hostr-php-fpm exports into the worker (`env[FOO] = bar` lines in the pool config). Useful for sites that need different DB creds per env.

## Mid-term

- **Distribution**
  - AUR package (`hostr-bin`) so Arch users `paru -S hostr-bin`.
- **Bundled services (Herd-Pro-style)**
  - **MariaDB / Postgres** — managed user systemd unit per version, ports 3306/5432, data under `~/.local/share/hostr/db/`.
  - **Redis** — single user-space instance.
  - **Mailpit** — SMTP catcher on :1025, web UI on :8025, optionally proxied as `mail.test`.
  - CLI shape: `hostr db install mariadb 11`, `hostr db start/stop/list`, `hostr mail start`. TUI panel for these.
- **Auto-proxy detection** — when a site's path has `package.json` with a recognized `dev` script, optionally run/proxy it: `hostr dev <name>` runs `npm run dev`, finds the bound port, sets up a proxy automatically, tears down on exit.
- **PHP extension management** — currently we ship the upstream "bulk" extension set; add `hostr php ext list/enable/disable <ver> <ext>` for finer control. Static-php-cli supports custom builds — could fetch alternative variants.
- **Xdebug toggle** — install xdebug-enabled PHP variant alongside, `hostr php xdebug on/off <ver>` flips the loaded ini.

## Backlog / ideas

- **More TLD support** — currently hardcoded `.test`. Allow `.localhost` or arbitrary local TLDs.
- **Multi-host (LAN sharing)** — bind hostr-caddy to LAN IP, have other devices on the network resolve `*.test` against your machine. Useful for testing on phones/tablets.
- **Caddy admin API integration** — drive site changes via the admin API instead of file fragments + reload (faster, atomic).
- **Plugin / driver system** — Laravel-style "drivers" for unusual project layouts so the auto-detect can be extended without touching core.
- **Web dashboard** — small local web UI (in addition to TUI) for users who prefer a browser.
- **macOS support** — most of the stack (Caddy, php-fpm, miekg/dns) is portable; the resolver bits are Linux-specific. Probably not worth doing while Herd exists.
- **Tests** — almost none today; site detect, valet migration, version resolution, and cutover state detection are the highest-leverage units to cover.

## Won't do

- **GUI app** — explicit project rejection from day one; Herd already covers this niche.
- **Auto-updating the binary in place** — leave to OS package managers (AUR, brew, deb, rpm) and `git pull && bash install.sh`.
