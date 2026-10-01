package catalog

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Photos library lives on a Mac and a browser cannot reach it, so the work
// is done by Cull Sync, a small helper app on the Mac. The helper connects out
// to this process and asks for work, which is what lets the button work from
// any device on the tailnet, a phone included, whenever the Mac is awake.
//
// A sync is one job that moves through these states:
//
//	queued_check  the page asked for a check; waiting for the helper to take it
//	checking      the helper is matching every entry in the Photos library
//	planned       the matches are back and the page shows them; nothing changed
//	queued_apply  the reviewer chose what to change; waiting for the helper
//	applying      the helper is changing Photos; macOS asks on the Mac itself
//	done          the helper reported what really happened, and it is recorded
//	failed        something went wrong; the message says whether Photos changed
//	cancelled     the reviewer stopped it before anything was changed
//
// There is one job at a time and it lives only in this process's memory, with
// the Photos thumbnails the helper sent. A restart loses a job in flight, which
// is harmless: what was actually changed is written to photos_sync the moment
// the helper reports it, and a new check starts from that.
const (
	photosPollWait = 25 * time.Second
	// The helper polls at least every 25 seconds and sends a heartbeat every
	// 15, so 45 seconds without either means it is not there.
	photosOnlineWindow = 45 * time.Second
	// A check that hears nothing for this long has stopped. Nothing was
	// changed, so it is simply failed.
	photosCheckSilence = 90 * time.Second
	// Applying waits on a macOS dialog, which the helper keeps heart-beating
	// through; silence this long means the helper or the Mac went away.
	photosApplySilence = 3 * time.Minute
	// A change nobody picks up is dropped rather than left to run whenever
	// the Mac next wakes, perhaps days later, with nobody watching.
	photosApplyPickup = 10 * time.Minute
	// Photos can change underneath an old check, so one this old must be run
	// again before it can be applied.
	photosPlanLifetime = time.Hour
	// Photos is checked again by itself this often while the helper is
	// online, so what the page shows is never older than this and a plan is
	// renewed before it grows too old to apply. A plan is only replaced once
	// it is this old, so a reviewer part way through one is left alone.
	photosAutoCheckEvery = 30 * time.Minute
	// How often the server looks whether a check is due.
	photosAutoCheckLook = time.Minute
	// Thumbnails are freed this long after a job ends.
	photosThumbLifetime = time.Hour
	// A 240 pixel JPEG is 10 to 40 KB; anything much larger is not one.
	photosThumbMax = 128 << 10
	// Every thumbnail of a job together, so a huge plan cannot exhaust the
	// container's memory. Past it, rows simply show no Photos thumbnail.
	photosThumbBudget = 96 << 20
	// Photos may hold a few same-named assets on one day, but never dozens.
	photosMaxAssetsPerEntry = 20
	// PhotosAgentKeyMin is the shortest shared secret the helper may use.
	PhotosAgentKeyMin = 32
)

var (
	// ErrPhotosDisabled means PHOTOS_AGENT_KEY is not set, so no helper can
	// ever take a job.
	ErrPhotosDisabled = errors.New("the Apple Photos helper is not configured on the server")
	// ErrPhotosBusy means another job is still running.
	ErrPhotosBusy = errors.New("a sync is already in progress")
	// ErrPhotosStale means the job has moved on from the state the request
	// expected, usually because another tab acted first.
	ErrPhotosStale = errors.New("this sync has moved on; reload the page")
)

// PhotosHub holds the current job and what the helper last said.
type PhotosHub struct {
	s   *Store
	now func() time.Time
	// fixedKey is PHOTOS_AGENT_KEY, kept only so a setup command can hand it
	// out; without it keys are generated and only their hash is held.
	fixedKey string
	// started lets the page tell a helper that is gone from one that has not
	// had time to call in since this process started.
	started time.Time

	mu sync.Mutex
	// keyHash is the key the helper must present; enabled says there is one.
	// Both change when a setup command hands out a new key.
	keyHash [sha256.Size]byte
	enabled bool
	job     *photosJob
	agent   photosAgentSeen
	// agentKeyed is when a request last arrived with the right key.
	agentKeyed time.Time
	changed    chan struct{}
	setups     []*photosSetup
	helper     fs.FS

	rotating    sync.Mutex
	payloadOnce sync.Once
	payload     []byte
	payloadSum  string
	payloadErr  error
}

type photosAgentSeen struct {
	at              time.Time
	version, access string
}

type photosJob struct {
	id               string
	state            string
	created, updated time.Time
	finished         time.Time
	rev              int
	stage, message   string
	done, total      int
	err              string
	entries          []PhotosEntry
	byID             map[string]int
	held             []PhotosHeld
	undated          int
	matches          map[string]photosMatch
	reported         map[string]bool
	// why says, for an entry Photos does not hold, what the helper saw
	// instead: photosWhySharedAlbum or photosWhyOtherDay, or nothing at all.
	why map[string]string
	// nearHeld marks entries Photos found only a day off where the archive
	// still holds a file of that name on the day either side: the match is
	// that file's photograph, so it is held, not offered.
	nearHeld   map[string]bool
	thumbs     [][]byte
	thumbBytes int
	selected   []string
	skipped    int
	outcomes   map[string]photosOutcome
	result     *PhotosResult
	// checked is set once the helper has said it looked at every entry, so an
	// entry it never mentioned can be called not in Photos.
	checked bool
	// wasApplying marks a job failed for silence while it was changing Photos,
	// so a late report of what the helper really did is still recorded.
	wasApplying bool
}

type photosMatch struct {
	how    string
	assets []photosAsset
}

type photosAsset struct {
	id, name, created string
	favourite         bool
	thumb             int
}

