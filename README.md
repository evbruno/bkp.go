# bkp.go

A small Go CLI that reads a YAML backup spec, runs a backup command per
project (optionally gzipping the source first), optionally backs up its own
orchestrator database, and logs every backup call to a single SQLite table.

See [PLAN.md](PLAN.md) for the full design and [CHANGELOG.md](CHANGELOG.md)
for release history.

## Install

Download a prebuilt binary from the [releases page](https://github.com/evbruno/bkp.go/releases)
(`linux/amd64`, `linux/arm64`, `darwin/arm64`):

```sh
curl -fsSL -o bkp.tar.gz \
  https://github.com/evbruno/bkp.go/releases/latest/download/bkp-linux-amd64.tar.gz
tar -xzf bkp.tar.gz
sudo mv bkp-linux-amd64 /usr/local/bin/bkp
```

Swap `amd64` for `arm64` on ARM hosts (Graviton, Ampere, Raspberry Pi, `aarch64`, etc).

Each release also publishes a `checksums.txt` — verify the download before
installing it:

```sh
curl -fsSL -o checksums.txt \
  https://github.com/evbruno/bkp.go/releases/latest/download/checksums.txt
sha256sum --ignore-missing -c checksums.txt
```

## Build from source

Requires Go 1.26+.

```sh
make build      # builds ./bin/bkp for the current platform
make test       # runs the test suite
make run        # builds, then runs against examples/demo/config.yaml
make release    # cross-compiles linux/amd64, linux/arm64, darwin/arm64 into ./dist/*.tar.gz
```

`bkp --version` reports the build's version, embedded via `-ldflags -X
main.version=...`. The binaries are pure Go (`modernc.org/sqlite`, no CGO),
so cross-compilation only needs `GOOS`/`GOARCH` — no C toolchain required.

## Releasing

Before tagging, move the relevant [CHANGELOG.md](CHANGELOG.md) entries out
of `## [Unreleased]` into a new `## [X.Y.Z] - YYYY-MM-DD` section (version
number without the `v` prefix, matching the tag). The release workflow
extracts that section and uses it as the GitHub release's body.

Then tag and push:

```sh
git tag v0.0.4
git push origin v0.0.4
```

Pushing a `vX.Y.Z` tag triggers [.github/workflows/release.yml](.github/workflows/release.yml),
which cross-compiles `linux/amd64`, `linux/arm64`, and `darwin/arm64`,
publishes them as tarballs (plus a `checksums.txt`), and sets the release
body from that version's `CHANGELOG.md` section (GitHub's auto-generated
notes are appended below it — a "Full Changelog" compare link, mainly
useful since this repo doesn't use PRs). If no matching section is found,
the workflow logs a warning but still publishes the release with just the
auto-generated notes.

[.github/workflows/ci.yml](.github/workflows/ci.yml) runs `go vet`, `go
test`, and `make release` on every push/PR to `main`, so a broken
cross-compile is caught before it's tagged.

## Usage

```sh
bkp --config path/to/spec.yaml [--dry-run]
```

- `--config` (required): path to the YAML backup spec.
- `--dry-run`: validate the config and report what would run, without
  compressing, executing commands, or writing log rows.

Exit code is non-zero if any project (or the self-backup) fails.

### `bkp status`

Read-only: prints the most recent `backup_log` row for each project (a
single query, via `ROW_NUMBER() OVER (PARTITION BY project ...)`). It never
runs a project's `command` or writes a log row.

```sh
bkp status --config path/to/spec.yaml
```

```
PROJECT       LAST RUN (UTC)       STATUS   SIZE   GZ SIZE  DURATION  ERROR
app-one       2026-07-08 22:47:12  ok       43     67       7ms
app-two       2026-07-08 22:47:12  skipped  32     -        0s
orchestrator  2026-07-08 22:47:12  ok       12288  -        0s
```

## Config format

```yaml
title: VPS1 backup
target: /home/ubuntu/backups.sqlite3    # orchestrator SQLite DB (created if missing)

backup_self: true                       # optional, default true
self_command: rclone copy backups.sqlite3 backups:orchestrator  # required iff backup_self

projects:
  - name: My cool rails App
    base_dir: /home/ubuntu/dbs/
    file: production1.sqlite3
    command: rclone copy {{file}} backups:my-app
    compress: true                      # optional, default true
    timestamp: true                     # optional, default true (only matters when compress: true)
    skip_unchanged: true                # optional, default true
    keep_compressed: false              # optional, default false (only matters when compress: true)

  - name: My Petshop platform
    base_dir: /home/ubuntu/dbs/
    file: production2.sqlite3
    command: rclone copy {{file}} backups:my-app
```

