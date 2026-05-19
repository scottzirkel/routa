# Release Process

routa uses SemVer-style tags.

## Version Rules

- Patch: docs-only changes, small bug fixes, or internal fixes with no new user-facing workflow.
- Minor: new commands, new user-facing behavior, config format changes, or workflow improvements.
- Major: start at `v1.0.0` when the CLI and config shape are stable enough to treat breaking changes seriously.

Before `v1.0.0`, minor releases may still include breaking changes, but prefer calling them out in the release notes.

## Checklist

1. Confirm the worktree only contains release-scoped changes:

   ```bash
   git status -sb
   git diff --stat
   ```

2. Run checks:

   ```bash
   GOCACHE=/tmp/routa-go-cache go test ./...
   GOCACHE=/tmp/routa-go-cache go vet ./...
   ```

3. Choose the next version from the version rules.

4. Commit the release:

   ```bash
   git add <files>
   git commit -m "Release vX.Y.Z"
   ```

5. Create an annotated tag on the release commit:

   ```bash
   git tag -a vX.Y.Z -m "routa vX.Y.Z"
   ```

6. Push the branch and tags:

   ```bash
   git push origin main --tags
   ```

7. Create the GitHub release from the pushed tag. The release-artifacts workflow
   attaches Linux `amd64` and `arm64` archives plus a checksum file.

8. When the supported upstream PHP patch versions change, run the
   `php xdebug artifacts` workflow for those exact versions so
   `routa php install <version>` can fetch matching managed Xdebug shared
   extensions from the `php-xdebug` release.

9. For AUR releases, update `packaging/aur/routa-bin/PKGBUILD` with the new
   `pkgver` and release checksums, regenerate `.SRCINFO`, then publish those
   files to the AUR package repository.

10. Verify locally:

   ```bash
   git status -sb
   git log --oneline --decorate --max-count=5
   git tag --list --sort=version:refname -n
   ```

## Retrospective Tags

The initial release line was reconstructed from the first commits:

- `v0.1.0`: initial routa implementation.
- `v0.2.0`: TUI, proxy command, and CLI polish.
- `v0.3.0`: version command, custom docroot override, and completion docs.
- `v0.3.1`: roadmap documentation.
- `v0.4.0`: PHP ini management, CLI PHP/Composer proxies, safer Caddy rendering, and PHP removal fixes.
- `v0.4.1`: release process documentation.
- `v0.5.0`: 1.0 roadmap organization, `doctor --json`, state file versioning, Caddy log rotation, migration/root coverage, release workflow, and expanded routing/cutover tests.
- `v0.5.1`: proxy target validation, PHP-FPM uninstall cleanup, safer rollback resolver restoration, corrected Phase 1 detection with existing systemd-resolved, and cutover guard for missing systemd-networkd `.network` files.
- `v0.5.2`: purge safety guard and extra routing/migration coverage.
- `v0.6.0`: routing, install, uninstall, cutover, rollback, and Valet migration coverage; documented systemd-networkd requirements, rollback resolver behavior, purge scope, and source/tag-only release policy.
- `v0.7.0`: required dependency diagnostics fail fast in `routa init`, install dependency guidance is distro-neutral, and `routa doctor` preserves service-check failure details.
- `v1.0.0`: stable Linux support contract, clearer doctor diagnostics, certificate trust troubleshooting, and completed DNS/port/certificate documentation.
- `v1.1.0`: bare `routa` launches the interactive dashboard; the TUI gains a split inspector, health strip, log previews, filters, sorting, collapsible groups, help/prompts, and inline site actions.
- `v1.2.0`: project rename from hostr to routa, `track`/`untrack` commands with Valet-compatible aliases, ignored tracked-site support, static `public/` detection, and static SPA fallback routing.
- `v1.3.0`: generic `routa dev` command for process-backed apps, detection for package.json dev scripts, Rails, Phoenix, and Django, port discovery, and WebSocket-friendly proxy forwarding headers.
- `v1.4.0`: site aliases with `routa alias`/`unalias`, tracked-dir root overrides with `routa track --root`, per-site PHP `.env` passthrough through generated PHP-FPM pools, and expanded routing edge coverage.
- `v1.5.0`: versioned optional services for Redis, Mailpit, MariaDB, Postgres, Meilisearch, Typesense, and MinIO with systemd user units and isolated data directories.
- `v1.6.0`: routa-managed MySQL runtime installation, named MySQL database instances, application credentials, runtime dependency diagnostics, and active optional-service restarts.
- `v1.7.0`: optional service dashboard visibility and start/stop/restart actions.
- `v1.8.0`: optional service diagnostics, Linux release artifacts, AUR metadata, PHP Xdebug toggles, optional service proxy helpers, PHP-FPM restart aliases, retrying PHP downloads, and safer PHP-FPM env rendering.
- `v1.8.1`: PHP public front-controller detection no longer requires `composer.json`, so non-Composer apps with `public/index.php` resolve as PHP sites.
- `v1.9.0`: compact database list output with configured ports, clearer doctor cutover wording, safer dev-server port parsing, Mailpit/search status listen-address headers, and expanded routing/rendering coverage.
- `v1.10.0`: current-site PHP Xdebug, extension, and ini inspection workflows; optional service port output polish for Mailpit, search, and storage; stateful service backup guidance; Mailpit tag/plus-address inbox guidance; longer local TLS certificates; and more routing edge coverage.
- `v1.11.0`: managed Xdebug shared-extension installs for PHP versions, glibc static-php-cli PHP builds, current/default-aware `routa php xdebug install`, FPM php.ini rendering for Zend extensions, and Xdebug artifact publishing workflow.

## Pending Release Notes

### Next

- PHP sites use stable per-site FPM sockets without copying project `.env` values into generated FPM config, so editing `.env` values no longer requires `routa reload`.
- Site fragments now include wildcard subdomain hosts like `*.app.test`, while explicit subdomain links and aliases continue to render their own exact host fragments.
- Valet-compatible PHP drivers are supported via project-local `LocalValetDriver.php`, global `*ValetDriver.php` files in `~/.config/routa/Drivers/`, and the Valet `serves` / `isStaticFile` / `frontControllerPath` method contract.
- PHP sites can inject request-time `$_SERVER` values from `.routa-env.php`, with `.valet-env.php` supported as a Valet-compatible fallback.
