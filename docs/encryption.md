# Encryption and restore

## Encryption

dbak encrypts each backup with age before the upload. The SFTP server gets
only encrypted files. The VM keeps only the public keys, so a person with root
access to the VM cannot read the old backups.

In `dbak init`, select one option:

- **Make a new key for me.** dbak makes a key and shows the private key one
  time. Save the key in a password manager. Then paste the key back. dbak makes
  sure that the key is correct, clears the screen, and keeps only the public
  key.
- **I have an age public key.** Make the key on a different computer with
  `age-keygen -o dbak-key.txt`. Paste the `age1...` public key into
  `dbak init`. The private key never touches the VM.

You can add more public keys, for example for a team member or an emergency
key. Each key can decrypt all backups. dbak accepts only X25519 keys, not the
post-quantum keys from `age-keygen -pq`. The `age` tool in many Linux
distributions cannot decrypt files for post-quantum keys.

> **Warning:** If you lose all private keys, nobody can restore the encrypted
> backups. Keep two copies of the private key in different places. Do not keep
> the only copy on the VM or on the SFTP server.

If you turn encryption off, the next backups are not encrypted. The old
backups stay encrypted.

## Restore with dbak

On the VM, run:

```sh
sudo dbak restore
```

1. Select the source.
2. Select the backup. The list shows the date, the database, and the size.
3. Type the directory. The default directory is `~/backups` of the user that
   runs `sudo`, for example `/home/sukuna/backups`.
4. If the backup is encrypted, paste the age private key. dbak uses the key
   only for this restore. dbak does not save the key.

dbak decrypts the backup and writes the dump to the directory. A MySQL dump
becomes a `.sql` file. A PostgreSQL dump stays a `.dump` file. The file has
mode 0600, and the user that runs `sudo` owns it. dbak does not change a
database.

Then restore the file with the database tools:

```sh
pg_restore --host HOST --username USER --dbname app_prod --no-owner --clean --if-exists app_prod_20261015T190000Z.dump
mysql --host HOST --user USER -p < shop_20261015T190000Z.sql
```

If you type the private key on the VM, a person with root access to the VM
can get the key. If you do not trust the VM, do the restore on a different
computer without dbak.

## Restore after a disaster

You do not need dbak or the old VM. On any computer, you need the private key,
the [age](https://github.com/FiloSottile/age) tool, and the restore tools of the database.

1. Download the backup from the SFTP server.
2. Decrypt the backup and restore it in one command. The decrypted data does
   not go to the disk.

PostgreSQL:

```sh
age -d -i dbak-key.txt app_prod_20261015T190000Z.dump.age \
  | pg_restore --host HOST --username USER --dbname app_prod --no-owner --clean --if-exists
```

MySQL and MariaDB:

```sh
age -d -i dbak-key.txt shop_20261015T190000Z.sql.zst.age | zstd -d | mysql --host HOST --user USER -p
```

The MySQL dump contains `CREATE DATABASE` and `USE`, so it restores into the
database with the original name. For a backup without encryption, do not use
the `age -d` step.

Use a database user with write privileges. The read-only backup user cannot
restore. Do a test restore one time after the setup.
