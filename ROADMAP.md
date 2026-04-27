# Roadmap

Tracking what's not yet done. Order within sections is rough priority, not commitment.

## Near-term (small, well-scoped)

- **TUI: help overlay** — `?` opens a full keymap modal so the footer can stay short.
- **TUI: auto-refresh** — re-probe every N seconds (off by default), toggle with a key.
- **TUI: inline actions** — `u` unlink, `s` toggle secure, `R` change `--root` for the highlighted site without dropping to the CLI.
- **TUI: filter by HTTPS column** — already by status/kind/secure; add a `proxy`-only quick filter (currently you can `t` cycle to it but the kind enum only has `php`/`static`).
- **`hostr alias <existing> <new>`** — register additional names that resolve to the same site (multiple `.test` hostnames → one source dir/proxy/php config).
- **`hostr park --root <path>`** — apply a default `--root` to every subdir of a parked dir (e.g. all subdirs are vite apps with `dist/` outputs).
- **Per-site env file passthrough** — let a site declare a `.env` whose vars hostr-php-fpm exports into the worker (`env[FOO] = bar` lines in the pool config). Useful for sites that need different DB creds per env.
- **`hostr doctor --json`** — machine-readable health output for CI / scripts.
- **Caddy log rotation** — `output file` doesn't rotate; either configure logrotate snippet on install or switch to size-bounded rolling logs.

## Mid-term

- **Distribution**
  - AUR package (`hostr-bin`) so Arch users `paru -S hostr-bin`.
  - GitHub Releases with prebuilt binaries (musl-static so they run anywhere) via a `release.yml` workflow.
  - Tagged releases with proper semver; `hostr version` already prints `git describe`.
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