type photosOutcome struct {
	status, err string
}

// NewPhotosHub returns a hub. A valid key (see ValidPhotosAgentKey) is the
// only one ever accepted. Otherwise the key a setup command last handed out is
// accepted, if there was one; until then the helper endpoints refuse
// everything and the page offers to set Cull Sync up, but the rest of the app
// is unaffected.
func NewPhotosHub(s *Store, key string) *PhotosHub {
	h := &PhotosHub{s: s, now: time.Now, started: time.Now(), changed: make(chan struct{})}
	if validPhotosKey(key) {
		h.fixedKey, h.enabled = key, true
		h.keyHash = sha256.Sum256([]byte(key))
	} else if hash, ok := s.loadPhotosKeyHash(); ok {
		h.keyHash, h.enabled = hash, true
	}
	return h
}

// Enabled reports whether there is a key a helper can connect with.
func (h *PhotosHub) Enabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enabled
}

// String keeps the key and its hash out of anything that prints the hub.
func (h *PhotosHub) String() string { return "PhotosHub" }

// GoString does the same for %#v.
func (h *PhotosHub) GoString() string { return h.String() }

// authorised compares the presented key in constant time. Both sides are
// hashed first so the comparison does not even leak the key's length.
func (h *PhotosHub) authorised(presented string) bool {
	if presented == "" {
		return false
	}
	got := sha256.Sum256([]byte(presented))
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.enabled || subtle.ConstantTimeCompare(got[:], h.keyHash[:]) != 1 {
		return false
	}
	h.agentKeyed = h.now()
	return true
}

// bumpLocked wakes every long poll waiting for a change.
func (h *PhotosHub) bumpLocked() {
	close(h.changed)
	h.changed = make(chan struct{})
}

func (j *photosJob) terminal() bool {
	return j.state == "done" || j.state == "failed" || j.state == "cancelled"
}

func (h *PhotosHub) setStateLocked(j *photosJob, state string) {
	j.state = state
	j.updated = h.now()
	j.rev++
	// Progress belongs to the stage the helper is working through. Once it is
	// not working, the last count would only read as a stalled progress bar.
	if state != "checking" && state != "applying" {
		j.stage, j.message, j.done, j.total = "", "", 0, 0
	}
	if j.terminal() {
		j.finished = j.updated
	}
	h.bumpLocked()
}

func (h *PhotosHub) failLocked(j *photosJob, message string) {
	j.err = message
	h.setStateLocked(j, "failed")
}

// tickLocked applies the time limits. It runs on every request rather than on a
// timer, because nothing can observe a job except through a request.
func (h *PhotosHub) tickLocked() {
	j := h.job
	if j == nil {
		return
	}
	now := h.now()
	switch j.state {
	case "queued_apply":
		if now.Sub(j.updated) > photosApplyPickup {
			h.failLocked(j, "The Mac did not pick this up within 10 minutes, so nothing was changed in Photos. Check again when the Mac is awake.")
		}
	case "planned":
		if now.Sub(j.updated) > photosPlanLifetime {
			h.failLocked(j, "This check is more than an hour old, and Photos may have changed since. Check again before applying.")
		}
	case "checking":
		if now.Sub(h.agent.at) > photosCheckSilence {
			h.failLocked(j, "The Mac stopped answering during the check. Nothing was changed in Photos.")
		}
	case "applying":
		if now.Sub(h.agent.at) > photosApplySilence {
			j.wasApplying = true
			h.failLocked(j, "The Mac stopped answering while it was changing Photos. Some changes may already have been made; check again to see where things stand.")
		}
	}
	if j.terminal() && j.thumbs != nil && now.Sub(j.finished) > photosThumbLifetime {
		j.thumbs, j.thumbBytes = nil, 0
		for id, match := range j.matches {
			for i := range match.assets {
				match.assets[i].thumb = 0
			}
			j.matches[id] = match
		}
		j.rev++
	}
}

func newPhotosJobID() string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		panic(err)
	}
	return hex.EncodeToString(random)
}

// StartCheck works out what Photos should be told and queues a check for the
// helper. A plan nobody applied is replaced; a job still waiting on the helper
// or changing Photos is not.
func (h *PhotosHub) StartCheck(ctx context.Context) (PhotosJobView, error) {
	return h.startCheck(ctx, nil)
}

