#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d)"
vm="dbak-e2e-install"
server="dbak-e2e-release"
image="dbak-e2e-install-image"

cleanup() {
	docker rm -f "$vm" "$server" >/dev/null 2>&1 || true
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

mkdir -p "$work/srv/download/v9.9.9" "$work/srv/download/v9.9.8" "$work/srv/latest/download"
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
	-ldflags "-s -w -X github.com/developerabdan/dbak/internal/app.Version=9.9.9 -X github.com/developerabdan/dbak/internal/app.Commit=e2e" \
	-o "$work/srv/download/v9.9.9/dbak_linux_$arch" ./cmd/dbak)
(cd "$work/srv/download/v9.9.9" && shasum -a 256 "dbak_linux_$arch" >checksums.txt)
cp "$work/srv/download/v9.9.9/"* "$work/srv/latest/download/"
cp "$work/srv/download/v9.9.9/dbak_linux_$arch" "$work/srv/download/v9.9.8/"
printf '%s  %s\n' "0000000000000000000000000000000000000000000000000000000000000000" "dbak_linux_$arch" >"$work/srv/download/v9.9.8/checksums.txt"

docker run -d --name "$server" --network dbaktest_default -w /srv python:3.12-alpine python -m http.server 8000 >/dev/null
docker cp "$work/srv/." "$server:/srv"

docker build -q -t "$image" - >/dev/null <<'EOF'
FROM ubuntu:24.04
RUN apt-get update \
	&& DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
		systemd systemd-sysv dbus postgresql-client openssh-client curl ca-certificates tzdata \
	&& rm -rf /var/lib/apt/lists/*
CMD ["/sbin/init"]
EOF

docker run -d --name "$vm" --hostname vm-e2e-install --network dbaktest_default \
	--privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock "$image" >/dev/null
for _ in $(seq 1 30); do
	state="$(docker exec "$vm" systemctl is-system-running 2>/dev/null || true)"
	[[ "$state" == running || "$state" == degraded ]] && break
	sleep 1
done

installer() {
	docker exec -i "$@" bash <"$root/install.sh"
}

echo "== installer as a normal user =="
out="$(installer -u 65534 -e DBAK_BASE_URL=http://dbak-e2e-release:8000 "$vm" 2>&1)" && fail "the installer ran without root"
grep -q "The installer must run as root" <<<"$out" || fail "no root message: $out"

echo "== installer with a bad checksum =="
out="$(installer -e DBAK_BASE_URL=http://dbak-e2e-release:8000 -e DBAK_VERSION=v9.9.8 "$vm" 2>&1)" && fail "the installer accepted a bad checksum"
echo "$out"
grep -q "The SHA-256 checksum of dbak_linux_$arch is not correct" <<<"$out" || fail "no checksum message"
docker exec "$vm" test ! -e /usr/local/bin/dbak || fail "the installer installed a file with a bad checksum"

echo "== installer with an unknown version =="
out="$(installer -e DBAK_BASE_URL=http://dbak-e2e-release:8000 -e DBAK_VERSION=v0.0.1 "$vm" 2>&1)" && fail "the installer accepted an unknown version"
grep -q "Make sure that the version v0.0.1 exists" <<<"$out" || fail "no version message: $out"

echo "== installer with a pinned version and DBAK_NO_INIT =="
out="$(installer -e DBAK_BASE_URL=http://dbak-e2e-release:8000 -e DBAK_VERSION=v9.9.9 -e DBAK_NO_INIT=1 "$vm")"
echo "$out"
grep -q "dbak 9.9.9 (commit e2e" <<<"$out" || fail "the installed version"
grep -q "To configure dbak, run: sudo dbak init" <<<"$out" || fail "no init message"
docker exec "$vm" sh -c '
	set -e
	test "$(stat -c %U:%a /usr/local/bin/dbak)" = root:755
	test "$(stat -c %U:%a /etc/dbak)" = dbak:750
	test "$(stat -c %U:%a /etc/dbak/secrets)" = dbak:700
	test "$(stat -c %U:%a /var/lib/dbak)" = dbak:750
	test "$(getent passwd dbak | cut -d: -f7)" = /usr/sbin/nologin
' || fail "owners and modes after the install"

echo "== installer with the latest version and no terminal =="
out="$(installer -e DBAK_BASE_URL=http://dbak-e2e-release:8000 "$vm")"
echo "$out"
grep -q "The installer cannot open a terminal for dbak init" <<<"$out" || fail "no terminal message"

echo "== dbak init --config =="
docker cp "$root/testdata/integration/ssh/id_ed25519" "$vm:/root/sftp_key"
docker exec "$vm" sh -c 'ssh-keyscan -t ed25519 sftp 2>/dev/null > /root/known_hosts'
docker exec -i "$vm" sh -c 'cat > /root/dbak.yaml' <<'EOF'
version: 1
source:
  engine: postgres
  host: postgres16
  username: backup_ro
  ssl_mode: disable
  databases:
    - app_prod
destination:
  type: sftp
  host: sftp
  username: dbak
  private_key_file: /root/sftp_key
  known_hosts_file: /root/known_hosts
  remote_path: /upload
schedule:
  frequency: daily
  time: "02:00"
  timezone: Asia/Jakarta
  randomized_delay: "0"
retention:
  daily: 7
  weekly: 4
  monthly: 6
notify:
  when: failure
EOF
password="$(cat "$root/testdata/integration/db_password")"
out="$(docker exec -e DBAK_DB_PASSWORD="$password" "$vm" dbak init --config /root/dbak.yaml)"
echo "$out"
grep -q "OK: The dump check of app_prod passed." <<<"$out" || fail "no dump check"
grep -q "OK: dbak installed the systemd timer dbak.timer" <<<"$out" || fail "no schedule"
grep -qF "$password" <<<"$out" && fail "the output contains the password"
docker exec "$vm" sh -c '
	set -e
	test "$(stat -c %U:%a /etc/dbak/config.yaml)" = dbak:600
	test "$(stat -c %U:%a /etc/dbak/secrets/db_password)" = dbak:600
	test "$(stat -c %U:%a /etc/dbak/secrets/id_ed25519)" = dbak:600
	grep -q "private_key_file: /etc/dbak/secrets/id_ed25519" /etc/dbak/config.yaml
	systemctl is-active dbak.timer
	systemctl start dbak.service
	journalctl -u dbak.service --no-pager | grep -q "status=success"
' || fail "the installed config does not work"
docker exec "$vm" dbak doctor | grep -q "0 failed" || fail "dbak doctor found a failure"

out="$(docker exec "$vm" dbak init --config /root/missing.yaml 2>&1)" && fail "dbak init accepted a missing file"
grep -q "does not exist" <<<"$out" || fail "no missing file message"
code=0
docker exec "$vm" sh -c 'sed "s/app_prod/app_missing/" /root/dbak.yaml > /root/bad.yaml && dbak init --config /root/bad.yaml' >/dev/null 2>&1 || code=$?
[[ "$code" == 2 ]] || fail "got exit code $code for an unknown database, want 2"

echo "The end-to-end test of install.sh passed."
