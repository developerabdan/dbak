# dbak

A database backup agent for Linux virtual machines.

dbak makes backups of PostgreSQL and MySQL databases on a schedule. It sends
the backups to an SFTP server.

## Features

- **One line to install.** A `curl | sudo bash` command installs the binary.
  Then `dbak init` asks some questions, and the first backup starts.
- **PostgreSQL, MySQL, and MariaDB.** dbak finds the server type and version.
  It uses the correct dump options for each server.
- **Read-only backup user.** `dbak user-helper` shows the SQL that makes a user
  with only the privileges that a backup needs.
- **Dump check before the first backup.** dbak finds the tables that the backup
  user cannot read. It shows the GRANT statement that corrects the problem.
- **No local copy.** The dump goes directly to the SFTP server. A file gets its
  final name only after the upload is complete.
- **Pinned host key.** dbak pins the SFTP host key in `dbak init`. If the key
  changes, the backup stops.
- **Schedule in your timezone.** dbak installs a systemd timer. If the system
  has no systemd, dbak uses cron.
- **GFS retention.** dbak keeps daily, weekly, and monthly backups. It never
  removes the newest backup. It never touches files that it did not make.
- **Notifications.** A webhook, Telegram, and a healthcheck ping in the style
  of healthchecks.io.
- **Restore help.** `dbak restore` downloads a backup and shows the correct
  restore command. It does not change your database.
- **No secrets in logs.** Passwords and tokens are in files with mode 0600.
  They are not in logs, in process arguments, or in `config.yaml`.

## Requirements

- Linux on `amd64` or `arm64`.
- systemd, or cron if the system has no systemd.
- PostgreSQL, MySQL 5.7 or newer, or MariaDB. The tests use PostgreSQL 13
  and 16, MySQL 8.0, and MariaDB 10.11.
- `pg_dump` with the same major version as the server or a newer version, or
  `mysqldump`. `dbak init` can install the client tools for you.
- An SFTP server that accepts SSH keys. Password authentication is not
  supported.
- Root access for the install and for `dbak init`. The backups run as the
  `dbak` system user.
- To build from source: Go 1.26 or newer.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/developerabdan/dbak/main/install.sh | sudo bash
```

The script downloads the release for your platform and examines the SHA-256
checksum. It installs one file, `/usr/local/bin/dbak`, and makes the `dbak`
system user. Then it starts `dbak init`.

Installer options:

| Variable | Effect |
|---|---|
| `DBAK_VERSION=v1.0.0` | Install this version, not the latest version. |
| `DBAK_NO_INIT=1` | Do not start `dbak init` after the install. |

Put the variables after `sudo`:

```sh
curl -fsSL https://raw.githubusercontent.com/developerabdan/dbak/main/install.sh | sudo DBAK_NO_INIT=1 bash
```

With Go:

```sh
go install github.com/developerabdan/dbak/cmd/dbak@latest
sudo install -m 0755 "$(go env GOPATH)/bin/dbak" /usr/local/bin/dbak
sudo dbak init
```

## Quick start

1. Install dbak. The installer starts `dbak init`.
2. Select the database engine and type the connection settings.
3. If you do not have a backup user, select the SQL helper. Run the SQL on the
   database server.
4. Type the SFTP server and the remote path.
5. Add the public key that dbak shows to `~/.ssh/authorized_keys` on the SFTP
   server.
6. Make sure that the host key fingerprint is correct.
7. Select the schedule, the retention, and the notifications.
8. Let dbak run the first backup.

To see the result, run `dbak status`.

## Commands

| Command | What it does |
|---|---|
| `dbak init` | Asks the questions, saves the config, and installs the schedule. |
| `dbak run [--db NAME]` | Makes a backup now. The schedule uses this command. |
| `dbak status [--json]` | Shows the last run of each database and the next scheduled run. |
| `dbak list [--db NAME]` | Shows the backups on the SFTP server. |
| `dbak restore --db NAME [--at TIME \| --latest] [--out DIR]` | Downloads a backup and shows the restore command. |
| `dbak doctor` | Examines the config, the permissions, the tools, the connections, and the schedule. |
| `dbak user-helper` | Shows the SQL that makes a read-only backup user. |
| `dbak schedule [--show]` | Installs the schedule from the config again, or shows it. |
| `dbak uninstall` | Removes the schedule and the binary. The backups on the SFTP server stay. |
| `dbak version` | Shows the version, the commit, and the build date. |

Run `dbak help COMMAND` for the flags of a command.

## Configuration

`dbak init` writes `/etc/dbak/config.yaml`:

```yaml
version: 1
source:
  engine: postgres
  host: 10.0.0.10
  port: 5432
  username: backup_ro
  password_file: /etc/dbak/secrets/db_password
  ssl_mode: prefer
  databases:
    - app_prod
    - app_audit
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
  every_hours: 0
  weekday: ""
  timezone: Asia/Jakarta
  randomized_delay: 5m
retention:
  daily: 7
  weekly: 4
  monthly: 6