// startCheck is StartCheck, going ahead only while due, if given, still holds
// once the catalogue has been read.
func (h *PhotosHub) startCheck(ctx context.Context, due func() bool) (PhotosJobView, error) {
	if !h.Enabled() {
		return PhotosJobView{}, ErrPhotosDisabled
	}
	if view, busy := h.busyView(); busy {
		return view, ErrPhotosBusy
	}
	// Outside the lock: reading the catalogue takes a moment, and the helper's
	// heartbeats must not wait for it.
	plan, err := h.s.PhotosPlan(ctx)
	if err != nil {
		return PhotosJobView{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tickLocked()
	if h.job != nil && !h.job.terminal() && h.job.state != "planned" {
		return h.viewLocked(h.job), ErrPhotosBusy
	}
	if due != nil && !due() {
		return PhotosJobView{}, errPhotosNotDue
	}
	now := h.now()
	j := &photosJob{
		id: newPhotosJobID(), state: "queued_check", created: now, updated: now,
		byID: map[string]int{}, held: plan.Held, undated: plan.Undated,
		matches: map[string]photosMatch{}, reported: map[string]bool{}, why: map[string]string{}, nearHeld: map[string]bool{}, outcomes: map[string]photosOutcome{},
	}
	j.entries = append(append(j.entries, plan.Delete...), plan.Favourite...)
	for i, entry := range j.entries {
		j.byID[entry.ID] = i
	}
	h.job = j
	if len(j.entries) == 0 {
		j.result = &PhotosResult{Nothing: true}
		h.setStateLocked(j, "done")
	} else {
		h.bumpLocked()
	}
	return h.viewLocked(j), nil
}

// errPhotosNotDue says an automatic check found it was no longer needed.
var errPhotosNotDue = errors.New("no check is due")

// KeepChecking checks Photos by itself whenever a check is due, until ctx
// ends, so the page always has a recent answer and nobody has to ask for it.
func (h *PhotosHub) KeepChecking(ctx context.Context) {
	look := time.NewTicker(photosAutoCheckLook)
	defer look.Stop()
	for {
		if _, err := h.AutoCheck(ctx); err != nil && ctx.Err() == nil {
			log.Printf("Apple Photos check: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-look.C:
		}
	}
}

// AutoCheck starts a check if one is due and says whether it did. One is due
// while the helper is online and there is no job, or the last one ended, or
// its plan was made, photosAutoCheckEvery ago. A job waiting on the helper or
// changing Photos is never touched.
func (h *PhotosHub) AutoCheck(ctx context.Context) (bool, error) {
	due := func() bool {
		if !h.enabled || h.agent.at.IsZero() || h.now().Sub(h.agent.at) > photosOnlineWindow {
			return false
		}
		switch j := h.job; {
		case j == nil:
			return true
		case j.state == "planned":
			return h.now().Sub(j.created) >= photosAutoCheckEvery
		case j.terminal():
			return h.now().Sub(j.finished) >= photosAutoCheckEvery
		}
		return false
	}
	h.mu.Lock()
	h.tickLocked()
	now := due()
	h.mu.Unlock()
	if !now {
		return false, nil
	}
	_, err := h.startCheck(ctx, due)
	if errors.Is(err, errPhotosNotDue) || errors.Is(err, ErrPhotosBusy) {
		return false, nil
	}
	return err == nil, err
}

func (h *PhotosHub) busyView() (PhotosJobView, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tickLocked()
	if h.job != nil && !h.job.terminal() && h.job.state != "planned" {
		return h.viewLocked(h.job), true
	}
	return PhotosJobView{}, false
}

// Apply queues the reviewer's chosen changes for the helper. The catalogue is
// read again first, and anything that has changed since the check - a file
// restored, a heart taken away - is left out rather than acted on from a stale
// plan.
func (h *PhotosHub) Apply(ctx context.Context, jobID string, deletes, favourites []string) (PhotosJobView, error) {
	h.mu.Lock()
	h.tickLocked()
	j := h.job
	if j == nil || j.id != jobID || j.state != "planned" {
		h.mu.Unlock()
		return PhotosJobView{}, ErrPhotosStale
	}
	chosen := make([]PhotosEntry, 0, len(deletes)+len(favourites))
	seen := map[string]bool{}
	for _, group := range []struct {
		action string
		ids    []string
	}{{"favourite", favourites}, {"delete", deletes}} {
		action := group.action
		for _, id := range group.ids {
			at, ok := j.byID[id]
			if !ok || seen[id] || j.entries[at].Action != action || len(j.matches[id].assets) == 0 {
				h.mu.Unlock()
				return PhotosJobView{}, ErrInvalid
			}
			seen[id] = true
			chosen = append(chosen, j.entries[at])
		}
	}
	h.mu.Unlock()
	if len(chosen) == 0 {
		return PhotosJobView{}, ErrInvalid
	}

	plan, err := h.s.PhotosPlan(ctx)
	if err != nil {
		return PhotosJobView{}, err
	}
	still := map[string]bool{}
	for _, entry := range append(append([]PhotosEntry{}, plan.Delete...), plan.Favourite...) {
		for _, key := range entry.Keys {
			still[entry.Action+"\x00"+key] = true
		}
	}
	selected := make([]string, 0, len(chosen))
	for _, entry := range chosen {
		current := true
		for _, key := range entry.Keys {
			current = current && still[entry.Action+"\x00"+key]
		}
		if current {
			selected = append(selected, entry.ID)
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.tickLocked()
	if h.job != j || j.state != "planned" {
		return PhotosJobView{}, ErrPhotosStale
	}
	if len(selected) == 0 {
		return h.viewLocked(j), fmt.Errorf("%w: everything chosen has changed in Cull since the check", ErrPhotosStale)
	}
	j.selected, j.skipped = selected, len(chosen)-len(selected)
	j.err = ""
	h.setStateLocked(j, "queued_apply")
	return h.viewLocked(j), nil
}

// Cancel stops a job before Photos is changed. Once the helper is applying,
// the only way to stop is the macOS dialog on the Mac, so it is refused.
func (h *PhotosHub) Cancel(jobID string) (PhotosJobView, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tickLocked()
	j := h.job
	if j == nil || j.id != jobID {
		return PhotosJobView{}, ErrPhotosStale
	}
	switch j.state {
	case "queued_check", "checking", "planned", "queued_apply":
		h.setStateLocked(j, "cancelled")
		return h.viewLocked(j), nil
	}
	return h.viewLocked(j), ErrPhotosStale
}

// PhotosTask is what the helper is asked to do.
type PhotosTask struct {
	// JobID is the job's id, which every report about it names in its path.
	JobID string `json:"jobId"`
	// Task is check, to find Entries in Photos and change nothing, or apply,
	// to carry out Favourites and Deletes.
	Task string `json:"task"`
	// Entries is what a check must find in Photos; omitted for an apply.
	Entries []PhotosTaskEntry `json:"entries,omitempty"`
	// Favourites is what an apply must mark as a favourite, possibly none;
	// omitted for a check.
	Favourites []PhotosTaskApply `json:"favourites,omitempty"`
	// Deletes is what an apply must delete, in one request macOS asks about,
	// possibly none; omitted for a check.
	Deletes []PhotosTaskApply `json:"deletes,omitempty"`
}

// PhotosTaskEntry is one thing to find in Photos.
type PhotosTaskEntry struct {
	// ID names the entry in every report about it.
	ID string `json:"id"`
	// Action is delete or favourite.
	Action string `json:"action"`
	// Name is the filename Photos knows the file by.
	Name string `json:"name"`
	// Stem is Name without its extension, with the archive's duplicate
	// suffixes such as " (2)" already removed, for matching on.
	Stem string `json:"stem"`
	// Ext is the extension in lower case, without the dot; empty when there
	// is none.
	Ext string `json:"ext"`
	// Day is the day the photograph was taken, as YYYY-MM-DD.
	Day string `json:"day"`
}

// PhotosTaskApply is one change, naming the exact Photos assets the check
// found, by PhotoKit local identifier.
type PhotosTaskApply struct {
	// ID is the entry's id, from the check.
	ID string `json:"id"`
	// Photos lists the PhotoKit local identifiers of every asset the check
	// matched to the entry; all of them are changed.
	Photos []string `json:"photos"`
}

// Seen records a heartbeat and returns whether the helper should abandon the
// job it is working on, because the reviewer cancelled it or it was replaced.
func (h *PhotosHub) Seen(beat PhotosHeartbeat) (cancel bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agent.at = h.now()
	h.agent.version, h.agent.access = clip(beat.Version, 40), clip(beat.Access, 40)
	h.tickLocked()
	if beat.Job == "" {
		return false
	}
	j := h.job
	if j == nil || j.id != beat.Job || (j.state != "checking" && j.state != "applying") {
		return true
	}
	j.stage, j.message = clip(beat.Stage, 40), clip(beat.Message, 200)
	j.done, j.total = max(beat.Done, 0), max(beat.Total, 0)
	return false
}

// Claim waits up to photosPollWait for work and hands it to the helper, moving
// the job on so no second helper takes it too.
func (h *PhotosHub) Claim(ctx context.Context) (*PhotosTask, error) {
	deadline := time.NewTimer(photosPollWait)
	defer deadline.Stop()
	for {
		h.mu.Lock()
		h.agent.at = h.now()
		h.tickLocked()
		task := h.claimLocked()
		wait := h.changed
		h.mu.Unlock()
		if task != nil {
			return task, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, nil
		case <-wait:
		}
	}
}

func (h *PhotosHub) claimLocked() *PhotosTask {
	j := h.job
	if j == nil {
		return nil
	}
	switch j.state {
	case "queued_check":
		task := &PhotosTask{JobID: j.id, Task: "check", Entries: make([]PhotosTaskEntry, 0, len(j.entries))}
		for _, entry := range j.entries {
			task.Entries = append(task.Entries, PhotosTaskEntry{ID: entry.ID, Action: entry.Action, Name: entry.Name, Stem: entry.Stem, Ext: entry.Ext, Day: entry.Day})
		}
		j.stage, j.message, j.done, j.total = "reading", "", 0, 0
		h.setStateLocked(j, "checking")
		return task
	case "queued_apply":
		task := &PhotosTask{JobID: j.id, Task: "apply", Favourites: []PhotosTaskApply{}, Deletes: []PhotosTaskApply{}}
		for _, id := range j.selected {
			ids := make([]string, 0)
			for _, asset := range j.matches[id].assets {
				ids = append(ids, asset.id)
			}
			item := PhotosTaskApply{ID: id, Photos: ids}
			if j.entries[j.byID[id]].Action == "favourite" {
				task.Favourites = append(task.Favourites, item)
			} else {
				task.Deletes = append(task.Deletes, item)
			}
		}
		j.stage, j.message, j.done, j.total = "starting", "", 0, 0
		h.setStateLocked(j, "applying")
		return task
	}
	return nil
}

// PhotosHeartbeat is what the helper says about itself every few seconds.
type PhotosHeartbeat struct {
	// Version is Cull Sync's CFBundleShortVersionString, such as 1.1, or dev
	// for a build without one. Only the first 40 bytes are kept.
	Version string `json:"version"`
	// Access is what macOS allows Cull Sync to do with Photos: authorized,
	// limited, denied, restricted, notDetermined or unknown. Only the first 40
	// bytes are kept.
	Access string `json:"access"`
	// Job is the id of the job the helper is working on; empty when it is idle.
	Job string `json:"job,omitempty"`
	// Stage is the step of that job in hand. A check goes through reading,
	// shared, matching and thumbnails; an apply through starting, favourites,
	// confirm and verifying. Only the first 40 bytes are kept.
	Stage string `json:"stage,omitempty"`
	// Message is free text about the step, shown on the page. Only the first
	// 200 bytes are kept; Cull Sync itself sends none.
	Message string `json:"message,omitempty"`
	// Done is how many of Total the step has got through.
	Done int `json:"done,omitempty"`
	// Total is how many items the step has to get through; 0 when not known.
	Total int `json:"total,omitempty"`
}

// PhotosMatchReport carries the matches for some of a check's entries. The
// helper sends them in several small reports, so that no single request has
// to carry every thumbnail at once.
type PhotosMatchReport struct {
	// Matches are entries Photos holds, each with the assets that answer to it.
	Matches []PhotosReportedMatch `json:"matches"`
	// Missing lists the ids of entries Photos does not hold.
	Missing []string `json:"missing"`
	// Reasons says, for some of Missing, what Photos holds instead, so the page
	// can tell a reviewer why a file they know is in Photos was not offered.
	Reasons map[string]string `json:"reasons,omitempty"`
}

// The reasons a helper gives for not finding an entry. A photograph in a
// Shared Album is not in the library, and Cull Sync never changes an album; a
// name found only on another day is another photograph, since camera numbers
// repeat.
const (
	photosWhySharedAlbum = "shared-album"
	photosWhyOtherDay    = "other-day"
)

// PhotosReportedMatch is one entry and the Photos assets that answer to it.
type PhotosReportedMatch struct {
	// ID is the entry's id, from the task.
	ID string `json:"id"`
	// How is exact when name, type and day all agree, or near when the asset
	// is the only one of that name and type in the library and was taken a
	// day either side.
	How string `json:"how"`
	// Photos lists the assets that answer to the entry, 1 to 20 of them.
	Photos []PhotosReportedAsset `json:"photos"`
}

// PhotosReportedAsset is one Photos asset, with its thumbnail as a JPEG.
type PhotosReportedAsset struct {
	// ID is the asset's PhotoKit local identifier, up to 200 bytes; required.
	ID string `json:"id"`
	// Name is the asset's original filename in Photos, up to 255 bytes.
	Name string `json:"name"`
	// Created is when Photos says the asset was taken, in ISO 8601, up to 40
	// bytes; empty when Photos has no date for it.
	Created string `json:"created"`
	// Favourite is whether the asset is already a favourite in Photos.
	Favourite bool `json:"favourite"`
	// Thumb is a small JPEG of the asset, base64 encoded. One larger than
	// 128 KB, or that is not a JPEG, is dropped and the page shows none.
	Thumb []byte `json:"thumb"`
}

// Matches records part of a check's answer. A report sent twice replaces the
// first, so the helper may safely retry one whose reply it did not receive.
func (h *PhotosHub) Matches(jobID string, report PhotosMatchReport) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agent.at = h.now()
	h.tickLocked()
	j := h.job
	if j == nil || j.id != jobID || j.state != "checking" {
		return ErrPhotosStale
	}
	for _, match := range report.Matches {
		if _, ok := j.byID[match.ID]; !ok || (match.How != "exact" && match.How != "near") || len(match.Photos) == 0 || len(match.Photos) > photosMaxAssetsPerEntry {
			return ErrInvalid
		}
		for _, asset := range match.Photos {
			if asset.ID == "" || len(asset.ID) > 200 || len(asset.Name) > 255 || len(asset.Created) > 40 {
				return ErrInvalid
			}
		}
	}
	missing := make(map[string]bool, len(report.Missing))
	for _, id := range report.Missing {
		if _, ok := j.byID[id]; !ok {
			return ErrInvalid
		}
		missing[id] = true
	}
	for id, why := range report.Reasons {
		if !missing[id] || (why != photosWhySharedAlbum && why != photosWhyOtherDay) {
			return ErrInvalid
		}
	}
	for _, match := range report.Matches {
		h.dropThumbsLocked(j, match.ID)
		if entry := j.entries[j.byID[match.ID]]; match.How == "near" && entry.nearKept != "" {
			delete(j.matches, match.ID)
			delete(j.why, match.ID)
			j.reported[match.ID] = true
			j.nearHeld[match.ID] = true
			continue
		}
		delete(j.nearHeld, match.ID)
		stored := photosMatch{how: match.How}
		for _, asset := range match.Photos {
			seen := photosAsset{id: asset.ID, name: asset.Name, created: asset.Created, favourite: asset.Favourite}
			// Only something that starts like a JPEG is kept and served; the
			// page shows it as an image, so it must be one.
			if len(asset.Thumb) > 0 && len(asset.Thumb) <= photosThumbMax && bytes.HasPrefix(asset.Thumb, []byte{0xff, 0xd8, 0xff}) && j.thumbBytes+len(asset.Thumb) <= photosThumbBudget {
				j.thumbs = append(j.thumbs, asset.Thumb)
				j.thumbBytes += len(asset.Thumb)
				seen.thumb = len(j.thumbs)
			}
			stored.assets = append(stored.assets, seen)
		}
		j.matches[match.ID] = stored
		j.reported[match.ID] = true
		delete(j.why, match.ID)
	}
	for _, id := range report.Missing {
		h.dropThumbsLocked(j, id)
		delete(j.matches, id)
		delete(j.nearHeld, id)
		j.reported[id] = true
		if why := report.Reasons[id]; why != "" {
			j.why[id] = why
		} else {
			delete(j.why, id)
		}
	}
	j.rev++
	return nil
}

func (h *PhotosHub) dropThumbsLocked(j *photosJob, id string) {
	for _, asset := range j.matches[id].assets {
		if asset.thumb > 0 && asset.thumb <= len(j.thumbs) {
			j.thumbBytes -= len(j.thumbs[asset.thumb-1])
			j.thumbs[asset.thumb-1] = nil
		}
	}
}

// Checked ends a check. Entries the helper never mentioned are treated as not
// found in Photos, which leaves them alone.
func (h *PhotosHub) Checked(jobID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agent.at = h.now()
	h.tickLocked()
	j := h.job
	if j == nil || j.id != jobID {
		return ErrPhotosStale
	}
	if j.state == "planned" {
		return nil
	}
	if j.state != "checking" {
		return ErrPhotosStale
	}
	j.checked = true
	h.setStateLocked(j, "planned")
	return nil
}

// Failed records the helper's own report that it could not go on.
func (h *PhotosHub) Failed(jobID, reason string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.agent.at = h.now()
	h.tickLocked()
	j := h.job
	if j == nil || j.id != jobID || (j.state != "checking" && j.state != "applying") {
		return ErrPhotosStale
	}
	reason = clip(strings.TrimSpace(reason), 300)
	if reason == "" {
		reason = "no reason given"
	}
	j.wasApplying = j.state == "applying"
	h.failLocked(j, "The Mac could not finish: "+reason)
	return nil
}

// PhotosAppliedReport is what the helper found after changing Photos. Deleted
// means the helper looked each asset up again afterwards and Photos no longer
// has it: a deletion cancelled at the macOS dialog can still come back as a
// success, so the call's own answer is not taken as proof.
type PhotosAppliedReport struct {
	// Favourites is the outcome of each favourite the task asked for. Each
	// entry may appear once, and only entries the task named are accepted.
	Favourites []PhotosAppliedItem `json:"favourites"`
	// Deletes is the outcome of each deletion the task asked for, on the same
	// terms as Favourites.
	Deletes []PhotosAppliedItem `json:"deletes"`
	// Note is anything the helper has to say about the apply as a whole, such
	// as the deletion being declined on the Mac; empty when there is nothing.
	// Only the first 300 bytes are kept.
	Note string `json:"note"`
}

// PhotosAppliedItem is the outcome for one entry.
type PhotosAppliedItem struct {
	// ID is the entry's id, from the task.
	ID string `json:"id"`
	// Done is true when Photos was seen to change: for a deletion, Photos no
	// longer returns any of the entry's assets; for a favourite, every one of
	// them reads back as a favourite. Only done entries are recorded as synced.
	Done bool `json:"done"`
	// Error says why an entry is not done, such as "Still in Photos."; empty
	// when it is. Only the first 200 bytes are kept.
	Error string `json:"error"`
}

// PhotosResult sums up a finished job.
type PhotosResult struct {
	// Nothing is true when the check had nothing to find, so the Mac was never
	// asked; the counts are then all 0.
	Nothing bool `json:"nothing,omitempty"`
	// Deleted counts the chosen deletions Photos was seen to carry out.
	Deleted int `json:"deleted"`
	// NotDeleted counts the chosen deletions Photos did not carry out, or not
	// for every copy.
	NotDeleted int `json:"notDeleted"`
	// Favourited counts the chosen favourites Photos was seen to set.
	Favourited int `json:"favourited"`
	// FavouriteFailed counts the chosen favourites Photos did not keep.
	FavouriteFailed int `json:"favouriteFailed"`
	// Note is the helper's note about the apply as a whole, up to 300 bytes;
	// omitted when it gave none.
	Note string `json:"note,omitempty"`
}

// Applied records what the helper did. Only entries this job asked for are
// accepted, and only confirmed ones are written to photos_sync.
func (h *PhotosHub) Applied(ctx context.Context, jobID string, report PhotosAppliedReport) error {
	h.mu.Lock()
	h.agent.at = h.now()
	h.tickLocked()
	j := h.job
	if j == nil || j.id != jobID {
		h.mu.Unlock()
		return ErrPhotosStale
	}
	if j.state == "done" && j.result != nil && !j.result.Nothing {
		// A retry of a report already recorded.
		h.mu.Unlock()
		return nil
	}
	if j.state != "applying" && !(j.state == "failed" && j.wasApplying) {
		h.mu.Unlock()
		return ErrPhotosStale
	}
	chosen := map[string]bool{}
	for _, id := range j.selected {
		chosen[id] = true
	}
	outcomes := map[string]photosOutcome{}
	result := &PhotosResult{Note: clip(strings.TrimSpace(report.Note), 300)}
	done := make([]PhotosDone, 0)
	for action, items := range map[string][]PhotosAppliedItem{"favourite": report.Favourites, "delete": report.Deletes} {
		for _, item := range items {
			at, ok := j.byID[item.ID]
			if !ok || !chosen[item.ID] || j.entries[at].Action != action {
				h.mu.Unlock()
				return ErrInvalid
			}
			if _, twice := outcomes[item.ID]; twice {
				h.mu.Unlock()
				return ErrInvalid
			}
			entry := j.entries[at]
			outcome := photosOutcome{err: clip(strings.TrimSpace(item.Error), 200)}
			switch {
			case action == "delete" && item.Done:
				outcome.status = "deleted"
				result.Deleted++
			case action == "delete":
				outcome.status = "not-deleted"
				result.NotDeleted++
			case item.Done:
				outcome.status = "favourited"
				result.Favourited++
			default:
				outcome.status = "failed"
				result.FavouriteFailed++
			}
			outcomes[item.ID] = outcome
			if item.Done {
				ids := make([]string, 0)
				for _, asset := range j.matches[item.ID].assets {
					ids = append(ids, asset.id)
				}
				done = append(done, PhotosDone{Entry: entry, PhotosID: ids})
			}
		}
	}
	h.mu.Unlock()

	_, recordErr := h.s.RecordPhotosSync(ctx, done, h.now())

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.job != j {
		return ErrPhotosStale
	}
	j.outcomes, j.result = outcomes, result
	j.err = ""
	if recordErr != nil {
		// Photos has changed and Cull could not write that down. The next
		// check will not find the deleted assets and will leave them alone,
		// so nothing is at risk, but the reviewer should know.
		h.failLocked(j, "Photos was changed, but Cull could not record it. The next check will show the deleted photographs as not in Photos.")
		return recordErr
	}
	h.setStateLocked(j, "done")
	return nil
}

// Thumb returns one of the current job's Photos thumbnails.
func (h *PhotosHub) Thumb(jobID string, n int) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tickLocked()
	j := h.job
	if j == nil || j.id != jobID || n < 1 || n > len(j.thumbs) || j.thumbs[n-1] == nil {
		return nil, false
	}
	return j.thumbs[n-1], true
}

// PhotosAgentVersion is the Cull Sync this server was built with, the
// CFBundleShortVersionString in mac/CullSync/Info.plist. A helper reporting an
// older one still works, but misses what was added since, such as saying why
// a file was not found or matching a day off when the same number recurs
// years away, so the page offers to update it.
const PhotosAgentVersion = "1.2"

// PhotosAgentView is how the page describes the helper.
type PhotosAgentView struct {
	// Online is true when the helper has called in within the last 45
	// seconds, by polling for work, a heartbeat or a report.
	Online bool `json:"online"`
	// LastSeen is when the helper last called in, in RFC 3339 UTC; omitted
	// when it has not since this server started.
	LastSeen string `json:"lastSeen,omitempty"`
	// Version is the Cull Sync version the helper last reported, such as 1.1.
	Version string `json:"version,omitempty"`
	// Access is the Photos access macOS gives the helper, as it last reported
	// it: authorized, limited, denied, restricted, notDetermined or unknown.
	// Only authorized lets a check run.
	Access string `json:"access,omitempty"`
	// Outdated is true when the helper is older than the Cull Sync this
	// server was built with, so the page offers to update it.
	Outdated bool `json:"outdated,omitempty"`
	// Latest is the Cull Sync version this server was built with; only sent
	// when Outdated is true.
	Latest string `json:"latest,omitempty"`
}

// photosOlder reports whether dotted version a is older than b. A version
// that is not dotted numbers, such as a "dev" build, is never called older.
func photosOlder(a, b string) bool {
	parse := func(v string) ([]int, bool) {
		parts := strings.Split(v, ".")
		out := make([]int, len(parts))
		for i, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 {
				return nil, false
			}
			out[i] = n
		}
		return out, true
	}
	x, okA := parse(a)
	y, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := 0; i < max(len(x), len(y)); i++ {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			return p < q
		}
	}
	return false
}

// PhotosJobSummary is the part of a job the page polls for; the full view is
// only fetched again when Rev changes.
type PhotosJobSummary struct {
	// ID is the sync's id, 24 hex characters.
	ID string `json:"id"`
	// State is where the sync stands: queued_check, checking, planned,
	// queued_apply, applying, done, failed or cancelled.
	State string `json:"state"`
	// Rev goes up whenever anything in the sync changes, including matches
	// arriving; fetch the full view again when it does.
	Rev int `json:"rev"`
	// Stage is the step the Mac reports it is on while checking or applying:
	// reading, shared, matching or thumbnails for a check, and starting,
	// favourites, confirm or verifying for an apply. Omitted in other states.
	Stage string `json:"stage,omitempty"`
	// Message is the Mac's own words about the step, up to 200 bytes; omitted
	// when it sent none, and outside checking and applying.
	Message string `json:"message,omitempty"`
	// Done is how many of Total the step has got through; 0 outside checking
	// and applying.
	Done int `json:"done"`
	// Total is how many items the step has to get through; 0 when not known,
	// and outside checking and applying.
	Total int `json:"total"`
	// Error says, when State is failed, what went wrong and whether Photos
	// was changed; omitted otherwise.
	Error string `json:"error,omitempty"`
}

// PhotosStatus is the light status the page polls.
type PhotosStatus struct {
	// Configured is true when there is a key Cull Sync can connect with.
	Configured bool `json:"configured"`
	// Settling is true while a helper that is running could simply not have
	// called in yet since this process started, so the page waits before it
	// offers to set Cull Sync up.
	Settling bool `json:"settling,omitempty"`
	// Now is the server's time, in RFC 3339 UTC, to measure LastSeen against.
	Now string `json:"now"`
	// Agent is the helper as last heard; all empty when it has not called in
	// since this server started.
	Agent PhotosAgentView `json:"agent"`
	// Job is the sync in hand, finished or not; null when there has been none
	// since this server started.
	Job *PhotosJobSummary `json:"job"`
}

// Status is cheap enough to poll every second or two.
func (h *PhotosHub) Status() PhotosStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tickLocked()
	now := h.now()
	status := PhotosStatus{Configured: h.enabled, Now: now.UTC().Format(time.RFC3339)}
	status.Settling = h.enabled && h.agent.at.IsZero() && now.Sub(h.started) < photosOnlineWindow
	if !h.agent.at.IsZero() {
		status.Agent = PhotosAgentView{
			Online:   now.Sub(h.agent.at) <= photosOnlineWindow,
			LastSeen: h.agent.at.UTC().Format(time.RFC3339),
			Version:  h.agent.version, Access: h.agent.access,
		}
		if photosOlder(h.agent.version, PhotosAgentVersion) {
			status.Agent.Outdated, status.Agent.Latest = true, PhotosAgentVersion
		}
	}
	if j := h.job; j != nil {
		status.Job = &PhotosJobSummary{ID: j.id, State: j.state, Rev: j.rev, Stage: j.stage, Message: j.message, Done: j.done, Total: j.total, Error: j.err}
	}
	return status
}

