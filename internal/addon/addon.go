// Package addon is how Cull is extended. Everything beyond reviewing dates,
// finding duplicates and the Bin is an addon: the ones that come with Cull are
// compiled in, and anyone can add another as a folder holding an addon.json,
// written in any language, that works through Cull's documented API.
//
// Turning an addon off hides its pages, stops its routes and its background
// work, and refuses its key. Files it already moved to the Bin stay there and
// can be put back: the Bin belongs to Cull, not to the addon.
package addon

//go:generate go run ../apidoc/cmd/apidoc daddy-cull/next/internal/addon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"daddy-cull/next/internal/api"
)

// Sections of the sidebar an addon's page can sit in.
const (
	Collections = "collections"
	Sync        = "sync"
	Tools       = "tools"
)

// Page is a page an addon adds to the sidebar.
type Page struct {
	// ID names the page within its addon, in lower case with hyphens.
	ID string `json:"id"`
	// Label is the page's name in the sidebar.
	Label string `json:"label"`
	// Icon is a Material Symbols name from the set Cull ships; an unknown
	// one is drawn as a puzzle piece.
	Icon string `json:"icon"`
	// Section is where in the sidebar it goes: collections, sync or tools.
	Section string `json:"section"`
	// Path is where the page is in Cull. For an addon of your own it is
	// /addons/<addon>/<page> and is filled in by Cull.
	Path string `json:"path"`
	// URL is the page an addon of your own serves, which Cull shows inside
	// its frame. Cull's own addons have none.
	URL string `json:"url,omitempty"`
}

// Manifest describes an addon. An addon of your own gives it as addon.json.
type Manifest struct {
	// ID is the addon's name in lower case with hyphens. It is also the
	// name of its folder.
	ID string `json:"id"`
	// Name is how the addon is called on the Addons page, in the reference
	// and in error messages.
	Name string `json:"name"`
	// Version is the addon's own version, in any form it likes. It is
	// required, and Cull does not compare it.
	Version string `json:"version"`
	// Summary says in one sentence what it is for.
	Summary string `json:"summary"`
	// Description says more: what it adds, and what it needs.
	Description string `json:"description,omitempty"`
	// Author is who wrote the addon, or empty.
	Author string `json:"author,omitempty"`
	// Homepage is an address to read more about the addon, or empty.
	Homepage string `json:"homepage,omitempty"`
	// Icon is a Material Symbols name for the addon, as for a page's icon, or
	// empty for the puzzle piece.
	Icon string `json:"icon,omitempty"`
	// Pages are the pages the addon adds to the sidebar while it is on, if
	// any.
	Pages []Page `json:"pages,omitempty"`
	// Permissions are what the addon's key may do beyond reading: review,
	// bin, delete or settings. Cull's own addons are part of Cull and ask
	// for none.
	Permissions []string `json:"permissions,omitempty"`
	// Needs lists what it needs to work, in words.
	Needs []string `json:"needs,omitempty"`
	// Work lists what it does in the background while it is on, in words.
	Work []string `json:"work,omitempty"`
}

// Status says whether an addon has what it needs.
type Status struct {
	// State is ready, setup when it waits for something to be set up, or
	// problem when it cannot work.
	State string `json:"state"`
	// Detail says what it holds, or what it waits for, in a sentence.
	Detail string `json:"detail"`
}

// Ready, Setup and Problem are the states a Status can be in.
const (
	Ready   = "ready"
	Setup   = "setup"
	Problem = "problem"
)

// BuiltIn is an addon that comes with Cull.
type BuiltIn struct {
	Manifest
	// Status says whether it has what it needs and what it holds.
	Status func(ctx context.Context) Status
	// Default is whether it is on before anyone has turned it on or off:
	// usually whether there is anything for it to show.
	Default func(ctx context.Context) bool
}

// Store keeps whether each addon was turned on or off.
type Store interface {
	// AddonChoice says whether the addon was turned on, and whether anyone
	// ever chose.
	AddonChoice(ctx context.Context, id string) (on, chosen bool, err error)
	SetAddonChoice(ctx context.Context, id string, on bool) error
}

