package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeImmichKey = "immich-test-key-3f9a1c7e5b2d8a4f6c0e"

// fakeImmich answers the two endpoints the sync is allowed to use, from an
// in-memory list of assets, and records every request it is sent.
type fakeImmich struct {
	t        *testing.T
	mu       sync.Mutex
	assets   []immichAsset
	down     bool
	status   int
	reason   string
	requests int
	searches int
	updates  []fakeImmichUpdate
	onSearch func()
	// refuse lists assets the key may find but not change, as Immich answers
	// for an asset in another user's library.
	refuse map[string]bool
	server *httptest.Server
}

type fakeImmichUpdate struct {
	IDs        []string `json:"ids"`
	IsFavorite *bool    `json:"isFavorite"`
}

func newFakeImmich(t *testing.T, assets ...immichAsset) *fakeImmich {
	f := &fakeImmich{t: t, assets: assets}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeImmich) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if r.Header.Get("x-api-key") != fakeImmichKey {
		f.mu.Unlock()
		f.t.Errorf("request without the key: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unauthorised", 401)
		return
	}
	f.requests++
	if f.down {
		f.mu.Unlock()
		http.Error(w, "starting", 503)
		return
	}
	if f.status != 0 && f.reason != "" {
		status, reason := f.status, f.reason
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"message": reason, "statusCode": status})
		return
	}
	if f.status != 0 {
		status := f.status
		f.mu.Unlock()
		// A real proxy or error page may well repeat what it was sent.
		http.Error(w, "rejected key "+fakeImmichKey, status)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata":
		var query map[string]any
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil || len(query) != 1 {
			f.mu.Unlock()
			f.t.Errorf("unexpected search body %v %v", query, err)
			http.Error(w, "bad", 400)
			return
		}
		want, _ := query["originalPath"].(string)
		f.searches++
		// Immich matches originalPath loosely, case-insensitively and as a
		// substring, which the fake reproduces so the exact-path check is tested.
		var items []immichAsset
		for _, a := range f.assets {
			if strings.Contains(strings.ToLower(a.OriginalPath), strings.ToLower(want)) {
				items = append(items, a)
			}
		}
		hook := f.onSearch
		f.onSearch = nil
		f.mu.Unlock()
		if hook != nil {
			hook()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"assets": map[string]any{"items": items, "total": len(items)}})
	case r.Method == http.MethodPut && r.URL.Path == "/api/assets":
		var update fakeImmichUpdate
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&update); err != nil || len(update.IDs) != 1 || update.IsFavorite == nil {
			f.mu.Unlock()
			f.t.Errorf("unexpected update body %+v %v", update, err)
			http.Error(w, "bad", 400)
			return
		}
		f.updates = append(f.updates, update)
		if f.refuse[update.IDs[0]] {
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"message": "Not found or no asset.update access", "statusCode": 400})
			return
		}
		known := false
		for i := range f.assets {
			known = known || f.assets[i].ID == update.IDs[0]
		}
		if !known {
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"message": "Not found or no asset.update access", "statusCode": 400})
			return
		}
		for i := range f.assets {
			if f.assets[i].ID == update.IDs[0] {
				f.assets[i].IsFavorite = *update.IsFavorite
			}
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		f.mu.Unlock()
		f.t.Errorf("the sync used an endpoint it must not: %s %s", r.Method, r.URL.Path)
		http.Error(w, "not found", 404)
	}
}

func (f *fakeImmich) favourite(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.assets {
		if a.ID == id {
			return a.IsFavorite
		}
	}
	f.t.Fatalf("no fake asset %s", id)
	return false
}

func (f *fakeImmich) counts() (searches, updates int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.searches, len(f.updates)
}

