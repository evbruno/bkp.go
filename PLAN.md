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
- **Deleting the compressed artifact**: when `compress: true` and the command
  succeeds, the gzip artifact is deleted from `base_dir` (`keep_compressed:
  false`, the default) — it's a temporary file produced only to hand to
  `command`. Set `keep_compressed: true` to leave it on disk. Has no effect
  when `compress: false` (there's no artifact to delete). A failing command
  leaves the artifact in place either way, so it can be inspected/retried.
- **Skip unchanged files**: before compressing/running, sha1 the uncompressed
  source file and compare it to the sha1 recorded on that project's last
  *successful* (`status = "ok"`) row. On a match (`skip_unchanged: true`, the
  default), skip compression and the command entirely, and log a `skipped`
  row (same sha1/file_size, no `compressed_size`, no command run). A prior
  `error` row never counts as a match — only `ok` rows anchor the comparison,
  so a broken previous run always retries. Set `skip_unchanged: false` to
  always run regardless of the hash.
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
    skip_unchanged: true                # optional, default true
    keep_compressed: false              # optional, default false (only matters when compress: true)

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
- `skip_unchanged` defaults to `true` when omitted.
- `keep_compressed` defaults to `false` when omitted; only relevant when `compress: true`.
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
  status             TEXT    NOT NULL,   -- "ok" | "error" | "skipped"
  error              TEXT,               -- populated when status = "error"
  duration_ms        INTEGER,            -- wall-clock of the backup command
  sha1               TEXT                -- sha1 of the uncompressed source file, added via
                                          -- ALTER TABLE for DBs created before this column existed
);
```

`project` is the project `name`, or the literal `orchestrator` for the self-backup row.

## Execution flow

1. Parse flags: `--config <path.yaml>` (required), `--dry-run` (optional).
2. Load + validate YAML.
3. Open/create the orchestrator SQLite DB at `target`; ensure `backup_log` exists.
4. For each project (sequentially):
   a. Resolve source path = `base_dir/file`; stat for `file_size`; sha1 the file contents.
   b. If `skip_unchanged` and the sha1 matches the project's last `ok` row: insert a
      `skipped` row and move on — no compression, no command.
   c. If `compress`: gzip to `file.gz` (or `file.<ISO8601>.gz` if `timestamp`),
      record `compressed_size`; artifact = the `.gz` name. Else: artifact = source.
   d. Substitute `{{file}}` → artifact path in `command`.
   e. Run `sh -c command` in `base_dir`, time it.
   f. If `compress` and the command succeeded and `keep_compressed` is not
      set: delete the gzip artifact.
   g. Insert a `backup_log` row (ok/error), including the sha1 computed in (a).
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
  (one project fails, others still logged), timestamped vs fixed artifact
  names, skip-unchanged behavior (skips on matching sha1, re-runs on content
  change, never skips based on a prior `error` row), using a fake command
  (`true`/`false`), compressed-artifact deletion (deleted by default on
  success, kept with `keep_compressed: true`, kept on command failure
  regardless).
- `store`: migrate is idempotent (including adding `sha1` to a pre-existing
  table); InsertLog round-trips; `LatestOKSHA1` / `LatestPerProject` queries.

## Open / future
- Concurrency across projects (currently sequential).
- Retention/pruning of old artifacts.
- Retries on command failure.
```