// PhotosJobView is the whole job as the page draws it.
type PhotosJobView struct {
	PhotosJobSummary
	// Created is when the check was asked for, in RFC 3339 UTC.
	Created string `json:"created"`
	// Updated is when State last changed, in RFC 3339 UTC.
	Updated string `json:"updated"`
	// ToCheck counts the entries the Mac is asked to find in Photos,
	// deletions and favourites together.
	ToCheck int `json:"toCheck"`
	// Delete lists the deletions Photos holds a match for; empty while
	// the check is queued or running.
	Delete []PhotosRow `json:"delete"`
	// Favourite lists the favourites Photos holds a match for; empty while
	// the check is queued or running.
	Favourite []PhotosRow `json:"favourite"`
	// Missing lists the entries Photos does not hold, which are left alone;
	// empty while the check is queued or running.
	Missing []PhotosMissing `json:"missing"`
	// Held lists the removals not offered to Photos at all, because the
	// archive still holds the photograph under another file.
	Held []PhotosHeld `json:"held"`
	// Undated counts removed and favourited files skipped because they have
	// no day to find them in Photos by.
	Undated int `json:"undated"`
	// Selected lists the ids of the rows being carried out, those chosen that
	// had not changed in Cull since the check; empty until an apply.
	Selected []string `json:"selected"`
	// Skipped counts the rows chosen for an apply but left out because they
	// changed in Cull since the check.
	Skipped int `json:"skipped"`
	// Result sums up the sync once the Mac has reported what it changed, or
	// once a check found nothing to do; omitted until then.
	Result *PhotosResult `json:"result,omitempty"`
}

