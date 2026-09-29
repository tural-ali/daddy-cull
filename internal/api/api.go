// Package api is how Cull's HTTP API is both served and described. Every
// route is registered with a Route that says what it does, what it takes and
// what it returns, and the reference in the app and at /api/openapi.json is
// generated from those same values, so the documentation cannot drift from
// the code: a route that is not described cannot be served.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Version is the API's version. Routes may be added and responses may gain
// fields within a version; a change that would break a caller gets a new
// version.
const Version = "1"

// VersionHeader is sent with every API response.
const VersionHeader = "X-Cull-API-Version"

// Permissions an addon asks for, from least to most trusting. Every GET needs
// Read; every other route names the one it needs.
const (
	// Read sees the library, the catalogue's records and the settings.
	Read = "read"
	// Review saves choices: keep or remove, favourites, turns, reviewed dates.
	Review = "review"
	// Import adds files to the library from outside it, such as Google
	// Photos.
	Import = "import"
	// Bin moves files into the Bin and back out of it.
	Bin = "bin"
	// Delete deletes files from the Bin for good.
	Delete = "delete"
	// Settings changes Cull's settings and turns addons on and off.
	Settings = "settings"
)

// Permissions lists every permission with what it lets an addon do, in the
// order they are shown.
var Permissions = []struct{ Name, Doc string }{
	{Read, "See your library, its dates, choices and settings"},
	{Review, "Save choices: keep or remove, favourites, turns and reviewed dates"},
	{Import, "Add files to the library from outside it, such as Google Photos"},
	{Bin, "Move files into the Bin and put them back"},
	{Delete, "Delete files in the Bin for good"},
	{Settings, "Change settings and turn addons on and off"},
}

// ValidPermission reports whether name is one of Permissions.
func ValidPermission(name string) bool {
	for _, p := range Permissions {
		if p.Name == name {
			return true
		}
	}
	return false
}

// Param is a path or query parameter.
type Param struct {
	Name string
	// In is "path" or "query".
	In string
	// Type is "string", "integer" or "boolean".
	Type     string
	Doc      string
	Required bool
	Enum     []string
	// Example is a value that works, used by the reference's Try it form.
	Example string
}

// Path is a required string path parameter.
func Path(name, doc, example string) Param {
	return Param{Name: name, In: "path", Type: "string", Doc: doc, Required: true, Example: example}
}

// PathInt is a required integer path parameter.
func PathInt(name, doc, example string) Param {
	return Param{Name: name, In: "path", Type: "integer", Doc: doc, Required: true, Example: example}
}

// Query is an optional query parameter.
func Query(name, typ, doc string) Param {
	return Param{Name: name, In: "query", Type: typ, Doc: doc}
}

// Error is a failure a caller should expect, with when it happens.
type Error struct {
	Status int
	When   string
}

// Route describes one endpoint.
type Route struct {
	Method string
	// Path is a net/http pattern path, such as /api/today/{md}.
	Path string
	// Addon is the id of the addon the route belongs to, or empty for Cull
	// itself. An addon's routes answer only while it is on.
	Addon string
	// Tag groups routes in the reference.
	Tag     string
	Summary string
	Doc     string
	Params  []Param
	// Body is a value of the request body's type, or nil for none.
	Body any
	// Returns is a value of the response's type, or nil for none.
	Returns any
	// Produces is the response's media type when it is not JSON, such as
	// image/jpeg or text/event-stream.
	Produces string
	// Status is the success status; 200 by default, or 204 when the route
	// returns nothing.
	Status int
	Errors []Error
	// Needs is the permission a caller needs: Read for every GET, and one
	// of the others for each route that changes something.
	Needs string
	// Internal marks a route for one of Cull's own helpers rather than for
	// addons; it is documented so nothing is hidden, and shown apart.
	Internal bool
}

// Pattern is the route's net/http pattern.
func (r Route) Pattern() string { return r.Method + " " + r.Path }

// OperationID is a stable name for the route, such as getTodayMd.
func (r Route) OperationID() string {
	var b strings.Builder
	b.WriteString(strings.ToLower(r.Method))
	for _, part := range strings.FieldsFunc(strings.TrimPrefix(r.Path, "/api"), func(c rune) bool { return c == '/' || c == '-' || c == '{' || c == '}' || c == '.' }) {
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}

func (r Route) successStatus() int {
	if r.Status != 0 {
		return r.Status
	}
	if r.Returns == nil && r.Produces == "" {
		return 204
	}
	return 200
}

var pathParam = regexp.MustCompile(`\{([a-zA-Z]+)\.{0,3}\}`)

// check refuses a route whose description does not match itself, so a
// mistake is found when the server starts rather than by a reader.
func (r Route) check() error {
	if r.Method != "GET" && r.Method != "POST" {
		return fmt.Errorf("%s: only GET and POST are served", r.Pattern())
	}
	if !strings.HasPrefix(r.Path, "/api/") {
		return fmt.Errorf("%s: API routes live under /api/", r.Pattern())
	}
	if r.Tag == "" || r.Summary == "" {
		return fmt.Errorf("%s: a tag and a summary are required", r.Pattern())
	}
	if strings.HasSuffix(r.Summary, ".") {
		return fmt.Errorf("%s: a summary is a title, without a full stop", r.Pattern())
	}
	if r.Method == "GET" && r.Needs != Read {
		return fmt.Errorf("%s: a GET needs read", r.Pattern())
	}
	if r.Method != "GET" && (r.Needs == Read || !ValidPermission(r.Needs)) {
		return fmt.Errorf("%s: a write names the permission it needs", r.Pattern())
	}
	var inPath []string
	for _, m := range pathParam.FindAllStringSubmatch(r.Path, -1) {
		inPath = append(inPath, m[1])
	}
	var described []string
	for _, p := range r.Params {
		if p.Doc == "" {
			return fmt.Errorf("%s: parameter %s is not described", r.Pattern(), p.Name)
		}
		switch p.In {
		case "path":
			described = append(described, p.Name)
		case "query":
		default:
			return fmt.Errorf("%s: parameter %s is in neither the path nor the query", r.Pattern(), p.Name)
		}
		if p.Type != "string" && p.Type != "integer" && p.Type != "boolean" {
			return fmt.Errorf("%s: parameter %s has type %q", r.Pattern(), p.Name, p.Type)
		}
	}
	slices.Sort(inPath)
	slices.Sort(described)
	if !slices.Equal(inPath, described) {
		return fmt.Errorf("%s: path parameters %v are described as %v", r.Pattern(), inPath, described)
	}
	return nil
}

// Book holds every route's description and the tags that group them.
type Book struct {
	mu     sync.Mutex
	routes []Route
	tags   map[string]string
	addons map[string]string
}

// NewBook starts an empty book.
func NewBook() *Book {
	return &Book{tags: map[string]string{}, addons: map[string]string{}}
}

// Tag describes a group of routes.
func (b *Book) Tag(name, doc string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tags[name] = doc
}

// AddonName records the name an addon id is shown with.
func (b *Book) AddonName(id, name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.addons[id] = name
}

// Routes lists the described routes, grouped by tag in the order the tags
// were first used and then by path.
func (b *Book) Routes() []Route {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.routes)
}

