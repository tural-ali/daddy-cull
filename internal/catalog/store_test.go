package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSeparateDatabase(t *testing.T) {
	p := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE inventory(path TEXT)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(p); err == nil {
		s.Close()
		t.Fatal("accepted legacy database")
	}
}

func TestEmpty(t *testing.T) {
	n, err := testStore(t).Count(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("count %d: %v", n, err)
	}
}

// A version 8 catalogue keeps every Immich heart through the change that lets a
// heart be set aside as refused.
func TestVersion8CatalogueAllowsRefusedImmichHearts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "catalog.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite3", p)
	if err != nil {
		t.Fatal(err)
	}
	// Back to the version 8 shape, with one heart queued.
	for _, statement := range []string{
		"DROP TABLE immich_favourites",
		`CREATE TABLE immich_favourites (asset_id INTEGER PRIMARY KEY REFERENCES assets(id), desired INTEGER NOT NULL CHECK(desired IN (0,1)), immich_id TEXT NOT NULL DEFAULT '', set_by_cull INTEGER NOT NULL DEFAULT 0 CHECK(set_by_cull IN (0,1)), state TEXT NOT NULL CHECK(state IN ('pending','done','failed')), attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', next_attempt_at INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL)`,
		"INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/a.jpg',1,'image',1,'archive')",
		"INSERT INTO immich_favourites(asset_id,desired,immich_id,set_by_cull,state,attempts,last_error,next_attempt_at,updated_at) VALUES(1,1,'im-1',1,'failed',38,'no access',5,'2026-09-27T00:00:00Z')",
		"PRAGMA user_version=8",
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	db.Close()
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version, attempts int
	var state, id string
	if err = s.read.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 10 {
		t.Fatalf("version %d %v", version, err)
	}
	if err = s.read.QueryRow("SELECT state,attempts,immich_id FROM immich_favourites WHERE asset_id=1").Scan(&state, &attempts, &id); err != nil || state != "failed" || attempts != 38 || id != "im-1" {
		t.Fatalf("the heart did not survive: %s %d %s %v", state, attempts, id, err)
	}
	if _, err = s.write.Exec("UPDATE immich_favourites SET state='refused' WHERE asset_id=1"); err != nil {
		t.Fatalf("refused is still not allowed: %v", err)
	}
}