func (f *fakeImmich) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func (f *fakeImmich) set(change func(*fakeImmich)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// immichClock is a hand-wound clock, so backoff is tested without sleeping.
type immichClock struct{ at time.Time }

func (c *immichClock) now() time.Time             { return c.at }
func (c *immichClock) advance(step time.Duration) { c.at = c.at.Add(step) }

func immichFixture(t *testing.T, f *fakeImmich) (*Store, *ImmichSync, *immichClock) {
	t.Helper()
	s := testStore(t)
	return s, immichSyncFor(t, s, f), &immichClock{}
}

func immichSyncFor(t *testing.T, s *Store, f *fakeImmich) *ImmichSync {
	t.Helper()
	y, err := NewImmichSync(s, ImmichConfig{URL: f.server.URL + "/", Key: fakeImmichKey})
	if err != nil || y == nil {
		t.Fatalf("sync not created: %v", err)
	}
	return y
}

func withClock(y *ImmichSync, c *immichClock) {
	if c.at.IsZero() {
		c.at = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	}
	y.now = c.now
}

func addImmichTestAsset(t *testing.T, s *Store, id int64, path, source string) {
	t.Helper()
	if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,?,?,?,?)", id, path, 1600000000+id, "image", 1000, source); err != nil {
		t.Fatal(err)
	}
}

var immichRequests int