notify:
  when: failure
  webhook_url: ""
  telegram_token_file: ""
  telegram_chat_id: ""
  healthcheck_url: ""
```

| Field | Values |
|---|---|
| `source.engine` | `postgres` or `mysql`. Use `mysql` also for MariaDB. |
| `schedule.frequency` | `daily`, `every_n_hours`, or `weekly`. |
| `schedule.every_hours` | 1, 2, 3, 4, 6, 8, or 12. Only for `every_n_hours`. |
| `schedule.weekday` | `Sun` to `Sat`. Only for `weekly`. |
| `schedule.randomized_delay` | `0`, `5m`, or `15m`. |
| `notify.when` | `failure`, `always`, or `never`. The healthcheck ping does not use this field. |

The config does not contain secrets. Each secret is in a separate file that the
config names.

If you change the config manually, run `sudo dbak schedule`. To change the
config with questions, run `sudo dbak init` again and select one section.

## Retention

dbak runs the retention after each good backup of a database.

- **Daily** keeps the newest backup of each of the last N days that have a
  backup.
- **Weekly** keeps the newest backup of each of the last N ISO weeks that have
  a backup.
- **Monthly** keeps the newest backup of each of the last N months that have a
  backup.

One file can count for more than one rule. Days, weeks, and months use the
timezone of the schedule. With `every_n_hours`, dbak keeps all backups of today.
The daily rule applies to the days before today.

dbak never removes the newest backup. It removes `.partial` files that are older
than 24 hours. It does not touch files with other names.

## Notifications

The webhook gets a POST request with this JSON body:

```json
{
  "tool": "dbak",
  "host": "vm-app-01",
  "status": "partial",
  "started_at": "2026-10-15T19:00:00Z",
  "finished_at": "2026-10-15T19:02:13Z",
  "databases": [
    {"name": "app_prod", "status": "success", "size_bytes": 104857600, "remote_path": "/backups/vm-app-01/app_prod/app_prod_20261015T190000Z.dump"},
    {"name": "app_audit", "status": "failure", "error": "pg_dump: permission denied for table audit_log"}
  ]
}
```

The `status` is `success`, `partial`, or `failure`.

Telegram gets a short text with the host, the status, and the databases. For
the healthcheck, dbak sends a request to `<URL>/start` when a run starts. After
a good run, it sends a request to `<URL>`. After a failure, it sends a request to
`<URL>/fail`.

Each request has a timeout of 10 seconds and 2 retries. A failed notification
does not change the exit code.

## Backup files

```
<remote_path>/<hostname>/<database>/<database>_<YYYYMMDD>T<HHMMSS>Z.<ext>
```

The time is in UTC, so the alphabetical order is also the time order.

| Engine | Extension | Format |
|---|---|---|
| PostgreSQL | `.dump` | `pg_dump --format=custom` |
| MySQL, MariaDB | `.sql.zst` | `mysqldump` output, compressed with zstd |

## Restore

`dbak restore` downloads a backup and shows the command. For PostgreSQL:

```sh
pg_restore --host H --port P --username <write_user> --dbname <target_db> --no-owner --clean --if-exists app_prod_20261015T190000Z.dump
```

For MySQL and MariaDB:

```sh
mysql --host H --port P --user <write_user> -p <target_db> < app_prod_20261015T190000Z.sql
```

The `backup_ro` user cannot do a restore. Use a user with write privileges.

## Files on the server

| Path | Content | Owner and mode |
|---|---|---|
| `/usr/local/bin/dbak` | The binary | root, 0755 |
| `/etc/dbak/config.yaml` | The config | dbak, 0600 |
| `/etc/dbak/secrets/` | The password, the SSH key, the host key, and the Telegram token | dbak, 0700 for the directory and 0600 for the files |
| `/var/lib/dbak/state.json` | The history of the last 50 runs | dbak, 0600 |
| `/var/log/dbak/dbak.log` | The log, only with cron | dbak, 0640 |

With systemd, the log is in the journal. To see it, run `journalctl -u dbak.service`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | All backups are good. |
| 1 | A general error occurred. |
| 2 | The config is not valid. |
| 3 | dbak cannot connect to the database server. |
| 4 | The dump of all databases failed. |
| 5 | The upload to the SFTP server failed. |
| 6 | The backup of some databases failed. |
| 7 | A different `dbak run` is in progress. |

## Build and test

```sh
go build -o dbak ./cmd/dbak
go test ./...
```

The integration tests need Docker and the client tools:

```sh
docker compose -f docker-compose.test.yml -p dbaktest up -d
DBAK_IT_PG_BIN=/path/to/postgresql/bin \
DBAK_IT_MYSQL_BIN=/path/to/mysql/bin \
DBAK_IT_MARIADB_BIN=/path/to/mariadb/bin \
go test -tags integration ./internal/engine/ ./internal/backup/
```

The end-to-end tests run dbak in Linux containers:

```sh
./test/e2e/init.sh
./test/e2e/schedule.sh
```

To stop the test containers, run
`docker compose -f docker-compose.test.yml -p dbaktest down -v`.
