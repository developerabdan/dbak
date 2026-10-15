# dbak

A database backup agent for Linux virtual machines.

dbak makes backups of PostgreSQL and MySQL databases on a schedule. It sends
the backups to an SFTP server.

## Features

- **One line to install.** A `curl | sudo bash` command installs the binary.
  Then `dbak init` asks some questions, and the first backup starts.
- **PostgreSQL, MySQL, and MariaDB.** dbak finds the server type and version.
  It uses the correct dump options for each server.
- **More than one database server.** One install can make backups from many
  database sources. For example, a PostgreSQL server and a MySQL server on the
  same virtual machine. One run sends one notification.
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
| `DBAK_BASE_URL=URL` | Download the release from a mirror of the GitHub Releases page. |

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
2. Select the database engine, give the source a name, and type the
   connection settings.
3. If you do not have a backup user, select the SQL helper. Run the SQL on the
   database server.
4. To add a different database server, answer Yes to "Add another database
   source?".
5. Type the SFTP server and the remote path.
6. Add the public key that dbak shows to `~/.ssh/authorized_keys` on the SFTP
   server.
7. Make sure that the host key fingerprint is correct.
8. Select the schedule, the retention, and the notifications.
9. Let dbak run the first backup.

To see the result, run `dbak status`.

## Setup without questions

For automation, give dbak init a config file. dbak does not ask questions:

```sh
sudo DBAK_DB_PASSWORD_MAIN_PG='the password' \
  DBAK_DB_PASSWORD_SHOP_MYSQL='the other password' \
  dbak init --config /root/dbak.yaml
```

dbak reads the file and examines the database, the dump check, and the SFTP
upload. Only after all checks pass, it writes `/etc/dbak/config.yaml` and the
secrets, and installs the schedule. If a check fails, dbak changes nothing.

- The password of each source comes from `DBAK_DB_PASSWORD_<NAME>` or from
  the `password_file` of the source. `<NAME>` is the source name in capital
  letters. If the config has one source, `DBAK_DB_PASSWORD` is also correct.
- The Telegram bot token comes from `DBAK_TELEGRAM_TOKEN` or from
  `notify.telegram_token_file`.
- `destination.private_key_file` must be a key file that exists, without a
  passphrase.
- `destination.known_hosts_file` must contain the host key of the SFTP server.
  Make it with `ssh-keyscan` and compare the fingerprint with the server.

dbak copies the key and the known_hosts file to `/etc/dbak/secrets/`. The exit
codes are the same as for `dbak run`.

## Commands

| Command | What it does |
|---|---|
| `dbak init [--config FILE]` | Asks the questions, saves the config, and installs the schedule. With `--config`, it uses the file and does not ask questions. |
| `dbak run [--source NAME] [--db NAME]` | Makes a backup now. The schedule uses this command. |
| `dbak status [--json]` | Shows the last run of each database and the next scheduled run. |
| `dbak list [--source NAME] [--db NAME]` | Shows the backups on the SFTP server. |
| `dbak doctor` | Examines the config, the permissions, the tools, the connections, and the schedule. It examines each source. |
| `dbak user-helper` | Shows the SQL that makes a read-only backup user. |
| `dbak schedule [--show]` | Installs the schedule from the config again, or shows it. |
| `dbak uninstall` | Removes the schedule and the binary. The backups on the SFTP server stay. |
| `dbak version` | Shows the version, the commit, and the build date. |

Run `dbak help COMMAND` for the flags of a command.

If two sources have a database with the same name, `--db` must have
`--source`.

## Configuration

`dbak init` writes `/etc/dbak/config.yaml`:

```yaml
version: 2
sources:
  - name: main_pg
    engine: postgres
    host: 10.0.0.10
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
| `sources[].name` | Letters, digits, and underscores. Each source has a different name. dbak uses the name in the remote directory. |
| `sources[].engine` | `postgres` or `mysql`. Use `mysql` also for MariaDB. |
| `sources[].bin_dir` | Optional. The directory of `pg_dump` or `mysqldump` for this source. If it is empty, dbak uses `PATH`. |
| `sources[].retention` | Optional. The retention for this source. If it is empty, the source uses the global `retention`. |
| `schedule.frequency` | `daily`, `every_n_hours`, or `weekly`. |
| `schedule.every_hours` | 1, 2, 3, 4, 6, 8, or 12. Only for `every_n_hours`. |
| `schedule.weekday` | `Sun` to `Sat`. Only for `weekly`. |
| `schedule.randomized_delay` | `0`, `5m`, or `15m`. |
| `notify.when` | `failure`, `always`, or `never`. The healthcheck ping does not use this field. |

The config does not contain secrets. Each secret is in a separate file that the
config names.

If you change the config manually, run `sudo dbak schedule`. To change the
config with questions, run `sudo dbak init` again and select one section. In
the section "Database sources", you can add, change, or remove a source.

### Upgrade from version 1

dbak 0.2 reads a version 1 config with one `source` block. It uses the block as
one source with the name `default`. The next `sudo dbak init` writes the config
in version 2.

The new backups go to `<remote_path>/<hostname>/default/<database>/`. The old
backups stay in `<remote_path>/<hostname>/<database>/`. `dbak list` and the
retention do not see the old backups. When you do not need them, remove them
manually on the SFTP server.

## Retention

dbak runs the retention after each good backup of a database. Each source uses
its own `retention`, or the global `retention` if it has none.

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
    {"source": "main_pg", "name": "app_prod", "status": "success", "size_bytes": 104857600, "remote_path": "/backups/vm-app-01/main_pg/app_prod/app_prod_20261015T190000Z.dump"},
    {"source": "main_pg", "name": "app_audit", "status": "failure", "error": "pg_dump: permission denied for table audit_log"}
  ]
}
```

The `status` is `success`, `partial`, or `failure`.

Telegram gets a short text with the host, the status, and the databases, for
example `main_pg/app_prod: success, 100.0 MiB`. For
the healthcheck, dbak sends a request to `<URL>/start` when a run starts. After
a good run, it sends a request to `<URL>`. After a failure, it sends a request to
`<URL>/fail`.

Each request has a timeout of 10 seconds and 2 retries. A failed notification
does not change the exit code.

## Backup files

```
<remote_path>/<hostname>/<source>/<database>/<database>_<YYYYMMDD>T<HHMMSS>Z.<ext>
```

The time is in UTC, so the alphabetical order is also the time order.

| Engine | Extension | Format |
|---|---|---|
| PostgreSQL | `.dump` | `pg_dump --format=custom` |
| MySQL, MariaDB | `.sql.zst` | `mysqldump` output, compressed with zstd |

## Files on the server

| Path | Content | Owner and mode |
|---|---|---|
| `/usr/local/bin/dbak` | The binary | root, 0755 |
| `/etc/dbak/config.yaml` | The config | dbak, 0600 |
| `/etc/dbak/secrets/` | The SSH key, the host key, and the Telegram token | dbak, 0700 for the directory and 0600 for the files |
| `/etc/dbak/secrets/<source>/db_password` | The database password of one source | dbak, 0700 for the directory and 0600 for the file |
| `/var/lib/dbak/state.json` | The history of the last 50 runs | dbak, 0600 |
| `/var/log/dbak/dbak.log` | The log, only with cron | dbak, 0640 |

With systemd, the log is in the journal. To see it, run `journalctl -u dbak.service`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | All backups are good. |
| 1 | A general error occurred. |
| 2 | The config is not valid. |
| 3 | dbak cannot connect to a database server. All sources failed. |
| 4 | The dump of all databases failed. |
| 5 | The upload to the SFTP server failed. |
| 6 | The backup of some databases failed. For example, one source is not available. |
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

## Release

1. Make sure that the CI workflow passes on `main`.
2. Make a tag, for example `git tag v1.0.0`.
3. Push the tag with `git push origin v1.0.0`.

The release workflow runs GoReleaser. GoReleaser puts the binaries for
`linux/amd64` and `linux/arm64`, `checksums.txt`, and `install.sh` on the GitHub
Releases page.

To build a release locally without a publish, run
`goreleaser release --snapshot --clean`.

## License

MIT license, see [LICENSE](LICENSE).