// View is an addon as the Addons page shows it.
type View struct {
	Manifest
	// BuiltIn is true for Cull's own addons and false for one of your own.
	BuiltIn bool `json:"builtIn"`
	// On is whether the addon is on now, by choice or by default. An addon of
	// your own with a problem is never on.
	On bool `json:"on"`
	// Chosen says someone turned it on or off, rather than it being on or
	// off by default.
	Chosen bool `json:"chosen"`
	// Status says whether it has what it needs, and what it holds or waits
	// for.
	Status Status `json:"status"`
	// Routes counts its API routes, which the API reference lists.
	Routes int `json:"routes"`
	// Folder is where an addon of your own lives, relative to the addons
	// folder; its key is written there as "key" when it is turned on.
	Folder string `json:"folder,omitempty"`
	// Problem says why an addon of your own cannot be loaded.
	Problem string `json:"problem,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,39}$`)

// ErrUnknown is returned for an addon id nothing answers to.
var ErrUnknown = errors.New("no such addon")

// ManifestFile and KeyFile are the files in an addon of your own's folder.
const (
	ManifestFile = "addon.json"
	KeyFile      = "key"
)

type external struct {
	manifest Manifest
	folder   string
	problem  string
	keyHash  [32]byte
	hasKey   bool
}

// Registry knows every addon and whether it is on.
type Registry struct {
	builtIns []BuiltIn
	dir      string
	store    Store
	book     *api.Book

	mu        sync.Mutex
	externals map[string]*external
	scanned   time.Time
	// on caches each addon's state briefly, since every request to an
	// addon's route asks.
	on      map[string]cached
	changed chan struct{}
}

type cached struct {
	on, chosen bool
	at         time.Time
}

const cacheFor = 5 * time.Second

// NewRegistry knows the built-in addons given, and the addons of your own in
// dir, which may be empty for none.
func NewRegistry(store Store, book *api.Book, dir string, builtIns ...BuiltIn) (*Registry, error) {
	seen := map[string]bool{}
	for _, b := range builtIns {
		if err := validManifest(b.Manifest, true); err != nil {
			return nil, err
		}
		if seen[b.ID] {
			return nil, fmt.Errorf("addon %s is registered twice", b.ID)
		}
		seen[b.ID] = true
		book.AddonName(b.ID, b.Name)
	}
	r := &Registry{builtIns: builtIns, dir: dir, store: store, book: book, externals: map[string]*external{}, on: map[string]cached{}, changed: make(chan struct{})}
	r.scan(true)
	return r, nil
}

func (r *Registry) builtIn(id string) (BuiltIn, bool) {
	for _, b := range r.builtIns {
		if b.ID == id {
			return b, true
		}
	}
	return BuiltIn{}, false
}

func validManifest(m Manifest, builtIn bool) error {
	if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("id %q should be 2 to 40 lower-case letters, digits and hyphens", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Summary) == "" || strings.TrimSpace(m.Version) == "" {
		return errors.New("name, version and summary are required")
	}
	pageIDs := map[string]bool{}
	for _, p := range m.Pages {
		if !idPattern.MatchString(p.ID) || pageIDs[p.ID] {
			return fmt.Errorf("page id %q should be unique, lower-case letters, digits and hyphens", p.ID)
		}
		pageIDs[p.ID] = true
		if strings.TrimSpace(p.Label) == "" {
			return fmt.Errorf("page %s needs a label", p.ID)
		}
		if p.Section != Collections && p.Section != Sync && p.Section != Tools {
			return fmt.Errorf("page %s: section should be collections, sync or tools", p.ID)
		}
		if builtIn {
			if !strings.HasPrefix(p.Path, "/") {
				return fmt.Errorf("page %s needs a path", p.ID)
			}
			continue
		}
		u, err := url.Parse(p.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("page %s: url should be the http or https address the addon serves it at", p.ID)
		}
	}
	for _, p := range m.Permissions {
		if builtIn {
			return errors.New("Cull's own addons are part of Cull and ask for no permissions")
		}
		if p == api.Read || !api.ValidPermission(p) {
			return fmt.Errorf("permission %q should be one of review, bin, delete or settings; every addon may read", p)
		}
	}
	return nil
}

