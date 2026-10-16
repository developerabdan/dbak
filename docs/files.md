# Files

## Backup files

```
<remote_path>/<hostname>/<source>/<database>/<database>_<YYYYMMDD>T<HHMMSS>Z.<ext>
```

The time is in UTC, so the alphabetical order is also the time order.

| Engine | Extension | Format |
|---|---|---|
| PostgreSQL | `.dump` | `pg_dump --format=custom` |
| MySQL, MariaDB | `.sql.zst` | `mysqldump` output, compressed with zstd |

With encryption, the file name gets `.age` after the extension, for example
`.dump.age`. The retention and `dbak list` accept files with and without
`.age`.

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