func (b *Book) add(r Route) {
	if err := r.check(); err != nil {
		panic(err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, known := range b.routes {
		if known.Pattern() == r.Pattern() {
			panic(fmt.Sprintf("%s is described twice", r.Pattern()))
		}
	}
	b.routes = append(b.routes, r)
}

type callerKey struct{}

type caller struct {
	id          string
	permissions []string
}

// Caller is who made a request: the id of the addon whose key it carried,
// or empty for Cull's own pages.
func Caller(ctx context.Context) string {
	c, _ := ctx.Value(callerKey{}).(caller)
	return c.id
}

// WithCaller returns ctx carrying the addon that made the request and the
// permissions it asked for.
func WithCaller(ctx context.Context, addon string, permissions []string) context.Context {
	return context.WithValue(ctx, callerKey{}, caller{addon, slices.Clone(permissions)})
}

// Allowed reports whether the request may do what permission lets it do.
// Cull's own pages may do anything; an addon may read, and do what it asked
// for. A route checks this itself only where what it needs depends on the
// body, such as an action that restores or deletes; the guard has already
// checked the permission the route names.
func Allowed(ctx context.Context, permission string) bool {
	c, ok := ctx.Value(callerKey{}).(caller)
	return !ok || permission == Read || slices.Contains(c.permissions, permission)
}

// Guard stands in front of every route: it may answer a request itself, or
// pass it on to next, perhaps with more in its context.
type Guard func(route Route, next http.Handler) http.Handler

// Mux serves described routes, and only those.
type Mux struct {
	mux    *http.ServeMux
	book   *Book
	guards []Guard
}

// NewMux starts a mux whose routes are described in book.
func NewMux(book *Book) *Mux {
	return &Mux{mux: http.NewServeMux(), book: book}
}

// Book is where this mux's routes are described.
func (m *Mux) Book() *Book { return m.book }

// Guard adds a check every request passes before it reaches its route.
// Guards run in the order they were added, for routes added before or after.
func (m *Mux) Guard(g Guard) { m.guards = append(m.guards, g) }

// Handle serves route with h.
func (m *Mux) Handle(route Route, h http.Handler) {
	m.book.add(route)
	m.mux.Handle(route.Pattern(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Guards are read at request time, so one added after a route still
		// stands in front of it. The first added is the outermost.
		next := h
		for i := len(m.guards) - 1; i >= 0; i-- {
			next = m.guards[i](route, next)
		}
		next.ServeHTTP(w, r)
	}))
}

// HandleFunc serves route with f.
func (m *Mux) HandleFunc(route Route, f func(http.ResponseWriter, *http.Request)) {
	m.Handle(route, http.HandlerFunc(f))
}

func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(VersionHeader, Version)
	if m.Pattern(r) == "" {
		// Answered as every other failure is, in JSON.
		for _, method := range []string{"GET", "POST"} {
			other := r.Clone(r.Context())
			other.Method = method
			if method != r.Method && m.Pattern(other) != "" {
				w.Header().Set("Allow", method)
				Fail(w, 405, r.URL.Path+" answers "+method+" only.")
				return
			}
		}
		Fail(w, 404, "There is no "+r.URL.Path+" in the API. Every route is listed at /developers.")
		return
	}
	m.mux.ServeHTTP(w, r)
}

// Pattern is the pattern that would serve r, or empty when none would.
func (m *Mux) Pattern(r *http.Request) string {
	_, pattern := m.mux.Handler(r)
	return pattern
}

// IsJSON reports whether a request's body is declared as JSON, with or
// without a charset.
func IsJSON(r *http.Request) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && media == "application/json"
}

// Fail answers with a status and a message a person can act on, as JSON.
func Fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// SameOrigin refuses a write sent by a page on another site, which a browser
// would otherwise send with the reader's access to Cull. Scripts and addons
// send no Origin and are not affected; each write also requires JSON, which
// a plain cross-site form cannot send.
func SameOrigin(route Route, next http.Handler) http.Handler {
	if route.Method == "GET" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
				Fail(w, 403, "Writes are accepted from Cull's own pages and from addons, not from other sites.")
				return
			}
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			Fail(w, 403, "Writes are accepted from Cull's own pages and from addons, not from other sites.")
			return
		}
		next.ServeHTTP(w, r)
	})
}