func heart(t *testing.T, s *Store, assetID int64, favourite bool) {
	t.Helper()
	var revision int64
	if err := s.read.QueryRow("SELECT COALESCE((SELECT revision FROM decisions WHERE asset_id=?),0)", assetID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	immichRequests++
	if _, err := s.Decide(context.Background(), Decision{RequestID: fmt.Sprintf("immich-request-%d", immichRequests), AssetID: assetID, ExpectedRevision: revision, Status: "keep", Favourite: favourite}); err != nil {
		t.Fatal(err)
	}
}

type immichQueueRow struct {
	desired, setByCull bool
	immichID, state    string
	attempts           int
	lastError          string
	next               int64
}

func queueRow(t *testing.T, s *Store, assetID int64) immichQueueRow {
	t.Helper()
	var row immichQueueRow
	if err := s.read.QueryRow("SELECT desired,set_by_cull,immich_id,state,attempts,last_error,next_attempt_at FROM immich_favourites WHERE asset_id=?", assetID).Scan(&row.desired, &row.setByCull, &row.immichID, &row.state, &row.attempts, &row.lastError, &row.next); err != nil {
		t.Fatal(err)
	}
	return row
}

func drainNow(t *testing.T, y *ImmichSync) {
	t.Helper()
	if err := y.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestImmichFavouriteThenUnfavouriteMirrorsWhatCullSet(t *testing.T) {
	f := newFakeImmich(t, immichAsset{ID: "im-1", OriginalPath: "/mnt/family-archive/2021/05/IMG_0001.HEIC"})
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	addImmichTestAsset(t, s, 1, "/archive/2021/05/IMG_0001.HEIC", "archive")

	heart(t, s, 1, true)
	if row := queueRow(t, s, 1); row.state != "pending" || !row.desired {
		t.Fatalf("heart not queued: %+v", row)
	}
	drainNow(t, y)
	if !f.favourite("im-1") {
		t.Fatal("Immich favourite not set")
	}
	if row := queueRow(t, s, 1); row.state != "done" || !row.setByCull || row.immichID != "im-1" {
		t.Fatalf("ownership not recorded: %+v", row)
	}

	heart(t, s, 1, false)
	drainNow(t, y)
	if f.favourite("im-1") {
		t.Fatal("favourite Cull set was not cleared")
	}
	if row := queueRow(t, s, 1); row.state != "done" || row.setByCull || row.desired {
		t.Fatalf("clearing not recorded: %+v", row)
	}
	if searches, updates := f.counts(); searches != 1 || updates != 2 {
		t.Fatalf("the cached Immich id was not reused: %d searches, %d updates", searches, updates)
	}
}

func TestImmichNeverClearsAFavouriteImmichAlreadyHad(t *testing.T) {
	f := newFakeImmich(t, immichAsset{ID: "im-2", IsFavorite: true, OriginalPath: "/mnt/family-archive/2019/beach.jpg"})
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	addImmichTestAsset(t, s, 2, "/archive/2019/beach.jpg", "archive")

	heart(t, s, 2, true)
	drainNow(t, y)
	if row := queueRow(t, s, 2); row.state != "done" || row.setByCull {
		t.Fatalf("Immich's own favourite claimed by Cull: %+v", row)
	}
	heart(t, s, 2, false)
	drainNow(t, y)
	heart(t, s, 2, true)
	drainNow(t, y)
	heart(t, s, 2, false)
	drainNow(t, y)
	if !f.favourite("im-2") {
		t.Fatal("a favourite Immich already had was cleared")
	}
	if _, updates := f.counts(); updates != 0 {
		t.Fatalf("Cull wrote to an asset it never owned: %d updates", updates)
	}
}

func TestImmichCoalescesTogglesToTheLatestHeart(t *testing.T) {
	f := newFakeImmich(t,
		immichAsset{ID: "im-3", OriginalPath: "/mnt/family-archive/a.jpg"},
		immichAsset{ID: "im-4", OriginalPath: "/mnt/family-archive/b.jpg"})
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	addImmichTestAsset(t, s, 3, "/archive/a.jpg", "archive")
	addImmichTestAsset(t, s, 4, "/archive/b.jpg", "archive")

	heart(t, s, 3, true)
	heart(t, s, 3, false)
	heart(t, s, 3, true)
	// On and off again before anything synced: Immich never hears about it.
	heart(t, s, 4, true)
	heart(t, s, 4, false)
	var rows int
	s.read.QueryRow("SELECT count(*) FROM immich_favourites").Scan(&rows)
	if rows != 2 {
		t.Fatalf("toggles were not coalesced: %d rows", rows)
	}
	drainNow(t, y)
	if !f.favourite("im-3") || f.favourite("im-4") {
		t.Fatal("Immich does not show the latest hearts")
	}
	if searches, updates := f.counts(); searches != 1 || updates != 1 {
		t.Fatalf("coalesced toggles sent %d searches and %d updates", searches, updates)
	}
}

func TestImmichHeartDuringARequestIsNotLost(t *testing.T) {
	f := newFakeImmich(t, immichAsset{ID: "im-5", OriginalPath: "/mnt/family-archive/c.jpg"})
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	addImmichTestAsset(t, s, 5, "/archive/c.jpg", "archive")
	heart(t, s, 5, true)
	// The heart is taken away while Immich is still being asked about it. The
	// save must not wait on the worker, and the worker must not forget it.
	f.set(func(f *fakeImmich) {
		f.onSearch = func() {
			done := make(chan struct{})
			go func() { heart(t, s, 5, false); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("a save waited on an Immich request")
			}
		}
	})
	if err := y.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if row := queueRow(t, s, 5); row.state != "done" || row.desired || row.setByCull {
		t.Fatalf("the later heart was lost: %+v", row)
	}
	if f.favourite("im-5") {
		t.Fatal("Immich still shows the favourite that was taken away")
	}
}

func TestImmichDownIsQueuedAndRetriedWithBackoffAcrossRestarts(t *testing.T) {
	f := newFakeImmich(t, immichAsset{ID: "im-6", OriginalPath: "/mnt/family-archive/d.jpg"})
	f.set(func(f *fakeImmich) { f.down = true })
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	y := immichSyncFor(t, s, f)
	clock := &immichClock{}
	withClock(y, clock)
	addImmichTestAsset(t, s, 6, "/archive/d.jpg", "archive")

	started := time.Now()
	heart(t, s, 6, true)
	if time.Since(started) > time.Second {
		t.Fatal("a heart waited for Immich")
	}
	drainNow(t, y)
	row := queueRow(t, s, 6)
	if row.state != "pending" || row.attempts != 1 || row.next != clock.at.Add(30*time.Second).Unix() || !strings.Contains(row.lastError, "503") {
		t.Fatalf("first failure not backed off by 30s: %+v", row)
	}
	drainNow(t, y)
	if requests := f.requestCount(); requests != 1 {
		t.Fatalf("retried before the backoff ended: %d requests", requests)
	}
	clock.advance(30 * time.Second)
	drainNow(t, y)
	if row = queueRow(t, s, 6); row.attempts != 2 || row.next != clock.at.Add(time.Minute).Unix() {
		t.Fatalf("backoff did not double: %+v", row)
	}

	// The queue outlives the process.
	s.Close()
	if s, err = Open(dbPath); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	y = immichSyncFor(t, s, f)
	withClock(y, clock)
	if row = queueRow(t, s, 6); row.state != "pending" || row.attempts != 2 {
		t.Fatalf("queue lost on restart: %+v", row)
	}
	f.set(func(f *fakeImmich) { f.down = false })
	clock.advance(time.Minute)
	drainNow(t, y)
	if row = queueRow(t, s, 6); row.state != "done" || row.attempts != 0 || row.lastError != "" || !row.setByCull {
		t.Fatalf("retry did not succeed: %+v", row)
	}
	if !f.favourite("im-6") {
		t.Fatal("Immich favourite not set after it came back")
	}
}

func TestImmichUnreachableIsRetriedNotFailed(t *testing.T) {
	f := newFakeImmich(t)
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	f.server.Close()
	addImmichTestAsset(t, s, 7, "/archive/e.jpg", "archive")
	heart(t, s, 7, true)
	drainNow(t, y)
	if row := queueRow(t, s, 7); row.state != "pending" || row.attempts != 1 || !strings.Contains(row.lastError, "unreachable") {
		t.Fatalf("connection failure not queued for retry: %+v", row)
	}
}

func TestImmichBackoffCapsAtAnHour(t *testing.T) {
	for attempts, want := range map[int]time.Duration{1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute, 7: 32 * time.Minute, 8: time.Hour, 50: time.Hour} {
		if got := immichBackoff(attempts); got != want {
			t.Fatalf("attempt %d: %s, want %s", attempts, got, want)
		}
	}
}

func TestImmichNoMatchOrTwoMatchesFailWithoutAHotLoop(t *testing.T) {
	f := newFakeImmich(t,
		// Loose matches Immich returns for the first file, none of them exact.
		immichAsset{ID: "im-near-1", OriginalPath: "/mnt/family-archive/2020/IMG_1.JPG"},
		immichAsset{ID: "im-near-2", OriginalPath: "/mnt/family-archive/2020/IMG_1.jpg.mov"},
		// Two assets at exactly the second file's path.
		immichAsset{ID: "im-dup-1", OriginalPath: "/mnt/family-archive/2020/twice.jpg"},
		immichAsset{ID: "im-dup-2", OriginalPath: "/mnt/family-archive/2020/twice.jpg"})
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	addImmichTestAsset(t, s, 8, "/archive/2020/IMG_1.jpg", "archive")
	addImmichTestAsset(t, s, 9, "/archive/2020/twice.jpg", "archive")
	heart(t, s, 8, true)
	heart(t, s, 9, true)
	drainNow(t, y)
	if row := queueRow(t, s, 8); row.state != "failed" || !strings.Contains(row.lastError, "no asset at /mnt/family-archive/2020/IMG_1.jpg") {
		t.Fatalf("no exact match not recorded: %+v", row)
	}
	if row := queueRow(t, s, 9); row.state != "failed" || !strings.Contains(row.lastError, "2 assets") || row.next != clock.at.Add(24*time.Hour).Unix() {
		t.Fatalf("two matches not recorded: %+v", row)
	}
	for range 5 {
		clock.advance(time.Hour)
		drainNow(t, y)
	}
	if searches, updates := f.counts(); searches != 2 || updates != 0 {
		t.Fatalf("failed rows were retried in a loop: %d searches, %d updates", searches, updates)
	}
	// A restart looks at failures once more, then they wait again.
	if _, err := s.QueueImmichBackfill(context.Background(), clock.at); err != nil {
		t.Fatal(err)
	}
	drainNow(t, y)
	drainNow(t, y)
	if searches, _ := f.counts(); searches != 4 {
		t.Fatalf("restart retried failures %d times", searches-2)
	}
	if _, updates := f.counts(); updates != 0 {
		t.Fatal("an ambiguous asset was written to")
	}
}

func TestImmichRefusedKeyFailsAndNeverLeaksTheKey(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f := newFakeImmich(t, immichAsset{ID: "im-10", OriginalPath: "/mnt/family-archive/f.jpg"})
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	addImmichTestAsset(t, s, 10, "/archive/f.jpg", "archive")
	addImmichTestAsset(t, s, 11, "/archive/g.jpg", "archive")
	heart(t, s, 10, true)
	heart(t, s, 11, true)
	f.set(func(f *fakeImmich) { f.status = 401 })
	drainNow(t, y)
	if row := queueRow(t, s, 10); row.state != "failed" || !strings.Contains(row.lastError, "401") {
		t.Fatalf("refused key not recorded as a failure: %+v", row)
	}
	f.set(func(f *fakeImmich) { f.status = 0 })
	f.server.Close()
	clock.advance(25 * time.Hour)
	drainNow(t, y)

	// Even an error that did carry the key would be scrubbed before it is kept.
	if got := y.redact("header x-api-key: " + fakeImmichKey); strings.Contains(got, fakeImmichKey) {
		t.Fatal("redaction missed the key")
	}
	var stored strings.Builder
	rows, err := s.read.Query("SELECT last_error FROM immich_favourites")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var message string
		rows.Scan(&message)
		stored.WriteString(message + "\n")
	}
	rows.Close()
	if logs.Len() == 0 || stored.Len() == 0 {
		t.Fatal("nothing was logged or recorded, so nothing was checked")
	}
	printed := fmt.Sprintf("%v %+v %#v", y, y, y)
	for name, text := range map[string]string{"logs": logs.String(), "stored errors": stored.String(), "a printed worker": printed} {
		if strings.Contains(text, fakeImmichKey) {
			t.Fatalf("the key leaked into %s: %s", name, text)
		}
	}
}

func TestImmichBackfillQueuesExistingFavouritesOnce(t *testing.T) {
	f := newFakeImmich(t,
		immichAsset{ID: "im-21", OriginalPath: "/mnt/family-archive/x/1.jpg"},
		immichAsset{ID: "im-22", OriginalPath: "/mnt/family-archive/x/2.jpg"},
		immichAsset{ID: "im-23", IsFavorite: true, OriginalPath: "/mnt/family-archive/x/3.jpg"})
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	for id := int64(21); id <= 23; id++ {
		addImmichTestAsset(t, s, id, fmt.Sprintf("/archive/x/%d.jpg", id-20), "archive")
	}
	addImmichTestAsset(t, s, 24, "/upgrades/x/4.jpg", "takeout")
	addImmichTestAsset(t, s, 25, "/archive/x/5.jpg", "archive")
	// Favourites that arrived as the catalogue was imported, not as decisions.
	if _, err := s.write.Exec("INSERT INTO decisions VALUES(21,'unreviewed',1,0),(22,'keep',1,3),(23,'unreviewed',1,0),(24,'unreviewed',1,0),(25,'keep',0,1)"); err != nil {
		t.Fatal(err)
	}
	queued, err := s.QueueImmichBackfill(context.Background(), clock.at)
	if err != nil || queued != 3 {
		t.Fatalf("queued %d existing favourites: %v", queued, err)
	}
	if again, _ := s.QueueImmichBackfill(context.Background(), clock.at); again != 0 {
		t.Fatalf("backfill queued %d favourites twice", again)
	}
	drainNow(t, y)
	if !f.favourite("im-21") || !f.favourite("im-22") || !f.favourite("im-23") {
		t.Fatal("existing favourites did not reach Immich")
	}
	if row := queueRow(t, s, 23); row.setByCull {
		t.Fatal("a favourite Immich already had was claimed by the backfill")
	}
	// A heart removed behind the queue's back, as a legacy import can, is
	// noticed by the next backfill and cleared only where Cull set it.
	if _, err = s.write.Exec("UPDATE decisions SET favourite=0 WHERE asset_id IN (21,23)"); err != nil {
		t.Fatal(err)
	}
	if queued, _ = s.QueueImmichBackfill(context.Background(), clock.at); queued != 2 {
		t.Fatalf("drift re-queued %d rows", queued)
	}
	drainNow(t, y)
	if f.favourite("im-21") || !f.favourite("im-23") || !f.favourite("im-22") {
		t.Fatal("reconciliation did not respect ownership")
	}
}

func TestImmichRunBackfillsAndWakesOnAHeart(t *testing.T) {
	f := newFakeImmich(t,
		immichAsset{ID: "im-31", OriginalPath: "/mnt/family-archive/r/1.jpg"},
		immichAsset{ID: "im-32", OriginalPath: "/mnt/family-archive/r/2.jpg"})
	s, y, _ := immichFixture(t, f)
	addImmichTestAsset(t, s, 31, "/archive/r/1.jpg", "archive")
	addImmichTestAsset(t, s, 32, "/archive/r/2.jpg", "archive")
	if _, err := s.write.Exec("INSERT INTO decisions VALUES(31,'unreviewed',1,0)"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { y.Run(ctx); close(stopped) }()
	defer func() { cancel(); <-stopped }()
	waitFor(t, func() bool { return f.favourite("im-31") })
	// The idle poll is a minute, so only the wake-up can make this prompt.
	heart(t, s, 32, true)
	waitFor(t, func() bool { return f.favourite("im-32") })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestImmichDisabledWithoutURLOrKeyAndRejectsBadSettings(t *testing.T) {
	s := testStore(t)
	for _, cfg := range []ImmichConfig{{}, {URL: "http://immich:2283"}, {Key: fakeImmichKey}, {URL: " ", Key: fakeImmichKey}} {
		if y, err := NewImmichSync(s, cfg); y != nil || err != nil {
			t.Fatalf("%+v should disable the sync cleanly: %v", cfg.URL, err)
		}
	}
	for _, cfg := range []ImmichConfig{
		{URL: "immich:2283", Key: fakeImmichKey},
		{URL: "ftp://immich", Key: fakeImmichKey},
		{URL: "http://user:pass@immich:2283", Key: fakeImmichKey},
		{URL: "http://immich:2283", Key: fakeImmichKey, PathPrefix: "relative/archive"},
	} {
		y, err := NewImmichSync(s, cfg)
		if y != nil || err == nil {
			t.Fatalf("%s accepted", cfg.URL)
		}
		if strings.Contains(err.Error(), fakeImmichKey) {
			t.Fatal("the key leaked into a settings error")
		}
	}
	// Hearts are still queued while the sync is off, so none is lost.
	addImmichTestAsset(t, s, 40, "/archive/h.jpg", "archive")
	addImmichTestAsset(t, s, 41, "/screenshots/i.png", "screenshots")
	heart(t, s, 40, true)
	heart(t, s, 41, true)
	var rows int
	s.read.QueryRow("SELECT count(*) FROM immich_favourites").Scan(&rows)
	if rows != 1 {
		t.Fatalf("expected only the archive heart queued, got %d rows", rows)
	}
}

func TestImmichStatsReportTheQueue(t *testing.T) {
	f := newFakeImmich(t, immichAsset{ID: "im-50", OriginalPath: "/mnt/family-archive/s.jpg"})
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	addImmichTestAsset(t, s, 50, "/archive/s.jpg", "archive")
	addImmichTestAsset(t, s, 51, "/archive/missing.jpg", "archive")
	addImmichTestAsset(t, s, 52, "/archive/later.jpg", "archive")
	heart(t, s, 50, true)
	heart(t, s, 51, true)
	drainNow(t, y)
	heart(t, s, 52, true)
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/api/stats", nil))
	var stats map[string]any
	if err := json.NewDecoder(r.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats["immichSynced"] != 1.0 || stats["immichPending"] != 1.0 || stats["immichFailed"] != 1.0 {
		t.Fatalf("stats: synced=%v pending=%v failed=%v", stats["immichSynced"], stats["immichPending"], stats["immichFailed"])
	}
}

// The family library belongs to its own Immich user, and a key of anyone else's
// is refused with a 400 that looks like any other. Immich's short reason is kept,
// because it is the only thing that tells the two apart, and the key is still
// scrubbed if a reply ever repeats it.
func TestImmichKeepsImmichsReasonForARefusal(t *testing.T) {
	f := newFakeImmich(t, immichAsset{ID: "im-12", OriginalPath: "/mnt/family-archive/h.jpg"})
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	addImmichTestAsset(t, s, 12, "/archive/h.jpg", "archive")
	heart(t, s, 12, true)
	f.set(func(f *fakeImmich) { f.status = 400; f.reason = "Not found or no asset.update access " + fakeImmichKey })
	drainNow(t, y)
	row := queueRow(t, s, 12)
	if row.state != "failed" || !strings.Contains(row.lastError, "no asset.update access") {
		t.Fatalf("the refusal lost Immich's reason: %+v", row)
	}
	if strings.Contains(row.lastError, fakeImmichKey) {
		t.Fatalf("the key leaked into the stored error: %s", row.lastError)
	}
}

// A file in another Immich user's library is found by path but cannot be
// changed with Cull's key. Asking again cannot help, so the heart is set aside,
// not retried every day and at every restart, until it changes.
func TestImmichPhotoOfAnotherUserIsNotRetried(t *testing.T) {
	f := newFakeImmich(t, immichAsset{ID: "im-70", OriginalPath: "/mnt/family-archive/2024/IMG_6419.MOV"})
	f.refuse = map[string]bool{"im-70": true}
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	addImmichTestAsset(t, s, 70, "/archive/2024/IMG_6419.MOV", "archive")
	heart(t, s, 70, true)
	drainNow(t, y)
	row := queueRow(t, s, 70)
	if row.state != "refused" || row.next != 0 || !strings.Contains(row.lastError, "another Immich user") {
		t.Fatalf("not set aside: %+v", row)
	}
	before := f.requestCount()
	clock.advance(48 * time.Hour)
	if _, err := s.QueueImmichBackfill(context.Background(), clock.now()); err != nil {
		t.Fatal(err)
	}
	drainNow(t, y)
	if got := f.requestCount(); got != before {
		t.Fatalf("a set-aside heart was asked about again: %d requests", got-before)
	}
	if _, _, _, refused := s.ImmichQueueCounts(context.Background()); refused != 1 {
		t.Fatalf("refused count %d", refused)
	}
	// A new heart is a new request, and is tried once more.
	heart(t, s, 70, false)
	heart(t, s, 70, true)
	if row := queueRow(t, s, 70); row.state != "pending" {
		t.Fatalf("a new heart did not queue again: %+v", row)
	}
}

// Immich gives a file a new id when it is scanned in again. Clearing a
// favourite Cull set then looks the file up again instead of failing on the
// id it remembered.
func TestImmichUnfavouriteFollowsANewID(t *testing.T) {
	f := newFakeImmich(t, immichAsset{ID: "im-80", OriginalPath: "/mnt/family-archive/k.jpg"})
	s, y, clock := immichFixture(t, f)
	withClock(y, clock)
	addImmichTestAsset(t, s, 80, "/archive/k.jpg", "archive")
	heart(t, s, 80, true)
	drainNow(t, y)
	f.set(func(f *fakeImmich) {
		f.assets = []immichAsset{{ID: "im-81", OriginalPath: "/mnt/family-archive/k.jpg", IsFavorite: true}}
	})
	heart(t, s, 80, false)
	drainNow(t, y)
	if row := queueRow(t, s, 80); row.state != "done" || row.immichID != "im-81" || row.setByCull {
		t.Fatalf("the new id was not followed: %+v", row)
	}
	if f.favourite("im-81") {
		t.Fatal("the favourite was not cleared under the new id")
	}
}
