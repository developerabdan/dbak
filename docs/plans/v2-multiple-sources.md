# Plan: more than one database connection (v2)

Status: accepted on 2026-10-15. The last section shows the decisions. The
build uses these decisions.

## Goal

One dbak install can make backups from more than one database connection. For
example, one VM has a PostgreSQL server and a MySQL server. Each connection
can have many databases.

In v0.1.0, one install has one connection. SPEC.md section 1 puts this feature
in the v2 list. Before the build, SPEC.md must move this feature into the
scope.

## Selected design: a list of sources in one config

One config has a list of sources. One timer runs all sources in order. One run
sends one notification.

The rejected design is named instances, with one config and one timer for each
connection. It is more difficult to use, and it sends one notification for each
connection.

## Config version 2

```yaml
version: 2
sources:
  - name: main_pg
    engine: postgres
    host: 127.0.0.1
    port: 5432
    username: backup_ro
    password_file: /etc/dbak/secrets/main_pg/db_password
    ssl_mode: prefer
    bin_dir: /usr/lib/postgresql/16/bin
    databases:
      - app_prod
      - app_audit
  - name: shop_mysql
    engine: mysql
    host: 127.0.0.1
    port: 3306
    username: backup_ro
    password_file: /etc/dbak/secrets/shop_mysql/db_password
    ssl_mode: preferred
    databases:
      - shop
    retention:
      daily: 14
      weekly: 0
      monthly: 0
destination:
  type: sftp
  host: backup.example.com
  port: 22
  username: dbak
  private_key_file: /etc/dbak/secrets/id_ed25519
  known_hosts_file: /etc/dbak/secrets/known_hosts
  remote_path: /backups
schedule:
  frequency: daily
  time: "02:00"
  timezone: Asia/Jakarta
  randomized_delay: 5m
retention:
  daily: 7
  weekly: 4
  monthly: 6
notify:
  when: failure
```

Rules:

- `sources` has one or more items.
- `name` uses only letters, digits, and underscores. Each name is unique.
- `bin_dir` is optional. If it is set, dbak uses the client tool from this
  directory. One VM can then use a different `pg_dump` version for each source.
- The password of each source is in `/etc/dbak/secrets/<name>/db_password`.
  The directory has mode 0700 and the file has mode 0600.
- `retention` is optional for each source. If a source has no `retention`,
  it uses the global `retention`.
- `destination`, `schedule`, and `notify` stay global.

### Version 1 configs

The loader still reads version 1. It converts the `source` block to one item in
`sources` with the name `default`. The next save from `dbak init` writes
version 2. dbak does not change the old file before that save.

## Remote layout

All sources use this layout:
`<remote_path>/<hostname>/<source>/<db>/<db>_<timestamp>.<ext>`

After an upgrade from version 1, the old backups stay in
`<remote_path>/<hostname>/<db>/`. `list` and the retention do not see them. The
user removes them manually.

## Backup run

1. Take the lock and send the healthcheck `/start` ping. This step does not
   change.
2. For each source, in the config order:
   1. Connect to the database server.
   2. If the connection fails, mark all databases of this source as failed.
      Continue with the next source.
   3. After the first good database connection, connect to the SFTP server.
      The run uses this one SFTP connection for all sources.
   4. Make the backup of each database, as in v1.
   5. Run the retention of the source for each database with a good backup.
4. Save one run in `state.json`. Each database result gets a `source` field.
5. Send one notification and the healthcheck ping.

Exit codes:

| Result | Code |
|---|---|
| All databases of all sources are good | 0 |
| Some databases are good | 6 |
| All connections to the database servers failed | 3 |
| All password files cannot be read | 2 |
| All dumps failed | 4 |
| All uploads failed | 5 |
| No database is good and the causes are different | 4 |

New flags for `dbak run`:

- `--source NAME` makes a backup of one source only.
- `--db NAME` must have `--source` if two sources have a database with that
  name.

## Notifications

- The webhook payload gets a `source` field in each database item. The other
  fields do not change, so the change is compatible.
- The Telegram text uses `source/database`, for example
  `main_pg/app_prod: success, 100.0 MiB`.

## Commands

| Command | Change |
|---|---|
| `init` | First run: ask for the name of the source. After the dump check, ask "Add another database source?". With a config: the menu can add, change, or remove a source. The retention section can set a retention for each source. |
| `init --config` | Read version 1 or 2. The password for a source comes from `DBAK_DB_PASSWORD_<NAME>`, with the source name in capital letters. `DBAK_DB_PASSWORD` stays valid for a config with one source. |
| `run` | New flag `--source`. |
| `status` | New `SOURCE` column. The JSON gets a `source` field. |
| `list` | New `SOURCE` column and new flag `--source`. |
| `doctor` | Examine the client tool, the connection, and the dump check for each source. The item names show the source, for example `Database main_pg`. |
| `uninstall` | Also remove `/etc/dbak/secrets/<name>/` for each source. |
| `user-helper` | No change. |
| `schedule` | No change. |

## Work steps

Do each step with tests and small commits. Run the checks from CLAUDE.md before
each commit.

1. **Config v2.** Types, validation, the conversion of version 1, and the
   `bin_dir` field. Table tests for the validation and the conversion.
2. **Pipeline.** Run each source, continue after a failed connection, the new
   remote layout, the retention of each source, the `source` field in the
   state, the exit codes, and the notification changes.
3. **Commands.** `run --source`, `status`, `list`, `doctor`, and `uninstall`.
4. **Onboarding.** Add, change, and remove a source in `dbak init`. Update
   `init --config` and the environment variables.
5. **Integration and end-to-end tests.** One config with PostgreSQL 16 and
   MySQL 8.0. One test where one source fails and the run gives exit code 6.
   One test that upgrades a version 1 config.
6. **Documents and release.** Update README.md and SPEC.md, then release
   `v0.2.0`.

## Decisions

The user confirmed these decisions on 2026-10-15.

| ID | Question | Decision |
|---|---|---|
| D1 | A list of sources in one config, or named instances? | A list of sources. |
| D2 | For an upgrade from v1: keep the old remote layout, or use the new layout? | The new layout. The user removes the old backups manually. |
| D3 | Retention for each source, or one global retention? | A retention for each source. It is optional. Without it, the source uses the global retention. |
| D4 | Add `bin_dir` for each source? | Yes. |
| D5 | Can dbak update SPEC.md with the v2 scope before the build? | Yes. |
| D6 | Version number of the release? | `v0.2.0`. |
