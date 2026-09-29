# Running Daddy Cull on a server

On a Mac, use `install.sh`: see the [README](../README.md).
On a Linux server or a NAS, such as Unraid, Synology or TrueNAS, run it with Docker Compose.

## What you need

- Docker with Compose v2.
- A library folder: `YYYY/YYYY-MM/YYYY-MM-DD` folders, or an empty folder to start with.
- An Import folder, where new photos are dropped.
- A state folder on a local disk for the catalogue and previews; SQLite should not sit on a network share.
- About 2 GB of memory for the web container while it draws previews of large HEIC photos.

## Start it

```bash
git clone https://github.com/tural-ali/daddy-cull.git
cd daddy-cull
cp examples/.env.example examples/.env
```

Fill in `examples/.env`: the three folders, the user and group that own them, the address to listen on, and a private key from `openssl rand -hex 32`.
Then build and start both containers:

```bash
VERSION=$(tools/version.sh) docker compose -f examples/docker-compose.yml up -d --build
```

`VERSION` only labels the build, so Settings and the logs say which release is running.
Open `http://<LISTEN_IP>:<PORT>`.
The first start catalogues what the library already holds, and Duplicates works through it in the background.

## How it is laid out

| Container | Mounts | Does |
|---|---|---|
| `web` | library read-only, state | Pages, previews, playback, background hashing |
| `writer` | library, Import, state | Files Import, moves to and from the Bin, deletes after the grace period, scans hourly |

Only the writer can change the library.
The two share `CULL_BIN_KEY`, and the writer is reachable only from the web container.
Both run as `PUID:PGID` with a read-only root filesystem and no capabilities.

On a server the folders are set in `examples/.env`, so the Setup page shows them but cannot change them.
iCloud downloads and the Apple Photos import are Mac features; on a server, point icloudpd or any other downloader at the Import folder instead.

## Google Photos

Mount a folder for Google Takeout exports read-only into both containers, at the same path, and give both the flag:

```yaml
    volumes:
      - /srv/photos/takeout:/takeout-inbox:ro
    command:
      - -takeout-inbox=/takeout-inbox
```

Save each `.zip` part of the export in that folder, then open **Google Photos** in the sidebar.

## Immich

Set `IMMICH_URL`, `IMMICH_KEY` and `IMMICH_PATH_PREFIX` in `examples/.env`.
`IMMICH_PATH_PREFIX` is the library folder as Immich's external library sees it.
The key needs the `asset.update` permission.

## Jellyfin

Set `JELLYFIN_URL` and `JELLYFIN_KEY` in `examples/.env`.
Create the key in Jellyfin under Dashboard, API Keys.
`JELLYFIN_URL` is Jellyfin's address as the container reaches it, which for a Jellyfin on the host network is the host's LAN address, not `localhost`.

## Security

There is no login.
Bind `LISTEN_IP` to a private address, and reach it from outside through a VPN such as Tailscale or WireGuard, never by forwarding the port.
`-demo-network` in the compose file is what allows a non-loopback address; it is not authentication.

## Updates and backups

```bash
git pull
VERSION=$(tools/version.sh) docker compose -f examples/docker-compose.yml up -d --build
```

Back up the state folder, above all `library.db`, before updating: new versions may add tables.
Stop the containers first, or copy it with `sqlite3 library.db ".backup library.db.bak"`, so the copy is consistent.

## Checking a change

`examples/test-compose.sh` starts the compose file against empty folders, files a test photo, checks the web container shows it, and stops everything again.
CI runs it on every push.
