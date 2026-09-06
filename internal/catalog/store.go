package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

// This application ID keeps the prototype from migrating a legacy catalogue.
const applicationID = 1129663538

type Store struct{ read, write *sql.DB }

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
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
	if version > 2 {
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
	if _, err = w.Exec(fmt.Sprintf(`
BEGIN IMMEDIATE;
PRAGMA application_id=%d;
PRAGMA user_version=2;
CREATE TABLE IF NOT EXISTS sources (
 id TEXT PRIMARY KEY,
 label TEXT NOT NULL,
 read_only INTEGER NOT NULL CHECK(read_only=1)
);
INSERT OR IGNORE INTO sources VALUES('archive','Family archive (sample)',1),('takeout','Google Takeout (sample)',1);
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
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS stats (id INTEGER PRIMARY KEY CHECK(id=1), total INTEGER NOT NULL);
INSERT OR IGNORE INTO stats VALUES(1,0);
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
	return &Store{read: r, write: w}, nil
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
