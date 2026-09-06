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
