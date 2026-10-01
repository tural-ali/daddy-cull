package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"

	_ "github.com/mattn/go-sqlite3"
)

// lockOpening holds a lock beside the catalogue until the returned function
// is called.
func lockOpening(path string) (func(), error) {
	lock, err := os.OpenFile(path+".open-lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		lock.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
	}, nil
}

// This application ID keeps the prototype from migrating a legacy catalogue.
const applicationID = 1129663538

// immichWake is how a saved favourite reaches the Immich worker at once instead
// of at its next poll. It holds at most one pending signal, so a burst of hearts
// costs one extra pass, and a process that runs no worker simply never reads it.
type Store struct {
	read, write *sql.DB
	immichWake  chan struct{}
	// sidecarRoots is where copy groups read each copy's sidecars from, and
	// sidecarCache what they read, by file. See sidecar_facts.go.
	sidecarRoots atomic.Pointer[MediaRoots]
	sidecarCache sync.Map
}

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// The web process and the writer open the same catalogue, often at the
	// same moment when they start together. Switching a new file to WAL and
	// making or migrating its tables take locks that do not wait, so one
	// process opens at a time.
	unlock, err := lockOpening(abs)
	if err != nil {
		return nil, err
	}
	defer unlock()
	u := url.URL{Scheme: "file", Path: abs}
	base := u.String()
	w, err := sql.Open("sqlite3", base+"?mode=rwc&_busy_timeout=3000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	fail := func(e error) (*Store, error) { w.Close(); return nil, e }
	var app, tables, version int
	if err = w.QueryRow("PRAGMA application_id").Scan(&app); err != nil {
		return fail(err)
	}
	if err = w.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version > 11 {
		return fail(fmt.Errorf("catalogue schema is newer than this application"))
	}
	if err = w.QueryRow("SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		return fail(err)
	}
	if app != applicationID && (app != 0 || tables != 0) {
		return fail(fmt.Errorf("refusing unknown database: use a separate scale-lab database"))
	}
	if _, err = w.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;"); err != nil {
		return fail(err)
	}
	if app == applicationID && version == 1 {
		if _, err = w.Exec("BEGIN IMMEDIATE; ALTER TABLE assets ADD COLUMN anchor_id INTEGER REFERENCES assets(id); PRAGMA user_version=2; COMMIT;"); err != nil {
			return fail(err)
		}
	}
	// Version 11: an arrival is seen once its date has been opened.
	if app == applicationID && version == 10 {
		if _, err = w.Exec("BEGIN IMMEDIATE; ALTER TABLE asset_arrivals ADD COLUMN seen_at TEXT; PRAGMA user_version=11; COMMIT;"); err != nil {
			return fail(err)
		}
	}
	if app == applicationID && version < 9 {
		if err = allowRefusedImmichState(w); err != nil {
			return fail(err)
		}
	}
	if _, err = w.Exec(fmt.Sprintf(`
BEGIN IMMEDIATE;
PRAGMA application_id=%d;
PRAGMA user_version=11;
CREATE TABLE IF NOT EXISTS sources (
 id TEXT PRIMARY KEY,
 label TEXT NOT NULL,
 read_only INTEGER NOT NULL CHECK(read_only=1)
);
INSERT OR IGNORE INTO sources VALUES('archive','Family archive',1),('takeout','Google Takeout',1),('screenshots','Screenshot holding area',1),('shadow','Physical disk copy',1);
CREATE TABLE IF NOT EXISTS assets (
 id INTEGER PRIMARY KEY,
 relative_path TEXT NOT NULL,
 captured_at INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('image','raw','video')),
 size_bytes INTEGER NOT NULL CHECK(size_bytes>=0),
 version INTEGER NOT NULL DEFAULT 1,
 source_id TEXT NOT NULL DEFAULT 'archive' REFERENCES sources(id),
 anchor_id INTEGER REFERENCES assets(id),
 UNIQUE(source_id,relative_path)
);
CREATE TABLE IF NOT EXISTS decisions (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id),
 status TEXT NOT NULL CHECK(status IN ('unreviewed','keep','later','cull')),
 favourite INTEGER NOT NULL CHECK(favourite IN (0,1)),
 revision INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS decision_events (
 request_id TEXT PRIMARY KEY,
 asset_id INTEGER NOT NULL REFERENCES assets(id),
 expected_revision INTEGER NOT NULL,
 status TEXT NOT NULL,
 favourite INTEGER NOT NULL,
 previous_status TEXT NOT NULL,
 previous_favourite INTEGER NOT NULL,
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS assets_timeline ON assets(captured_at,id);
CREATE INDEX IF NOT EXISTS assets_kind_timeline ON assets(kind,captured_at,id);
CREATE INDEX IF NOT EXISTS assets_source_timeline ON assets(source_id,captured_at,id);
CREATE INDEX IF NOT EXISTS assets_source_kind_timeline ON assets(source_id,kind,captured_at,id);
CREATE INDEX IF NOT EXISTS assets_anchor ON assets(anchor_id);
CREATE INDEX IF NOT EXISTS memories_timeline ON assets(captured_at,id) WHERE anchor_id IS NULL;
CREATE INDEX IF NOT EXISTS memories_kind_timeline ON assets(kind,captured_at,id) WHERE anchor_id IS NULL;
CREATE TABLE IF NOT EXISTS file_state (asset_id INTEGER PRIMARY KEY REFERENCES assets(id), state TEXT NOT NULL, plan_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS file_plans (id TEXT PRIMARY KEY, body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS related_assets (asset_id INTEGER PRIMARY KEY REFERENCES assets(id), group_key TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS related_group ON related_assets(group_key,asset_id);
-- raw_pairs held one export per RAW; raw_stacks holds every export, and is
-- rebuilt from the files on every index, so the old table goes unread.
DROP TABLE IF EXISTS raw_pairs;
CREATE TABLE IF NOT EXISTS raw_stacks (export_id INTEGER PRIMARY KEY REFERENCES assets(id), raw_id INTEGER NOT NULL REFERENCES assets(id));
CREATE INDEX IF NOT EXISTS raw_stacks_raw ON raw_stacks(raw_id);
CREATE TABLE IF NOT EXISTS raw_pair_splits (
 raw_id INTEGER NOT NULL,
 partner_id INTEGER NOT NULL,
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(raw_id,partner_id)
);
-- A HEIC and a JPEG of one name in one folder, with no RAW beside them, and
-- rebuilt with raw_stacks. They are one photo only once exposures proves them
-- one exposure; see format_pairs.go. A split pair is in raw_pair_splits, the
-- HEIC as raw_id.
CREATE TABLE IF NOT EXISTS format_pairs (jpeg_id INTEGER PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE, heic_id INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE);
CREATE INDEX IF NOT EXISTS format_pairs_heic ON format_pairs(heic_id);
-- When and with what a photo was taken, as its metadata records it, read at
-- size_bytes for the files of format_pairs. An empty value is one the file
-- does not record, or a file the reader could not make sense of.
CREATE TABLE IF NOT EXISTS exposures (asset_id INTEGER PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE, size_bytes INTEGER NOT NULL, taken TEXT NOT NULL, subsec TEXT NOT NULL, model TEXT NOT NULL);
-- The video of each Live Photo, found by name in .live-photos/ or beside the
-- photo, and rebuilt from the folders at every scan; see live_photos.go.
-- clip_id is the video's own catalogue entry, when it has one.
CREATE TABLE IF NOT EXISTS live_clips (
 photo_id INTEGER NOT NULL REFERENCES assets(id),
 clip TEXT NOT NULL,
 clip_id INTEGER REFERENCES assets(id),
 size_bytes INTEGER NOT NULL,
 PRIMARY KEY(photo_id,clip)
);
CREATE INDEX IF NOT EXISTS live_clips_asset ON live_clips(clip_id) WHERE clip_id IS NOT NULL;
CREATE TABLE IF NOT EXISTS video_durations (asset_id INTEGER PRIMARY KEY REFERENCES assets(id), size_bytes INTEGER NOT NULL, seconds REAL NOT NULL);
CREATE TABLE IF NOT EXISTS media_shapes (asset_id INTEGER PRIMARY KEY REFERENCES assets(id), size_bytes INTEGER NOT NULL, width INTEGER NOT NULL, height INTEGER NOT NULL);
-- How far the reviewer turned a file in Cull, in quarter turns clockwise. The
-- file itself is never changed; a file shown as it is has no row.
CREATE TABLE IF NOT EXISTS asset_turns (asset_id INTEGER PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE, quarters INTEGER NOT NULL CHECK(quarters BETWEEN 1 AND 3), turned_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE IF NOT EXISTS asset_days (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE,
 day TEXT NOT NULL CHECK(length(day)=10)
);
CREATE INDEX IF NOT EXISTS asset_days_calendar ON asset_days(substr(day,6,5),day,asset_id);
CREATE TABLE IF NOT EXISTS day_progress (
 day TEXT PRIMARY KEY,
 status TEXT NOT NULL CHECK(status IN ('pending','done')),
 reviewed_at TEXT
);
CREATE TABLE IF NOT EXISTS day_progress_events (
 request_id TEXT PRIMARY KEY,
 day TEXT NOT NULL,
 status TEXT NOT NULL,
 previous_status TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS asset_evidence (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE,
 mtime INTEGER,
 partial_signature TEXT,
 full_hash TEXT,
 perceptual_hash TEXT,
 hashed_at TEXT
);
CREATE INDEX IF NOT EXISTS asset_evidence_full_hash ON asset_evidence(full_hash) WHERE full_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS asset_evidence_perceptual_hash ON asset_evidence(perceptual_hash) WHERE perceptual_hash IS NOT NULL;
-- A video's footage, as footage.go reads it: its media data's length, the
-- hash of how its header plays it, and once another video matches both, the
-- hash of the footage itself. media_bytes is 0, and playback_hash NULL, for a
-- file that is not a movie whose footage can be compared. A row read at a size
-- the file no longer has is read again.
CREATE TABLE IF NOT EXISTS asset_footage (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE,
 size_bytes INTEGER NOT NULL,
 mtime INTEGER NOT NULL,
 media_bytes INTEGER NOT NULL,
 playback_hash TEXT,
 footage_hash TEXT,
 located INTEGER NOT NULL DEFAULT 0 CHECK(located IN (0,1)),
 read_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS asset_footage_playback ON asset_footage(media_bytes,playback_hash) WHERE playback_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS asset_footage_hash ON asset_footage(footage_hash) WHERE footage_hash IS NOT NULL;
-- Who last wrote a video, as its header says: converted when HandBrake
-- encoded it from another file, retagged when it carries an XMP packet, which
-- only a tool rewriting its metadata puts there. Read with the footage, and
-- kept apart from it so a catalogue stays readable by a version without it.
CREATE TABLE IF NOT EXISTS asset_writer (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE,
 size_bytes INTEGER NOT NULL,
 converted INTEGER NOT NULL CHECK(converted IN (0,1)),
 retagged INTEGER NOT NULL CHECK(retagged IN (0,1))
);
CREATE TABLE IF NOT EXISTS legacy_culled (
 legacy_id INTEGER PRIMARY KEY,
 batch TEXT NOT NULL,
 kind TEXT NOT NULL,
 original_path TEXT NOT NULL,
 culled_path TEXT NOT NULL,
 day TEXT,
 size_bytes INTEGER NOT NULL,
 reason TEXT,
 culled_at TEXT NOT NULL,
 restored_at TEXT,
 purged_at TEXT,
 photos_deleted_at TEXT
);
CREATE INDEX IF NOT EXISTS legacy_culled_live ON legacy_culled(restored_at,purged_at);
CREATE TABLE IF NOT EXISTS shadow_entries (
 kind TEXT NOT NULL,
 group_key TEXT NOT NULL,
 disk TEXT NOT NULL,
 relative_path TEXT NOT NULL,
 size_bytes INTEGER NOT NULL,
 mtime INTEGER NOT NULL,
 asset_id INTEGER REFERENCES assets(id),
 PRIMARY KEY(kind,group_key,disk,relative_path)
);
CREATE TABLE IF NOT EXISTS screenshot_suspects (
 path TEXT PRIMARY KEY,
 day TEXT,
 name TEXT NOT NULL,
 size_bytes INTEGER NOT NULL,
 width INTEGER,
 height INTEGER,
 reason TEXT NOT NULL,
 found_at TEXT NOT NULL,
 verdict TEXT,
 decided_at TEXT,
 moved_to TEXT
 ,asset_id INTEGER REFERENCES assets(id)
);
CREATE TABLE IF NOT EXISTS screenshot_items (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id),
 path TEXT UNIQUE NOT NULL,
 day TEXT,
 name TEXT NOT NULL,
 size_bytes INTEGER NOT NULL,
 mtime INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('waiting','kept','bin','missing'))
);
CREATE INDEX IF NOT EXISTS screenshot_items_state ON screenshot_items(state,day,name);
CREATE TABLE IF NOT EXISTS social_items (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id),
 path TEXT UNIQUE NOT NULL,
 day TEXT,
 name TEXT NOT NULL,
 size_bytes INTEGER NOT NULL,
 score INTEGER NOT NULL,
 evidence TEXT NOT NULL,
 width INTEGER NOT NULL DEFAULT 0,
 height INTEGER NOT NULL DEFAULT 0,
 duration REAL NOT NULL DEFAULT 0,
 letterbox_top INTEGER NOT NULL DEFAULT 0,
 letterbox_bottom INTEGER NOT NULL DEFAULT 0,
 poster TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL CHECK(state IN ('waiting','missing'))
);
CREATE INDEX IF NOT EXISTS social_items_rank ON social_items(state,score DESC,day,name);
CREATE TABLE IF NOT EXISTS upgrade_history (
 archive_file TEXT PRIMARY KEY,
 source_file TEXT NOT NULL,
 accepted_as TEXT NOT NULL,
 size_bytes INTEGER NOT NULL,
 accepted_at TEXT NOT NULL,
 via TEXT
);
CREATE TABLE IF NOT EXISTS upgrade_candidates (
 archive_asset_id INTEGER NOT NULL REFERENCES assets(id),
 source_asset_id INTEGER NOT NULL REFERENCES assets(id),
 capture_date TEXT NOT NULL,
 archive_day TEXT NOT NULL,
 ratio REAL NOT NULL CHECK(ratio>1),
 source_pixels TEXT NOT NULL,
 archive_pixels TEXT NOT NULL,
 album TEXT NOT NULL,
 source_available INTEGER NOT NULL CHECK(source_available IN (0,1)),
 PRIMARY KEY(archive_asset_id,source_asset_id)
);
CREATE INDEX IF NOT EXISTS upgrade_candidates_ratio ON upgrade_candidates(ratio DESC,archive_asset_id);
CREATE TABLE IF NOT EXISTS upgrade_plans (
 id TEXT PRIMARY KEY,
 archive_asset_id INTEGER NOT NULL REFERENCES assets(id),
 source_asset_id INTEGER NOT NULL REFERENCES assets(id),
 body TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS legacy_file_plans (
 id TEXT PRIMARY KEY,
 body TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS screenshot_plans (
 id TEXT PRIMARY KEY,
 asset_id INTEGER NOT NULL REFERENCES assets(id),
 body TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS screenshot_plans_asset ON screenshot_plans(asset_id);
CREATE TABLE IF NOT EXISTS immich_favourites (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id),
 desired INTEGER NOT NULL CHECK(desired IN (0,1)),
 immich_id TEXT NOT NULL DEFAULT '',
 set_by_cull INTEGER NOT NULL DEFAULT 0 CHECK(set_by_cull IN (0,1)),
 state TEXT NOT NULL CHECK(state IN ('pending','done','failed','refused')),
 attempts INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '',
 next_attempt_at INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS immich_favourites_due ON immich_favourites(state,next_attempt_at);
-- File operations queued from the pages, run one batch at a time by the
-- task runner; see tasks.go. A file waiting in a task has left its page.
CREATE TABLE IF NOT EXISTS tasks (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL,
 label TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','running','done','failed','cancelled')),
 created_at TEXT NOT NULL,
 started_at TEXT,
 finished_at TEXT,
 note TEXT NOT NULL DEFAULT '',
 kept_days INTEGER NOT NULL DEFAULT 0,
 chunk_now INTEGER,
 undo_of TEXT REFERENCES tasks(id) ON DELETE SET NULL,
 cleared INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS task_items (
 task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
 seq INTEGER NOT NULL,
 chunk INTEGER NOT NULL,
 asset_id INTEGER,
 trash_key TEXT,
 trash_group TEXT,
 source_task TEXT,
 source_seq INTEGER NOT NULL DEFAULT 0,
 name TEXT NOT NULL,
 size INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL CHECK(state IN ('queued','done','failed','cancelled')),
 error TEXT NOT NULL DEFAULT '',
 plan_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(task_id,seq)
);
CREATE INDEX IF NOT EXISTS task_items_queued_asset ON task_items(asset_id) WHERE state='queued';
CREATE INDEX IF NOT EXISTS task_items_queued_key ON task_items(trash_key) WHERE state='queued';
CREATE INDEX IF NOT EXISTS task_items_chunk ON task_items(task_id,state,chunk);
CREATE TABLE IF NOT EXISTS trash_deletions (
 grp TEXT PRIMARY KEY,
 deleted_at TEXT NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS photos_sync (
 asset_key TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('delete','favourite')),
 synced_at TEXT NOT NULL,
 photos_id TEXT NOT NULL DEFAULT '',
 name TEXT NOT NULL DEFAULT '',
 day TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(asset_key,action)
);
-- Archive files the last scan did not find on disk, typically moved away by a
-- host script. They stay catalogued, with their decisions and history, but
-- leave the calendar and related groups until a scan finds them again.
CREATE TABLE IF NOT EXISTS missing_assets (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE,
 since TEXT NOT NULL
);
-- Photos deleted on a phone after they graduated into the archive, each
-- handled once; see phone_deletions.go.
CREATE TABLE IF NOT EXISTS phone_deletions (
 zone TEXT NOT NULL,
 rel_path TEXT NOT NULL,
 size_bytes INTEGER NOT NULL,
 reported_at TEXT NOT NULL,
 asset_id INTEGER REFERENCES assets(id),
 outcome TEXT NOT NULL CHECK(outcome IN ('marked','already-marked','kept','favourite','in-bin','not-catalogued','changed')),
 handled_at TEXT NOT NULL,
 PRIMARY KEY(zone,rel_path,size_bytes)
);
-- Google Takeout exports dropped into the inbox, the files in them, and the
-- photos those files are, each once however many exports hold it; see
-- google_photos.go. The exports are only ever read.
CREATE TABLE IF NOT EXISTS takeout_archives (
 id INTEGER PRIMARY KEY,
 name TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN ('zip','folder','unsupported')),
 size_bytes INTEGER NOT NULL,
 modified INTEGER NOT NULL,
 media INTEGER NOT NULL DEFAULT 0,
 scanned_at TEXT NOT NULL,
 error TEXT NOT NULL DEFAULT '',
 present INTEGER NOT NULL DEFAULT 1 CHECK(present IN (0,1))
);
CREATE TABLE IF NOT EXISTS takeout_items (
 id INTEGER PRIMARY KEY,
 name TEXT NOT NULL,
 size_bytes INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('image','raw','video')),
 taken_at INTEGER,
 local_at INTEGER,
 taken_from TEXT NOT NULL DEFAULT '' CHECK(taken_from IN ('','google','name')),
 facts TEXT NOT NULL DEFAULT '{}',
 outcome TEXT NOT NULL DEFAULT 'pending' CHECK(outcome IN ('pending','missing','alternative','uncertain','represented','removed')),
 reason TEXT NOT NULL DEFAULT '',
 match_asset_id INTEGER REFERENCES assets(id) ON DELETE SET NULL,
 state TEXT NOT NULL DEFAULT 'waiting' CHECK(state IN ('waiting','added','skipped')),
 added_as TEXT NOT NULL DEFAULT '',
 added_at TEXT,
 first_seen TEXT NOT NULL,
 UNIQUE(name,size_bytes)
);
CREATE INDEX IF NOT EXISTS takeout_items_tab ON takeout_items(state,outcome,local_at,id);
CREATE TABLE IF NOT EXISTS takeout_entries (
 archive_id INTEGER NOT NULL REFERENCES takeout_archives(id) ON DELETE CASCADE,
 path TEXT NOT NULL,
 inner_dir TEXT NOT NULL,
 name TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('media','sidecar')),
 size_bytes INTEGER NOT NULL,
 crc32 INTEGER,
 facts TEXT NOT NULL DEFAULT '',
 item_id INTEGER REFERENCES takeout_items(id),
 PRIMARY KEY(archive_id,path)
);
CREATE INDEX IF NOT EXISTS takeout_entries_item ON takeout_entries(item_id);
CREATE TABLE IF NOT EXISTS takeout_compared (
 item_id INTEGER NOT NULL REFERENCES takeout_items(id) ON DELETE CASCADE,
 asset_id INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
 asset_size INTEGER NOT NULL,
 same INTEGER NOT NULL CHECK(same IN (0,1)),
 PRIMARY KEY(item_id,asset_id)
);
CREATE TABLE IF NOT EXISTS takeout_plans (
 id TEXT PRIMARY KEY,
 item_id INTEGER NOT NULL REFERENCES takeout_items(id),
 body TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS stats (id INTEGER PRIMARY KEY CHECK(id=1), total INTEGER NOT NULL);
INSERT OR IGNORE INTO stats VALUES(1,0);
-- Goes up whenever the catalogue gains or loses a file, or a file is marked
-- by something other than a person on a page, so an open page can tell it is
-- showing yesterday's archive; see catalogue_generation.go.
CREATE TABLE IF NOT EXISTS catalogue_generation (id INTEGER PRIMARY KEY CHECK(id=1), value INTEGER NOT NULL);
INSERT OR IGNORE INTO catalogue_generation VALUES(1,0);
CREATE TRIGGER IF NOT EXISTS assets_added_to_catalogue AFTER INSERT ON assets BEGIN UPDATE catalogue_generation SET value=value+1 WHERE id=1; END;
CREATE TRIGGER IF NOT EXISTS assets_removed_from_catalogue AFTER DELETE ON assets BEGIN UPDATE catalogue_generation SET value=value+1 WHERE id=1; END;
-- Goes up whenever a file moves into the Bin, back out of it, or is deleted
-- from it, whichever process does it, so a service reading the library's
-- folders can be told they changed; see jellyfin.go.
CREATE TABLE IF NOT EXISTS file_state_generation (id INTEGER PRIMARY KEY CHECK(id=1), value INTEGER NOT NULL);
INSERT OR IGNORE INTO file_state_generation VALUES(1,0);
CREATE TRIGGER IF NOT EXISTS file_state_added AFTER INSERT ON file_state BEGIN UPDATE file_state_generation SET value=value+1 WHERE id=1; END;
CREATE TRIGGER IF NOT EXISTS file_state_changed AFTER UPDATE ON file_state BEGIN UPDATE file_state_generation SET value=value+1 WHERE id=1; END;
CREATE TRIGGER IF NOT EXISTS file_state_removed AFTER DELETE ON file_state BEGIN UPDATE file_state_generation SET value=value+1 WHERE id=1; END;
-- What reached the catalogue with nobody on a page to see it, for the bell,
-- and the files each archive scan added; see notifications.go.
CREATE TABLE IF NOT EXISTS notifications (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 kind TEXT NOT NULL CHECK(kind IN ('arrivals','phone-deletions')),
 created_at TEXT NOT NULL,
 files INTEGER NOT NULL,
 bytes INTEGER NOT NULL,
 read_at TEXT
);
CREATE TABLE IF NOT EXISTS notification_days (
 notification_id INTEGER NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
 day TEXT NOT NULL CHECK(length(day)=10),
 files INTEGER NOT NULL,
 reopened INTEGER NOT NULL CHECK(reopened IN (0,1)),
 PRIMARY KEY(notification_id,day)
);
CREATE TABLE IF NOT EXISTS asset_arrivals (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE,
 arrived_at TEXT NOT NULL,
 seen_at TEXT
);
CREATE INDEX IF NOT EXISTS day_progress_events_day ON day_progress_events(day,status);
COMMIT;`, applicationID)); err != nil {
		return fail(err)
	}
	r, err := sql.Open("sqlite3", base+"?mode=ro&_query_only=on&_busy_timeout=3000&_cache_size=-8192")
	if err != nil {
		return fail(err)
	}
	r.SetMaxOpenConns(4)
	r.SetMaxIdleConns(4)
	if err = r.Ping(); err != nil {
		r.Close()
		return fail(err)
	}
	return &Store{read: r, write: w, immichWake: make(chan struct{}, 1)}, nil
}

