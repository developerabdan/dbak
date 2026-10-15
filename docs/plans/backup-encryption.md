# Plan: backup encryption with age

Status: accepted on 2026-10-15. The user accepted all recommendations in the
last section.

## Goal

The SFTP server gets only encrypted backups. A person with access to the SFTP
server cannot read the data. A person with root access to the VM cannot read
the old backups.

The restore must work without dbak and without the old VM. The user needs only
the private key, the `age` tool, and the normal restore tools.

SPEC.md section 1 puts archive encryption with age in the v2 list. Before the
build, SPEC.md must move this feature into the scope.

## Rules

1. dbak writes the standard age format only. It adds no header and no wrapper.
2. dbak keeps only public keys. The private key is never on the VM disk.
3. The content inside the encryption is the same as today: the `pg_dump`
   custom format, or the `mysqldump` SQL compressed with zstd.
4. Encryption is optional. If it is off, dbak works as in v0.2.0.

## Config

```yaml
encryption:
  recipients:
    - age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p
    - age1lggyhqrw2nlhcxprm67z43rta597azn8gknawjehu9d9dl0jq3yqqvfafg
```

Rules:

- `encryption` is optional. If it is not in the config, or `recipients` is
  empty, dbak does not encrypt.
- Each recipient is an age X25519 public key that starts with `age1`. dbak does
  not accept the post-quantum hybrid keys (`age1pq1...`). The `age` tool in
  many Linux distributions cannot decrypt files for these keys.
- Each recipient can decrypt all backups.
- `encryption` is global. It applies to all sources.
- The config version stays 2. A config without `encryption` stays valid.

## Onboarding

`dbak init` gets a new section "Backup encryption" after the retention section:

```
Encrypt the backups?
  > Yes, make a new key for me
    Yes, I have an age public key
    No
```

### Option A: dbak makes the key

1. dbak makes an age X25519 key pair in memory.
2. dbak shows the private key one time. It shows this warning: "Save this key
   in a password manager. Without this key, nobody can restore the backups."
3. dbak asks the user to paste the private key back. The input is hidden.
4. dbak examines the pasted key. If its public key is not the same, dbak shows
   an error. The user can paste again or show the key again.
5. dbak encrypts a short test text and decrypts it with the pasted key, in
   memory. This proves that the key works.
6. dbak clears the screen and saves only the public key in the config.

dbak never writes the private key to a file or to the log.

### Option B: the user gives a public key

1. The user runs `age-keygen -o dbak-key.txt` on a different computer.
2. The user pastes the `age1...` public key into `dbak init`.
3. dbak examines the format of the key.

### More recipients

After option A or B, dbak asks: "Add another public key?". The user can add a
key for a team member or an emergency key.

### Change an existing config

The menu of `dbak init` gets the section "Backup encryption". The user can add
a key, remove a key, or turn encryption off. If the user turns encryption off,
dbak shows a warning. dbak does not decrypt or change old backups.

### init --config

`dbak init --config` reads `encryption.recipients` from the file. It does not
make a key. It examines the format of each key.

## Backup pipeline

The stream changes from:

```
dump → (zstd for MySQL) → SFTP
```

to:

```
dump → (zstd for MySQL) → age encrypt → SFTP
```

- The encryption runs in the Go process. dbak does not need the `age` tool.
- dbak closes the zstd writer first, then the age writer. The age writer
  writes its last chunk when it closes.
- No file goes to the local disk. This rule does not change.

## Remote file names

| Engine | Without encryption | With encryption |
|---|---|---|
| PostgreSQL | `.dump` | `.dump.age` |
| MySQL, MariaDB | `.sql.zst` | `.sql.zst.age` |

The retention and `dbak list` accept all four extensions. One database
directory can contain old files without encryption and new files with
encryption. The retention counts them together.

## Other commands

| Command | Change |
|---|---|
| `doctor` | New item "Encryption". OK with the number of public keys. WARN if encryption is off. FAIL if a key is not valid. |
| `status` | Shows "Encryption: on, 2 public keys" or "Encryption: off". |
| `list` | No change. The file name shows `.age`. |
| `run` | The log shows `encrypted=true` for each backup. |

## Restore without dbak

The README gets a section "Restore after a disaster":

```sh
age -d -i dbak-key.txt app_prod_20261015T190000Z.dump.age \
  | pg_restore --host H --username U --dbname app_prod --no-owner --clean --if-exists

age -d -i dbak-key.txt shop_20261015T190000Z.sql.zst.age | zstd -d | mysql --host H --user U -p shop
```

## Remove old temporary credential files

This item is separate from the encryption, but it is small.

After a hard crash, a `pgpass-*` or `my-*.cnf` file can stay in
`/var/lib/dbak`. After `dbak run` gets the lock, it removes these files if
they are older than 1 hour. The age limit keeps the files of a `dbak init` or
`dbak doctor` that runs at the same time.

## Dependency

The encryption needs `filippo.io/age`. SPEC.md section 2 does not list this
library. The user must approve it.

## Work steps

Do each step with tests and small commits. Run the checks from CLAUDE.md before
each commit.

1. **Cleanup.** Remove old temporary credential files at the start of a run.
2. **Config.** The `encryption` block, the validation of the keys, and table
   tests.
3. **Pipeline.** The age writer, the close order, and the new extensions in the
   naming, the retention, and `list`. A unit test decrypts an uploaded file.
4. **Onboarding.** Options A and B, the paste-back check, the test in memory,
   more recipients, the edit menu, and `init --config`.
5. **Commands.** `doctor` and `status`.
6. **Integration and end-to-end tests.** Encrypted backups from PostgreSQL 16
   and MySQL 8.0. Decrypt each file with the `age` tool and restore it.
7. **Documents and release.** Update README.md and SPEC.md, then release
   `v0.3.0`.

## Decisions

The user accepted these decisions on 2026-10-15.

| ID | Question | Decision |
|---|---|---|
| E1 | Can dbak add the dependency `filippo.io/age`? | Yes. It is the reference library of the age format. |
| E2 | Can dbak update SPEC.md and move age encryption into the scope? | Yes. |
| E3 | Encryption for all sources, or for each source? | For all sources. One key set is easier to keep safe. |
| E4 | Which option is the default in `dbak init`? | Option A, "make a new key for me". |
| E5 | Accept SSH public keys (`ssh-ed25519`) as recipients too? | No, only `age1` keys in v0.3.0. |
| E6 | Must `doctor` show WARN if encryption is off? | Yes. |
| E7 | Add an `encrypted` field to the webhook payload? | No. The file name shows `.age`. |
| E8 | Include the cleanup of old temporary credential files? | Yes, as the first step. |
| E9 | Version number of the release? | `v0.3.0`. |
