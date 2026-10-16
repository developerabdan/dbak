# Configuration

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
encryption:
  recipients:
    - age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p
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
| `notify.telegram_thread_id` | Optional. The topic ID (`message_thread_id`) in a Telegram forum group. Without it, the message goes to the main chat or to General. |
| `encryption.recipients` | Optional. A list of age X25519 public keys that start with `age1`. If the list is empty, dbak does not encrypt. |

The config does not contain secrets. Each secret is in a separate file that the
config names.

If you change the config manually, run `sudo dbak schedule`. To change the
config with questions, run `sudo dbak init` again and select one section. To
change only the sources or the notifications, use `sudo dbak source` or
`sudo dbak notify`.

## Upgrade from version 1

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

The `status` is `success`, `partial`, or `failure`. `dbak notify test` sends
the status `test` with an empty `databases` list.

Telegram gets a short text with the host, the status, and the databases, for
example `main_pg/app_prod: success, 100.0 MiB`.

To send to a topic in a Telegram forum group, give the chat ID of the group and
the topic ID. Send a message in the topic, then open
`https://api.telegram.org/bot<TOKEN>/getUpdates`. In the result, `chat.id` is
the chat ID, for example `-1003749220292`. `message_thread_id` is the topic ID,
for example `316`. For
the healthcheck, dbak sends a request to `<URL>/start` when a run starts. After
a good run, it sends a request to `<URL>`. After a failure, it sends a request to
`<URL>/fail`.

Each request has a timeout of 10 seconds and 2 retries. A failed notification
does not change the exit code.
