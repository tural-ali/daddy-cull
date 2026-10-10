"""Reads what Apple Photos has worked out about each picture and hands it to
Daddy Cull, which shows it as hints on a day page.

Photos scores every picture in the library for Memories: an overall
aesthetic score, whether the shot failed, whether its subject is sharp, how
dark or noisy it is, which faces it found and how good each one looked, and a
list of what is in it. None of that is reachable through PhotoKit, which is
what Cull Sync uses, so this script reads it the way osxphotos does, from the
library's own database, read-only. It runs under osxphotos:

    osxphotos run photos-scores.py            send to the server in sync.conf
    osxphotos run photos-scores.py --dump f   write JSON lines to f instead

The server is the one in ~/.config/daddy-cull/sync.conf, with the key the
Cull Sync setup wrote there; --server and --token override it. Nothing in
Photos is changed, nothing is written anywhere but the dump file, and only
what is listed in ITEM below leaves the Mac: filenames, days, scores, labels
and Apple's one-line caption. No face names, places or thumbnails.
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import pathlib
import sys
import urllib.error
import urllib.request

import osxphotos

VERSION = "apple-photos-v1"
PAGE = 1000
MAX_LABELS = 20
MAX_CAPTION = 200

# What the server stores for each item; keep in step with PhotosScore in
# next/internal/catalog/photos_scores.go. A number Photos has not worked out is
# left out rather than sent as 0, so "no score" and "scored 0" stay apart.
ITEM = """photosId name day kind size favourite hidden adjusted deleted
overall curation failure sharp blur lowLight noise intrusive
composition framing subject interesting timing lighting
faces faceMin faceMax smiles labels caption""".split()


def read_sync_conf() -> tuple[str, str]:
    """The url and token the Cull Sync setup wrote, as Config.swift reads them."""
    url, token = "http://127.0.0.1:8830", ""
    path = pathlib.Path.home() / ".config/daddy-cull/sync.conf"
    try:
        text = path.read_text(encoding="utf-8")
    except OSError:
        return url, token
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key, value = key.strip().lower(), value.strip()
        if key == "url" and value:
            url = value
        elif key == "token":
            token = value
    return url.rstrip("/"), token


def score(value: float | None) -> float | None:
    """Photos leaves 0 where it never scored; the server wants those absent."""
    if value is None or value == 0:
        return None
    return round(float(value), 4)


def item(photo: osxphotos.PhotoInfo, captions: bool) -> dict:
    s = photo.score
    faces = [f.quality for f in photo.face_info if f.quality is not None and f.quality >= 0]
    smiles = sum(1 for f in photo.face_info if getattr(f, "has_smile", False))
    # The day in this Mac's zone, as Cull Sync keys its matches, so the two
    # channels agree about which day a picture belongs to.
    day = photo.date.astimezone().strftime("%Y-%m-%d") if photo.date else ""
    out = {
        "photosId": photo.uuid,
        "name": photo.original_filename or "",
        "day": day,
        "kind": "video" if photo.ismovie else "image",
        "size": int(photo.original_filesize or 0),
        "favourite": bool(photo.favorite),
        "hidden": bool(photo.hidden),
        "adjusted": bool(photo.hasadjustments),
        # In Recently Deleted: still scored, and still the archive's picture
        # if the archive kept its file, so it is sent and marked.
        "deleted": bool(photo.intrash),
        "overall": score(s.overall),
        "curation": score(s.curation),
        "failure": score(s.failure),
        "sharp": score(s.sharply_focused_subject),
        "blur": score(s.tastefully_blurred),
        "lowLight": score(s.low_light),
        "noise": score(s.noise),
        "intrusive": score(s.intrusive_object_presence),
        "composition": score(s.pleasant_composition),
        "framing": score(s.well_framed_subject),
        "subject": score(s.well_chosen_subject),
        "interesting": score(s.interesting_subject),
        "timing": score(s.well_timed_shot),
        "lighting": score(s.pleasant_lighting),
        "faces": len(photo.face_info),
        "faceMin": round(min(faces), 4) if faces else None,
        "faceMax": round(max(faces), 4) if faces else None,
        "smiles": smiles,
        "labels": sorted(set(photo.labels_normalized))[:MAX_LABELS],
        "caption": "",
    }
    if captions:
        try:
            out["caption"] = (photo.ai_caption or "")[:MAX_CAPTION]
        except Exception:  # a library without media analysis, or a locked one
            out["caption"] = ""
    return {k: v for k, v in out.items() if v is not None}


def post(server: str, token: str, body: dict) -> dict:
    data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(
        server + "/api/photos/agent/scores",
        data=data,
        method="POST",
        headers={"Content-Type": "application/json", "X-Photos-Agent-Key": token},
    )
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        detail = e.read().decode("utf-8", "replace")[:300]
        raise SystemExit(f"photos-scores: the server answered {e.code}: {detail}")
    except urllib.error.URLError as e:
        raise SystemExit(f"photos-scores: could not reach {server}: {e.reason}")


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="photos-scores")
    parser.add_argument("--dump", help="write JSON lines here instead of sending")
    parser.add_argument("--server", help="Daddy Cull's address; default from sync.conf")
    parser.add_argument("--token", help="Cull Sync's key; default from sync.conf")
    parser.add_argument("--no-captions", action="store_true", help="leave Apple's captions out")
    parser.add_argument("--library", help="a Photos library other than the current one")
    args = parser.parse_args(argv)

    db = osxphotos.PhotosDB(dbfile=args.library) if args.library else osxphotos.PhotosDB()
    # Shared-album items are not the library's own and Photos never scores
    # them. Recently Deleted items are kept: Photos still holds their scores.
    photos = [p for p in db.photos(intrash=False) + db.photos(intrash=True) if not p.shared]
    print(f"photos-scores: {len(photos)} items in the library", file=sys.stderr)
    captions = not args.no_captions

    if args.dump:
        with open(args.dump, "w", encoding="utf-8") as out:
            for photo in photos:
                out.write(json.dumps(item(photo, captions), ensure_ascii=False) + "\n")
        print(f"photos-scores: wrote {len(photos)} items to {args.dump}", file=sys.stderr)
        return 0

    server, token = read_sync_conf()
    server = (args.server or os.environ.get("CULL_SYNC_URL") or server).rstrip("/")
    token = args.token or os.environ.get("CULL_SYNC_TOKEN") or token
    if not token:
        raise SystemExit("photos-scores: no key. Set Cull Sync up from the Apple Photos page, or pass --token.")

    run = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    stored = total = 0
    for start in range(0, len(photos), PAGE):
        page = [item(p, captions) for p in photos[start : start + PAGE]]
        last = start + PAGE >= len(photos)
        answer = post(server, token, {"version": VERSION, "run": run, "items": page, "done": last})
        stored += int(answer.get("stored", 0))
        total = int(answer.get("total", 0))
        print(f"photos-scores: sent {min(start + PAGE, len(photos))} of {len(photos)}", file=sys.stderr)
    print(f"photos-scores: {stored} items sent, {total} held by the server", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
