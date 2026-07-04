---
title: de migrate
---

> Generated from `de migrate --help`. Run `make docs-cli` to refresh.

## `de migrate`

```text
Author and apply sequentially-numbered SQL schema migrations.

Migrations are golang-migrate-style files applied by the org-standard
infobloxopen/migrate fork: NNNN_<name>.up.sql + NNNN_<name>.down.sql, 4-digit
zero-padded, no gaps, no timestamps. Each change ships its inverse 'down'.

Near-zero-downtime rules (enforced/encoded here):
  - lock_timeout / statement_timeout live on the migration CONNECTION, never in
    a file (a per-file SET line breaks CREATE INDEX CONCURRENTLY).
  - CREATE/DROP INDEX CONCURRENTLY must be ALONE in its own file.
  - 'de migrate lint' fails on a numbering gap, a missing/duplicate pair, or a
    CONCURRENTLY statement mixed with others; 'squawk' adds deep Postgres checks
    when it is on PATH.

Usage:
  de migrate [command]

Available Commands:
  lint        Check migration numbering/pairing (+ best-effort squawk)
  new         Scaffold the next-numbered up/down migration pair
  status      Report current applied version vs on-disk target (+ dirty state)
  up          Apply pending migrations to the on-disk target version
  verify      Assert the schema is at the on-disk target and not dirty

Flags:
  -h, --help   help for migrate

Use "de migrate [command] --help" for more information about a command.
```

### `de migrate lint`

```text
Two checks:

  (a) A pure-Go sequence/pairing gate (always runs, authoritative): file names
      match NNNN_<name>.(up|down).sql, no numbering gaps, no duplicate numbers,
      every up has a matching down, and no CONCURRENTLY statement is mixed with
      others in the same file.

  (b) A best-effort 'squawk' run over *.up.sql for deep Postgres safety checks
      (blocking CREATE INDEX, ADD CONSTRAINT without NOT VALID, unsafe type
      changes, ...). squawk is a separate tool; if it is not on PATH the deep
      check is skipped (never a failure) and an install hint is printed.

Usage:
  de migrate lint [flags]

Flags:
      --dependency string   which postgres dependency to target when the project declares several
      --dir string          migrations directory (default: the devedge.yaml dependency's, else ./module/migrations for an SDK service scaffold, else ./db/migrations)
  -f, --file string         project config file (to resolve the migrations dir + DSN) (default "devedge.yaml")
  -h, --help                help for lint
```

### `de migrate new`

```text
Scaffold NNNN_<name>.up.sql and NNNN_<name>.down.sql at the next
sequential number (4-digit zero-padded) in the project's migrations directory.

The files carry a header comment documenting the authoring rules. Timeouts are
NOT written into the file — they belong on the connection (de sets them).

Usage:
  de migrate new NAME [flags]

Flags:
      --dependency string   which postgres dependency to target when the project declares several
      --dir string          migrations directory (default: the devedge.yaml dependency's, else ./module/migrations for an SDK service scaffold, else ./db/migrations)
  -f, --file string         project config file (to resolve the migrations dir + DSN) (default "devedge.yaml")
  -h, --help                help for new
```

### `de migrate status`

```text
Report current applied version vs on-disk target (+ dirty state)

Usage:
  de migrate status [flags]

Flags:
      --dependency string            which postgres dependency to target when the project declares several
      --dir string                   migrations directory (default: the devedge.yaml dependency's, else ./module/migrations for an SDK service scaffold, else ./db/migrations)
      --dsn string                   postgres DSN to apply against (default: DATABASE_URL, else the DSN de project up wrote)
  -f, --file string                  project config file (to resolve the migrations dir + DSN) (default "devedge.yaml")
  -h, --help                         help for status
      --lock-timeout duration        lock_timeout set on the migration connection (0 disables) (default 2s)
      --statement-timeout duration   statement_timeout set on the migration connection (0 disables) (default 1m0s)
```

### `de migrate up`

```text
Apply migrations through the infobloxopen/migrate fork up (or down) to
the highest version on disk. lock_timeout and statement_timeout are set on the
migration CONNECTION so a contended migration fails fast. Idempotent; recovers a
dirty database left by a prior failed run.

Usage:
  de migrate up [flags]

Flags:
      --dependency string            which postgres dependency to target when the project declares several
      --dir string                   migrations directory (default: the devedge.yaml dependency's, else ./module/migrations for an SDK service scaffold, else ./db/migrations)
      --downstore string             persisted down-store dir (default: the project's, else <migrations>/../.devedge-downstore)
      --dsn string                   postgres DSN to apply against (default: DATABASE_URL, else the DSN de project up wrote)
  -f, --file string                  project config file (to resolve the migrations dir + DSN) (default "devedge.yaml")
  -h, --help                         help for up
      --lock-timeout duration        lock_timeout set on the migration connection (0 disables) (default 2s)
      --statement-timeout duration   statement_timeout set on the migration connection (0 disables) (default 1m0s)
```

### `de migrate verify`

```text
Post-apply assertion (the unattended Job's final gate): the database is
at the highest on-disk migration version and not in a dirty state. Exits non-zero
with an actionable message otherwise.

Usage:
  de migrate verify [flags]

Flags:
      --dependency string            which postgres dependency to target when the project declares several
      --dir string                   migrations directory (default: the devedge.yaml dependency's, else ./module/migrations for an SDK service scaffold, else ./db/migrations)
      --dsn string                   postgres DSN to apply against (default: DATABASE_URL, else the DSN de project up wrote)
  -f, --file string                  project config file (to resolve the migrations dir + DSN) (default "devedge.yaml")
  -h, --help                         help for verify
      --lock-timeout duration        lock_timeout set on the migration connection (0 disables) (default 2s)
      --statement-timeout duration   statement_timeout set on the migration connection (0 disables) (default 1m0s)
```

