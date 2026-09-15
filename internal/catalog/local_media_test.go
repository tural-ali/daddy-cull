package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// serveMedia drives the handler the way the mux does, since the route values are
// what the handler reads rather than the raw path.
func serveMedia(t *testing.T, handler http.Handler, id, mode string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest("GET", "/api/media/"+id+"/"+mode, nil)
	request.SetPathValue("id", id)
	request.SetPathValue("mode", mode)
	for key, values := range header {
		request.Header[key] = values
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// The handler must read only what the catalogue names, inside the archive root,
// and must never be able to reach a file outside it. The archive is irreplaceable,
// so this is the test that matters most on this path.
func TestLocalMediaStaysInsideTheArchiveRoot(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.mp4"), []byte("must not be served"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "2020/2020-09/2020-09-28"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "2020/2020-09/2020-09-28/clip.mp4"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A symlink inside the root pointing out of it is the case a plain prefix
	// check would miss, so it is the one worth proving.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	paths := map[int64]string{
		1: "/archive/2020/2020-09/2020-09-28/clip.mp4",
		2: "/archive/../" + filepath.Base(outside) + "/secret.mp4",
		3: "/archive/escape/secret.mp4",
	}
	for id, path := range paths {
		if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,'video',10,'archive')", id, path); err != nil {
			t.Fatal(err)
		}
	}
	handler := s.LocalMediaHandler(root, "")

	if got := serveMedia(t, handler, "1", "original", nil); got.Code != 200 || got.Body.String() != "0123456789" {
		t.Fatalf("catalogued file not served: %d %q", got.Code, got.Body.String())
	}
	for _, id := range []string{"2", "3"} {
		if got := serveMedia(t, handler, id, "original", nil); got.Code == 200 {
			t.Fatalf("asset %s escaped the archive root: %q", id, got.Body.String())
		}
	}
	if got := serveMedia(t, handler, "999", "original", nil); got.Code != 404 {
		t.Fatalf("unknown asset returned %d", got.Code)
	}
}

// Seeking in a video depends entirely on the server answering byte ranges, so
// playback is only really wired up if this holds.
func TestLocalMediaServesByteRangesForPlayback(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/clip.mp4',1,'video',10,'archive')"); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(root, "")
	got := serveMedia(t, handler, "1", "original", http.Header{"Range": {"bytes=2-5"}})
	if got.Code != http.StatusPartialContent || got.Body.String() != "2345" {
		t.Fatalf("range request returned %d %q", got.Code, got.Body.String())
	}
	if got.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("unexpected Content-Range %q", got.Header().Get("Content-Range"))
	}
}

// A video has no still a browser can render, so its preview is the captured
// poster. Without one the answer is a plain 404 and the page keeps its own
// fallback, rather than an <img> being handed a container it cannot decode.
func TestLocalMediaPreviewUsesPosterForVideo(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	root := t.TempDir()
	posters := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(posters, "00001.jpg"), []byte("jpegbytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/clip.mp4',1,'video',5,'archive'),(2,'/archive/other.mp4',1,'video',5,'archive')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO social_items(asset_id,path,day,name,score,evidence,size_bytes,width,height,duration,letterbox_top,letterbox_bottom,poster,state) VALUES(1,'/archive/clip.mp4','2020-09-28','clip.mp4',10,'test',5,100,100,1,0,0,'00001.jpg','waiting')"); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(root, posters)
	if got := serveMedia(t, handler, "1", "preview", nil); got.Code != 200 || got.Body.String() != "jpegbytes" {
		t.Fatalf("poster not served for video preview: %d %q", got.Code, got.Body.String())
	}
	if got := serveMedia(t, handler, "2", "preview", nil); got.Code != 404 {
		t.Fatalf("video without a poster returned %d", got.Code)
	}
	// The original is still the video itself, which is what playback requests.
	if got := serveMedia(t, handler, "1", "original", nil); got.Code != 200 || got.Body.String() != "video" {
		t.Fatalf("original not served: %d %q", got.Code, got.Body.String())
	}
}

// Formats a browser cannot decode are refused rather than streamed as a download
// the page has no use for.
func TestLocalMediaRefusesFormatsNeedingATranscode(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "raw.ARW"), []byte("raw"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/raw.ARW',1,'image',3,'archive')"); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(root, "")
	if got := serveMedia(t, handler, "1", "original", nil); got.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("RAW original returned %d", got.Code)
	}
}

// Nothing on this path may write, so every method that could is refused.
func TestLocalMediaIsReadOnly(t *testing.T) {
	s := testStore(t)
	handler := s.LocalMediaHandler(t.TempDir(), "")
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		request := httptest.NewRequest(method, "/api/media/1/original", nil)
		request.SetPathValue("id", strconv.Itoa(1))
		request.SetPathValue("mode", "original")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s returned %d", method, response.Code)
		}
	}
}
