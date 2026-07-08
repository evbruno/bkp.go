# bkp.go

A small Go CLI that reads a YAML backup spec, runs a backup command per
project (optionally gzipping the source first), optionally backs up its own
orchestrator database, and logs every backup call to a single SQLite table.

See [PLAN.md](PLAN.md) for the full design.

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

Pushing a `vX.Y.Z` tag triggers [.github/workflows/release.yml](.github/workflows/release.yml),
which cross-compiles `linux/amd64`, `linux/arm64`, and `darwin/arm64`, and
publishes them as tarballs (plus a `checksums.txt`) on a new GitHub release:

```sh
git tag v0.0.3
git push origin v0.0.3
```

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
PROJECT       LAST RUN (UTC)       STATUS  SIZE   GZ SIZE  DURATION  ERROR
app-one       2026-07-08 19:50:52  ok      32     56       8ms
app-two       2026-07-08 19:50:52  ok      32     -        6ms
orchestrator  2026-07-08 19:50:52  ok      12288  -        0s
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

  - name: My Petshop platform
    base_dir: /home/ubuntu/dbs/
    file: production2.sqlite3
    command: rclone copy {{file}} backups:my-app
```

`{{file}}` in `command` is substituted with the final artifact path
(`file.gz` when `compress: true`, otherwise `file`). If `command` doesn't
contain `{{file}}`, it runs verbatim. Commands run as `sh -c "<command>"`
with working directory `base_dir`.

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

Output:

```
Backup summary: Demo backup
----------------------------------------------------------------------
app-one                        ok            9ms
app-two                        ok            6ms
orchestrator                   ok            7ms
----------------------------------------------------------------------
```

`examples/demo/remote/` now contains `production1.sqlite3.gz` (compressed,
since `app-one` left `compress` at its default of `true`), `production2.sqlite3`
(uncompressed, since `app-two` set `compress: false`), and a copy of the
orchestrator DB itself.

Every call is logged to `orchestrator.sqlite3` in the `backup_log` table:

```sh
sqlite3 examples/demo/orchestrator.sqlite3 \
  "SELECT project, file_path, file_size, compressed_size, status, duration_ms FROM backup_log;"
```

```
app-one|examples/demo/dbs/production1.sqlite3|32|56|ok|8
app-two|examples/demo/dbs/production2.sqlite3|32||ok|5
orchestrator|examples/demo/orchestrator.sqlite3|12288||ok|0
```

Run the same config with `--dry-run` to see what would happen without
touching the filesystem or the log:

```sh
./bin/bkp --config examples/demo/config.yaml --dry-run
```

```
Backup summary: Demo backup
----------------------------------------------------------------------
app-one                        dry-run        0s
app-two                        dry-run        0s
orchestrator                   dry-run        0s
----------------------------------------------------------------------
```

Re-running `make run` overwrites `examples/demo/orchestrator.sqlite3` and the
files under `examples/demo/remote/`; both are gitignored generated output,
not fixtures.

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

gzips `$HOME/dbs/logs_user.txt` to `$HOME/dbs/logs_user.txt.gz` (compression
always writes next to the source), then copies that artifact into
`$HOME/dbs_bkp/`, alongside the orchestrator DB itself.
