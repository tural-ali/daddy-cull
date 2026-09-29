package catalog

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeJellyfinKey = "0f3c9a7e5b2d8a4f6c1e9b3d7a5c2e8f"

// fakeJellyfin answers the one request Cull may send, and counts it.
type fakeJellyfin struct {
	mu       sync.Mutex
	status   int
	requests int
	server   *httptest.Server
}

func newFakeJellyfin(t *testing.T) *fakeJellyfin {
	f := &fakeJellyfin{status: http.StatusNoContent}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/Library/Refresh" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		if got := r.Header.Get("Authorization"); got != `MediaBrowser Token="`+fakeJellyfinKey+`"` {
			t.Errorf("Authorization header %q", got)
		}
		f.requests++
		if f.status != http.StatusNoContent {
			// A real proxy or error page may well repeat what it was sent.
			http.Error(w, "rejected "+fakeJellyfinKey, f.status)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeJellyfin) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func (f *fakeJellyfin) answer(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

// jellyfinClock drives the worker's clock by hand: each sleep moves time on
// at once and calls tick with how many sleeps there have been, so the test
// can change the library between them. A sleep past limit ends the pass, as
// Cull stopping would.
type jellyfinClock struct {
	at     time.Time
	sleeps int
	slept  []time.Duration
	limit  int
	tick   func(n int)
}

func testJellyfin(t *testing.T, s *Store, f *fakeJellyfin) (*JellyfinRefresh, *jellyfinClock) {
	t.Helper()
	j, err := NewJellyfinRefresh(s, JellyfinConfig{URL: f.server.URL + "/", Key: fakeJellyfinKey})
	if err != nil || j == nil {
		t.Fatalf("worker %v: %v", j, err)
	}
	clock := &jellyfinClock{at: time.Date(2026, 9, 29, 22, 0, 0, 0, time.UTC), limit: 1000}
	j.now = func() time.Time { return clock.at }
	j.sleep = func(ctx context.Context, d time.Duration) bool {
		clock.sleeps++
		clock.slept = append(clock.slept, d)
		if clock.sleeps > clock.limit {
			return false
		}
		clock.at = clock.at.Add(d)
		if clock.tick != nil {
			clock.tick(clock.sleeps)
		}
		return true
	}
	return j, clock
}

// scanned sends the first request, which every new worker does once, so a
// test starts from a Jellyfin that is up to date.
func scanned(t *testing.T, j *JellyfinRefresh, f *fakeJellyfin) {
	t.Helper()
	if !j.pass(context.Background()) || f.count() != 1 {
		t.Fatalf("the first pass sent %d requests, want 1", f.count())
	}
}

func binFiles(t *testing.T, s *Store, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'video',1,'archive')`, id, fmt.Sprintf("/archive/2020/2020-09/2020-09-25/IMG_%d.MOV", id)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.Exec(`INSERT INTO file_state VALUES(?,'bin','plan-1')`, id); err != nil {
			t.Fatal(err)
		}
	}
}

// Emptying the Bin is one scan once the library has held still for a minute,
// not one per file, and a library that does not change is not scanned again.
func TestJellyfinScansOnceTheBinSettles(t *testing.T) {
	s, f := testStore(t), newFakeJellyfin(t)
	j, clock := testJellyfin(t, s, f)
	scanned(t, j, f)

	// Nothing changes for an hour: nothing is sent.
	clock.sleeps, clock.limit = 0, 120
	if j.pass(context.Background()) || f.count() != 1 {
		t.Fatalf("an unchanged library was scanned: %d requests", f.count())
	}

	// Three files are binned, then emptied over the next two and a half
	// minutes, one change every half minute.
	binFiles(t, s, 1, 2, 3)
	clock.sleeps, clock.limit = 0, 1000
	clock.tick = func(n int) {
		if n <= 5 {
			if _, err := s.write.Exec(`UPDATE file_state SET state=? WHERE plan_id='plan-1'`, []string{"purging", "purged"}[n%2]); err != nil {
				t.Fatal(err)
			}
		}
		if f.count() != 1 {
			t.Fatalf("scanned while the Bin was still being emptied, after %d sleeps", n)
		}
	}
	if !j.pass(context.Background()) {
		t.Fatal("the pass ended early")
	}
	if f.count() != 2 {
		t.Fatalf("%d requests, want one scan for the whole Empty Bin", f.count()-1)
	}
	// The last change was at the fifth sleep, and a minute is two more.
	if clock.sleeps != 7 {
		t.Fatalf("scanned after %d sleeps, want 7", clock.sleeps)
	}
	last, problem := s.JellyfinStatus(context.Background())
	if problem != "" || !last.Equal(clock.at.Truncate(time.Second)) {
		t.Fatalf("status %v %q, want the scan at %v", last, problem, clock.at)
	}
}

// A library that never holds still, such as during a long import, is still
// scanned every ten minutes.
func TestJellyfinScansDuringALongChange(t *testing.T) {
	s, f := testStore(t), newFakeJellyfin(t)
	j, clock := testJellyfin(t, s, f)
	scanned(t, j, f)
	next := int64(100)
	binFiles(t, s, next)
	clock.sleeps = 0
	clock.tick = func(int) { next++; binFiles(t, s, next) }
	j.pass(context.Background())
	if f.count() != 2 || clock.sleeps != 20 {
		t.Fatalf("%d scans after %d sleeps, want one after ten minutes", f.count()-1, clock.sleeps)
	}
}

// A file put back from the Bin is a change too.
func TestJellyfinScansWhenAFileIsPutBack(t *testing.T) {
	s, f := testStore(t), newFakeJellyfin(t)
	binFiles(t, s, 7)
	j, _ := testJellyfin(t, s, f)
	scanned(t, j, f)
	if _, err := s.write.Exec(`UPDATE file_state SET state='restored' WHERE asset_id=7`); err != nil {
		t.Fatal(err)
	}
	j.pass(context.Background())
	if f.count() != 2 {
		t.Fatalf("%d scans after a file came back, want 1", f.count()-1)
	}
}

// What Jellyfin was last told is remembered, so a restart asks again only
// when something changed while Cull was stopped.
func TestJellyfinRemembersAcrossRestarts(t *testing.T) {
	s, f := testStore(t), newFakeJellyfin(t)
	first, _ := testJellyfin(t, s, f)
	scanned(t, first, f)

	again, clock := testJellyfin(t, s, f)
	clock.limit = 10
	if again.pass(context.Background()) || f.count() != 1 {
		t.Fatalf("a restart with nothing changed sent %d requests", f.count()-1)
	}

	binFiles(t, s, 4)
	third, _ := testJellyfin(t, s, f)
	third.pass(context.Background())
	if f.count() != 2 {
		t.Fatalf("a restart after a change sent %d requests, want 1", f.count()-1)
	}
}

// A refused key shows on the Addons page without the key, and is tried again
// after five minutes, then ten, rather than at once.
func TestJellyfinReportsARefusedKey(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	s, f := testStore(t), newFakeJellyfin(t)
	f.answer(http.StatusUnauthorized)
	j, clock := testJellyfin(t, s, f)
	j.pass(context.Background())
	if _, problem := s.JellyfinStatus(context.Background()); problem != "Jellyfin refused the API key (401)" {
		t.Fatalf("problem %q", problem)
	}

	clock.slept = nil
	j.pass(context.Background())
	if f.count() != 2 || len(clock.slept) == 0 || clock.slept[0] != 5*time.Minute {
		t.Fatalf("retried after %v with %d requests, want 5m", clock.slept, f.count())
	}
	clock.slept = nil
	f.answer(http.StatusNoContent)
	j.pass(context.Background())
	if f.count() != 3 || len(clock.slept) == 0 || clock.slept[0] != 10*time.Minute {
		t.Fatalf("retried after %v, want 10m", clock.slept)
	}
	if _, problem := s.JellyfinStatus(context.Background()); problem != "" {
		t.Fatalf("the problem stayed after a scan went through: %q", problem)
	}
	var stored string
	s.read.QueryRow("SELECT value FROM settings WHERE key=?", jellyfinSetting).Scan(&stored)
	if strings.Contains(logs.String(), fakeJellyfinKey) || strings.Contains(stored, fakeJellyfinKey) {
		t.Fatal("the key reached the log or the catalogue")
	}
}

func TestJellyfinConfig(t *testing.T) {
	s := testStore(t)
	if j, err := NewJellyfinRefresh(s, JellyfinConfig{URL: "http://jellyfin.local:8096"}); j != nil || err != nil {
		t.Fatalf("no key: %v %v", j, err)
	}
	for _, bad := range []string{"jellyfin.local:8096", "ftp://jellyfin.local", "http://user:pass@jellyfin.local", "http://jellyfin.local/?x=1"} {
		if _, err := NewJellyfinRefresh(s, JellyfinConfig{URL: bad, Key: fakeJellyfinKey}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	j, err := NewJellyfinRefresh(s, JellyfinConfig{URL: "http://jellyfin.local:8096/", Key: fakeJellyfinKey})
	if err != nil {
		t.Fatal(err)
	}
	if printed := fmt.Sprintf("%v %+v %#v %s", j, j, j, j); strings.Contains(printed, fakeJellyfinKey) {
		t.Fatalf("the key was printed: %s", printed)
	}
}
