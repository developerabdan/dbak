#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d)"
container="dbak-e2e-init"
image="dbak-e2e-init-image"

cleanup() {
	docker rm -f "$container" >/dev/null 2>&1 || true
	docker rmi -f "$image" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

case "$(docker info --format '{{.Architecture}}')" in
	aarch64 | arm64) arch=arm64 ;;
	*) arch=amd64 ;;
esac

sftp_container="$(docker compose -f "$root/docker-compose.test.yml" -p dbaktest ps -q sftp | head -n 1)"
if [[ -z "$sftp_container" ]]; then
	echo "The test containers are not running. Run: docker compose -f docker-compose.test.yml -p dbaktest up -d" >&2
	exit 1
fi

(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$work/dbak" ./cmd/dbak)

printf '%s\n' '' '' postgres16 '' '' 'Te:st\pa"ss#1' 2 '' sftp '' dbak /upload 2 /tmp/id_ed25519 y \
	'' '' Asia/Jakarta '' '' '' '' '' '' '' '' '' '' >"$work/answers.txt"

docker create -i --name "$container" --hostname vm-e2e-01 --network dbaktest_default \
	--entrypoint /usr/local/bin/dbak postgres:16 init >/dev/null
docker cp "$work/dbak" "$container:/usr/local/bin/dbak"
docker cp "$root/testdata/integration/ssh/id_ed25519" "$container:/tmp/id_ed25519"
docker start -ai "$container" <"$work/answers.txt" | tee "$work/init.log"

grep -q "OK: dbak saved the config in /etc/dbak/config.yaml." "$work/init.log"
grep -q "OK: app_prod: " "$work/init.log"

docker commit "$container" "$image" >/dev/null
docker run --rm --network dbaktest_default --entrypoint sh "$image" -c '
	set -e
	test "$(stat -c %U:%a /etc/dbak/secrets)" = dbak:700
	test "$(stat -c %U:%a /etc/dbak/secrets/db_password)" = dbak:600
	test "$(stat -c %U:%a /etc/dbak/config.yaml)" = dbak:600
	gosu dbak /usr/local/bin/dbak run
'
echo "The end-to-end test of dbak init passed."
