package catalog

import (
	"context"
	"testing"
)

func TestRelatedCopiesScreenshotAndScope(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	paths := []string{"/archive/2000/2000-01-03/DSCF0421.JPG", "/archive/2000/2000-01-03/DSCF0421 (2).JPG", "/archive/2000/2000-01-04/DSCF0439.JPG", "/archive/2000/2000-01-04/DSCF0439 (2).JPG", "/archive/2001/2001-01-03/DSCF0421.JPG"}
	for i, p := range paths {
		if _, e := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,?,'image',?)", i+1, p, i+1, 100+i); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.IndexRelated(ctx); e != nil {
		t.Fatal(e)
	}
	for _, id := range []int64{1, 2, 3, 4} {
		got, e := s.Related(ctx, id)
		if e != nil || len(got) != 2 || got[0].RelatedCount != 1 {
			t.Fatal(id, got, e)
		}
	}
	got, e := s.Related(ctx, 5)
	if e != nil || len(got) != 1 || got[0].RelatedCount != 0 {
		t.Fatal("camera filename reused across dates grouped", got, e)
	}
	if _, e = s.Decide(ctx, Decision{AssetID: 1, Status: "keep", ExpectedRevision: 0, RequestID: "related-keep-test"}); e != nil {
		t.Fatal(e)
	}
	if e = s.IndexRelated(ctx); e != nil {
		t.Fatal(e)
	}
	got, e = s.Related(ctx, 2)
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range got {
		if a.ID == 1 && a.Status != "keep" {
			t.Fatal("reindex changed decision")
		}
		if a.ID == 2 && a.Status != "unreviewed" {
			t.Fatal("choice spread to sibling")
		}
	}
	s.write.Exec("INSERT INTO file_state VALUES(1,'bin','test')")
	got, e = s.Related(ctx, 2)
	if e != nil || len(got) != 1 || got[0].RelatedCount != 0 {
		t.Fatal("Bin file still suggested", got, e)
	}
}
func TestRelatedStemCompatibility(t *testing.T) {
	for _, name := range []string{"IMG_1.JPG", "IMG_1 (2).JPG", "IMG_1 (r2).jpg", "IMG_1-edited.jpg", "IMG_1_HEVC.mov", "IMG_1-2.jpg"} {
		if got := relatedKey("/archive/day/" + name); got != "/archive/day/img_1" {
			t.Fatal(name, got)
		}
	}
}

func TestGroupQueueNeverHidesUndecidedSibling(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i, p := range []string{"/archive/2000/A.jpg", "/archive/2000/A (2).jpg", "/archive/2000/B.jpg"} {
		if _, e := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,1,'image',10)", i+1, p); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.IndexRelated(ctx); e != nil {
		t.Fatal(e)
	}
	pg, e := s.page(ctx, "", "", "", 1, true, "", false, "unreviewed", true)
	if e != nil || len(pg.Assets) != 1 || pg.Assets[0].ID != 1 || pg.Next == "" {
		t.Fatal(pg, e)
	}
	next, e := s.page(ctx, pg.Next, "", "", 10, true, "", false, "unreviewed", true)
	if e != nil || len(next.Assets) != 1 || next.Assets[0].ID != 3 {
		t.Fatal("split across pages", next, e)
	}
	s.write.Exec("INSERT INTO decisions VALUES(1,'keep',0,1)")
	pg, e = s.page(ctx, "", "", "", 10, true, "", false, "unreviewed", true)
	if e != nil || len(pg.Assets) != 2 || pg.Assets[0].ID != 2 {
		t.Fatal("kept sibling hides work", pg, e)
	}
	s.write.Exec("INSERT INTO decisions VALUES(2,'cull',0,1)")
	pg, e = s.page(ctx, "", "", "", 10, true, "", false, "unreviewed", true)
	if e != nil || len(pg.Assets) != 1 || pg.Assets[0].ID != 3 {
		t.Fatal("resolved group repeated", pg, e)
	}
}
