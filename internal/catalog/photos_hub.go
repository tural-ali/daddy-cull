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
	thumbs           [][]byte
	thumbBytes       int
	selected         []string
	skipped          int
	outcomes         map[string]photosOutcome
	result           *PhotosResult
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
	now := h.now()
	j := &photosJob{
		id: newPhotosJobID(), state: "queued_check", created: now, updated: now,
		byID: map[string]int{}, held: plan.Held, undated: plan.Undated,
		matches: map[string]photosMatch{}, reported: map[string]bool{}, outcomes: map[string]photosOutcome{},
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
	JobID      string            `json:"jobId"`
	Task       string            `json:"task"`
	Entries    []PhotosTaskEntry `json:"entries,omitempty"`
	Favourites []PhotosTaskApply `json:"favourites,omitempty"`
	Deletes    []PhotosTaskApply `json:"deletes,omitempty"`
}

// PhotosTaskEntry is one thing to find in Photos.
type PhotosTaskEntry struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Name   string `json:"name"`
	Stem   string `json:"stem"`
	Ext    string `json:"ext"`
	Day    string `json:"day"`
}

// PhotosTaskApply is one change, naming the exact Photos assets the check
// found, by PhotoKit local identifier.
type PhotosTaskApply struct {
	ID     string   `json:"id"`
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
	Version string `json:"version"`
	Access  string `json:"access"`
	Job     string `json:"job,omitempty"`
	Stage   string `json:"stage,omitempty"`
	Message string `json:"message,omitempty"`
	Done    int    `json:"done,omitempty"`
	Total   int    `json:"total,omitempty"`
}

// PhotosMatchReport carries the matches for some of a check's entries. The
// helper sends them in several small reports, so that no single request has
// to carry every thumbnail at once.
type PhotosMatchReport struct {
	Matches []PhotosReportedMatch `json:"matches"`
	Missing []string              `json:"missing"`
}

// PhotosReportedMatch is one entry and the Photos assets that answer to it.
type PhotosReportedMatch struct {
	ID     string                `json:"id"`
	How    string                `json:"how"`
	Photos []PhotosReportedAsset `json:"photos"`
}

// PhotosReportedAsset is one Photos asset, with its thumbnail as a JPEG.
type PhotosReportedAsset struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Created   string `json:"created"`
	Favourite bool   `json:"favourite"`
	Thumb     []byte `json:"thumb"`
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
	for _, id := range report.Missing {
		if _, ok := j.byID[id]; !ok {
			return ErrInvalid
		}
	}
	for _, match := range report.Matches {
		h.dropThumbsLocked(j, match.ID)
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
	}
	for _, id := range report.Missing {
		h.dropThumbsLocked(j, id)
		delete(j.matches, id)
		j.reported[id] = true
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
	Favourites []PhotosAppliedItem `json:"favourites"`
	Deletes    []PhotosAppliedItem `json:"deletes"`
	Note       string              `json:"note"`
}

// PhotosAppliedItem is the outcome for one entry.
type PhotosAppliedItem struct {
	ID    string `json:"id"`
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

// PhotosResult sums up a finished job.
type PhotosResult struct {
	Nothing         bool   `json:"nothing,omitempty"`
	Deleted         int    `json:"deleted"`
	NotDeleted      int    `json:"notDeleted"`
	Favourited      int    `json:"favourited"`
	FavouriteFailed int    `json:"favouriteFailed"`
	Note            string `json:"note,omitempty"`
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

// PhotosAgentView is how the page describes the helper.
type PhotosAgentView struct {
	Online   bool   `json:"online"`
	LastSeen string `json:"lastSeen,omitempty"`
	Version  string `json:"version,omitempty"`
	Access   string `json:"access,omitempty"`
}

// PhotosJobSummary is the part of a job the page polls for; the full view is
// only fetched again when Rev changes.
type PhotosJobSummary struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Rev     int    `json:"rev"`
	Stage   string `json:"stage,omitempty"`
	Message string `json:"message,omitempty"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Error   string `json:"error,omitempty"`
}

// PhotosStatus is the light status the page polls.
type PhotosStatus struct {
	Configured bool `json:"configured"`
	// Settling is true while a helper that is running could simply not have
	// called in yet since this process started, so the page waits before it
	// offers to set Cull Sync up.
	Settling bool              `json:"settling,omitempty"`
	Now      string            `json:"now"`
	Agent    PhotosAgentView   `json:"agent"`
	Job      *PhotosJobSummary `json:"job"`
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
	}
	if j := h.job; j != nil {
		status.Job = &PhotosJobSummary{ID: j.id, State: j.state, Rev: j.rev, Stage: j.stage, Message: j.message, Done: j.done, Total: j.total, Error: j.err}
	}
	return status
}

// PhotosJobView is the whole job as the page draws it.
type PhotosJobView struct {
	PhotosJobSummary
	Created   string          `json:"created"`
	Updated   string          `json:"updated"`
	ToCheck   int             `json:"toCheck"`
	Delete    []PhotosRow     `json:"delete"`
	Favourite []PhotosRow     `json:"favourite"`
	Missing   []PhotosMissing `json:"missing"`
	Held      []PhotosHeld    `json:"held"`
	Undated   int             `json:"undated"`
	Selected  []string        `json:"selected"`
	Skipped   int             `json:"skipped"`
	Result    *PhotosResult   `json:"result,omitempty"`
}

// PhotosRow is an entry with what Photos answered and what happened to it.
type PhotosRow struct {
	PhotosEntry
	How          string            `json:"how"`
	Photos       []PhotosAssetView `json:"photos"`
	Outcome      string            `json:"outcome,omitempty"`
	OutcomeError string            `json:"outcomeError,omitempty"`
}

// PhotosAssetView is one matched Photos asset.
type PhotosAssetView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Created   string `json:"created"`
	Favourite bool   `json:"favourite"`
	Thumb     string `json:"thumb,omitempty"`
}

// PhotosMissing is an entry Photos does not hold.
type PhotosMissing struct {
	Action string `json:"action"`
	Name   string `json:"name"`
	Day    string `json:"day"`
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
		Held: j.held, Undated: j.undated, Selected: append([]string{}, j.selected...), Skipped: j.skipped, Result: j.result,
	}
	if view.Held == nil {
		view.Held = []PhotosHeld{}
	}
	if j.state == "queued_check" || j.state == "checking" {
		return view
	}
	for _, entry := range j.entries {
		match, found := j.matches[entry.ID]
		if !found {
			if j.reported[entry.ID] || j.checked {
				view.Missing = append(view.Missing, PhotosMissing{Action: entry.Action, Name: entry.Name, Day: entry.Day})
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
