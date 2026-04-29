# Release Process

hostr uses SemVer-style tags, with `v0.x.y` while the project is pre-1.0.

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
   GOCACHE=/tmp/hostr-go-cache go test ./...
   GOCACHE=/tmp/hostr-go-cache go vet ./...
   ```

3. Choose the next version from the version rules.

4. Commit the release:

   ```bash
   git add <files>
   git commit -m "Release vX.Y.Z"
   ```

5. Create an annotated tag on the release commit:

   ```bash
   git tag -a vX.Y.Z -m "hostr vX.Y.Z"
   ```

6. Push the branch and tags:

   ```bash
   git push origin main --tags
   ```

7. Create the GitHub release from the pushed tag. Attach prebuilt artifacts if
   publishing binaries for that release.

8. Verify locally:

   ```bash
   git status -sb
   git log --oneline --decorate --max-count=5
   git tag --list --sort=version:refname -n
   ```

## Retrospective Tags

The initial release line was reconstructed from the first commits:

- `v0.1.0`: initial hostr implementation.
- `v0.2.0`: TUI, proxy command, and CLI polish.
- `v0.3.0`: version command, custom docroot override, and completion docs.
- `v0.3.1`: roadmap documentation.
- `v0.4.0`: PHP ini management, CLI PHP/Composer proxies, safer Caddy rendering, and PHP removal fixes.
- `v0.4.1`: release process documentation.
- `v0.5.0`: 1.0 roadmap organization, `doctor --json`, state file versioning, Caddy log rotation, migration/root coverage, release workflow, and expanded routing/cutover tests.
- `v0.5.1`: proxy target validation, PHP-FPM uninstall cleanup, safer rollback resolver restoration, corrected Phase 1 detection with existing systemd-resolved, and cutover guard for missing systemd-networkd `.network` files.
