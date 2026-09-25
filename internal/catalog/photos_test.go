package catalog

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestPhotosNormaliseMatchesTheHelper(t *testing.T) {
	// The same table is in mac/CullSync/Tests/main.swift; both sides must agree
	// or the join silently finds nothing.
	for stem, want := range map[string]string{
		"IMG_2866":                      "IMG_2866",
		"IMG_2866 (2)":                  "IMG_2866",
		"IMG_2866 (2023-08-25)":         "IMG_2866",
		"IMG_2866 (2023-08-25) (2)":     "IMG_2866",
		"IMG_2866-1234567":              "IMG_2866",
		"IMG_2866-12":                   "IMG_2866-12",
		"IMG_2866(r2)":                  "IMG_2866",
		"IMG_2866(r 2)":                 "IMG_2866",
		"IMG_2866_HEVC":                 "IMG_2866_HEVC",
		"IMG_2866(2)":                   "IMG_2866(2)",
		"Copy of IMG_1 (3) (4) (5) (6)": "Copy of IMG_1 (3)",
		" padded ":                      "padded",
		"2022-06-22_IMG_4938":           "2022-06-22_IMG_4938",
		"Screenshot 2022-06-22 (12)":    "Screenshot 2022-06-22",
	} {
		if got := PhotosNormalise(stem); got != want {
			t.Errorf("PhotosNormalise(%q) = %q, want %q", stem, got, want)
		}
	}
	for name, want := range map[string][2]string{
		"IMG_6748.PNG": {"IMG_6748", "png"},
		"IMG_6748.DNG": {"IMG_6748", "dng"},
		"a.tar.gz":     {"a.tar", "gz"},
		".hidden":      {".hidden", ""},
		"trailing.":    {"trailing.", ""},
		"plain":        {"plain", ""},
	} {
		stem, ext := photosSplit(name)
		if stem != want[0] || ext != want[1] {
			t.Errorf("photosSplit(%q) = %q %q, want %q %q", name, stem, ext, want[0], want[1])
		}
	}
}

// photosFixture is a small library holding every kind of removal and the
// traps the old script learned about: a duplicate pair, an upgrade kept under
// another extension, a Google Takeout copy, a screenshot renamed on its way to
// the holding area, and the earlier tool's history.
type photosFixture struct {
	s *Store
}