// PhotosRow is an entry with what Photos answered and what happened to it.
type PhotosRow struct {
	PhotosEntry
	// How is exact when name, type and day all agree, or near when the match
	// is the only one of that name and type in Photos and was taken a day
	// either side.
	How string `json:"how"`
	// Photos lists the Photos assets matched to the entry, 1 to 20 of them.
	// All of them are changed if the row is applied.
	Photos []PhotosAssetView `json:"photos"`
	// Outcome is what the apply did: deleted or not-deleted for a deletion,
	// favourited or failed for a favourite; omitted until the Mac reports it.
	Outcome string `json:"outcome,omitempty"`
	// OutcomeError is the Mac's reason for an outcome that did not succeed,
	// such as "Still in Photos."; omitted when there is none.
	OutcomeError string `json:"outcomeError,omitempty"`
}

// PhotosAssetView is one matched Photos asset.
type PhotosAssetView struct {
	// ID is the asset's PhotoKit local identifier.
	ID string `json:"id"`
	// Name is the asset's original filename in Photos.
	Name string `json:"name"`
	// Created is when Photos says the asset was taken, in ISO 8601 as the Mac
	// sent it; empty when Photos has no date for it.
	Created string `json:"created"`
	// Favourite is whether the asset was already a favourite in Photos at the
	// check.
	Favourite bool `json:"favourite"`
	// Thumb is the path of the asset's JPEG thumbnail, under
	// /api/photos/thumb/; omitted when the Mac sent none that could be kept,
	// and an hour after the sync ends.
	Thumb string `json:"thumb,omitempty"`
}

