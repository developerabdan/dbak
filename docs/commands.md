# Commands

| Command | What it does |
|---|---|
| `dbak init [--config FILE]` | Asks the questions, saves the config, and installs the schedule. With `--config`, it uses the file and does not ask questions. |
| `dbak run [--source NAME] [--db NAME]` | Makes a backup now. The schedule uses this command. |
| `dbak status [--json]` | Shows the last run of each database and the next scheduled run. |
| `dbak list [--source NAME] [--db NAME]` | Shows the backups on the SFTP server. |
| `dbak doctor` | Examines the config, the permissions, the tools, the connections, and the schedule. It examines each source. |
| `dbak source list` | Shows the database sources. |
| `dbak source add` | Adds a database source with questions, examines it, and can make a backup of it. |
| `dbak source edit NAME` | Changes a database source with questions. |
| `dbak source remove NAME` | Removes a database source and its password file. The backups on the SFTP server stay. |
| `dbak notify` | Changes the webhook, Telegram, and healthcheck settings with questions. |
| `dbak notify test` | Sends a test message to the webhook and to Telegram. |
| `dbak user-helper` | Shows the SQL that makes a read-only backup user. |
| `dbak schedule [--show]` | Installs the schedule from the config again, or shows it. |
| `dbak uninstall` | Removes the schedule and the binary. The backups on the SFTP server stay. |
| `dbak version` | Shows the version, the commit, and the build date. |

Run `dbak help COMMAND` for the flags of a command.

If two sources have a database with the same name, `--db` must have
`--source`.

Run `init`, `source add`, `source edit`, `source remove`, and `notify` with
`sudo`. These commands write the config and the secret files.

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