func newPhotosFixture(t *testing.T) photosFixture {
	t.Helper()
	s := testStore(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.write.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	asset := func(id int64, path, source string) {
		exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,?,?,?,?)", id, path, 0, mediaKind(path), 100, source)
	}
	decide := func(id int64, status string, favourite bool) {
		exec("INSERT INTO decisions(asset_id,status,favourite,revision) VALUES(?,?,?,1)", id, status, favourite)
	}

	// 1: marked in review, nothing else holds it.
	asset(1, "/archive/2023/2023-08-25/IMG_1001.HEIC", "archive")
	decide(1, "cull", false)
	// 2 and 3: a duplicate pair; the copy is removed, the original kept.
	asset(2, "/archive/2023/2023-08-25/IMG_2866.MOV", "archive")
	asset(3, "/archive/2023/2023-08-25/IMG_2866 (2).MOV", "archive")
	decide(3, "cull", false)
	// 4 and 5: both copies of a pair removed, so one Photos asset.
	asset(4, "/archive/2023/2023-08-26/IMG_3000.JPG", "archive")
	asset(5, "/archive/2023/2023-08-26/IMG_3000 (2).JPG", "archive")
	// 6: emptied from the Bin for good by this app's writer.
	asset(6, "/archive/2021/2021-01-02/IMG_4000.HEIC", "archive")
	// 7: in the Bin, then restored.
	asset(7, "/archive/2021/2021-01-02/IMG_4001.HEIC", "archive")
	// 8 and 9: the original removed while the upgraded copy, under another
	// extension, is kept.
	asset(8, "/archive/2020/2020-05-05/IMG_5000.JPG", "archive")
	decide(8, "cull", false)
	asset(9, "/archive/2020/2020-05-05/IMG_5000.HEIC", "archive")
	// 10 and 11: a Google Takeout copy does not count as the archive holding it.
	asset(10, "/archive/2020/2020-05-06/IMG_5001.JPG", "archive")
	decide(10, "cull", false)
	asset(11, "/takeout/Photos from 2020/IMG_5001.JPG", "takeout")
	// 12 and 13: the screenshot and the raw photograph iOS numbered alike, two
	// years apart. Removing the screenshot must not reach the raw.
	asset(12, "/archive/2022/2022-06-22/IMG_6748.PNG", "archive")
	decide(12, "cull", false)
	asset(13, "/archive/2024/2024-03-01/IMG_6748.DNG", "archive")
	// 14: removed with no day to match on.
	asset(14, "/archive/misc/loose.jpg", "archive")
	decide(14, "cull", false)
	// 15: a screenshot removed from the holding area, where its name carries a
	// date prefix Photos never saw.
	asset(15, "/screenshots/2022-06-22_IMG_4938.PNG", "screenshots")
	exec("INSERT INTO screenshot_items(asset_id,path,day,name,size_bytes,mtime,state) VALUES(15,'/screenshots/2022-06-22_IMG_4938.PNG','2022-06-22','2022-06-22_IMG_4938.PNG',100,1,'bin')")
	shot, _ := json.Marshal(ScreenshotPlan{ID: "shotplan", AssetID: 15, Action: "remove", State: "bin", Created: "2026-09-01T10:00:00Z", Files: []ScreenshotPlanFile{{Source: "2022-06-22_IMG_4938.PNG", Destination: "x", Size: 100, Phase: "bin"}}})
	exec("INSERT INTO screenshot_plans(id,asset_id,body) VALUES('shotplan',15,?)", string(shot))
	// 16 and 17: hearts. 17 is also removed, so it is not a favourite.
	asset(16, "/archive/2019/2019-12-25/IMG_7000.HEIC", "archive")
	decide(16, "keep", true)
	asset(17, "/archive/2019/2019-12-25/IMG_7001.HEIC", "archive")
	decide(17, "cull", true)

	for _, id := range []int64{4, 5} {
		decide(id, "cull", false)
	}
	decide(6, "cull", false)
	decide(7, "unreviewed", false)
	bin, _ := json.Marshal(BinPlan{ID: "binplan", State: "bin", Created: "2026-09-02T10:00:00Z",
		Assets: []BinAsset{{ID: 4, Path: "/archive/2023/2023-08-26/IMG_3000.JPG"}, {ID: 5, Path: "/archive/2023/2023-08-26/IMG_3000 (2).JPG"}},
		Files:  []BinFile{{Original: "2023/2023-08-26/IMG_3000.JPG", Size: 100, Phase: "bin"}, {Original: "2023/2023-08-26/IMG_3000 (2).JPG", Size: 100, Phase: "bin"}}})
	exec("INSERT INTO file_plans VALUES('binplan',?)", string(bin))
	exec("INSERT INTO file_state VALUES(4,'bin','binplan'),(5,'bin','binplan'),(6,'purged','old'),(7,'restored','older')")

	// The earlier tool's history.
	exec(`INSERT INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,day,size_bytes,reason,culled_at,restored_at,purged_at,photos_deleted_at) VALUES
		(20,'a','media','/archive/2018/2018-07-01/IMG_8000.JPG','/archive/.culled/a/IMG_8000.JPG','2018-07-01',1,'review','2026-01-01T00:00:00Z',NULL,NULL,NULL),
		(21,'a','sidecar','/archive/2018/2018-07-01/IMG_8000.JPG.xmp','/archive/.culled/a/IMG_8000.JPG.xmp','2018-07-01',1,'review','2026-01-01T00:00:00Z',NULL,NULL,NULL),
		(22,'b','media','/archive/2018/2018-07-02/IMG_8001.JPG','/archive/.culled/b/IMG_8001.JPG','2018-07-02',1,'review','2026-01-01T00:00:00Z',NULL,'2026-02-01T00:00:00Z',NULL),
		(23,'c','media','/archive/2018/2018-07-03/IMG_8002.JPG','/archive/.culled/c/IMG_8002.JPG','2018-07-03',1,'review','2026-01-01T00:00:00Z','2026-01-02T00:00:00Z',NULL,NULL),
		(24,'d','media','/archive/2018/2018-07-04/IMG_8003.JPG','/archive/.culled/d/IMG_8003.JPG','2018-07-04',1,'review','2026-01-01T00:00:00Z',NULL,NULL,'2026-01-03T00:00:00Z'),
		(25,'e','media','/disks/disk1/2018/2018-07-05/IMG_8004.JPG','/disks/disk1/.culled/IMG_8004.JPG','2018-07-05',1,'shadowed: cache copy kept','2026-01-01T00:00:00Z',NULL,NULL,NULL),
		(26,'f','media','/screenshots/2017-01-01_IMG_0100.PNG','/screenshots/.culled/2017-01-01_IMG_0100.PNG',NULL,1,'screenshot','2026-01-01T00:00:00Z',NULL,NULL,NULL)`)
	return photosFixture{s: s}
}