`{{file}}` in `command` is substituted with the final artifact path. With
`compress: true` (the default), that's the gzip output — by default named
`file.<ISO8601>.gz` (e.g. `production1.sqlite3.20260708T193000Z.gz`) so each
run's artifact gets its own file instead of overwriting the last one. Set
`timestamp: false` to go back to a fixed `file.gz` name. With
`compress: false`, `{{file}}` is just `file` — no artifact is created, so
`timestamp` has no effect. If `command` doesn't contain `{{file}}`, it runs
verbatim. Commands run as `sh -c "<command>"` with working directory
`base_dir`.

The gzip artifact is a temporary file: once `command` succeeds, it's deleted
from `base_dir` (`keep_compressed: false`, the default). Set
`keep_compressed: true` to leave it on disk instead. Either way, a failing
`command` leaves the artifact in place so it can be inspected or retried.

Before doing anything else, the source file is sha1'd (uncompressed content).
By default (`skip_unchanged: true`), if that sha1 matches the sha1 recorded
on the project's last **successful** run, the whole thing — compression and
`command` — is skipped, and a `skipped` row is logged instead (an `error`
row never counts as a match, so a broken previous run always retries). Set
`skip_unchanged: false` to always run regardless of the hash.

`target`, `base_dir`, and `file` support `$VAR` / `${VAR}` and a leading `~`
(e.g. `target: $HOME/backups.sqlite3` or `base_dir: ~/dbs`) — these are
expanded by the app itself before use. `command` and `self_command` don't
need this: they already run through `sh -c`, which expands env vars on its
own.

## Working example

[examples/demo/](examples/demo/) is a self-contained demo config that uses
local `cp` commands instead of a real remote, so it runs with no external
dependencies:

```sh
make run
```

which builds the binary and runs:

```sh
./bin/bkp --config examples/demo/config.yaml
```

[examples/demo/config.yaml](examples/demo/config.yaml) backs up two fake
project databases under `examples/demo/dbs/` and itself, copying everything
into `examples/demo/remote/`:

```yaml
title: Demo backup
target: examples/demo/orchestrator.sqlite3
backup_self: true
self_command: cp orchestrator.sqlite3 remote/

projects:
  - name: app-one
    base_dir: examples/demo/dbs
    file: production1.sqlite3
    command: cp {{file}} ../remote/
    compress: true

  - name: app-two
    base_dir: examples/demo/dbs
    file: production2.sqlite3
    command: cp {{file}} ../remote/
    compress: false
```

First run's output — note the `FILE`/`SHA1` columns, which are always the
*uncompressed* source, even when `compress: true` (app-one's gz artifact is
`production1.sqlite3.<timestamp>.gz`, but its sha1 is of the plain
`production1.sqlite3`):

```
Backup summary: Demo backup
PROJECT       FILE                  SHA1                                      STATUS  DURATION  ERROR
app-one       production1.sqlite3   4b501c61148e0cf7da293fd1a546561dc22c323b  ok      11ms
app-two       production2.sqlite3   716d22c5713091892cc87ef128e323de99ddba60  ok      8ms
orchestrator  orchestrator.sqlite3  -                                         ok      7ms
```

`examples/demo/remote/` now contains something like
`production1.sqlite3.20260708T213516Z.gz` (compressed, since `app-one` left
`compress` and `timestamp` at their defaults of `true` — the timestamp
suffix will differ on your run), `production2.sqlite3` (uncompressed, since
`app-two` set `compress: false`), and a copy of the orchestrator DB itself.
`examples/demo/dbs/` no longer has a matching `.gz` file, though: it was a
temporary artifact, and `app-one` left `keep_compressed` at its default of
`false`, so it's deleted right after the `cp` into `remote/` succeeds.
The orchestrator's own row has no sha1: it's not sha1'd or subject to
`skip_unchanged` — `backup_log` gets a new row on every single invocation
(even a skip is a row), so the tracking DB never has "unchanged" content to
detect, and skipping the one backup meant to preserve that history would
defeat its purpose.

Since the demo's fixture files never change, running `make run` again
**skips** both projects (`skip_unchanged` also defaults to `true`, and the
sha1 matches) — the orchestrator itself still runs every time:

```
Backup summary: Demo backup
PROJECT       FILE                  SHA1                                      STATUS   DURATION  ERROR
app-one       production1.sqlite3   4b501c61148e0cf7da293fd1a546561dc22c323b  skipped  0s
app-two       production2.sqlite3   716d22c5713091892cc87ef128e323de99ddba60  skipped  0s
orchestrator  orchestrator.sqlite3  -                                         ok       6ms
```

Touching `examples/demo/dbs/production1.sqlite3` (changing its sha1) makes
`app-one` run again — and only `app-one`, since `app-two`'s file (and sha1)
is still unchanged:

```
Backup summary: Demo backup
PROJECT       FILE                  SHA1                                      STATUS   DURATION  ERROR
app-one       production1.sqlite3   bae30e81ca6ede5aba9ae620d69dee2fe761779c  ok       8ms
app-two       production2.sqlite3   716d22c5713091892cc87ef128e323de99ddba60  skipped  0s
orchestrator  orchestrator.sqlite3  -                                         ok       6ms
```

Every call — including skips — is logged to `orchestrator.sqlite3` in the
`backup_log` table (the `sha1` column mirrors what's in the summary above):

```sh
sqlite3 examples/demo/orchestrator.sqlite3 \
  "SELECT project, file_path, file_size, compressed_size, status, duration_ms FROM backup_log;"
```

```
app-one|examples/demo/dbs/production1.sqlite3|32|56|ok|11
app-two|examples/demo/dbs/production2.sqlite3|32||ok|7
orchestrator|examples/demo/orchestrator.sqlite3|12288||ok|0
app-one|examples/demo/dbs/production1.sqlite3|32||skipped|0
app-two|examples/demo/dbs/production2.sqlite3|32||skipped|0
orchestrator|examples/demo/orchestrator.sqlite3|12288||ok|0
app-one|examples/demo/dbs/production1.sqlite3|43|67|ok|7
app-two|examples/demo/dbs/production2.sqlite3|32||skipped|0
orchestrator|examples/demo/orchestrator.sqlite3|12288||ok|0
```

Run the same config with `--dry-run` to see what would happen without
touching the filesystem or the log (no sha1 is computed, since dry-run reads
nothing beyond `stat`):

```sh
./bin/bkp --config examples/demo/config.yaml --dry-run
```

```
Backup summary: Demo backup
PROJECT       FILE                  SHA1  STATUS   DURATION  ERROR
app-one       production1.sqlite3   -     dry-run  0s
app-two       production2.sqlite3   -     dry-run  0s
orchestrator  orchestrator.sqlite3  -     dry-run  0s
```

`examples/demo/orchestrator.sqlite3` and everything under
`examples/demo/remote/` are gitignored generated output, not fixtures — feel
free to delete them and re-run `make run` from a clean slate.

### `$HOME`-based example

[testdata/example_loca.yaml](testdata/example_loca.yaml) shows the `$VAR`
expansion described above, backing up a real file from `$HOME` instead of a
repo-local fixture:

```yaml
title: Home logs backup
target: $HOME/dbs_bkp/orchestrator.sqlite3
backup_self: false

projects:
  - name: user-logs
    base_dir: $HOME/dbs
    file: logs_user.txt
    command: cp {{file}} $HOME/dbs_bkp/
    compress: true
```

It assumes `$HOME/dbs/logs_user.txt` and `$HOME/dbs_bkp/` already exist.
Running it:

```sh
./bin/bkp --config testdata/example_loca.yaml
```

```
Backup summary: Home logs backup
----------------------------------------------------------------------
user-logs                      ok           13ms
----------------------------------------------------------------------
```

gzips `$HOME/dbs/logs_user.txt` to something like
`$HOME/dbs/logs_user.txt.20260708T193000Z.gz` (compression always writes next
to the source, and defaults to a timestamped name so re-running doesn't
clobber the previous artifact), then copies that artifact into
`$HOME/dbs_bkp/`, alongside the orchestrator DB itself. Since `keep_compressed`
defaults to `false`, the copy in `$HOME/dbs/` is deleted right after the `cp`
succeeds — only the one in `$HOME/dbs_bkp/` remains. Since `skip_unchanged`
defaults to `true`, running this again against the same unmodified log file
logs a `skipped` row instead of re-gzipping it — a real log file that's
actively appended to would instead get a new sha1 each time and back up
normally.
