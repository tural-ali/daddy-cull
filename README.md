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

Each tree the catalogue names has its own mount, so a preview can never be drawn from the wrong file: `-archive-media`, `-screenshots-media`, `-upgrades-media` and `-disks-media`.
A tree with no mount answers 404 rather than reaching into another one.
The one exception is deliberate: the cache half of a shadowed pair is the copy the user share already resolves that path to, so it is served from the archive mount when no disk mount exists.

`-review-media` is the last resort for files the share cannot expose under their own names.
The physically shadowed copies sit at paths the merged share resolves to the other copy, and some pairs differ only by letter case, which a case-insensitive client folds together, so neither half can be addressed by name.
It points at a flat directory of hardlinks named by asset id, where a collision cannot occur by construction and the name is exactly what the request already carries.
A real mount for the tree always wins over it.

The Bin's own files are served the same way, through `/api/bin-media/{id}`.
A culled file has not left the archive share, it has moved to a `.culled` folder inside it, so it can still be looked at: deciding whether to restore or permanently delete a photograph from its filename alone is not a real choice.
With `-disks-media` mounted, the recorded disk-qualified path names the exact file and is served as it stands, provided the file is still there.
Unraid's mover migrates the cache onto the array, so a file recorded on the cache may since have moved to a disk.
In that case, or without the disk mount, the path is rewritten to the one the merged share uses, and the rewrite is refused when another file still in the Bin has that same path on a different disk, since the share exposes only one of the two and nothing here can tell which.

Previews are generated rather than proxied, into the directory given by `-preview-cache`, keyed by file, size and modification time.
The catalogue and the imported Bin history each number from one, so the key carries which of the two it counts in and their small integers cannot collide in the one shared cache directory.
A gallery tile is a downscaled JPEG, which took a 13 MB screenshot from 13,193,991 bytes to 21,206.
`-frame-tool` (default `ffmpeg`) decodes a frame for video and for HEIC, and `-raw-tool` (default `exiftool`) reads the full-size JPEG a camera embeds in a RAW file, which is around nine times faster than demosaicing it.
Both are handed the already-validated file descriptor rather than a path, so no second path lookup can resolve anywhere else.
A tool that is not installed is logged once at startup and the previews that needed it stay unavailable; nothing else is affected.

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

## Immich favourites

Hearting an archive photo also marks it as a favourite in Immich, one way only.
Removing the heart clears Immich's favourite only when Cull set it; a photo already favourited in Immich keeps its favourite.
Only the favourite flag is sent, through `POST /api/search/metadata` to find the asset and `PUT /api/assets` to set the flag, with a key that needs `asset.update`.
Hearts are queued in the `immich_favourites` table in the same transaction as the decision, so a save never waits on Immich.
The preview drains the queue in the background, retrying from 30 seconds up to hourly while Immich is down, and queues existing favourites at start-up.
A path Immich has no single exact match for is recorded as failed and retried daily and at restart.
Settings shows how many favourites are synced, waiting and failed.

| Variable | Default | Meaning |
|---|---|---|
| `IMMICH_URL` | `http://192.168.1.10:2283` in compose | Immich base URL; empty disables the mirror |
| `IMMICH_KEY` | *(unset)* | Immich API key, read from the environment only and never logged; empty disables the mirror |
| `IMMICH_PATH_PREFIX` | `/mnt/family-archive` | the archive path as Immich's external library recorded it |

## Apple Photos

The Apple Photos page carries removals and favourites over to the Photos library on the Mac.
The browser never touches Photos: Cull Sync, a menu-bar helper in `mac/CullSync`, does the work through PhotoKit and connects out to the preview.
Check Photos asks the helper to find each item by normalised name, extension and day, with one day of tolerance only when the name and extension are unique in the library.
The page shows the archive file beside what the helper found, and nothing changes until Apply in Photos is pressed.
Favourites are set first, then one deletion request moves the chosen photographs to Recently Deleted after the person confirms on the Mac.
The helper reads every change back, and only what Photos really made is recorded in the `photos_sync` table.
A removal is never offered while the archive still holds another live copy of the photograph.
If something deleted from Photos is later restored in Cull, the page says to recover it from Recently Deleted.
Job state and preview thumbnails live in the preview's memory and are lost on restart, which only means checking again.

| Variable | Default | Meaning |
|---|---|---|
| `PHOTOS_AGENT_KEY` | *(unset)* | optional fixed key for the helper, sent in `X-Photos-Agent-Key`; 32 or more printable characters without spaces, never logged; when unset, keys are generated by the setup command |

