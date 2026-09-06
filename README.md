# Daddy, Cull! next

A Go + React foundation for a growing family media library, hosted on Unraid Tower.
Read [the architecture](docs/ARCHITECTURE.md) for native media workers, Takeout intake, growth limits and the filesystem safety contract.

## Real-library preview

The application now supports importing the real archive catalogue and showing real previews through fixed read-only routes to the existing media service.
See [the real-library preview contract](docs/REAL-LIBRARY-PREVIEW.md) for deployment boundaries and unfinished work.
The Tower compose configuration now opens `/state/library.db`; the synthetic seed instructions below remain for isolated tests only.
Do not seed the real database or follow the old synthetic deployment sequence for this configuration.

## Synthetic foundation

This is a synthetic design and scale pilot, not the replacement production culling app.
It browses a source-aware SQLite catalogue and saves reversible review intent.
Original media, real previews, import and quarantine execution are not connected.
The application has no purge or file deletion endpoint.

- Go API backed by the native SQLite C library, with four bounded readers and one writer.
- Indexed, bounded cursor pagination, with source and media filters.
- Keep, Later, Favourite and Mark for culling, with explicit save acknowledgements.
- Idempotency keys, stale-revision protection and an append-only decision ledger.
- Session undo and bounded batches of 40 assets in React.
- Multi-stage Docker image running as a non-root user, with resource limits and no media mounts.

Undo controls and session progress currently last for the browser session.
The underlying decisions and event history survive application/container restarts.
Durable queue/session resumption and a history UI remain migration gates.

## Local development

Requires Go 1.27.1 with a C compiler, and Node 22.12+ or Node 24.

```sh
cd next
mkdir -p state
go test -race ./...
go run ./cmd/cull -db state/scale.db -seed 100000
cd web
npm ci
npm run build
cd ..
go run ./cmd/cull -db state/scale.db
```

Open [the local preview](http://127.0.0.1:8830).
Seeding refuses non-empty databases.
Use a new filename to create another synthetic fixture.
The server binds to loopback by default.
`-demo-network` exists only for the isolated synthetic Docker preview; it is not production authentication.

## Tower deployment

Source checkout: `/mnt/user/appdata/tower-cull-next-repo`.
Database: `/mnt/cache/appdata/tower-cull-next/scale.db` on Tower's local cache filesystem.
Container: `tower-cull-next`.
Preview ports: LAN and Tailscale on 8830.
The existing `tower-cull` on 8823 stays independent.

```sh
cd /mnt/user/appdata/tower-cull-next-repo
docker compose build
install -d -o 10001 -g 10001 /mnt/cache/appdata/tower-cull-next
docker compose run --rm --no-deps preview -db /state/scale.db -seed 100000
docker compose up -d preview
docker compose ps
```

The seed command is a one-time step for an empty state directory.
Do not rerun it over an existing catalogue.
Use `docker compose stop preview` to stop the preview without removing its state.
No archive, Takeout, Docker socket or legacy database is mounted into this container.
The database is not yet enrolled in the production backup schedule; it holds synthetic decisions only.

## Scale test

```sh
go run ./cmd/scale -assets 1000000 -requests 2000 -workers 8
```

The test creates and removes its own temporary synthetic database.
It measures in-process HTTP routing, indexed queries and JSON encoding at distributed positions in the catalogue.
It excludes real media, network latency, browser rendering and cold-disk performance.
Go heap output excludes SQLite/native allocations.
Record container RSS separately when testing on Tower.

## Production migration

Follow the ordered gates in the architecture document.
In particular, real media ingestion, decoder jobs, Takeout reconciliation and a crash-tested quarantine executor must land before cutover.
The target keeps Takeout read-only, imports by verified copy and never couples source cleanup to culling.
The target archive writer may only quarantine explicitly selected files and restore them safely.
