package catalog

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestPhoneDeletionsMarkWhatNobodyDecided(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	for _, statement := range []string{
		"INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/2026/2026-09/2026-09-27/IMG_0001.HEIC',1,'image',100,'archive')",
		"INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(2,'/archive/2026/2026-09/2026-09-27/IMG_0002.HEIC',1,'image',200,'archive')",
		"INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(3,'/archive/2026/2026-09/2026-09-27/IMG_0003.HEIC',1,'image',300,'archive')",
		"INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(4,'/archive/2026/2026-09/2026-09-27/IMG_0004.HEIC',1,'image',400,'archive')",
		"INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(5,'/archive/2026/2026-09/2026-09-27/IMG_0005.HEIC',1,'image',500,'archive')",
		"INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(6,'/archive/2026/2026-09/2026-09-27/IMG_0006.HEIC',1,'image',600,'archive')",
		"INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(7,'/archive/2026/2026-09/2026-09-27/IMG_0007.HEIC',1,'image',700,'archive')",
		"INSERT INTO decisions VALUES(2,'keep',0,3)",
		"INSERT INTO decisions VALUES(3,'unreviewed',1,1)",
		"INSERT INTO decisions VALUES(4,'cull',0,2)",
		"INSERT INTO decisions VALUES(5,'later',0,1)",
	} {
		if _, err := s.write.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	if _, err := s.write.Exec("INSERT INTO file_state(asset_id,state,plan_id) VALUES(6,'moving','p1')"); err != nil {
		t.Fatal(err)
	}
	root := "/photos/library"
	line := func(name string, size int) string {
		return root + "/2026/2026-09/2026-09-27/" + name + "\t" + strconv.Itoa(size) + "\tsam\t2026/09/27/" + name + "\t2026-09-28T01:00:00+00:00\n"
	}
	input := line("IMG_0001.HEIC", 100) + // unreviewed: marked
		line("IMG_0002.HEIC", 200) + // kept in Cull: left
		line("IMG_0003.HEIC", 300) + // a favourite: left
		line("IMG_0004.HEIC", 400) + // already marked
		line("IMG_0005.HEIC", 500) + // later: marked
		line("IMG_0006.HEIC", 600) + // in a Bin operation: left
		line("IMG_0007.HEIC", 1) + // a different file now
		line("IMG_0008.HEIC", 800) // never catalogued
	got, err := s.MarkPhoneDeletions(ctx, root, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := PhoneDeletionResult{Marked: 2, AlreadyMarked: 1, Kept: 1, Favourite: 1, InBin: 1, Changed: 1, NotCatalogued: 1}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	status := func(id int) (string, bool) {
		var st string
		var fav bool
		if err := s.read.QueryRow("SELECT status,favourite FROM decisions WHERE asset_id=?", id).Scan(&st, &fav); err != nil {
			return "none", false
		}
		return st, fav
	}
	for id, want := range map[int]string{1: "cull", 2: "keep", 3: "unreviewed", 4: "cull", 5: "cull", 7: "none"} {
		if st, _ := status(id); st != want {
			t.Errorf("asset %d: %s, want %s", id, st, want)
		}
	}
	if _, fav := status(3); !fav {
		t.Error("the heart went")
	}

	// Taken back in Cull, it is not marked again the next night.
	if _, err = s.write.Exec("UPDATE decisions SET status='keep',revision=revision+1 WHERE asset_id=1"); err != nil {
		t.Fatal(err)
	}
	again, err := s.MarkPhoneDeletions(ctx, root, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if again != (PhoneDeletionResult{HandledBefore: 8}) {
		t.Fatalf("second night: %+v", again)
	}
	if st, _ := status(1); st != "keep" {
		t.Fatalf("the mark came back: %s", st)
	}
}

func TestPhoneDeletionsRefuseABadLineBeforeMarkingAnything(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/a/IMG_0001.HEIC',1,'image',100,'archive')"); err != nil {
		t.Fatal(err)
	}
	good := "/r/a/IMG_0001.HEIC\t100\tsam\ta/IMG_0001.HEIC\t2026-09-28T01:00:00+00:00\n"
	for _, bad := range []string{
		"/r/a/IMG_0002.HEIC\t100\tsam\ta/IMG_0002.HEIC\n",
		"/elsewhere/IMG_0002.HEIC\t100\tsam\tx\tt\n",
		"/r/../etc/passwd\t100\tsam\tx\tt\n",
		"/r/a//b\t100\tsam\tx\tt\n",
		"/r/a/b\tbig\tsam\tx\tt\n",
		"/r/a/b\t-1\tsam\tx\tt\n",
		"/r/a/b\t1\t\tx\tt\n",
	} {
		_, err := s.MarkPhoneDeletions(ctx, "/r", strings.NewReader(good+bad))
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	var n int
	if err := s.read.QueryRow("SELECT count(*) FROM decisions").Scan(&n); err != nil || n != 0 {
		t.Fatalf("marked %d before refusing: %v", n, err)
	}
}