// scan reads the addons folder again, at most once a second unless forced,
// so a folder dropped in shows without a restart.
func (r *Registry) scan(force bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dir == "" || (!force && time.Since(r.scanned) < time.Second) {
		return
	}
	r.scanned = time.Now()
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		r.externals = map[string]*external{}
		return
	}
	found := map[string]*external{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		folder := entry.Name()
		e := &external{folder: folder, manifest: Manifest{ID: folder, Name: folder}}
		found[folder] = e
		raw, err := os.ReadFile(filepath.Join(r.dir, folder, ManifestFile))
		if err != nil {
			e.problem = "The folder has no readable " + ManifestFile + "."
			continue
		}
		var m Manifest
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&m); err != nil {
			e.problem = ManifestFile + " could not be read: " + err.Error()
			continue
		}
		if err := validManifest(m, false); err != nil {
			e.problem = ManifestFile + ": " + err.Error()
			continue
		}
		if m.ID != folder {
			e.problem = fmt.Sprintf("%s names the addon %q, but its folder is %q; they should match.", ManifestFile, m.ID, folder)
			continue
		}
		if _, clash := r.builtIn(m.ID); clash {
			e.problem = fmt.Sprintf("%q is the name of one of Cull's own addons.", m.ID)
			continue
		}
		for i := range m.Pages {
			m.Pages[i].Path = "/addons/" + m.ID + "/" + m.Pages[i].ID
		}
		e.manifest = m
		if key, err := os.ReadFile(filepath.Join(r.dir, folder, KeyFile)); err == nil && len(strings.TrimSpace(string(key))) >= 32 {
			e.keyHash = sha256.Sum256([]byte(strings.TrimSpace(string(key))))
			e.hasKey = true
		}
	}
	r.externals = found
}

// state says whether an addon is on, from the cache when it is fresh.
func (r *Registry) state(ctx context.Context, id string) (on, chosen bool) {
	r.mu.Lock()
	c, ok := r.on[id]
	r.mu.Unlock()
	if ok && time.Since(c.at) < cacheFor {
		return c.on, c.chosen
	}
	on, chosen, err := r.store.AddonChoice(ctx, id)
	if err != nil {
		// An unreadable setting leaves an addon as it was last seen, or off.
		return c.on, c.chosen
	}
	if !chosen {
		if b, ok := r.builtIn(id); ok && b.Default != nil {
			on = b.Default(ctx)
		} else {
			on = false
		}
	}
	r.mu.Lock()
	r.on[id] = cached{on: on, chosen: chosen, at: time.Now()}
	r.mu.Unlock()
	return on, chosen
}

// On reports whether an addon is on.
func (r *Registry) On(ctx context.Context, id string) bool {
	on, _ := r.state(ctx, id)
	return on
}

// Set turns an addon on or off. An addon of your own is given its key the
// first time it is turned on.
func (r *Registry) Set(ctx context.Context, id string, on bool) error {
	if _, ok := r.builtIn(id); !ok {
		r.scan(true)
		r.mu.Lock()
		e := r.externals[id]
		r.mu.Unlock()
		if e == nil {
			return ErrUnknown
		}
		if on && e.problem != "" {
			return fmt.Errorf("%w: %s", ErrBroken, e.problem)
		}
		if on && !e.hasKey {
			if err := r.writeKey(e); err != nil {
				return err
			}
		}
	}
	if err := r.store.SetAddonChoice(ctx, id, on); err != nil {
		return err
	}
	r.mu.Lock()
	r.on[id] = cached{on: on, chosen: true, at: time.Now()}
	close(r.changed)
	r.changed = make(chan struct{})
	r.mu.Unlock()
	return nil
}

// ErrBroken is returned for turning on an addon of your own that cannot be
// loaded.
var ErrBroken = errors.New("the addon cannot be loaded")

