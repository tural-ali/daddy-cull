package catalog

import (
	"context"
	"testing"
)

// Turns add up a quarter at a time, either way, wrap at a full turn, which
// leaves no record, and reach the files a day lists. A request naming a file
// that is not in the catalogue changes nothing.
func TestTurns(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for _, id := range []int64{1, 2} {
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'image',100,'archive')", id, "/archive/2025/2025-04/2025-04-08/IMG_000"+string(rune('0'+id))+".JPG"); err != nil {
			t.Fatal(err)
		}
	}
	turned, err := s.Turn(ctx, []int64{1, 2}, 1)
	if err != nil || turned[1] != 1 || turned[2] != 1 {
		t.Fatalf("first turn %v %v", turned, err)
	}
	if turned, err = s.Turn(ctx, []int64{1}, -2); err != nil || turned[1] != 3 {
		t.Fatalf("back two %v %v", turned, err)
	}
	if turned, err = s.Turn(ctx, []int64{1}, 1); err != nil || turned[1] != 0 {
		t.Fatalf("round to upright %v %v", turned, err)
	}
	var rows int
	if err = s.read.QueryRowContext(ctx, "SELECT count(*) FROM asset_turns WHERE asset_id=1").Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("an upright file keeps a row: %d %v", rows, err)
	}
	assets := []*Asset{{ID: 1}, {ID: 2}}
	if err = s.markShapes(ctx, assets); err != nil {
		t.Fatal(err)
	}
	if assets[0].Turn != 0 || assets[1].Turn != 1 {
		t.Fatalf("listed turns %d %d", assets[0].Turn, assets[1].Turn)
	}
	for _, bad := range []struct {
		ids      []int64
		quarters int
	}{{nil, 1}, {[]int64{1}, 0}, {[]int64{1}, 4}, {[]int64{1, 1}, 1}, {[]int64{0}, 1}} {
		if _, err = s.Turn(ctx, bad.ids, bad.quarters); err != ErrInvalid {
			t.Errorf("%v by %d: %v, want invalid", bad.ids, bad.quarters, err)
		}
	}
	if _, err = s.Turn(ctx, []int64{2, 99}, 1); err != ErrInvalid {
		t.Fatalf("unknown file: %v", err)
	}
	if err = s.markShapes(ctx, assets); err != nil || assets[1].Turn != 1 {
		t.Fatalf("a refused request changed a turn: %d %v", assets[1].Turn, err)
	}
}
