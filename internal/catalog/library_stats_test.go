package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The library's counts are its archive files as photos, RAW included, and
// videos, without what is in the Bin, deleted from it, missing from disk or
// waiting elsewhere; a file put back from the Bin counts again.
func TestLibraryStats(t *testing.T) {
	s := testStore(t)
	for _, row := range []struct {
		id     int64
		kind   string
		size   int64
		source string
	}{
		{1, "image", 3_000_000, "archive"},
		{2, "raw", 25_000_000, "archive"},
		{3, "video", 400_000_000, "archive"},
		{4, "video", 100_000_000, "archive"}, // in the Bin
		{5, "image", 2_000_000, "archive"},   // deleted from the Bin
		{6, "image", 4_000_000, "archive"},   // put back from the Bin
		{7, "image", 1_000_000, "archive"},   // missing from disk
		{8, "image", 5_000_000, "takeout"},
		{9, "video", 7_000_000, "screenshots"},
	} {
		if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,?,?,?)`,
			row.id, fmt.Sprintf("/archive/2020/2020-09/2020-09-25/IMG_%d.x", row.id), row.kind, row.size, row.source); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO file_state VALUES(4,'bin','plan-1')`,
		`INSERT INTO file_state VALUES(5,'purged','plan-1')`,
		`INSERT INTO file_state VALUES(6,'restored','plan-1')`,
		`INSERT INTO missing_assets VALUES(7,'2026-09-29')`,
	} {
		if _, err := s.write.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LibraryStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := LibraryStats{Photos: MediaTotal{Files: 3, Bytes: 32_000_000}, Videos: MediaTotal{Files: 1, Bytes: 400_000_000}}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if empty, err := testStore(t).LibraryStats(context.Background()); err != nil || empty != (LibraryStats{}) {
		t.Fatalf("an empty library: %+v %v", empty, err)
	}

	// The counts come with the stats while the Library totals addon is on,
	// which it is until someone turns it off.
	stats, err := s.Stats(context.Background(), time.UTC)
	if err != nil || stats.Library == nil || *stats.Library != want {
		t.Fatalf("stats before any choice: %+v %v", stats.Library, err)
	}
	// Settings' Media files counts the same files, so the two never disagree.
	if stats.Total != 4 {
		t.Fatalf("total %d, want the library's 4", stats.Total)
	}
	if _, err := s.write.Exec(`INSERT INTO settings(key,value) VALUES(?,'off')`, addonSetting(AddonLibrary)); err != nil {
		t.Fatal(err)
	}
	if stats, err = s.Stats(context.Background(), time.UTC); err != nil || stats.Library != nil || stats.Total != 4 {
		t.Fatalf("stats with the addon off: %+v %d %v", stats.Library, stats.Total, err)
	}
}
