# How it works

The [README](../README.md) covers installing and using Daddy Cull.
This page is for when you want to know exactly what it does to your files.
[ARCHITECTURE.md](ARCHITECTURE.md) covers the design.

## Filing the Import folder

The writer looks in the Import folder every minute, and takes a file once it has not changed for a few seconds.
Each file goes under `YYYY/YYYY-MM/YYYY-MM-DD` for the day it was taken, as the clock where it was taken showed it.
The date comes from the file's metadata, read by exiftool, and otherwise from its name or its file date.
A Live Photo's still and video, and sidecars such as `.xmp` and `.aae`, travel with their photo.
A name already taken on that day gets a number; a file whose bytes that day already holds is moved to `Import/Already in the library` instead.
Nothing is ever overwritten.

## Deciding

Keep, remove and favourite are saved as decisions, not file changes.
Every decision is also written to an append-only history, which the Log shows, so any of them can be undone later, not only with <kbd>⌘Z</kbd>.

## Duplicates

A file is a duplicate only when its bytes are identical to another's.
The web process hashes, with MD5, every file that shares its size with another, one at a time in the background, until every group is proven.
On a large library this means the disk is read steadily for a while after the first start.

## The Bin

Removing a file sends it to the Bin.
The writer checks the file's size and SHA-256 against what the page saw, then moves it to `.culled` inside the library, on the same disk.
Restore puts it back where it was, and refuses if another file has taken that name since.
Deleting from the Bin starts the grace period set in Settings, from 0 to 365 days.
Until it ends the file stays on disk and can be restored from the Log; then the writer deletes it for good.

## Previews and playback

Thumbnails are made on demand into the preview cache beside the catalogue, keyed by file, size and modification time, and never written into the library.
A gallery tile is a small JPEG: a 13 MB screenshot becomes about 21 KB.
ffmpeg draws video frames and HEIC photos.
exiftool reads the full-size JPEG a camera embeds in a RAW file, about nine times faster than decoding the RAW itself.
Videos are served with byte ranges, so the browser can seek without downloading the whole file.
Requests carry only a catalogue ID, and files are opened through `os.Root`, so nothing outside the library can be read, even through a symlink.

## iCloud Photos

[icloudpd](https://github.com/icloud-photos-downloader/icloud_photos_downloader) downloads new photos into the Import folder every six hours, and the writer files them like any other.
It keeps its iCloud session on this Mac only.
Apple asks you to sign in again about every two months; Settings says when, and `daddy-cull icloud sign-in` does it.
Photos removed in Daddy Cull are not removed from iCloud; the Apple Photos addon does that on a Mac that syncs with iCloud.

## Apple Photos

**Bringing photos in.**
`daddy-cull apple-photos import` uses [osxphotos](https://github.com/RhetTbull/osxphotos) to copy each photo, or its edited version, into `Import/Apple Photos`.
Only photos it has not brought across before come, so it can be run again at any time.

**Carrying choices back.**
Cull Sync is a small menu-bar app, built on your Mac from the sources in `mac/CullSync`, that changes Photos through Apple's PhotoKit, never its database.
It finds each photo you removed or favourited by name, type and day, and shows what it found beside the library's copy.
Nothing changes until you press Apply in Photos.
Favourites are set first; removals go to Recently Deleted, after macOS asks you to confirm.
A removal is never offered while the library still holds another copy of the photo.
If you later restore a photo in Daddy Cull that was deleted from Photos, the page says to recover it from Recently Deleted.

Cull Sync is set up from the wizard or the Apple Photos page, with a command that works once and expires after 15 minutes.
It installs `~/Applications/Cull Sync.app`, keeps its key in `~/.config/daddy-cull/sync.conf` with mode 600, and starts at login through the LaunchAgent `app.daddycull.sync`.
Daddy Cull keeps only a hash of that key, and each new setup command replaces it.
The first time, macOS asks whether Cull Sync may use Photos.

## Google Photos

There is no icloudpd for Google Photos.
Since 31 March 2025, Google's Photos API only reads what an app uploaded itself, so [Google Takeout](https://takeout.google.com) is the one supported way to get a whole library out.
Browser automation such as [gphotos-cdp](https://github.com/spraot/gphotos-cdp) can pull a library too, but it drives a signed-in Chrome and breaks when Google changes its pages, so Daddy Cull does not ship it.

Save each `.zip` part of an export in the Takeout folder you chose in the wizard; an export already unzipped is read the same way.
Daddy Cull reads the folder every five minutes, and never changes it.
It pairs each photo with its JSON sidecar for the date taken, and compares it with the library by name, size, day and bytes:

| Tab | What it holds |
|---|---|
| Not in the library | No copy in the library; these can be added |
| Different copies | A file of the same name and day, but different bytes; these can be added beside it |
| Unsure | No date from Google, or a copy could not be compared; these cannot be added |
| Already in the library | The same bytes, under this name or another |
| Removed in Cull | You removed the library's copy, so it is not offered again |
| Added, Skipped | What you added or set aside; Offer again brings skipped ones back |

Adding copies each photo under the day it was taken, or beside the library's copy as `IMG_1234 (Google Photos).JPG`.
The writer checks each copy by SHA-256 before it takes its place, and refuses a file whose bytes that day already holds.

## Immich

Favourites you set in Daddy Cull are set in Immich too, one way only.
Removing a favourite clears it in Immich only if Daddy Cull set it.
Daddy Cull finds each photo by its path in Immich's external library and sends only the favourite flag, with a key that needs the `asset.update` permission.
Favourites wait in a queue while Immich is unreachable, and Settings shows how many are sent, waiting or failed.
Set it up under Immich in Settings, or on a server with `IMMICH_URL`, `IMMICH_KEY` and `IMMICH_PATH_PREFIX`.

## Jellyfin

Jellyfin notices that a file has gone only when it next scans the library.
So whenever files are moved to the Bin, put back, deleted from it or added, Daddy Cull asks Jellyfin to scan its libraries again.
It waits until nothing has changed for a minute first, so emptying a Bin of a thousand files is one scan, and during a long change it still asks every ten minutes.
The one request it sends is `POST /Library/Refresh`; Jellyfin's database is never opened.
A request that fails is tried again after five minutes, then ten, up to once an hour, and the Addons page shows why.
Set it up under Jellyfin in Settings, or on a server with `JELLYFIN_URL` and `JELLYFIN_KEY`.

## For libraries moved from a NAS

Daddy Cull began on an Unraid server, and some addons serve a library moved from one.
Each needs a report made there, imported once, and stays off until it has one.

| Addon | What it needs |
|---|---|
| Screenshots | Screenshots moved into a holding area as they are downloaded, served with `-screenshots-media`, and indexed with `-import-screenshots` |
| Saved from social | A report scoring each video for signs it was saved from Instagram or a similar app, imported with `-import-social` |
| Takeout upgrades | A Takeout staging area served with `-upgrades-media`, and a confirmed report of higher-resolution originals, imported with `-import-upgrades` |
| Shadowed copies | The array's disks mounted read-only with `-disks-media`, for files hidden behind a same-named file on another disk of a merged share |
| Daddy Cull classic | The earlier app's database, imported with `-import-legacy-db` |

`-review-media` serves, from a flat folder of hard links named by catalogue ID, files the merged share cannot show under their own names.

## Measuring at scale

```bash
go run ./cmd/scale -assets 1000000 -requests 2000 -workers 8
```

This builds a synthetic catalogue of a million files and measures the API against it.
It measures routing, queries and JSON, not disks, decoding or the browser.
