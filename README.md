# Daddy, Cull! next

A Go + React foundation for a growing family media library, hosted on Unraid Tower.
Read [the architecture](docs/ARCHITECTURE.md) for native media workers, Takeout intake, growth limits and the filesystem safety contract.

## Real-library preview

The application now supports importing the real archive catalogue and showing real previews through fixed read-only routes to the existing media service.
See [the real-library preview contract](docs/REAL-LIBRARY-PREVIEW.md) for deployment boundaries and unfinished work.
The Tower compose configuration now opens `/state/library.db`; the synthetic seed instructions below remain for isolated tests only.
Do not seed the real database or follow the old synthetic deployment sequence for this configuration.

## Current implementation

The deployed app browses a real archive snapshot and saves reversible review intent.
Related-file grouping, comparison, pending-save recovery and an explicit Bin workflow are connected.
The private writer supports selected quarantine, restore and separately confirmed purge.
This is still an incomplete replacement; see [delivered and remaining work](docs/RELEASE-1-STATUS.md).

- Go API backed by the native SQLite C library, with four bounded readers and one writer.
- Indexed, bounded cursor pagination, with source and media filters.
- Keep, Later, Favourite and Mark for culling, with explicit save acknowledgements.
- Idempotency keys, stale-revision protection and an append-only decision ledger.
- Session undo and bounded batches of 40 group representatives in React.
- Non-root, resource-limited web and writer containers, with archive write access isolated to the writer.

Undo controls and session progress currently last for the browser session.
The underlying decisions and event history survive application/container restarts.
Same-browser queue resumption and pending-save recovery are implemented.
A durable history UI and cross-device session synchronisation remain unfinished.

## Saved from social

A separate page lists archive videos that look like they were saved from Instagram or a similar app rather than recorded on a camera.

Detection reads container headers and six greyscale thumbnails per video.
It never decodes a whole file, never writes to the archive, and uses no model, so it costs nothing per video beyond disk reads.
The signals are the filename shapes only a download tool produces, the absence of `com.apple.quicktime.make`/`model`, non-capture resolutions, bits per pixel per frame, exact whole-second durations, and the Story letterbox: rows that are flat left to right and unchanged across all six frames, above and below a moving middle.

The result is a score, not a rule, because single signals overlap.
What a high score proves is narrow and worth stating plainly: the file carries no camera fingerprint and a download-shaped name, so it left an app rather than a lens.
Whether that app was Instagram is a separate question the headers cannot answer.
In a 60-file sample of the band at or above 10, about half also showed visible Story furniture in the still - letterbox, stickers, burned-in captions, account handles - and the rest were plain clips that still had no capture metadata.
The Story letterbox is the strongest visual signal and ran about nine in ten true when the 121 hits were inspected, so it has its own filter.
Filenames are normalised before matching, so `Copy of X.mp4`, `X (2).mov` and `X (2022-04-07).mov` are recognised as one download.

Reviewing is a multi-select pass.
Tick a still or click it, shift-click to extend the run, or select every candidate on the page, then choose one of two actions.
**Keep** records the file as not a social video: it leaves this list and nothing on disk is touched.
**Move to Bin** records it for removal, which is the same decision every other page in this app writes: the file moves only when the Bin is run by the separate writer process, which re-verifies size and hash first, and the move stays recoverable afterwards.

Both actions write decisions and nothing else, so the page itself never touches the archive.
A decided candidate disappears from the list, and the header keeps a running count of what has been kept and what is marked.
Each batch offers an Undo that restores the previous state, and any decision can also be reversed later from the Log.
Decisions go out twenty at a time, the server's batch limit, so a large selection is sent as several requests in order; if one fails, the message says how many were already saved.

```bash
go run ./cmd/cull -db state/preview.db -import-social social-report.tsv
go run ./cmd/cull -db state/preview.db -web web/dist -social-posters state/social-posters -archive-media /Volumes/family-archive
```

`-social-archive-prefix` maps the host paths in the report onto the catalogue's archive root, and `-social-posters` is a read-only directory of stills captured during detection.
`-archive-media` points at a mounted copy of the archive and is what makes previews and video playback work without a separate media service.
The request carries nothing but a catalogue ID, the path comes from the database, and the handler opens files through `os.Root`, so it cannot read outside that mount even through a symlink, and it answers GET and HEAD only.
Byte ranges are served, which is what lets a browser seek within a video rather than pulling the whole file first.
Without the flag the media routes are simply not mounted and every card keeps its honest "preview unavailable" state.
A candidate already in the catalogue is linked to that asset so its decisions survive a re-import.

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
`-demo-network` permits private-network binding; it is not authentication.
Do not expose the deployed service publicly.

## Tower deployment

Source checkout: `/mnt/user/appdata/tower-cull-next-repo`.
Database: `/mnt/cache/appdata/tower-cull-next/library.db` on Tower's local cache filesystem.
Containers: `tower-cull-next` and private `tower-cull-writer`, running as UID 99, GID 100.
Preview ports: LAN and Tailscale on 8830.
The existing `tower-cull` on 8823 serves media previews with read-only media mounts and a guard blocking non-GET/HEAD requests.
The writer requires its private `CULL_BIN_KEY` in the deployment `.env`.
The existing state directory and SQLite files must be owned by UID 99, GID 100; back up the stopped catalogue before any ownership or schema migration.

```sh
cd /mnt/user/appdata/tower-cull-next-repo
docker compose build preview
docker compose up -d writer preview
docker compose ps
```

Never seed or replace the deployed real catalogue.
Use `docker compose stop preview` to stop the preview without removing its state.
The web container mounts Takeout read-only and its dedicated state read-write, with no archive, Docker socket or legacy database mount.
Only the writer receives the writable archive mount.
Deployment backups exist; a verified recurring catalogue/receipt backup and restore schedule is still required.

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
Incremental ingestion, native decoder jobs, full duplicate matching, Takeout reconciliation and legacy history/receipt migration remain before the old service can be retired.
The target keeps Takeout read-only, imports by verified copy and never couples source cleanup to culling.
The target archive writer may only quarantine explicitly selected files and restore them safely.
