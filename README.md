# UniFi Network List Sync

A Go rewrite of [unifi-firewall-group-updater](https://github.com/ferventgeek/unifi-firewall-group-updater) with a web UI for management and scheduling.

## What It Does

Automatically resolves DNS hostnames to IPs and pushes them into UniFi controller firewall groups. Replaces the manual process of maintaining IP allow-lists (e.g., Grafana Cloud synthetic monitoring probes).

**Features:**

- **Web UI** for creating and managing sync jobs
- **Cron scheduling** to keep firewall groups automatically updated
- **Multi-resolver DNS lookups** across authoritative DNS plus Cloudflare, Google, Quad9, and OpenDNS
- **Literal IPv4 and IPv4 CIDR support** in the input list for mixed hostname and direct-entry workflows
- **DNS preview** to test hostname resolution before syncing
- **Run history** with detailed diffs of every sync
- **Single binary** with embedded web interface — no external dependencies
- **SQLite** storage for jobs and logs

## Quick Start

### Build

```bash
go build -o go-unifi-network-list-sync .
```

### Run

```bash
./go-unifi-network-list-sync
```

Open [http://localhost:8080](http://localhost:8080) in your browser.

### Options

| Flag       | Default   | Description                                |
|------------|-----------|--------------------------------------------|
| `-addr`    | `:8080`   | HTTP listen address                        |
| `-db`      | `sync.db` | SQLite database path                       |
| `-debug`   | `false`   | Enable debug logging                       |
| `-verbose` | `false`   | Enable verbose logging                     |
| `-log-file`| `sync.log`| Log file path (`""` disables file logging) |

```bash
./go-unifi-network-list-sync -addr :9090 -db /var/lib/sync/data.db -log-file ./sync.log
```

## Usage

1. Open the web UI
2. Click **+ New Sync Job**
3. Fill in your UniFi controller details:

   - **Controller URL**: e.g., `https://192.168.1.4:8443`
   - **API Key**: generated in UniFi OS (`Settings -> System -> Advanced -> API Keys`)
   - **Site**: usually `default`
   - **Firewall Group ID**: find this in your UniFi console under  
      `Settings → Firewall → Groups → Edit` — copy the hex ID from the URL
   - **Hostnames**: one DNS name, IPv4 address, or IPv4 CIDR per line (comments with `#`)
   - **Schedule**: cron expression (e.g., `0 */6 * * *` for every 6 hours), or leave blank for manual-only

4. Click **Save**, then **Run Now** to test

## API Endpoints

| Method | Path                    | Description                     |
|--------|-------------------------|---------------------------------|
| GET    | `/api/jobs`             | List all sync jobs              |
| POST   | `/api/jobs`             | Create a new sync job           |
| GET    | `/api/jobs/{id}`        | Get a specific job              |
| PUT    | `/api/jobs/{id}`        | Update a job                    |
| DELETE | `/api/jobs/{id}`        | Delete a job and its history    |
| POST   | `/api/jobs/{id}/run`    | Trigger a sync immediately      |
| GET    | `/api/jobs/{id}/logs`   | Get run history for a job       |
| POST   | `/api/resolve`          | Preview DNS resolution          |

## Cron Schedule Examples

| Expression      | Meaning          |
|-----------------|------------------|
| `*/30 * * * *`  | Every 30 minutes |
| `0 */6 * * *`   | Every 6 hours    |
| `0 0 * * *`     | Daily at midnight|
| `0 0 * * 1`     | Every Monday     |

## Development

```bash
# Run in development
go run . -addr :8080

# Build for Linux
GOOS=linux GOARCH=amd64 go build -o go-unifi-network-list-sync .
```

## UniFi API Schema Reference

- Local copy: `docs/reference/unifi-network-10.1.85.json`
- Source: `https://github.com/beezly/unifi-apis/raw/refs/heads/main/unifi-network/10.1.85.json`

This file is treated as the authoritative schema reference for endpoint paths and response payload shapes.

## License

MIT
