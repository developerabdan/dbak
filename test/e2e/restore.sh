#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d)"
vm="dbak-e2e-restore-vm"
rescue="dbak-e2e-restore-rescue"
image="dbak-e2e-restore-image"
host="vm-e2e-restore"

cleanup() {
	docker rm -f "$vm" "$rescue" >/dev/null 2>&1 || true
	docker exec dbaktest-postgres16-1 dropdb -U postgres --if-exists restore_test >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

case "$(docker info --format '{{.Architecture}}')" in
	aarch64 | arm64) arch=arm64 ;;
	*) arch=amd64 ;;
esac

if [[ -z "$(docker compose -f "$root/docker-compose.test.yml" -p dbaktest ps -q sftp)" ]]; then
	echo "The test containers are not running. Run: docker compose -f docker-compose.test.yml -p dbaktest up -d" >&2
	exit 1
fi

(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$work/dbak" ./cmd/dbak)

docker build -q -t "$image" - >/dev/null <<'EOF'
FROM ubuntu:24.04
RUN apt-get update \
	&& DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
		postgresql-client mysql-client zstd age openssh-client ca-certificates \
	&& rm -rf /var/lib/apt/lists/*
EOF

echo "== Make a key with age-keygen =="
docker run --rm -v "$work:/work" "$image" age-keygen -o /work/key.txt 2>/dev/null
public_key="$(docker run --rm -v "$work:/work" "$image" age-keygen -y /work/key.txt)"
[[ "$public_key" == age1* ]] || fail "no public key"

cat >"$work/config.yaml" <<EOF
version: 2
sources:
  - name: pg16
    engine: postgres
    host: postgres16
    port: 5432
    username: backup_ro
    password_file: /etc/dbak/secrets/pg16/db_password
    ssl_mode: disable
    databases:
      - app_prod
  - name: mysql8
    engine: mysql
    host: mysql8
    port: 3306
    username: backup_ro
    password_file: /etc/dbak/secrets/mysql8/db_password
    ssl_mode: preferred
    databases:
      - app_prod
destination:
  type: sftp
  host: sftp
  port: 22
  username: dbak
  private_key_file: /etc/dbak/secrets/id_ed25519
  known_hosts_file: /etc/dbak/secrets/known_hosts
  remote_path: /upload
schedule:
  frequency: daily
  time: "02:00"
  timezone: UTC
retention:
  daily: 7
encryption:
  recipients:
    - $public_key
EOF

echo "== Encrypted backup on the VM =="
docker run -d --name "$vm" --hostname "$host" --network dbaktest_default "$image" sleep infinity >/dev/null
docker cp "$work/dbak" "$vm:/usr/local/bin/dbak"
docker exec "$vm" mkdir -p /etc/dbak/secrets/pg16 /etc/dbak/secrets/mysql8 /var/lib/dbak
docker cp "$work/config.yaml" "$vm:/etc/dbak/config.yaml"
docker cp "$root/testdata/integration/db_password" "$vm:/etc/dbak/secrets/pg16/db_password"
docker cp "$root/testdata/integration/db_password" "$vm:/etc/dbak/secrets/mysql8/db_password"
docker cp "$root/testdata/integration/ssh/id_ed25519" "$vm:/etc/dbak/secrets/id_ed25519"
docker exec "$vm" sh -c 'ssh-keyscan -t ed25519 sftp 2>/dev/null > /etc/dbak/secrets/known_hosts'
docker exec "$vm" dbak run >"$work/run.log" 2>&1 || {
	cat "$work/run.log"
	fail "dbak run failed"
}
grep -q "encrypted=true" "$work/run.log" || fail "the log does not show the encryption"
docker exec "$vm" grep -rq "AGE-SECRET-KEY" /etc/dbak && fail "the VM has the private key"

echo "== dbak restore on the VM =="
private_key="$(grep AGE-SECRET-KEY "$work/key.txt")"
docker exec "$vm" useradd --create-home sukuna
printf '1\n\n\n%s\n' "$private_key" | docker exec -i -e SUDO_USER=sukuna "$vm" dbak restore >"$work/restore-pg.log" 2>&1 || {
	cat "$work/restore-pg.log"
	fail "dbak restore of the PostgreSQL backup failed"
}
printf '2\n\n\n%s\n' "$private_key" | docker exec -i -e SUDO_USER=sukuna "$vm" dbak restore >"$work/restore-my.log" 2>&1 || {
	cat "$work/restore-my.log"
	fail "dbak restore of the MySQL backup failed"
}
grep -qF "$private_key" "$work/restore-pg.log" "$work/restore-my.log" && fail "the restore output shows the private key"
pg_local="$(docker exec "$vm" sh -c 'ls /home/sukuna/backups/*.dump')"
my_local="$(docker exec "$vm" sh -c 'ls /home/sukuna/backups/*.sql')"
[[ "$(docker exec "$vm" stat -c '%U %a' "$pg_local")" == "sukuna 600" ]] || fail "the restored file has the wrong owner or mode"
docker exec "$vm" pg_restore --list "$pg_local" | grep -q "TABLE public customers" || fail "the restored PostgreSQL dump has no customers table"
docker exec "$vm" grep -q "CREATE TABLE \`customers\`" "$my_local" || fail "the restored MySQL dump has no customers table"
docker exec "$vm" sh -c 'ls /home/sukuna/backups/*.partial' >/dev/null 2>&1 && fail "a partial file stays"

echo "== The VM is lost =="
docker rm -f "$vm" >/dev/null

remote="/home/dbak/upload/$host"
pg_file="$(docker exec dbaktest-sftp-1 sh -c "ls $remote/pg16/app_prod/*.dump.age" | tail -n 1)"
my_file="$(docker exec dbaktest-sftp-1 sh -c "ls $remote/mysql8/app_prod/*.sql.zst.age" | tail -n 1)"
[[ -n "$pg_file" && -n "$my_file" ]] || fail "no encrypted backups on the SFTP server"
docker cp "dbaktest-sftp-1:$pg_file" "$work/pg.dump.age"
docker cp "dbaktest-sftp-1:$my_file" "$work/my.sql.zst.age"
head -c 22 "$work/pg.dump.age" | grep -q "age-encryption.org/v1" || fail "the PostgreSQL backup is not an age file"
grep -aq "alpha" "$work/my.sql.zst.age" && fail "the MySQL backup contains plain data"

echo "== Restore on a different computer without dbak =="
docker exec dbaktest-postgres16-1 dropdb -U postgres --if-exists restore_test
docker exec dbaktest-postgres16-1 createdb -U postgres restore_test
docker run -d --name "$rescue" --network dbaktest_default -v "$work:/work" "$image" sleep infinity >/dev/null
docker exec "$rescue" test ! -e /usr/local/bin/dbak || fail "the rescue computer has dbak"
docker exec -e PGPASSWORD=rootpass "$rescue" sh -c '
	set -e
	age -d -i /work/key.txt /work/pg.dump.age \
		| pg_restore --host postgres16 --username postgres --dbname restore_test --no-owner
'
rows="$(docker exec dbaktest-postgres16-1 psql -U postgres -d restore_test -tAc 'SELECT count(*) FROM customers')"
[[ "$rows" == 1000 ]] || fail "the restored PostgreSQL table has $rows rows, want 1000"

docker exec "$rescue" sh -c 'age -d -i /work/key.txt /work/my.sql.zst.age | zstd -d > /work/my.sql'
grep -q "CREATE TABLE \`customers\`" "$work/my.sql" || fail "the MySQL dump has no customers table"
grep -q "'gamma'" "$work/my.sql" || fail "the MySQL dump has no rows"

echo "The end-to-end test of the encrypted restore passed."
