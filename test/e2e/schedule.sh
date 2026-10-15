#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d)"
systemd_container="dbak-e2e-systemd"
cron_container="dbak-e2e-cron"
cronie_container="dbak-e2e-cronie"
image="dbak-e2e-schedule-image"
cronie_image="dbak-e2e-cronie-image"

cleanup() {
	docker rm -f "$systemd_container" "$cron_container" "$cronie_container" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT
[[ -n "${KEEP:-}" ]] && trap - EXIT

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
		systemd systemd-sysv dbus postgresql-client openssh-client cron logrotate ca-certificates tzdata \
	&& rm -rf /var/lib/apt/lists/*
CMD ["/sbin/init"]
EOF

docker build -q -t "$cronie_image" - >/dev/null <<'EOF'
FROM rockylinux:9-minimal
RUN microdnf install -y cronie logrotate shadow-utils tzdata && microdnf clean all
EOF

cat >"$work/config.yaml" <<'EOF'
version: 1
source:
  engine: postgres
  host: postgres16
  port: 5432
  username: backup_ro
  password_file: /etc/dbak/secrets/db_password
  ssl_mode: disable
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
  timezone: Asia/Jakarta
  randomized_delay: 5m
retention:
  daily: 7
  weekly: 4
  monthly: 6
notify:
  when: failure
EOF

prepare() {
	local container="$1"
	docker cp "$work/dbak" "$container:/usr/local/bin/dbak"
	docker exec "$container" mkdir -p /etc/dbak/secrets /var/lib/dbak
	docker cp "$work/config.yaml" "$container:/etc/dbak/config.yaml"
	docker cp "$root/testdata/integration/db_password" "$container:/etc/dbak/secrets/db_password"
	docker cp "$root/testdata/integration/ssh/id_ed25519" "$container:/etc/dbak/secrets/id_ed25519"
	docker exec "$container" sh -c '
		set -e
		id dbak >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin dbak
		chown -R dbak:dbak /etc/dbak /var/lib/dbak
		chmod 0750 /var/lib/dbak
		chmod 0700 /etc/dbak/secrets
		chmod 0600 /etc/dbak/config.yaml /etc/dbak/secrets/*
	'
}

pin_host_key() {
	local container="$1"
	docker exec "$container" sh -c 'ssh-keyscan -t ed25519 sftp 2>/dev/null > /etc/dbak/secrets/known_hosts && chown dbak:dbak /etc/dbak/secrets/known_hosts && chmod 0600 /etc/dbak/secrets/known_hosts'
}

echo "== systemd =="
docker run -d --name "$systemd_container" --hostname vm-e2e-systemd --network dbaktest_default \
	--privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock "$image" >/dev/null
for _ in $(seq 1 30); do
	state="$(docker exec "$systemd_container" systemctl is-system-running 2>/dev/null || true)"
	[[ "$state" == running || "$state" == degraded ]] && break
	sleep 1
done
prepare "$systemd_container"
pin_host_key "$systemd_container"

out="$(docker exec "$systemd_container" dbak schedule)"
echo "$out"
grep -q "dbak installed the systemd timer dbak.timer: daily at 02:00 Asia/Jakarta." <<<"$out" || fail "dbak schedule output"
docker exec "$systemd_container" grep -qx "OnCalendar=\*-\*-\* 02:00:00 Asia/Jakarta" /etc/systemd/system/dbak.timer || fail "OnCalendar"
docker exec "$systemd_container" grep -qx "RandomizedDelaySec=5m" /etc/systemd/system/dbak.timer || fail "RandomizedDelaySec"
[[ "$(docker exec "$systemd_container" systemctl is-enabled dbak.timer)" == enabled ]] || fail "the timer is not enabled"
[[ "$(docker exec "$systemd_container" systemctl is-active dbak.timer)" == active ]] || fail "the timer is not active"
docker exec "$systemd_container" systemd-analyze verify /etc/systemd/system/dbak.service /etc/systemd/system/dbak.timer
docker exec "$systemd_container" systemctl list-timers dbak.timer --no-pager

show="$(docker exec "$systemd_container" dbak schedule --show)"
echo "$show"
grep -q "systemd timer dbak.timer (enabled, active)" <<<"$show" || fail "schedule --show state"
grep -Eq "Next backup:  .* (02:[0-9]{2}:[0-9]{2} WIB|19:[0-9]{2}:[0-9]{2} UTC)" <<<"$show" || fail "schedule --show next run"

docker exec "$systemd_container" systemctl start dbak.service || {
	docker exec "$systemd_container" journalctl -u dbak.service --no-pager
	fail "dbak.service failed"
}
docker exec "$systemd_container" journalctl -u dbak.service --no-pager | grep -q "status=success" || {
	docker exec "$systemd_container" journalctl -u dbak.service --no-pager
	fail "the journal does not show a good run"
}
docker exec dbaktest-sftp-1 sh -c 'ls /home/dbak/upload/vm-e2e-systemd/app_prod/' | grep -q '\.dump$' || fail "no backup on the SFTP server"

docker exec "$systemd_container" sh -c 'sed -i "s/frequency: daily/frequency: every_n_hours/; s/time: \"02:00\"/every_hours: 6/" /etc/dbak/config.yaml'
docker exec "$systemd_container" dbak schedule
docker exec "$systemd_container" grep -qx "OnCalendar=\*-\*-\* 00/6:00:00 Asia/Jakarta" /etc/systemd/system/dbak.timer || fail "every_n_hours OnCalendar"

echo "== status, list, doctor, uninstall =="
out="$(docker exec "$systemd_container" dbak status)"
echo "$out"
grep -Eq "^app_prod +success " <<<"$out" || fail "status table"
grep -q "Next backup:  .* (systemd)" <<<"$out" || fail "status next backup"
docker exec "$systemd_container" dbak status --json | grep -q '"scheduler": "systemd"' || fail "status JSON"
out="$(docker exec "$systemd_container" dbak list)"
echo "$out"
grep -Eq "^app_prod +.*/upload/vm-e2e-systemd/app_prod/app_prod_[0-9]{8}T[0-9]{6}Z\.dump$" <<<"$out" || fail "list output"
out="$(docker exec "$systemd_container" dbak doctor)" || {
	echo "$out"
	fail "dbak doctor found a failure"
}
echo "$out"
grep -q "^OK    SFTP: " <<<"$out" || fail "doctor SFTP"
grep -q "^OK    Schedule: " <<<"$out" || fail "doctor schedule"
docker exec "$systemd_container" chmod 0644 /etc/dbak/secrets/db_password
out="$(docker exec "$systemd_container" dbak doctor)" && fail "dbak doctor did not find the mode problem"
grep -q "Run: sudo chmod 0600 /etc/dbak/secrets/db_password" <<<"$out" || fail "doctor fix text"
docker exec "$systemd_container" chmod 0600 /etc/dbak/secrets/db_password
printf 'n\n' | docker exec -i "$systemd_container" dbak uninstall | grep -q "Nothing changed" || fail "uninstall did not stop"
docker exec "$systemd_container" test -f /etc/systemd/system/dbak.timer || fail "uninstall changed files after No"
printf 'y\ny\n' | docker exec -i "$systemd_container" dbak uninstall
for gone in /etc/systemd/system/dbak.timer /etc/systemd/system/dbak.service /usr/local/bin/dbak /etc/dbak /var/lib/dbak/state.json; do
	docker exec "$systemd_container" test ! -e "$gone" || fail "$gone still exists after uninstall"
done
docker exec "$systemd_container" systemctl list-timers --all --no-pager | grep -q dbak && fail "the timer is still loaded"
docker exec dbaktest-sftp-1 sh -c 'ls /home/dbak/upload/vm-e2e-systemd/app_prod/' | grep -q '\.dump$' || fail "uninstall removed the remote backups"

echo "== cron without CRON_TZ =="
docker run -d --name "$cron_container" --hostname vm-e2e-cron --network dbaktest_default --entrypoint sleep "$image" infinity >/dev/null
prepare "$cron_container"
pin_host_key "$cron_container"
out="$(docker exec "$cron_container" dbak schedule)"
echo "$out"
grep -q "does not support CRON_TZ" <<<"$out" || fail "no CRON_TZ warning"
cron="$(docker exec "$cron_container" cat /etc/cron.d/dbak)"
echo "$cron"
grep -q '^0 19 \* \* \* dbak sleep \$(shuf -i 0-300 -n 1) && /usr/local/bin/dbak run >> /var/log/dbak/dbak.log 2>&1$' <<<"$cron" || fail "cron line"
[[ "$(docker exec "$cron_container" stat -c %U:%a /etc/cron.d/dbak)" == root:644 ]] || fail "cron file owner"
[[ "$(docker exec "$cron_container" stat -c %U:%a /var/log/dbak/dbak.log)" == dbak:640 ]] || fail "log file owner"
[[ "$(docker exec "$cron_container" stat -c %U:%a /var/log/dbak)" == dbak:750 ]] || fail "log directory owner"
docker exec "$cron_container" logrotate -d /etc/logrotate.d/dbak 2>&1 | grep -qi "error" && fail "logrotate rejected the config"
docker exec "$cron_container" sh -c 'echo test > /var/log/dbak/dbak.log && logrotate -f /etc/logrotate.d/dbak && ls /var/log/dbak' | grep -q 'dbak.log.1.gz' || fail "logrotate did not rotate the log"
command="$(grep '^0 19' <<<"$cron" | cut -d' ' -f7- | sed 's/^sleep [^&]*&& //')"
docker exec "$cron_container" su -s /bin/sh dbak -c "$command" || fail "the cron command failed"
docker exec "$cron_container" grep -q "status=success" /var/log/dbak/dbak.log || fail "the log file does not show a good run"
show="$(docker exec "$cron_container" dbak schedule --show)"
echo "$show"
grep -q "Cron time:    0 19 \* \* \*" <<<"$show" || fail "schedule --show cron time"
docker exec "$cron_container" dpkg -s cron >/dev/null
docker exec "$cron_container" sh -c 'cron && sleep 1 && pgrep -x cron' >/dev/null || fail "cron does not start"

echo "== cronie with CRON_TZ =="
docker run -d --name "$cronie_container" --hostname vm-e2e-cronie --network dbaktest_default --entrypoint sleep "$cronie_image" infinity >/dev/null
prepare "$cronie_container"
out="$(docker exec "$cronie_container" dbak schedule)"
echo "$out"
cron="$(docker exec "$cronie_container" cat /etc/cron.d/dbak)"
echo "$cron"
grep -qx "CRON_TZ=Asia/Jakarta" <<<"$cron" || fail "no CRON_TZ line"
grep -q '^0 2 \* \* \* dbak ' <<<"$cron" || fail "cronie cron line"
grep -q "CRON_TZ" <<<"$out" && fail "a CRON_TZ warning with cronie"

echo "The end-to-end test of dbak schedule passed."