func (r *Registry) writeKey(e *external) error {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	key := "cull_" + hex.EncodeToString(secret)
	path := filepath.Join(r.dir, e.folder, KeyFile)
	// Written beside its manifest, readable by its owner only, and never
	// shown on a page or in a log.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("the key could not be written to the addon's folder: %w", err)
	}
	if _, err := file.WriteString(key + "\n"); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	r.mu.Lock()
	e.keyHash = sha256.Sum256([]byte(key))
	e.hasKey = true
	r.mu.Unlock()
	return nil
}

// List is every addon, Cull's own first, then yours by name.
func (r *Registry) List(ctx context.Context) []View {
	r.scan(false)
	routes := map[string]int{}
	for _, route := range r.book.Routes() {
		if route.Addon != "" {
			routes[route.Addon]++
		}
	}
	views := make([]View, 0, len(r.builtIns)+len(r.externals))
	for _, b := range r.builtIns {
		on, chosen := r.state(ctx, b.ID)
		status := Status{State: Ready}
		if b.Status != nil {
			status = b.Status(ctx)
		}
		views = append(views, View{Manifest: b.Manifest, BuiltIn: true, On: on, Chosen: chosen, Status: status, Routes: routes[b.ID]})
	}
	r.mu.Lock()
	externals := make([]*external, 0, len(r.externals))
	for _, e := range r.externals {
		externals = append(externals, e)
	}
	r.mu.Unlock()
	slices.SortFunc(externals, func(a, b *external) int {
		return strings.Compare(strings.ToLower(a.manifest.Name), strings.ToLower(b.manifest.Name))
	})
	for _, e := range externals {
		on, chosen := r.state(ctx, e.manifest.ID)
		view := View{Manifest: e.manifest, On: on && e.problem == "", Chosen: chosen, Folder: e.folder, Problem: e.problem}
		switch {
		case e.problem != "":
			view.Status = Status{State: Problem, Detail: e.problem}
		case !e.hasKey:
			view.Status = Status{State: Setup, Detail: "Its key is written to its folder when it is turned on."}
		default:
			view.Status = Status{State: Ready, Detail: "Its key is in its folder."}
		}
		views = append(views, view)
	}
	return views
}

// Changed is closed the next time any addon is turned on or off.
func (r *Registry) Changed() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changed
}

// While runs work for as long as addon id is on: it starts when the addon is
// turned on, its context ends when it is turned off, and it starts again when
// it is turned back on, until ctx ends.
func (r *Registry) While(ctx context.Context, id string, work func(context.Context)) {
	// Each wait is on the change channel taken before the addon is read, so
	// a change made between the two still ends the wait.
	for ctx.Err() == nil {
		for {
			changed := r.Changed()
			if r.On(ctx, id) {
				break
			}
			if !r.wait(ctx, changed) {
				return
			}
		}
		running, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			work(running)
		}()
		for {
			changed := r.Changed()
			if !r.On(ctx, id) || !r.wait(ctx, changed) {
				break
			}
		}
		stop()
		<-done
	}
}

// wait returns when changed closes, as it does when an addon is turned on or
// off, or false once ctx ends. The cache means a change made elsewhere is
// noticed within a few seconds.
func (r *Registry) wait(ctx context.Context, changed <-chan struct{}) bool {
	timer := time.NewTimer(cacheFor)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-changed:
	case <-timer.C:
	}
	return true
}

// keyOwner is the addon of your own whose key this is.
func (r *Registry) keyOwner(key string) (*external, bool) {
	hash := sha256.Sum256([]byte(key))
	find := func() *external {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, e := range r.externals {
			if e.hasKey && subtle.ConstantTimeCompare(e.keyHash[:], hash[:]) == 1 {
				return e
			}
		}
		return nil
	}
	if e := find(); e != nil {
		return e, true
	}
	// A key written since the last look is found by looking again.
	r.scan(false)
	e := find()
	return e, e != nil
}

// Name is how an addon is called on a page, or its id when there is none.
func (r *Registry) Name(id string) string {
	if b, ok := r.builtIn(id); ok {
		return b.Name
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.externals[id]; e != nil && e.problem == "" {
		return e.manifest.Name
	}
	return id
}
