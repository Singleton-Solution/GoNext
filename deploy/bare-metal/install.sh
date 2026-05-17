#!/usr/bin/env bash
# GoNext bare-metal installer.
#
# Idempotent installer for a single-VPS deploy on Debian / Ubuntu. Re-running
# this script reapplies the desired state without breaking existing data:
#
#   - System user is created only if missing.
#   - Binaries are overwritten (so you can re-run after a build).
#   - Unit files are reinstalled and systemd is reloaded.
#   - The env template is copied only if /etc/gonext/.env does not yet exist,
#     so secrets are preserved between runs.
#
# Usage:
#   sudo ./install.sh                      # install or update from ./dist
#   sudo BIN_DIR=/tmp/gonext-bin ./install.sh
#   sudo DRY_RUN=1 ./install.sh            # print actions, change nothing
#
# Expects these binaries at $BIN_DIR (default ./dist):
#   gonext-api
#   gonext-worker
#
# See docs/09-deployment-ops.md §6 for the full design.

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration (override via environment variables before invocation)
# ---------------------------------------------------------------------------
SERVICE_USER="${SERVICE_USER:-gonext}"
SERVICE_GROUP="${SERVICE_GROUP:-gonext}"
INSTALL_BIN_DIR="${INSTALL_BIN_DIR:-/usr/local/bin}"
CONFIG_DIR="${CONFIG_DIR:-/etc/gonext}"
STATE_DIR="${STATE_DIR:-/var/lib/gonext}"
SYSTEMD_DIR="${SYSTEMD_DIR:-/etc/systemd/system}"
BIN_DIR="${BIN_DIR:-./dist}"
DRY_RUN="${DRY_RUN:-0}"

# Resolve the directory this script lives in, so it works whether invoked
# from the repo root or from /usr/share/gonext after `make install`.
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
log() { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
warn() { printf "\033[1;33m==>\033[0m %s\n" "$*" >&2; }
die() { printf "\033[1;31m==>\033[0m %s\n" "$*" >&2; exit 1; }

run() {
	# Echo and execute, unless DRY_RUN=1.
	printf "    %s\n" "$*"
	if [[ "${DRY_RUN}" == "0" ]]; then
		eval "$@"
	fi
}

require_root() {
	if [[ "${EUID}" -ne 0 && "${DRY_RUN}" == "0" ]]; then
		die "Run as root (sudo ./install.sh)."
	fi
}

require_cmd() {
	command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"
}

# ---------------------------------------------------------------------------
# Steps
# ---------------------------------------------------------------------------
create_user() {
	log "Ensuring system user '${SERVICE_USER}' exists..."
	if id -u "${SERVICE_USER}" >/dev/null 2>&1; then
		printf "    user already exists — skipping\n"
		return
	fi
	# --system  => UID in the system range (<1000), no aging.
	# --no-create-home + --home-dir => state lives under /var/lib/gonext.
	# --shell /usr/sbin/nologin => the user cannot log in interactively.
	run "useradd --system --user-group --no-create-home \
		--home-dir '${STATE_DIR}' \
		--shell /usr/sbin/nologin \
		'${SERVICE_USER}'"
}

create_directories() {
	log "Creating /etc/gonext and /var/lib/gonext..."
	run "install -d -m 0750 -o root -g '${SERVICE_GROUP}' '${CONFIG_DIR}'"
	run "install -d -m 0750 -o '${SERVICE_USER}' -g '${SERVICE_GROUP}' '${STATE_DIR}'"
	run "install -d -m 0750 -o '${SERVICE_USER}' -g '${SERVICE_GROUP}' '${STATE_DIR}/media'"
	run "install -d -m 0750 -o '${SERVICE_USER}' -g '${SERVICE_GROUP}' '${STATE_DIR}/plugins'"
}

install_binaries() {
	log "Installing binaries to ${INSTALL_BIN_DIR}..."
	for bin in gonext-api gonext-worker; do
		local src="${BIN_DIR}/${bin}"
		if [[ ! -f "${src}" ]]; then
			warn "Binary not found: ${src} — skipping. Build with 'make build' first."
			continue
		fi
		# install(1) atomically replaces the binary; safe to run while the
		# service is up (the existing file is unlinked, not truncated).
		run "install -m 0755 -o root -g root '${src}' '${INSTALL_BIN_DIR}/${bin}'"
	done
}

install_units() {
	log "Installing systemd units to ${SYSTEMD_DIR}..."
	for unit in gonext-api.service gonext-worker.service gonext-cron.service; do
		run "install -m 0644 -o root -g root \
			'${SCRIPT_DIR}/systemd/${unit}' \
			'${SYSTEMD_DIR}/${unit}'"
	done
	run "systemctl daemon-reload"
}

install_env_template() {
	log "Installing env template..."
	local target="${CONFIG_DIR}/.env"
	if [[ -f "${target}" ]]; then
		printf "    %s already exists — preserving existing secrets\n" "${target}"
		return
	fi
	run "install -m 0640 -o root -g '${SERVICE_GROUP}' \
		'${SCRIPT_DIR}/env/.env.template' \
		'${target}'"
	warn "Edit ${target} and fill in DATABASE_URL + the three GONEXT_AUTH_* secrets."
	warn "Generate secrets with: openssl rand -base64 32"
}

install_caddyfile() {
	log "Installing example Caddyfile..."
	local target="/etc/caddy/Caddyfile.gonext.example"
	if [[ ! -d /etc/caddy ]]; then
		warn "/etc/caddy does not exist — install Caddy first (apt install caddy)."
		return
	fi
	run "install -m 0644 -o root -g root \
		'${SCRIPT_DIR}/caddy/Caddyfile.example' \
		'${target}'"
	warn "Review ${target}, then copy to /etc/caddy/Caddyfile and 'systemctl reload caddy'."
}

print_next_steps() {
	cat <<'EOF'

==> Install complete. Next steps:

    1. Edit /etc/gonext/.env and set DATABASE_URL + the three GONEXT_AUTH_* secrets.
       Generate each with:  openssl rand -base64 32

    2. Provision Postgres + Redis (if you haven't already):
         sudo apt install postgresql redis-server
         sudo -u postgres createuser gonext --pwprompt
         sudo -u postgres createdb gonext --owner gonext

    3. Run the database migrations:
         sudo -u gonext /usr/local/bin/gonext-api migrate

    4. Review /etc/caddy/Caddyfile.gonext.example, copy to /etc/caddy/Caddyfile,
       replace the example hostnames, then:
         sudo caddy validate --config /etc/caddy/Caddyfile
         sudo systemctl reload caddy

    5. Enable and start the services:
         sudo systemctl enable --now gonext-api.service
         sudo systemctl enable --now gonext-worker.service
         sudo systemctl enable --now gonext-cron.service

    6. Check status:
         systemctl status gonext-api gonext-worker gonext-cron
         journalctl -u gonext-api -f
EOF
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
	require_root
	require_cmd useradd
	require_cmd install
	require_cmd systemctl

	create_user
	create_directories
	install_binaries
	install_units
	install_env_template
	install_caddyfile

	print_next_steps
}

main "$@"
