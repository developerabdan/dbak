#!/usr/bin/env bash
set -euo pipefail

repo="developerabdan/dbak"
base_url="${DBAK_BASE_URL:-https://github.com/${repo}/releases}"
version="${DBAK_VERSION:-latest}"
install_path="/usr/local/bin/dbak"
service_user="dbak"
work=""

info() {
	printf '%s\n' "$*"
}

fail() {
	printf 'Error: %s\n' "$*" >&2
	exit 1
}

require_root() {
	if [[ "$(id -u)" -ne 0 ]]; then
		fail "The installer must run as root. Run: curl -fsSL https://raw.githubusercontent.com/${repo}/main/install.sh | sudo bash"
	fi
}

detect_platform() {
	local os machine
	os="$(uname -s)"
	machine="$(uname -m)"
	if [[ "$os" != "Linux" ]]; then
		fail "dbak runs only on Linux. This system is ${os}."
	fi
	case "$machine" in
		x86_64 | amd64) arch="amd64" ;;
		aarch64 | arm64) arch="arm64" ;;
		*) fail "dbak does not support the architecture ${machine}. Use an amd64 or arm64 server." ;;
	esac
}

download() {
	local url="$1" target="$2"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$target" "$url"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$target" "$url"
	else
		fail "The installer needs curl or wget. Install one of them, then run the installer again."
	fi
}

release_url() {
	local file="$1"
	if [[ "$version" == "latest" ]]; then
		printf '%s/latest/download/%s' "$base_url" "$file"
	else
		printf '%s/download/%s/%s' "$base_url" "$version" "$file"
	fi
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		fail "The installer needs sha256sum or shasum to examine the download."
	fi
}

install_binary() {
	local asset expected actual
	asset="dbak_linux_${arch}"
	work="$(mktemp -d)"
	trap 'rm -rf "$work"' EXIT
	info "Download of dbak ${version} for linux/${arch} started."
	download "$(release_url "$asset")" "$work/$asset" || fail "The download of ${asset} failed. Make sure that the version ${version} exists."
	download "$(release_url checksums.txt)" "$work/checksums.txt" || fail "The download of checksums.txt failed. Make sure that the version ${version} exists."
	expected="$(awk -v name="$asset" '$2 == name || $2 == "*" name { print $1 }' "$work/checksums.txt")"
	if [[ -z "$expected" ]]; then
		rm -f "$work/$asset"
		fail "checksums.txt has no entry for ${asset}. Do not use this release."
	fi
	actual="$(sha256_of "$work/$asset")"
	if [[ "$expected" != "$actual" ]]; then
		rm -f "$work/$asset"
		fail "The SHA-256 checksum of ${asset} is not correct. The installer removed the file. Do not use this download."
	fi
	install -m 0755 -o root -g root "$work/$asset" "$install_path"
	info "dbak is installed in ${install_path}: $("$install_path" version)"
}

create_user() {
	if id "$service_user" >/dev/null 2>&1; then
		return
	fi
	local shell="/usr/sbin/nologin"
	[[ -x "$shell" ]] || shell="/sbin/nologin"
	useradd --system --no-create-home --shell "$shell" "$service_user"
	info "The installer made the system user ${service_user}."
}

create_directories() {
	install -d -m 0750 -o "$service_user" -g "$service_user" /etc/dbak
	install -d -m 0700 -o "$service_user" -g "$service_user" /etc/dbak/secrets
	install -d -m 0750 -o "$service_user" -g "$service_user" /var/lib/dbak
}

start_init() {
	if [[ "${DBAK_NO_INIT:-}" == "1" ]]; then
		info "To configure dbak, run: sudo dbak init"
		return
	fi
	if ! { : </dev/tty; } 2>/dev/null; then
		info "The installer cannot open a terminal for dbak init. To configure dbak, run: sudo dbak init"
		return
	fi
	"$install_path" init </dev/tty
}

main() {
	require_root
	detect_platform
	install_binary
	create_user
	create_directories
	start_init
}

main "$@"
