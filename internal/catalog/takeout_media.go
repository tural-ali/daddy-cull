package catalog

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// os.Root rejects traversal and escaping symlinks. The mount is also read-only.
// JPEG/PNG originals preserve browser EXIF orientation until worker previews land.
func serveTakeout(w http.ResponseWriter, r *http.Request, p string) {
	root, e := os.OpenRoot("/takeout")
	if e != nil {
		http.Error(w, "Takeout unavailable", 503)
		return
	}
	defer root.Close()
	f, e := root.Open(strings.TrimPrefix(p, "/upgrades/"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(p))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
		http.Error(w, "preview format not yet supported", 415)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, filepath.Base(p), st.ModTime(), f)
}