func entryByName(entries []PhotosEntry) map[string]PhotosEntry {
	out := map[string]PhotosEntry{}
	for _, entry := range entries {
		out[entry.Name] = entry
	}
	return out
}

func TestPhotosPlanOffersRemovalsOnlyWhenNothingElseHoldsThePhotograph(t *testing.T) {
	f := newPhotosFixture(t)
	plan, err := f.s.PhotosPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := entryByName(plan.Delete)
	names := make([]string, 0, len(got))
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)
	want := []string{"IMG_0100.PNG", "IMG_1001.HEIC", "IMG_3000.JPG", "IMG_4000.HEIC", "IMG_4938.PNG", "IMG_5001.JPG", "IMG_6748.PNG", "IMG_7001.HEIC", "IMG_8000.JPG", "IMG_8001.JPG"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("deletions %v, want %v", names, want)
	}

	check := func(name, key, state, day, ext, preview string) {
		t.Helper()
		entry := got[name]
		if entry.Keys[0] != key || entry.State != state || entry.Day != day || entry.Ext != ext || entry.Preview != preview || entry.Action != "delete" || entry.ID != "delete:"+key {
			t.Errorf("%s: %+v", name, entry)
		}
	}
	check("IMG_1001.HEIC", "asset:1", "marked", "2023-08-25", "heic", "/api/media/1")
	check("IMG_4000.HEIC", "asset:6", "purged", "2021-01-02", "heic", "")
	check("IMG_8000.JPG", "legacy:20", "bin", "2018-07-01", "jpg", "/api/bin-media/20")
	check("IMG_8001.JPG", "legacy:22", "purged", "2018-07-02", "jpg", "")
	// The holding area's date prefix is Cull's, not Photos'.
	check("IMG_4938.PNG", "asset:15", "bin", "2022-06-22", "png", "/api/binned-media/shot/shotplan/0")
	check("IMG_0100.PNG", "legacy:26", "bin", "2017-01-01", "png", "/api/bin-media/26")
	// The extension travels with the name, so the screenshot can never be
	// matched to the raw photograph of the same number.
	check("IMG_6748.PNG", "asset:12", "marked", "2022-06-22", "png", "/api/media/12")

	// Both removed copies of one photograph are one Photos asset.
	pair := got["IMG_3000.JPG"]
	if !reflect.DeepEqual(pair.Keys, []string{"asset:4", "asset:5"}) || pair.Stem != "IMG_3000" || pair.State != "bin" {
		t.Errorf("pair: %+v", pair)
	}

	held := map[string]string{}
	for _, row := range plan.Held {
		held[row.Name] = row.Kept
	}
	if !reflect.DeepEqual(held, map[string]string{"IMG_2866 (2).MOV": "IMG_2866.MOV", "IMG_5000.JPG": "IMG_5000.HEIC"}) {
		t.Errorf("held %v", held)
	}
	if plan.Undated != 1 {
		t.Errorf("undated %d, want 1", plan.Undated)
	}
	if favs := entryByName(plan.Favourite); len(favs) != 1 || favs["IMG_7000.HEIC"].Keys[0] != "asset:16" || favs["IMG_7000.HEIC"].ID != "favourite:asset:16" {
		t.Errorf("favourites %+v", plan.Favourite)
	}
	if len(plan.Restored) != 0 {
		t.Errorf("restored %+v", plan.Restored)
	}
}

