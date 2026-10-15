# Plan: more than one database connection (v2)

Status: draft. The user must confirm the decisions in the last section before
the build starts.

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
- `destination`, `schedule`, `retention`, and `notify` stay global.

### Version 1 configs

The loader still reads version 1. It converts the `source` block to one item in
`sources` with the name `default` and `legacy_layout: true`. The next save from
`dbak init` writes version 2. dbak does not change the old file before that
save.

## Remote layout

- New layout: `<remote_path>/<hostname>/<source>/<db>/<db>_<timestamp>.<ext>`
- A source with `legacy_layout: true` keeps the v1 layout:
  `<remote_path>/<hostname>/<db>/`. The old backups stay visible to `list` and
  to the retention.
- Validation rejects a source name that is the same as a database name of a
  legacy source. Both use the directory `<hostname>/<name>/`.

## Backup run

1. Take the lock and send the healthcheck `/start` ping. This step does not
   change.
2. Connect to the SFTP server one time for the full run.
3. For each source, in the config order:
   1. Connect to the database server.
   2. If the connection fails, mark all databases of this source as failed.
      Continue with the next source.
   3. Make the backup of each database, as in v1.
   4. Run the retention for each database with a good backup.
4. Save one run in `state.json`. Each database result gets a `source` field.
5. Send one notification and the healthcheck ping.

Exit codes:

| Result | Code |
|---|---|
| All databases of all sources are good | 0 |
| Some databases are good | 6 |
| All connections to the database servers failed | 3 |
| All dumps failed | 4 |
| All uploads failed | 5 |

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
| `init` | First run: ask for the name of the connection. After the dump check, ask "Add another database connection?". With a config: the menu can add, change, or remove a connection. |
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
   remote layout, the legacy layout, the `source` field in the state, the exit
   codes, and the notification changes. Unit tests with two fake sources.
3. **Commands.** `run --source`, `status`, `list`, `doctor`, and `uninstall`.
4. **Onboarding.** Add, change, and remove a connection in `dbak init`. Update
   `init --config` and the environment variables.
5. **Integration and end-to-end tests.** One config with PostgreSQL 16 and
   MySQL 8.0. One test where one source fails and the run gives exit code 6.
   One test that upgrades a version 1 install and keeps the old backups in
   the retention.
6. **Documents and release.** Update README.md and SPEC.md, then release
   `v0.2.0`.

## Decisions for the user

Confirm or change each item before the build starts.

| ID | Question | Recommendation |
|---|---|---|
| D1 | A list of sources in one config, or named instances? | A list of sources. |
| D2 | For an upgrade from v1: keep the old remote layout, or use the new layout? | Keep the old layout for the converted source. |
| D3 | Retention for each source, or one global retention? | One global retention in v2.0. |
| D4 | Add `bin_dir` for each source? | Yes. |
| D5 | Can dbak update SPEC.md with the v2 scope before the build? | Yes. |
| D6 | Version number of the release? | `v0.2.0`. |
