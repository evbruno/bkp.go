# bkp.go

bkp is a small CLI to back up files using commands you already use (for example `rclone copy`, `cp`, or custom shell commands), while tracking every run in a SQLite log.

You define projects in one YAML file. For each project, bkp can:

- gzip the source file (optional, enabled by default)
- run your backup command with the produced artifact
- skip unchanged files using SHA1 tracking (enabled by default)
- record results (`ok`, `skipped`, `error`) in an orchestrator SQLite database

## Install

Download a prebuilt binary from the [releases page](https://github.com/evbruno/bkp.go/releases):

```sh
curl -fsSL -o bkp.tar.gz \
  https://github.com/evbruno/bkp.go/releases/latest/download/bkp-linux-amd64.tar.gz
tar -xzf bkp.tar.gz
sudo mv bkp-linux-amd64 /usr/local/bin/bkp
```

Swap `amd64` for `arm64` on ARM hosts.

## Quick start

1. Create a config file at `$HOME/.config/bkp/bkp.yaml`:

```yaml
title: My backups
backup_self: true
self_command: rclone copy bkp.sqlite3 backups:orchestrator

projects:
  - name: app-one
    base_dir: /home/ubuntu/dbs
    file: production1.sqlite3
    command: rclone copy {{file}} backups:app-one

  - name: app-two
    base_dir: /home/ubuntu/dbs
    file: production2.sqlite3
    command: rclone copy {{file}} backups:app-two
    compress: false
```

2. Run backups:

```sh
bkp
```

3. Check latest status per project:

```sh
bkp status
```

## Command usage

```sh
bkp [--config path/to/spec.yaml] [--dry-run]
bkp status [--config path/to/spec.yaml]
bkp --version
bkp version
```

### `--config` path resolution

If you do not pass `--config`, bkp resolves config in this order:

1. `BKP_CONFIG`
2. `$HOME/.config/bkp/bkp.yaml`

### `target` (orchestrator database) resolution

Inside the YAML file, `target` is optional. When missing, bkp resolves it in this order:

1. `BKP_TARGET`
2. `$HOME/.local/state/bkp/bkp.sqlite3`

If the target directory does not exist, bkp creates it automatically.

## Config reference

Top-level fields:

- `title`: summary title shown in output
- `target`: path to the orchestrator SQLite database (optional)
- `backup_self`: whether to back up the orchestrator DB itself (default: `true`)
- `self_command`: command used for orchestrator self-backup (required when `backup_self: true`)
- `projects`: list of backup projects (required, at least one)

Project fields:

- `name`: unique project name
- `base_dir`: working directory where source file lives
- `file`: source filename (relative to `base_dir`)
- `command`: command to run for backup; `{{file}}` is replaced with artifact path
- `compress`: gzip before command (default: `true`)
- `timestamp`: timestamp suffix for gzip filename (default: `true`)
- `skip_unchanged`: skip when SHA1 matches latest successful run (default: `true`)
- `keep_compressed`: keep `.gz` artifact after successful command (default: `false`)

## How `{{file}}` works

- With `compress: true`, `{{file}}` points to a gzip artifact.
- With `timestamp: true` (default), artifact looks like `my.db.20260708T193000Z.gz`.
- With `timestamp: false`, artifact is `my.db.gz`.
- With `compress: false`, `{{file}}` is just the original `file`.
- If `command` does not contain `{{file}}`, it runs as-is.

Commands run with `sh -c` in `base_dir`.

## Output and status

Main run output includes project-level summary columns:

- `PROJECT`
- `FILE`
- `SHA1`
- `STATUS`
- `DURATION`
- `ERROR`

`bkp status` is read-only: it prints the latest logged row per project and never executes backup commands.

## Dry run

Use dry run to validate config and preview what would execute:

```sh
bkp --dry-run
```

Dry run does not compress files, execute commands, or write log rows.

## Build from source

Requires Go 1.26+.

```sh
make build
make test
```
