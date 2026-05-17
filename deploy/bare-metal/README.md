# Bare-metal self-host

Artifacts for running GoNext directly on a VPS or dedicated server, with no
container runtime. Designed for users coming from cPanel / Plesk / WP-on-LAMP
who already manage their own Linux box.

This path trades convenience (no `docker compose up`) for two things people
who self-host care about: fewer moving parts and a smaller resource floor.
A 2-vCPU / 4 GB VPS is enough for a small site.

The full design rationale lives in
[`docs/09-deployment-ops.md`](../../docs/09-deployment-ops.md) §6. This README
covers the install path only.

---

## What you get

| File | Purpose |
| --- | --- |
| `install.sh` | Idempotent installer — creates the service user, lays down binaries, installs systemd units and the env file template. |
| `systemd/gonext-api.service` | The Go HTTP/GraphQL API. |
| `systemd/gonext-worker.service` | The async job worker. |
| `systemd/gonext-cron.service` | The scheduled-job runner (same binary as worker, `--mode=cron`). |
| `caddy/Caddyfile.example` | Reverse-proxy and TLS termination via Caddy. |
| `env/.env.template` | Pre-filled environment file for `/etc/gonext/.env`. |

All units pin `User=gonext`, `Group=gonext`, `EnvironmentFile=/etc/gonext/.env`,
`ReadWritePaths=/var/lib/gonext`, and a full systemd hardening preset
(`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`,
empty `CapabilityBoundingSet`, restricted address families, system-call
filter). See [`systemd/gonext-api.service`](systemd/gonext-api.service) for
the canonical commentary.

---

## Prerequisites

Tested on Ubuntu 22.04 LTS and Debian 12. Anything with systemd >= 247 and
glibc >= 2.31 should work.

```bash
sudo apt update
sudo apt install -y postgresql redis-server caddy
```

You also need the built GoNext binaries. From a checkout:

```bash
make build
ls dist/
# gonext-api  gonext-worker
```

Or download a release tarball and extract it to `./dist`.

---

## Install (single command)

From the repository root:

```bash
sudo ./deploy/bare-metal/install.sh
```

The script is idempotent — re-running it reapplies binaries, units, and the
example Caddyfile, but **never overwrites `/etc/gonext/.env`** so your secrets
survive upgrades.

Set `DRY_RUN=1` to print the commands without executing them:

```bash
sudo DRY_RUN=1 ./deploy/bare-metal/install.sh
```

What the script does, in order:

1. Creates the `gonext` system user (no shell, no home).
2. Creates `/etc/gonext/` (0750, root:gonext) and `/var/lib/gonext/`
   (0750, gonext:gonext, with `media/` and `plugins/` subdirs).
3. Installs `gonext-api` and `gonext-worker` to `/usr/local/bin/`.
4. Installs the three systemd units to `/etc/systemd/system/` and runs
   `systemctl daemon-reload`.
5. Copies `env/.env.template` to `/etc/gonext/.env` **only if absent**,
   with mode `0640` and group ownership `gonext`.
6. Copies the Caddyfile to `/etc/caddy/Caddyfile.gonext.example` for review.

The installer never starts services — you'll do that manually after editing
the env file and the Caddyfile.

---

## Manual install

If you'd rather not run the script, here's the same thing in shell.

### 1. Create the service user

```bash
sudo useradd --system --user-group --no-create-home \
    --home-dir /var/lib/gonext --shell /usr/sbin/nologin gonext
```

### 2. Create directories

```bash
sudo install -d -m 0750 -o root -g gonext /etc/gonext
sudo install -d -m 0750 -o gonext -g gonext /var/lib/gonext
sudo install -d -m 0750 -o gonext -g gonext /var/lib/gonext/media
sudo install -d -m 0750 -o gonext -g gonext /var/lib/gonext/plugins
```

### 3. Install binaries

```bash
sudo install -m 0755 ./dist/gonext-api    /usr/local/bin/gonext-api
sudo install -m 0755 ./dist/gonext-worker /usr/local/bin/gonext-worker
```

### 4. Install systemd units

```bash
sudo install -m 0644 deploy/bare-metal/systemd/gonext-api.service    /etc/systemd/system/
sudo install -m 0644 deploy/bare-metal/systemd/gonext-worker.service /etc/systemd/system/
sudo install -m 0644 deploy/bare-metal/systemd/gonext-cron.service   /etc/systemd/system/
sudo systemctl daemon-reload
```

### 5. Install the env file

```bash
sudo install -m 0640 -o root -g gonext \
    deploy/bare-metal/env/.env.template /etc/gonext/.env
sudoedit /etc/gonext/.env
```

Generate the three required secrets with:

```bash
openssl rand -base64 32
```

Set `DATABASE_URL` to whatever you created in step 6 below.

### 6. Provision Postgres + Redis

```bash
sudo -u postgres createuser gonext --pwprompt
sudo -u postgres createdb gonext --owner gonext
```

Redis works out of the box on `127.0.0.1:6379` with no auth, which matches
the default `REDIS_URL` in the env template.

### 7. Run migrations

```bash
sudo -u gonext /usr/local/bin/gonext-api migrate
```

### 8. Configure Caddy

```bash
sudo cp deploy/bare-metal/caddy/Caddyfile.example /etc/caddy/Caddyfile
sudoedit /etc/caddy/Caddyfile  # replace example.com hostnames + email
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

Caddy obtains a Let's Encrypt certificate automatically on the first request
to each new hostname, provided the A/AAAA records already point at this box
and ports 80/443 are open.

### 9. Open the firewall (optional)

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
```

### 10. Start the services

```bash
sudo systemctl enable --now gonext-api.service
sudo systemctl enable --now gonext-worker.service
sudo systemctl enable --now gonext-cron.service
```

---

## Verifying the install

```bash
systemctl status gonext-api gonext-worker gonext-cron
journalctl -u gonext-api -f
curl -fsS https://example.com/api/healthz
```

The first `curl` against your real hostname will trigger TLS issuance — watch
`journalctl -u caddy -f` if it takes more than a few seconds.

---

## Upgrading

```bash
# Build / download new binaries to ./dist
sudo ./deploy/bare-metal/install.sh
sudo systemctl restart gonext-api gonext-worker gonext-cron
```

The unit files and env file are preserved. If a release adds new env vars,
the changelog will call them out; copy them in from the latest
`env/.env.template`.

---

## Uninstall

```bash
sudo systemctl disable --now gonext-api gonext-worker gonext-cron
sudo rm /etc/systemd/system/gonext-{api,worker,cron}.service
sudo systemctl daemon-reload
sudo rm /usr/local/bin/gonext-api /usr/local/bin/gonext-worker

# State and config — back up first if you might want it back.
sudo rm -rf /var/lib/gonext /etc/gonext

sudo userdel gonext
```

---

## See also

- [`docs/09-deployment-ops.md`](../../docs/09-deployment-ops.md) — full
  deployment design, including the Kubernetes path and Docker Compose.
- [`.env.example`](../../.env.example) — canonical environment variable
  reference.
- [`docker-compose.yml`](../../docker-compose.yml) — the service set we
  reproduce here without containers.
