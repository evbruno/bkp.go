# bkp.go — Backup Orchestrator

A small Go 1.26 CLI that reads a YAML backup spec, runs a backup command per
project (optionally gzipping the source first), optionally backs up its own
orchestrator database, and logs every backup call to a single SQLite table.

## Decisions (settled)

- **Compress flow**: when `compress: true` (the default), gzip
  `base_dir/file` → `base_dir/file.gz`, then run the project `command`. The
  command operates on the produced artifact.
- **Timestamped artifacts**: when `compress: true`, the gzip artifact is
  named `file.<ISO8601>.gz` by default (basic-format UTC, e.g.
  `file.20260708T193000Z.gz`), so each run produces a new file instead of
  overwriting the last one. Set `timestamp: false` to go back to a fixed
  `file.gz` name. Has no effect when `compress: false` (no artifact is
  created in that case).
- **Command placeholder**: the tool substitutes `{{file}}` in `command` with the
  final artifact path (the timestamped/fixed `.gz` name when compressed,
  otherwise `file`). If a command contains no `{{file}}`, it runs verbatim
  (matches the rclone examples, which use relative names).
- **Command execution**: `sh -c "<command>"` with working directory = `base_dir`.
  Supports pipes/env/relative filenames.
- **On failure**: a failing project does not stop the run. Log the failure
  (status + error message), continue with the rest, and exit non-zero at the end
  if any project failed.

## YAML shape

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

  - name: My Petshop platform
    base_dir: /home/ubuntu/dbs/
    file: production2.sqlite3
    command: rclone copy {{file}} backups:my-app
```

### Validation rules
- `target` required, non-empty.
- `projects` required, at least one entry.
- Each project: `name`, `base_dir`, `file`, `command` required.
- `compress` defaults to `true` when omitted.
- `timestamp` defaults to `true` when omitted; only relevant when `compress: true`.
- `backup_self` defaults to `true`; if true, `self_command` is required.
- Duplicate project `name`s → error (ambiguous log rows).

## Log schema (single table)

```sql
CREATE TABLE IF NOT EXISTS backup_log (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp          TEXT    NOT NULL,   -- RFC3339 UTC
  project            TEXT    NOT NULL,   -- project name or "orchestrator"
  file_path          TEXT    NOT NULL,   -- full path of the source/artifact
  file_size          INTEGER,            -- original file size in bytes
  compressed_size    INTEGER,            -- gzip size in bytes, NULL if not compressed
  status             TEXT    NOT NULL,   -- "ok" | "error"
  error              TEXT,               -- populated when status = "error"
  duration_ms        INTEGER             -- wall-clock of the backup command
);
```

`project` is the project `name`, or the literal `orchestrator` for the self-backup row.

## Execution flow

1. Parse flags: `--config <path.yaml>` (required), `--dry-run` (optional).
2. Load + validate YAML.
3. Open/create the orchestrator SQLite DB at `target`; ensure `backup_log` exists.
4. For each project (sequentially):
   a. Resolve source path = `base_dir/file`; stat for `file_size`.
   b. If `compress`: gzip to `file.gz`, record `compressed_size`; artifact = `.gz`.
      Else: artifact = source.
   c. Substitute `{{file}}` → artifact path in `command`.
   d. Run `sh -c command` in `base_dir`, time it.
   e. Insert a `backup_log` row (ok/error).
5. If `backup_self`: run `self_command` similarly (working dir = dir of `target`),
   log as `orchestrator`. Note: the self-backup row itself is written *before*
   the self_command runs so the copied DB is consistent-ish; document this caveat.
6. Print a summary table; exit non-zero if any row failed.

## Project layout

```
bkp.go/
├── go.mod                     (module github.com/evbruno/bkp.go, go 1.26)
├── main.go                    (flag parsing, wiring, exit code)
├── PLAN.md
├── internal/
│   ├── config/config.go       (YAML structs, Load + Validate, defaults)
│   ├── store/store.go         (SQLite open, migrate, InsertLog)
│   └── runner/runner.go       (compress, command exec, orchestration)
└── testdata/
    └── example.yaml
```

## Dependencies
- `gopkg.in/yaml.v3` — YAML parsing.
- `modernc.org/sqlite` — pure-Go SQLite (no CGO; portable static binary).
  (Alternative: `mattn/go-sqlite3` if CGO is acceptable — pure-Go preferred.)
- gzip: stdlib `compress/gzip`.

## Testing
- `config`: valid/invalid YAML, default application.
- `runner`: compress size math, `{{file}}` substitution, failure isolation
  (one project fails, others still logged), using a fake command (`true`/`false`).
- `store`: migrate is idempotent; InsertLog round-trips.

## Open / future
- Concurrency across projects (currently sequential).
- Retention/pruning of old artifacts.
- Retries on command failure.
- Checksums per backup row.
```
