# Clash Subscription Manager

A self-hosted panel for managing Clash subscriptions: unify airport (provider) subscriptions and your own nodes, convert formats automatically, and generate your own personalized Clash config links from templates.

Great for running on a server/router to centralize multiple provider subscriptions and produce personal Clash configs with node filters, custom groups, and fixed rules for all your devices.

[![Docker Pulls](https://img.shields.io/docker/pulls/zhf883680/clash-subscription-manager)](https://hub.docker.com/r/zhf883680/clash-subscription-manager)

## Features

**Subscriptions**
- Two ways to add: a subscription URL, or pasted node links (`ss://` / `vmess://` / `trojan://` / `vless://` / `ssr://`) merged into one subscription
- Auto-detects formats and converts to Clash YAML, caching the file locally
- Per-subscription node Filter, custom request headers, and manual refresh
- Subscriptions refresh automatically every 24 hours (plus one refresh at startup)

**Templates**
- Edit Clash template YAML directly in the browser; create multiple templates
- Bind all or a subset of subscriptions per template; `proxy-providers` are filled in automatically when rendering
- Three output modes per template for different clients

**Security**
- Login required to use the panel
- Every public subscription/template link carries its own random key, and you can "Reset Link" to revoke the old one at any time

## Quick Start (Docker, recommended)

### 1. Run

```bash
docker run -d --name clash-manage \
  -p 8080:8080 \
  -e TOKEN="replace-with-a-strong-random-key" \
  -v $(pwd)/data:/app/data \
  zhf883680/clash-subscription-manager:latest
```

Notes:
- `-e TOKEN=...` sets the access key used to log in. Optional — see "Access Key" below.
- Mount a data directory with `-v $(pwd)/data:/app/data`. **Do not skip this**: subscriptions, templates, and cached files live here and survive container recreation.

### 2. Open the panel

Visit `http://your-address:8080`, enter the access key, and start managing.

## Access Key

| Situation | Key source |
| --- | --- |
| `TOKEN` env var is set | Uses `TOKEN` value (takes priority; config is not modified) |
| A non-default `token` exists in config | Uses the value from config |
| Neither is set (or still the default) | Generates a random 32-char key at startup, writes it to `config.yaml`, and prints it to the log |

Forgot the key?

```bash
docker logs clash-manage | grep "访问密钥"
# e.g. 未配置访问密钥，已随机生成并写入 config.yaml: 1a2b3c...
```

> Note: if you don't mount `config.yaml` or set `TOKEN`, the auto-generated key changes when the container is recreated. Use `-e TOKEN=...` for a stable key.

## Usage Guide

### 1. Add a subscription

On the **Subscriptions** page:

- **Subscription URL**: paste the provider URL. You can expand "Advanced" to set a node Filter or custom request headers (e.g. an auth header).
- **Node text**: paste one or more raw node links; they are merged into a single subscription.

The app downloads, converts, and caches the config right away.

### 2. Subscription list

For each subscription you can:

- **Copy download link**: a direct link to the full converted Clash YAML for that subscription — usable standalone in Clash.
- **Refresh**: re-fetch and update the cached file immediately (URL-based subscriptions).
- **Edit**: change name / URL / filter / headers. "Save changes only" keeps the cached file; "Save and refresh" re-downloads it.
- **Reset link**: regenerate the download link; **the old link stops working immediately** (see "Public links & resetting").
- **Delete**: removes the record together with its cached file.

### 3. Generate configs from templates

On the **Templates** page: template list on the left + editor on the right. Edit the YAML, save, then use the "Copy address ▾" menu:

| Output | Use |
| --- | --- |
| Copy template address | proxy-providers mode — use as the subscription URL in Clash / Mihomo etc. |
| Copy full-node address | expanded `proxies` config for setups without providers |
| Copy non-Clash mode | plain node links (e.g. `ss://`), for Shadowrocket / Loon etc. |

Tips:
- Bind all or selected subscriptions per template and set custom prefixes; bound subscriptions are rendered into `proxy-providers` automatically.
- If a subscription has a Filter set, the rendered provider includes it.

### 4. Use it in Clash

In Clash Verge / Mihomo, add a new subscription, paste the **template address** copied above, pick type Clash, save, and update. The server refreshes subscriptions daily, and Clash re-pulls on its own schedule.

### 5. Public links & resetting (important)

All public links carry their own random key:

```
http://your-address:8080/download/subscription-id?token=xxxx
http://your-address:8080/api/templates/template-id/render?token=xxxx
```

- A link is a password — **don't share it publicly**. If it leaks, click "Reset Link" on that subscription/template in the panel; the old link dies immediately and you copy a fresh one. No global key change needed.
- After resetting a subscription, template renders automatically use the new download key — nothing else to do.
- After upgrading from an older version, old links (without `?token=`) stop working; copy them again once.

## Security Notes (public deployment)

- Always use a strong random access key — never the default on the public internet (see "Access Key").
- Serve over HTTPS: reverse-proxy with Caddy / Nginx for automatic certificates, or set `https: true` and provide `cert.pem` / `key.pem`.
- When using Nginx/Caddy, don't add another basic-auth layer in front of the panel (it has built-in login) — let the proxy handle HTTPS only.
- If you don't need public health checks, block `/health` at the proxy.

## Configuration

`config.yaml` (some values can be overridden by environment variables):

| Key | Default | Description |
| --- | --- | --- |
| `port` | `8080` | Listen port |
| `data_dir` | `./data` | Data directory (subscriptions / templates / cached files) |
| `token` | auto-generated | Access key; overridden by the `TOKEN` env var |
| `rate_limit` | `60` | Max download requests per minute per IP |
| `download_timeout` | `30s` | Timeout when fetching upstream subscriptions |
| `max_file_size` | `52428800` | Max size of a downloaded payload (bytes) |
| `https` | `false` | When `true`, serves TLS using `cert.pem` / `key.pem` in the working directory |

> `backup_enabled`, `backup_interval`, and `file_retention_days` are reserved for future use and currently do nothing.

## Data & Backups

Everything lives under `data_dir` (default `./data`):

```
data/
├── subscriptions.json   # subscription records
├── templates.json       # template records
└── *.yaml               # cached configs per subscription
```

Back up the whole data directory. For Docker, mount it out (see Quick Start).

## Run from Source (developers / self-build)

Requires Go 1.25+:

```bash
go run .
```

Reads `config.yaml` from the current directory; default port is `8080`.

## FAQ

**I forgot the key.**
- Using `TOKEN`: restart with a new value.
- Configured in config: edit `token` in `config.yaml` and restart.
- Auto-generated: `docker logs <container> | grep "访问密钥"` (the startup log line is in Chinese); after recreating the container a new key is generated and printed in the logs.

**A previously configured Clash subscription suddenly returns 404?**
Most likely the link predates the upgrade and is no longer valid. Re-copy the template/download link from the panel once.

**How do I revoke a link for good?**
Click "Reset Link" on the subscription or template; the old link is invalid immediately.

**Panel slow / being scanned?**
Use a strong key and HTTPS on public deployments; add rate limiting at the reverse proxy and only expose the paths you need.

## Links

- GitHub: https://github.com/zhf883680/clash-subscription-manager
- Docker Hub: https://hub.docker.com/r/zhf883680/clash-subscription-manager

## Credits

Conversion logic is inspired by [tindy2013/subconverter](https://github.com/tindy2013/subconverter).
