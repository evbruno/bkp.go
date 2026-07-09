# Changelog

All notable changes to this project are documented here. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [0.0.4] - 2026-07-09

### Added

- Compressed artifacts are deleted from `base_dir` once the project's
  `command` succeeds (`keep_compressed`, default `false`), since the gzip
  file is just a temporary handoff to `command`. Set `keep_compressed: true`
  to keep it around. A failing command always leaves the artifact in place
  for inspection/retry.
- `CHANGELOG.md`, surfaced as the body of each GitHub release.

## [0.0.3] - 2026-07-08

### Added

- `FILE` and `SHA1` columns in the backup summary output, showing exactly
  which file (and its uncompressed sha1) each row refers to.
- Skip a project's backup entirely when its source file is unchanged since
  the last successful run (`skip_unchanged`, default `true`). A prior
  `error` row never counts as a match, so a broken run always retries.
- Compressed artifacts are timestamped by default
  (`file.<ISO8601>.gz`, e.g. `production1.sqlite3.20260708T193000Z.gz`) so
  repeated runs don't overwrite each other; set `timestamp: false` to keep
  the old fixed `file.gz` name.
- New read-only `bkp status` subcommand: prints the latest `backup_log` row
  per project via a single window-function query.
- Release binaries now include `darwin/arm64`, alongside `linux/amd64` and
  `linux/arm64`.

## [0.0.2] - 2026-07-08

### Added

- `checksums.txt` (SHA256) published alongside each release's binaries.

### Changed

- Restarted version numbering at `0.0.x` (superseding the initial `v0.1.0`
  release).