Cull Sync is set up from the Apple Photos page, which opens a setup dialog by itself whenever the helper is not connected.
The dialog shows one command to paste into Terminal on the Mac: `curl -fsSL http://<the address the page was opened at>/api/photos/install/<code> | bash`.
The code is random, works once and expires after 15 minutes.
Fetching it returns `mac/CullSync/setup.sh` with the preview's address, a fresh key and the helper's sources, which are compiled into the preview's binary, put in front of it.
The script writes `~/.config/daddy-cull/sync.conf` with mode 600, builds the app on the Mac with Apple's Command Line Tools, installs it in `~/Applications` and loads the LaunchAgent `~/Library/LaunchAgents/net.example.cullsync.plist` with `launchctl bootstrap`.
The LaunchAgent starts Cull Sync at login and after a crash, but not after Quit in its menu.
Running a new command again is how the helper is updated.
The first launch asks for full access to Photos, and someone at the Mac has to click Allow.

Without `PHOTOS_AGENT_KEY`, the preview keeps only the SHA-256 of the key it last handed out, in the `settings` table as `photos_agent_key_sha256`, so a copy of the catalogue lets no helper in.
Each setup command replaces the key, and the previous one stops working at once.
The install endpoint refuses while Cull Sync is applying changes, so a reinstall cannot cut an apply short.
`mac/CullSync/install.sh` installs from a checkout with the same script and keeps the key already in `sync.conf`.
`mac/CullSync/test.sh` runs the matching tests without touching Photos.

## Google Photos

The Google Photos page adds what a Google Photos library holds and the archive lacks.
There is no icloudpd for Google Photos: since 31 March 2025 the Google Photos Library API only reads what an app uploaded itself, so no app can download a whole library through it.
Google Takeout is the one supported way out, so Cull reads Takeout exports dropped into an inbox folder given with `-takeout-inbox`.
Browser automation such as gphotos-cdp can also pull a library, but it drives a signed-in Chrome session and breaks when Google changes its pages, so Cull does not ship it.

The page carries the steps for making an export: only Google Photos ticked, `.zip`, 50 GB parts, once or every two months for a year.
Every part is downloaded into the inbox as it is; an export already unpacked into a folder is read the same way.
The web process reads the inbox every five minutes while the addon is on, and on Read the inbox now.
It never changes the exports.
It pairs each photo with its JSON sidecar for the date taken, description, people and favourite, and checks it against the catalogue by name, size, day and bytes.

| Tab | What it holds |
|---|---|
| Not in the library | The archive has no copy; these can be added. |
| Different copies | The archive holds a file of the same name and day, but not these bytes; these can be added beside it. |
| Unsure | No date from Google, or a file like it could not be compared; these cannot be added. |
| Already in the library | The archive holds these bytes, under this name or another. |
| Removed in Cull | The archive's copy was removed in Cull, so the photo is not offered again. |
| Added | Added from this page. |
| Skipped | Set aside; Offer again brings them back. |

Add to the library queues a `google-photos.add` task under Tasks, and the photos leave the page at once.
The writer copies each one to `YYYY/YYYY-MM/YYYY-MM-DD/<name>` under the day it was taken, or beside the archive's copy as `<name> (Google Photos).<ext>` for a different copy.
It refuses a file whose bytes are already in that day's folder, never overwrites, writes through a temporary file it checks by SHA-256 before linking it into place, dates the file to when it was taken, and creates new folders with mode 777.
When the task finishes, the web process scans the archive so the new files join the catalogue.
Photos inside a zip are unpacked for viewing into `preview-cache/takeout-unpacked`, one file of at most 1 GB at a time, trimmed back to 2 GB once it passes 4 GB.
What was read, the outcome for each photo and every copy made are kept in the `takeout_archives`, `takeout_items` and `takeout_plans` tables.

To turn it on, mount one inbox folder read-only into both containers at the same path and give both the flag:

```yaml
volumes:
  - /mnt/user/takeout-inbox:/takeout-inbox:ro
command: [..., "-takeout-inbox", "/takeout-inbox"]
```

Adding needs the `import` permission, which the writer's own routes for Takeout upgrades now ask for too.

## Tower deployment

Source checkout: `/mnt/user/appdata/tower-cull-next-repo`.
Database: `/mnt/cache/appdata/tower-cull-next/library.db` on Tower's local cache filesystem.
Containers: `tower-cull-next` and private `tower-cull-writer`, running as UID 99, GID 100.
Preview ports: LAN and Tailscale on 8830.
The preview container serves media itself from read-only mounts of the archive, the screenshot holding area, both physical disks and the review hardlink farm, and writes generated tiles to `state/preview-cache`.
The image carries ffmpeg and exiftool for video frames, HEIC and RAW.
Social posters live in `state/social-posters`.
The writer requires its private `CULL_BIN_KEY` in the deployment `.env`.
The preview mirrors archive favourites to Immich when `IMMICH_KEY` is in the same `.env`; without it the mirror is off and the service still starts.
The Apple Photos helper needs no `.env` entry: it is set up from the Apple Photos page, and `PHOTOS_AGENT_KEY` in the `.env` pins a fixed key instead.
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