func TestPhotosSyncIsRecordedAndNotOfferedAgain(t *testing.T) {
	f := newPhotosFixture(t)
	ctx := context.Background()
	plan, err := f.s.PhotosPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dels, favs := entryByName(plan.Delete), entryByName(plan.Favourite)
	at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	n, err := f.s.RecordPhotosSync(ctx, []PhotosDone{
		{Entry: dels["IMG_3000.JPG"], PhotosID: []string{"A/L0/001"}},
		{Entry: dels["IMG_1001.HEIC"], PhotosID: []string{"B/L0/001", "C/L0/001"}},
		{Entry: favs["IMG_7000.HEIC"], PhotosID: []string{"D/L0/001"}},
	}, at)
	if err != nil || n != 4 {
		t.Fatalf("recorded %d: %v", n, err)
	}
	var ids string
	if err = f.s.read.QueryRow("SELECT photos_id FROM photos_sync WHERE asset_key='asset:1' AND action='delete'").Scan(&ids); err != nil || ids != "B/L0/001 C/L0/001" {
		t.Fatalf("photos ids %q: %v", ids, err)
	}
	plan, err = f.s.PhotosPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dels = entryByName(plan.Delete)
	if _, again := dels["IMG_3000.JPG"]; again {
		t.Error("a synced pair was offered again")
	}
	if _, again := dels["IMG_1001.HEIC"]; again {
		t.Error("a synced deletion was offered again")
	}
	if len(plan.Favourite) != 0 {
		t.Errorf("a synced favourite was offered again: %+v", plan.Favourite)
	}
	deleted, favourited, last := f.s.PhotosSyncCounts(ctx)
	if deleted != 3 || favourited != 1 || last != "2026-09-25T12:00:00Z" {
		t.Errorf("counts %d %d %q", deleted, favourited, last)
	}

	// The reviewer changes their mind about a photograph Photos has already
	// deleted. Photos cannot undelete, so the page must say so.
	if _, err = f.s.write.Exec("UPDATE decisions SET status='keep',revision=revision+1 WHERE asset_id=1"); err != nil {
		t.Fatal(err)
	}
	plan, err = f.s.PhotosPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Restored) != 1 || plan.Restored[0].Key != "asset:1" || plan.Restored[0].Name != "IMG_1001.HEIC" || plan.Restored[0].Day != "2023-08-25" {
		t.Fatalf("restored %+v", plan.Restored)
	}
	// Only a restored row can be forgotten; the pair is still removed.
	n, err = f.s.ForgetPhotosDeletions(ctx, []string{"asset:1", "asset:4"})
	if err != nil || n != 1 {
		t.Fatalf("forgot %d: %v", n, err)
	}
	plan, _ = f.s.PhotosPlan(ctx)
	if len(plan.Restored) != 0 {
		t.Errorf("still restored %+v", plan.Restored)
	}
	if _, again := entryByName(plan.Delete)["IMG_3000.JPG"]; again {
		t.Error("forgetting another row reopened the pair")
	}
	if _, err = f.s.ForgetPhotosDeletions(ctx, nil); err != ErrInvalid {
		t.Errorf("empty forget: %v", err)
	}
}

func TestPhotosRecordRefusesUnknownActions(t *testing.T) {
	s := testStore(t)
	if _, err := s.RecordPhotosSync(context.Background(), []PhotosDone{{Entry: PhotosEntry{Action: "purge", Keys: []string{"asset:1"}}}}, time.Now()); err != ErrInvalid {
		t.Fatalf("unknown action: %v", err)
	}
	if n := countQuery(context.Background(), s.read, "SELECT count(*) FROM photos_sync"); n != 0 {
		t.Fatalf("%d rows written", n)
	}
}

func TestPhotosSocialRemovalCountsLikeAnyOther(t *testing.T) {
	s := testStore(t)
	if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/2022/2022-04-07/reel.mp4',0,'video',1,'archive');
		INSERT INTO social_items(asset_id,path,day,name,size_bytes,score,evidence,state) VALUES(1,'/archive/2022/2022-04-07/reel.mp4','2022-04-07','reel.mp4',1,12,'','waiting');
		INSERT INTO decisions VALUES(1,'cull',0,1)`); err != nil {
		t.Fatal(err)
	}
	plan, err := s.PhotosPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Delete) != 1 || plan.Delete[0].Name != "reel.mp4" || plan.Delete[0].Kind != "video" {
		t.Fatalf("%+v", plan.Delete)
	}
}
