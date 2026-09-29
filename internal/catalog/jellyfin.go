package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Jellyfin reads the library's folders as a media library, and finds out that
// files went away only when it next scans them. Left alone that is once a
// night at best, so a video moved to the Bin or deleted from it goes on
// showing, and failing to play, for most of a day. Cull therefore asks
// Jellyfin to scan its libraries again whenever files leave or return to the
// library: when they are moved to the Bin, put back, deleted from it, or added
// by an import. It waits for the change to settle first, so emptying a Bin of
// a thousand files is one scan rather than a thousand.
//
// Nothing else is ever sent. The request is POST /Library/Refresh, which scans
// every library the server has, so it needs no knowledge of how Jellyfin names
// the library's folders. Jellyfin's database is never opened.
const (
	// A change is acted on once the library has held still for this long,
	// looked at every jellyfinPoll.
	jellyfinPoll   = 30 * time.Second
	jellyfinSettle = time.Minute
	// Files that go on changing for longer than this, such as a long import,
	// still get a scan now and then rather than none until the end.
	jellyfinLongestWait = 10 * time.Minute
	// A failed request is tried again after five minutes, doubling up to an
	// hour, so a Jellyfin that is down costs one request an hour.
	jellyfinFirstRetry = 5 * time.Minute
	jellyfinLastRetry  = time.Hour
	// One request is bounded well inside the retry interval.
	jellyfinRequestTimeout = 20 * time.Second
	jellyfinMaxResponse    = 1 << 20
)

// jellyfinSetting is where the last request is remembered, so a change made
// while Cull was stopped is still sent after it starts.
const jellyfinSetting = "jellyfin_refresh"

// JellyfinConfig is how the review app reaches Jellyfin. The key is read from
// the environment by the caller and is never logged, printed or stored.
type JellyfinConfig struct {
	URL string
	Key string
}

// JellyfinRefresh is the background worker that asks Jellyfin to scan again.
// It runs in the review app; the private writer never talks to Jellyfin.
type JellyfinRefresh struct {
	s      *Store
	base   string
	key    string
	client *http.Client
	// now and sleep are the clock, which tests drive by hand. sleep reports
	// false once ctx has ended.
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) bool
}

// jellyfinState is what the last request came to.
type jellyfinState struct {
	// Seen is the library's generation Jellyfin was last told about.
	Seen string `json:"seen"`
	// At is when Jellyfin was last asked successfully, in Unix seconds.
	At int64 `json:"at"`
	// Error is why the last request failed, or empty.
	Error string `json:"error,omitempty"`
	// Failures counts the requests that have failed in a row.
	Failures int `json:"failures,omitempty"`
	// FailedAt is when the last of them failed, in Unix seconds.
	FailedAt int64 `json:"failedAt,omitempty"`
}

// String keeps the key out of anything that prints the worker.
func (j *JellyfinRefresh) String() string { return "JellyfinRefresh(" + j.base + ")" }

// GoString keeps the key out of %#v.
func (j *JellyfinRefresh) GoString() string { return j.String() }

// NewJellyfinRefresh returns nil, nil when Jellyfin is not configured, and an
// error when its address is not one Cull can use.
func NewJellyfinRefresh(s *Store, cfg JellyfinConfig) (*JellyfinRefresh, error) {
	if strings.TrimSpace(cfg.URL) == "" || strings.TrimSpace(cfg.Key) == "" {
		return nil, nil
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(cfg.URL), "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("JELLYFIN_URL must be a plain http or https address such as http://jellyfin.local:8096")
	}
	client := &http.Client{
		Timeout: jellyfinRequestTimeout,
		// The key travels in a header, which Go would carry across a redirect
		// to another host, so a redirect is refused rather than followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &JellyfinRefresh{s: s, base: base.String(), key: strings.TrimSpace(cfg.Key), client: client, now: time.Now, sleep: sleepFor}, nil
}

// libraryGeneration changes whenever a file arrives in or leaves the library,
// through the catalogue or through the Bin.
func (s *Store) libraryGeneration(ctx context.Context) (string, error) {
	var catalogue, files int64
	err := s.read.QueryRowContext(ctx, "SELECT (SELECT value FROM catalogue_generation WHERE id=1),(SELECT value FROM file_state_generation WHERE id=1)").Scan(&catalogue, &files)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(catalogue, 10) + "/" + strconv.FormatInt(files, 10), nil
}

func (s *Store) jellyfinState(ctx context.Context) (jellyfinState, bool) {
	var state jellyfinState
	var value string
	if err := s.read.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", jellyfinSetting).Scan(&value); err != nil {
		return state, false
	}
	return state, json.Unmarshal([]byte(value), &state) == nil
}

