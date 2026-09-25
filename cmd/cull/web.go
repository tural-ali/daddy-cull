package main

import (
	"net/http"
	"path/filepath"
	"strings"
)

// webApp serves the built page and its assets.
//
// A page is always revalidated, because it names the bundle to load: a browser
// that reused yesterday's page would keep running yesterday's app after a
// deploy. A bundle under /assets/ carries a content hash in its name, so it can
// be kept for good; a new build gets a new name.
func webApp(dir string, routes []string) http.Handler {
	mux := http.NewServeMux()
	page := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	}
	// Every page also answers with a photo open on it, so a preview's own
	// address can be shared, bookmarked or reloaded.
	for _, route := range append([]string{"/on/{md}", "/day/{day}"}, routes...) {
		mux.HandleFunc("GET "+route, page)
		mux.HandleFunc("GET "+route+"/photo/{photo}", page)
	}
	files := http.FileServer(http.Dir(dir))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	}))
	return mux
}