// allowRefusedImmichState is the version 9 change: a heart on a photo that
// belongs to another Immich user is set aside as 'refused'. SQLite cannot widen
// a CHECK in place, so the small queue table is copied into its new shape in one
// transaction. A catalogue that never had the table gets it from the schema.
func allowRefusedImmichState(w *sql.DB) error {
	var exists int
	if err := w.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='immich_favourites'").Scan(&exists); err != nil || exists == 0 {
		return err
	}
	_, err := w.Exec(`
BEGIN IMMEDIATE;
CREATE TABLE immich_favourites_v9 (
 asset_id INTEGER PRIMARY KEY REFERENCES assets(id),
 desired INTEGER NOT NULL CHECK(desired IN (0,1)),
 immich_id TEXT NOT NULL DEFAULT '',
 set_by_cull INTEGER NOT NULL DEFAULT 0 CHECK(set_by_cull IN (0,1)),
 state TEXT NOT NULL CHECK(state IN ('pending','done','failed','refused')),
 attempts INTEGER NOT NULL DEFAULT 0,
 last_error TEXT NOT NULL DEFAULT '',
 next_attempt_at INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL
);
INSERT INTO immich_favourites_v9(asset_id,desired,immich_id,set_by_cull,state,attempts,last_error,next_attempt_at,updated_at)
 SELECT asset_id,desired,immich_id,set_by_cull,state,attempts,last_error,next_attempt_at,updated_at FROM immich_favourites;
DROP TABLE immich_favourites;
ALTER TABLE immich_favourites_v9 RENAME TO immich_favourites;
CREATE INDEX IF NOT EXISTS immich_favourites_due ON immich_favourites(state,next_attempt_at);
PRAGMA user_version=9;
COMMIT;`)
	if err != nil {
		w.Exec("ROLLBACK")
	}
	return err
}

func (s *Store) Close() error {
	a := s.read.Close()
	b := s.write.Close()
	if a != nil {
		return a
	}
	return b
}

func (s *Store) Count(ctx context.Context) (int64, error) {
	var n int64
	err := s.read.QueryRowContext(ctx, "SELECT total FROM stats WHERE id=1").Scan(&n)
	return n, err
}