func (s *Store) saveJellyfinState(ctx context.Context, state jellyfinState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.write.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", jellyfinSetting, string(body))
	return err
}

// JellyfinStatus is what the Addons page says about Jellyfin.
func (s *Store) JellyfinStatus(ctx context.Context) (lastAsked time.Time, problem string) {
	state, ok := s.jellyfinState(ctx)
	if !ok {
		return time.Time{}, ""
	}
	if state.At > 0 {
		lastAsked = time.Unix(state.At, 0)
	}
	return lastAsked, state.Error
}

// Run asks Jellyfin to scan whenever the library has changed since it was
// last asked, until ctx ends. The first time it runs it asks once, which also
// shows on the Addons page whether the address and key work.
func (j *JellyfinRefresh) Run(ctx context.Context) {
	for {
		if !j.pass(ctx) {
			return
		}
	}
}

// pass waits for a change, lets it settle, and sends one request. It reports
// false once ctx has ended.
func (j *JellyfinRefresh) pass(ctx context.Context) bool {
	state, _ := j.s.jellyfinState(ctx)
	if state.Failures > 0 {
		wait := min(jellyfinFirstRetry<<min(state.Failures-1, 10), jellyfinLastRetry)
		if due := time.Unix(state.FailedAt, 0).Add(wait); j.now().Before(due) && !j.sleep(ctx, due.Sub(j.now())) {
			return false
		}
	}
	current, err := j.s.libraryGeneration(ctx)
	for err != nil || current == state.Seen {
		if err != nil && ctx.Err() == nil {
			log.Printf("jellyfin: catalogue unavailable: %v", err)
		}
		if !j.sleep(ctx, jellyfinPoll) {
			return false
		}
		current, err = j.s.libraryGeneration(ctx)
	}
	// Wait for the library to hold still, so one Empty Bin is one scan.
	started, still := j.now(), j.now()
	for j.now().Sub(still) < jellyfinSettle && j.now().Sub(started) < jellyfinLongestWait {
		if !j.sleep(ctx, jellyfinPoll) {
			return false
		}
		if next, err := j.s.libraryGeneration(ctx); err == nil && next != current {
			current, still = next, j.now()
		}
	}
	if err = j.refresh(ctx); err != nil {
		if ctx.Err() != nil {
			return false
		}
		message := j.redact(err.Error())
		log.Printf("jellyfin: %s", message)
		state.Error, state.Failures, state.FailedAt = message, state.Failures+1, j.now().Unix()
	} else {
		state = jellyfinState{Seen: current, At: j.now().Unix()}
	}
	if err = j.s.saveJellyfinState(ctx, state); err != nil && ctx.Err() == nil {
		log.Printf("jellyfin: could not remember the last scan: %v", err)
	}
	return ctx.Err() == nil
}

// sleepFor waits d, and reports false if ctx ends first.
func sleepFor(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// refresh asks Jellyfin to scan every library. Its errors describe the request
// and Jellyfin's status, never the key or the body of the answer.
func (j *JellyfinRefresh) refresh(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, j.base+"/Library/Refresh", nil)
	if err != nil {
		return err
	}
	// Jellyfin 12 takes a key only in this header: X-Emby-Token and ?api_key=
	// are refused.
	request.Header.Set("Authorization", `MediaBrowser Token="`+j.key+`"`)
	response, err := j.client.Do(request)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("Jellyfin unreachable: %v", err)
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, jellyfinMaxResponse))
	switch {
	case response.StatusCode >= 200 && response.StatusCode <= 299:
		return nil
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return fmt.Errorf("Jellyfin refused the API key (%d)", response.StatusCode)
	default:
		return fmt.Errorf("Jellyfin answered the request to scan with %d", response.StatusCode)
	}
}

// redact is a last guard for the key, in case some lower layer ever repeats
// a request header in an error.
func (j *JellyfinRefresh) redact(message string) string {
	return strings.ReplaceAll(message, j.key, "[redacted]")
}