// PhotosMissing is an entry Photos does not hold.
type PhotosMissing struct {
	// Action is delete or favourite.
	Action string `json:"action"`
	// Name is the filename Photos was searched for.
	Name string `json:"name"`
	// Day is the day it was searched on, as YYYY-MM-DD.
	Day string `json:"day"`
	// Why is what Photos holds instead, when the helper saw something: see
	// photosWhySharedAlbum and photosWhyOtherDay.
	Why string `json:"why,omitempty"`
}

// Job returns the full view of a job.
func (h *PhotosHub) Job(jobID string) (PhotosJobView, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tickLocked()
	if h.job == nil || h.job.id != jobID {
		return PhotosJobView{}, false
	}
	return h.viewLocked(h.job), true
}

func (h *PhotosHub) viewLocked(j *photosJob) PhotosJobView {
	view := PhotosJobView{
		PhotosJobSummary: PhotosJobSummary{ID: j.id, State: j.state, Rev: j.rev, Stage: j.stage, Message: j.message, Done: j.done, Total: j.total, Error: j.err},
		Created:          j.created.UTC().Format(time.RFC3339), Updated: j.updated.UTC().Format(time.RFC3339),
		ToCheck: len(j.entries), Delete: []PhotosRow{}, Favourite: []PhotosRow{}, Missing: []PhotosMissing{},
		Held: append([]PhotosHeld{}, j.held...), Undated: j.undated, Selected: append([]string{}, j.selected...), Skipped: j.skipped, Result: j.result,
	}
	if j.state == "queued_check" || j.state == "checking" {
		return view
	}
	for _, entry := range j.entries {
		if j.nearHeld[entry.ID] {
			view.Held = append(view.Held, PhotosHeld{Name: entry.Name, Day: entry.Day, Kept: entry.nearKept, KeptDay: entry.nearKeptDay})
			continue
		}
		match, found := j.matches[entry.ID]
		if !found {
			if j.reported[entry.ID] || j.checked {
				view.Missing = append(view.Missing, PhotosMissing{Action: entry.Action, Name: entry.Name, Day: entry.Day, Why: j.why[entry.ID]})
			}
			continue
		}
		row := PhotosRow{PhotosEntry: entry, How: match.how, Photos: make([]PhotosAssetView, 0, len(match.assets))}
		for _, asset := range match.assets {
			seen := PhotosAssetView{ID: asset.id, Name: asset.name, Created: asset.created, Favourite: asset.favourite}
			if asset.thumb > 0 && asset.thumb <= len(j.thumbs) && j.thumbs[asset.thumb-1] != nil {
				seen.Thumb = fmt.Sprintf("/api/photos/thumb/%s/%d", j.id, asset.thumb)
			}
			row.Photos = append(row.Photos, seen)
		}
		if outcome, ok := j.outcomes[entry.ID]; ok {
			row.Outcome, row.OutcomeError = outcome.status, outcome.err
		}
		if entry.Action == "delete" {
			view.Delete = append(view.Delete, row)
		} else {
			view.Favourite = append(view.Favourite, row)
		}
	}
	return view
}

func clip(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	// Cut on a rune boundary so the page never shows half a character.
	cut := limit
	for cut > 0 && (value[cut]&0xc0) == 0x80 {
		cut--
	}
	return value[:cut]
}
