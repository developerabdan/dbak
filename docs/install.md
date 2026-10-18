# Install

## Requirements

- Linux on `amd64` or `arm64`.
- systemd, or cron if the system has no systemd.
- PostgreSQL, MySQL 5.7 or newer, or MariaDB. The tests use PostgreSQL 13,
  16, 17, and 18, MySQL 8.0, and MariaDB 10.11.
- `pg_dump` with the same major version as the server or a newer version, or
  `mysqldump`. `dbak init` can install the client tools for you.
- If the PostgreSQL server is newer than the `pg_dump` of the system, `dbak
  init` can install a newer client from the PGDG repository. For example,
  Ubuntu 24.04 has `pg_dump` 16, and a PostgreSQL 17 server needs `pg_dump` 17
  or newer. After the install, dbak sets `bin_dir` of the source to the new
  client. The automatic install works on Debian, Ubuntu, and RHEL 9 or newer.
  On other systems, `dbak init` shows the commands. One `pg_dump` 18 can make
  backups of all servers from PostgreSQL 13 to 18. To use a different
  `pg_dump` for one source, set `bin_dir`.
- An SFTP server that accepts SSH keys. Password authentication is not
  supported.
- Root access for the install and for `dbak init`. The backups run as the
  `dbak` system user.
- To build from source: Go 1.26 or newer.

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
8. Select the schedule, the retention, the backup encryption, and the
   notifications.
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
