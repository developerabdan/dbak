# dbak

A database backup agent for Linux virtual machines.

dbak makes backups of PostgreSQL, MySQL, and MariaDB databases on a schedule.
It sends the backups directly to an SFTP server. It can encrypt each backup
with [age](https://age-encryption.org) before the upload.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/developerabdan/dbak/main/install.sh | sudo bash
```

The script installs `/usr/local/bin/dbak` and starts `dbak init`. `dbak init`
asks some questions, installs the schedule, and starts the first backup.

To see the result, run `dbak status`.

## Restore

You do not need dbak to restore a backup. You need the private key, `age`,
and `zstd`.

1. On your computer, install [age](https://github.com/FiloSottile/age) and
   `zstd`.
2. Download the backup from the SFTP server.
3. Decrypt the backup:

```sh
age -d -i key.txt encryptedfile.sql.zst.age | zstd -d -o output.sql
```

For a PostgreSQL backup, use `age -d -i key.txt file.dump.age > output.dump`.
Then restore it with `pg_restore`.

## Documentation

- [Features](docs/features.md)
- [Install](docs/install.md): requirements, installer options, and setup
  without questions.
- [Commands](docs/commands.md): all commands and exit codes.
- [Configuration](docs/configuration.md): the config file, retention, and
  notifications.
- [Encryption and restore](docs/encryption.md)
- [Files](docs/files.md): backup file names and files on the server.
- [Development](docs/development.md): build, test, and release.

## License

MIT license, see [LICENSE](LICENSE).
