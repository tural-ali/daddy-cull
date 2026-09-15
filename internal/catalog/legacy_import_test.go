package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestImportLegacyDatabasePreservesNewerDecisions(t *testing.T) {
	legacyPath := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite3", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`
		CREATE TABLE days(day TEXT PRIMARY KEY,status TEXT,reviewed_at TEXT);
		CREATE TABLE review(path TEXT PRIMARY KEY,favourite INTEGER,reviewed_at TEXT,decision TEXT);
		CREATE TABLE culled(id INTEGER PRIMARY KEY,batch TEXT,kind TEXT,original_path TEXT,culled_path TEXT,day TEXT,size INTEGER,reason TEXT,culled_at TEXT,restored_at TEXT,purged_at TEXT,photos_deleted_at TEXT);
		CREATE TABLE shadows(kind TEXT,group_key TEXT,disk TEXT,relpath TEXT,size INTEGER,mtime INTEGER);
		CREATE TABLE hashes(path TEXT,size INTEGER,mtime INTEGER,psig TEXT,md5 TEXT,hashed_at TEXT);
		CREATE TABLE shot_suspects(path TEXT PRIMARY KEY,day TEXT,name TEXT,size INTEGER,width INTEGER,height INTEGER,reason TEXT,found_at TEXT,verdict TEXT,decided_at TEXT,moved_to TEXT);
		CREATE TABLE upgrades_accepted(archive_file TEXT PRIMARY KEY,source_file TEXT,accepted_as TEXT,bytes INTEGER,accepted_at TEXT,via TEXT);
		INSERT INTO days VALUES('2020-01-02','done','2026-01-01T10:00:00Z');
		INSERT INTO review VALUES('/archive/A.JPG',1,'2026-01-01T10:00:00Z','kept'),('/archive/B.JPG',0,'2026-01-01T10:00:00Z','kept');
		INSERT INTO culled VALUES(8,'batch','media','/disks/disk1/A.JPG','/disks/disk1/.culled/A.JPG','2020-01-02',10,'review','2026-01-01T10:00:00Z',NULL,NULL,NULL);
		INSERT INTO shadows VALUES('shadowed','key','disk1','A.JPG',10,123);
		INSERT INTO shot_suspects VALUES('/screenshots/A.png','2020-01-02','A.png',10,100,100,'name','2026-01-01T10:00:00Z',NULL,NULL,NULL);
		INSERT INTO upgrades_accepted VALUES('/archive/A.JPG','/upgrades/A.JPG','/archive/A hi-res.JPG',20,'2026-01-01T10:00:00Z','web');
	`)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	s := testStore(t)
	ctx := context.Background()
	if _, err = s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/A.JPG',1,'image',10,'archive'),(2,'/archive/B.JPG',1,'image',10,'archive')"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.write.ExecContext(ctx, "INSERT INTO asset_days(asset_id,day) VALUES(1,'2020-01-02'),(2,'2020-01-02'); INSERT INTO decisions VALUES(1,'later',0,3)"); err != nil {
		t.Fatal(err)
	}
	if err = s.ImportLegacyDatabase(ctx, legacyPath); err != nil {
		t.Fatal(err)
	}
	var status string
	var favourite bool
	var revision int
	if err = s.read.QueryRowContext(ctx, "SELECT status,favourite,revision FROM decisions WHERE asset_id=1").Scan(&status, &favourite, &revision); err != nil || status != "later" || !favourite || revision != 4 {
		t.Fatalf("newer decision was not preserved: %s %v %d %v", status, favourite, revision, err)
	}
	if err = s.read.QueryRowContext(ctx, "SELECT status FROM decisions WHERE asset_id=2").Scan(&status); err != nil || status != "keep" {
		t.Fatalf("legacy review not imported: %s %v", status, err)
	}
	for _, table := range []string{"day_progress", "legacy_culled", "shadow_entries", "screenshot_suspects", "upgrade_history"} {
		var count int
		if err = s.read.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count: %d %v", table, count, err)
		}
	}
}
