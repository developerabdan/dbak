# Features

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
- **Encrypted backups.** dbak can encrypt each backup with
  [age](https://age-encryption.org) before the upload. Only the public key is
  on the server. You can restore a backup without dbak.
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
