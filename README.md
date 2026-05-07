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
| `-version` | `false`   | Print build version metadata and exit      |

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

## Docker

### Build Local Image

```bash
docker build -t go-unifi-network-list-sync:dev .
```

### Run Container

```bash
docker run --rm -p 8080:8080 \
   -v unifi-sync-data:/data \
   ghcr.io/mstrhakr/go-unifi-network-list-sync:main
```

The container defaults to:

- `-addr :8080`
- `-db /data/sync.db`
- `-log-file /data/sync.log`

Use a bind mount or named volume for `/data` so DB and logs persist across upgrades.

## CI/CD And Release Structure

### Workflows

- `CI` workflow (`.github/workflows/ci.yml`): runs on every pull request and push to `main`, and executes `go vet ./...`, `go test ./...`, and a regular build.
- `Release` workflow (`.github/workflows/release.yml`): runs when a tag like `v1.2.3` (or `v1.2.3-rc.1`) is pushed, and uses GoReleaser to build and publish GitHub Release artifacts.
- `Container` workflow (`.github/workflows/container.yml`): runs on pushes to `main` and on published GitHub Releases, publishes multi-arch images (`linux/amd64`, `linux/arm64`) to GHCR, and tags images as `main`, `vMAJOR`, `vMAJOR.MINOR`, `vMAJOR.MINOR.PATCH`, and `sha-<commit>`.

### Versioning Policy (SemVer)

- Use `MAJOR.MINOR.PATCH` tags prefixed with `v`
- `v1.4.0` for new backward-compatible features
- `v1.4.1` for fixes
- `v2.0.0` for breaking changes
- Optional prereleases are supported (for example `v1.5.0-rc.1`)

### Release Artifacts

GoReleaser (`.goreleaser.yaml`) produces:

- `linux`, `darwin`, and `windows` binaries
- `amd64` and `arm64` builds
- `.tar.gz` archives (and `.zip` for Windows)
- `checksums.txt` for artifact verification

Build metadata is embedded into binaries via ldflags and exposed with:

```bash
./go-unifi-network-list-sync -version
```

### Suggested Release Flow

1. Merge tested changes into `main`
1. Create and push a SemVer tag

```bash
git tag v1.0.0
git push origin v1.0.0
```

1. GitHub Actions runs `Release` and publishes assets on a GitHub Release
1. Publishing the GitHub Release triggers `Container`, which publishes GHCR image tags such as `v1`, `v1.2`, and `v1.2.3`

## UniFi API Schema Reference

- Local copy: `docs/reference/unifi-network-10.1.85.json`
- Source: `https://github.com/beezly/unifi-apis/raw/refs/heads/main/unifi-network/10.1.85.json`

This file is treated as the authoritative schema reference for endpoint paths and response payload shapes.

## License

MIT
