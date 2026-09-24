package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// trashFixture fills the Bin from all four sources at once, the way the real
// library has it: a photograph marked in review and not yet moved, a batch of
// two this app's writer moved, a batch the earlier tool moved with its sidecar,
// and a removed screenshot. Every file is synthetic and lives in temp folders.
type trashFixture struct {
	s       *Store
	trash   *TrashWriter
	archive string
	disks   string
	shots   string
	binPlan string
	shot    string
}

func newTrashFixture(t *testing.T) trashFixture {
	t.Helper()
	ctx := context.Background()
	s := testStore(t)
	f := trashFixture{s: s, archive: t.TempDir(), disks: t.TempDir(), shots: t.TempDir()}
	write := func(root, name, body string) {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"A.jpg", "A.jpg.xmp", "B.jpg", "C.jpg", "KEEP.jpg"} {
		write(f.archive, "2020/day/"+name, "family original")
	}
	write(f.disks, "disk1/.culled/2020-01-02/L.jpg", "photo bytes")
	write(f.disks, "disk1/.culled/2020-01-02/L.jpg.xmp", "sidecar bytes")
	write(f.shots, "2020-01-02_S.png", "image bytes")
	if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/archive/2020/day/A.jpg',1,'image',15,'archive'),
		(2,'/archive/2020/day/B.jpg',1,'image',15,'archive'),
		(3,'/archive/2020/day/C.jpg',1,'image',15,'archive'),
		(10,'/screenshots/2020-01-02_S.png',1,'image',11,'screenshots');
		INSERT INTO decisions VALUES(1,'cull',1,1),(2,'cull',0,1),(3,'cull',0,1);
		INSERT INTO screenshot_items(asset_id,path,day,name,size_bytes,mtime,state) VALUES(10,'/screenshots/2020-01-02_S.png','2020-01-02','2020-01-02_S.png',11,1,'waiting');
		INSERT INTO legacy_culled(legacy_id,batch,kind,original_path,culled_path,day,size_bytes,reason,culled_at) VALUES
		(1,'batch-a','media','/disks/disk1/2020/2020-01/2020-01-02/L.jpg','/disks/disk1/.culled/2020-01-02/L.jpg','2020-01-02',11,'review','2026-01-01T00:00:00Z'),
		(2,'batch-a','sidecar','/disks/disk1/2020/2020-01/2020-01-02/L.jpg.xmp','/disks/disk1/.culled/2020-01-02/L.jpg.xmp','2020-01-02',13,'review','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	bin, err := NewBinEngine(s, f.archive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bin.Close() })
	legacy, err := NewLegacyBinEngine(s, f.disks)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { legacy.Close() })
	shots, err := NewScreenshotWriter(s, f.shots, f.archive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shots.Close() })

	plan, err := bin.Preview(ctx, []int64{2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bin.Run(ctx, plan.ID, "quarantine", ""); err != nil {
		t.Fatal(err)
	}
	f.binPlan = plan.ID
	shot, err := shots.Preview(ctx, 10, "remove")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = shots.Run(ctx, shot.ID); err != nil {
		t.Fatal(err)
	}
	f.shot = shot.ID
	f.trash = NewTrashWriter(s, bin, legacy, shots)
	return f
}

func (f trashFixture) items(t *testing.T) map[string]TrashItem {
	t.Helper()
	items, err := f.s.Trash(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]TrashItem{}
	for _, item := range items {
		byKey[item.Key] = item
	}
	return byKey
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// The reviewer sees one Bin, whichever tool put a file there, with a picture on
// every card that has one and sidecars counted into the photograph they belong to.
func TestTrashListsEverySourceAsOneBin(t *testing.T) {
	f := newTrashFixture(t)
	items := f.items(t)
	want := map[string]string{
		"marked:1":                "/api/media/1",
		"bin:" + f.binPlan + ":2": "/api/binned-media/bin/" + f.binPlan + "/0",
		"bin:" + f.binPlan + ":3": "/api/binned-media/bin/" + f.binPlan + "/1",
		"legacy:1":                "/api/bin-media/1",
		"shot:" + f.shot:          "/api/binned-media/shot/" + f.shot + "/0",
	}
	if len(items) != len(want) {
		t.Fatalf("Bin holds %d cards, wanted %d: %+v", len(items), len(want), items)
	}
	for key, preview := range want {
		if items[key].Preview != preview {
			t.Fatalf("%s preview %q, wanted %q", key, items[key].Preview, preview)
		}
	}
	if legacy := items["legacy:1"]; legacy.Sidecars != 1 || legacy.Size != 24 {
		t.Fatalf("the earlier tool's sidecar was not folded into its photograph: %+v", legacy)
	}
	if marked := items["marked:1"]; marked.Sidecars != 0 || marked.Name != "A.jpg" {
		t.Fatalf("marked card: %+v", marked)
	}
	if items["bin:"+f.binPlan+":2"].Group != items["bin:"+f.binPlan+":3"].Group {
		t.Fatal("a batch the writer moved together is not one group")
	}
	body, err := json.Marshal(items["marked:1"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "revision") || strings.Contains(string(body), "assetID") {
		t.Fatalf("writer-only fields reached the page: %s", body)
	}
}

// A batch whose photograph has left and only its sidecars remain is still
// shown, so nothing in the Bin is invisible to Empty Bin's reviewer.
func TestTrashShowsASidecarLeftAlone(t *testing.T) {
	items := legacyItems([]LegacyBinItem{
		{ID: 5, Batch: "b", Kind: "sidecar", Original: "/disks/disk1/2020/x.xmp", Stored: "/disks/disk1/.culled/x.xmp", Size: 3},
		{ID: 4, Batch: "b", Kind: "media", Original: "/disks/disk1/2020/x.jpg", Stored: "/disks/disk1/.culled/x.jpg", Size: 9, PurgedAt: "2026-01-01T00:00:00Z"},
	})
	if len(items) != 1 || items[0].Kind != "sidecar" || items[0].Name != "x.xmp" || items[0].Disk != "disk1" {
		t.Fatalf("a lone sidecar went missing: %+v", items)
	}
}

// Restoring puts every source's files back where they came from. A marked
// photograph was never moved, so restoring it is only a change of mind and
// leaves its favourite as it was.
func TestTrashRestoresAcrossEverySource(t *testing.T) {
	f := newTrashFixture(t)
	keys := make([]string, 0)
	for key := range f.items(t) {
		keys = append(keys, key)
	}
	result, err := f.trash.Restore(context.Background(), keys)
	if err != nil || result.Done != 5 || len(result.Failures) != 0 {
		t.Fatalf("restore: %+v %v", result, err)
	}
	if left := f.items(t); len(left) != 0 {
		t.Fatalf("the Bin still holds %+v", left)
	}
	for _, path := range []string{
		filepath.Join(f.archive, "2020/day/A.jpg"),
		filepath.Join(f.archive, "2020/day/B.jpg"),
		filepath.Join(f.archive, "2020/day/C.jpg"),
		filepath.Join(f.disks, "disk1/2020/2020-01/2020-01-02/L.jpg"),
		filepath.Join(f.disks, "disk1/2020/2020-01/2020-01-02/L.jpg.xmp"),
		filepath.Join(f.shots, "2020-01-02_S.png"),
	} {
		if !exists(t, path) {
			t.Fatalf("%s was not restored", path)
		}
	}
	var status string
	var favourite bool
	if err = f.s.read.QueryRow("SELECT status,favourite FROM decisions WHERE asset_id=1").Scan(&status, &favourite); err != nil || status != "unreviewed" || !favourite {
		t.Fatalf("marked photograph came back as %q favourite=%v: %v", status, favourite, err)
	}
}

// A deletion names the number of files it will destroy, counted after the
// selection is widened to whole batches, and anything else is refused with
// nothing touched.
func TestTrashDeleteNeedsTheExactCount(t *testing.T) {
	f := newTrashFixture(t)
	ctx := context.Background()
	one := []string{"bin:" + f.binPlan + ":2"}
	if count, err := f.trash.Selection(ctx, one); err != nil || count != 2 {
		t.Fatalf("selection widened to %d: %v", count, err)
	}
	if _, err := f.trash.Delete(ctx, one, "DELETE 1"); err == nil {
		t.Fatal("a deletion confirmed for one file destroyed its batch of two")
	}
	if len(f.items(t)) != 5 {
		t.Fatal("a refused deletion changed the Bin")
	}
	result, err := f.trash.Delete(ctx, one, "DELETE 2")
	if err != nil || result.Done != 2 || len(result.Failures) != 0 {
		t.Fatalf("delete: %+v %v", result, err)
	}
	left := f.items(t)
	if len(left) != 3 {
		t.Fatalf("delete reached beyond its batch: %+v", left)
	}
	// The writer keeps its receipt of what it destroyed; the photographs are gone.
	entries, _ := os.ReadDir(filepath.Join(f.archive, ".culled/next", f.binPlan))
	for _, entry := range entries {
		if entry.Name() != "receipt.json" {
			t.Fatalf("deleted file still on disk: %s", entry.Name())
		}
	}
	if !exists(t, filepath.Join(f.archive, "2020/day/KEEP.jpg")) || !exists(t, filepath.Join(f.archive, "2020/day/A.jpg")) {
		t.Fatal("a file outside the selection was touched")
	}
}

// A photograph marked but never moved is deleted through the writer's own
// checks, sidecar included, and nothing beside it is touched.
func TestTrashDeletesAMarkedPhotograph(t *testing.T) {
	f := newTrashFixture(t)
	result, err := f.trash.Delete(context.Background(), []string{"marked:1"}, "DELETE 1")
	if err != nil || result.Done != 1 || len(result.Failures) != 0 {
		t.Fatalf("delete: %+v %v", result, err)
	}
	for _, name := range []string{"A.jpg", "A.jpg.xmp"} {
		if exists(t, filepath.Join(f.archive, "2020/day", name)) {
			t.Fatalf("%s survived its deletion", name)
		}
	}
	if !exists(t, filepath.Join(f.archive, "2020/day/KEEP.jpg")) {
		t.Fatal("an unselected neighbour was deleted")
	}
}

// A page loaded before the Bin changed cannot act on what it shows: an unknown
// key is refused, and Empty Bin must name the count the Bin holds now.
func TestTrashRefusesAStalePage(t *testing.T) {
	f := newTrashFixture(t)
	ctx := context.Background()
	if _, err := f.trash.Restore(ctx, []string{"legacy:999"}); err == nil {
		t.Fatal("a key the Bin does not hold was accepted")
	}
	if _, err := f.trash.Restore(ctx, nil); err == nil {
		t.Fatal("an empty selection was accepted")
	}
	if _, err := f.trash.Empty(ctx, "DELETE 4"); err == nil {
		t.Fatal("Empty Bin went ahead on a count the reviewer was not shown")
	}
	if len(f.items(t)) != 5 {
		t.Fatal("a refused Empty Bin changed the Bin")
	}
	result, err := f.trash.Empty(ctx, "DELETE 5")
	if err != nil || result.Done != 5 || len(result.Failures) != 0 {
		t.Fatalf("empty: %+v %v", result, err)
	}
	if left := f.items(t); len(left) != 0 {
		t.Fatalf("Empty Bin left %+v", left)
	}
	if !exists(t, filepath.Join(f.archive, "2020/day/KEEP.jpg")) {
		t.Fatal("Empty Bin reached a file that was never in it")
	}
	if result, err = f.trash.Empty(ctx, ""); err != nil || result.Done != 0 {
		t.Fatalf("emptying an empty Bin: %+v %v", result, err)
	}
}

func serveBinned(t *testing.T, handler http.Handler, source, plan string, index int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest("GET", "/api/binned-media/"+source+"/"+plan+"/"+strconv.Itoa(index)+"/original", nil)
	request.SetPathValue("source", source)
	request.SetPathValue("plan", plan)
	request.SetPathValue("index", strconv.Itoa(index))
	request.SetPathValue("mode", "original")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// A card for a file this app moved shows the file from where it now sits, and
// stops showing it once it has left the Bin.
func TestBinnedMediaServesOnlyWhatTheBinHolds(t *testing.T) {
	f := newTrashFixture(t)
	handler := f.s.BinnedMediaHandler(MediaRoots{Archive: f.archive, Screenshots: f.shots})
	if got := serveBinned(t, handler, "bin", f.binPlan, 0); got.Code != 200 || got.Body.String() != "family original" {
		t.Fatalf("writer batch served %d %q", got.Code, got.Body.String())
	}
	if got := serveBinned(t, handler, "shot", f.shot, 0); got.Code != 200 || got.Body.String() != "image bytes" {
		t.Fatalf("screenshot served %d %q", got.Code, got.Body.String())
	}
	for _, bad := range []struct {
		source, plan string
		index        int
	}{{"bin", f.binPlan, 9}, {"shot", f.binPlan, 0}, {"other", f.binPlan, 0}, {"bin", "../../etc", 0}} {
		if got := serveBinned(t, handler, bad.source, bad.plan, bad.index); got.Code != 404 {
			t.Fatalf("%+v served %d", bad, got.Code)
		}
	}
	if _, err := f.trash.Restore(context.Background(), []string{"bin:" + f.binPlan + ":2", "shot:" + f.shot}); err != nil {
		t.Fatal(err)
	}
	for source, plan := range map[string]string{"bin": f.binPlan, "shot": f.shot} {
		if got := serveBinned(t, handler, source, plan, 0); got.Code != 404 {
			t.Fatalf("%s served %d after it left the Bin", source, got.Code)
		}
	}
}

// Only the preview process holds the key, and it only forwards the three fixed
// actions, same-origin, as JSON.
func TestTrashRoutesAreGuarded(t *testing.T) {
	f := newTrashFixture(t)
	secret := strings.Repeat("k", 32)
	writer := httptest.NewServer(f.trash.Handler(secret))
	defer writer.Close()
	response, err := http.Post(writer.URL+"/trash/empty", "application/json", strings.NewReader(`{"confirmation":"DELETE 5"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 || len(f.items(t)) != 5 {
		t.Fatalf("writer accepted a request without its key: %d", response.StatusCode)
	}
	gateway := TrashGateway(writer.URL, secret)
	for _, request := range []*http.Request{
		httptest.NewRequest("GET", "/api/trash/empty", nil),
		httptest.NewRequest("POST", "/api/trash/other", strings.NewReader(`{}`)),
	} {
		request.Header.Set("Content-Type", "application/json")
		got := httptest.NewRecorder()
		gateway.ServeHTTP(got, request)
		if got.Code != 404 {
			t.Fatalf("%s %s reached the writer: %d", request.Method, request.URL.Path, got.Code)
		}
	}
	crossSite := httptest.NewRequest("POST", "/api/trash/empty", strings.NewReader(`{"confirmation":"DELETE 5"}`))
	crossSite.Header.Set("Content-Type", "application/json")
	crossSite.Header.Set("Origin", "https://elsewhere.example")
	got := httptest.NewRecorder()
	gateway.ServeHTTP(got, crossSite)
	if got.Code != 403 || len(f.items(t)) != 5 {
		t.Fatalf("a cross-site request emptied the Bin: %d", got.Code)
	}
	restore := httptest.NewRequest("POST", "/api/trash/restore", strings.NewReader(`{"keys":["shot:`+f.shot+`"]}`))
	restore.Header.Set("Content-Type", "application/json")
	got = httptest.NewRecorder()
	gateway.ServeHTTP(got, restore)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"done":1`) || len(f.items(t)) != 4 {
		t.Fatalf("a same-origin restore failed: %d %s", got.Code, got.Body.String())
	}
}
