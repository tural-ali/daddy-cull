package catalog

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// serveMedia drives the handler the way the mux does, since the route values are
// what the handler reads rather than the raw path.
func serveMedia(t *testing.T, handler http.Handler, id, mode string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	return serveMediaAt(t, handler, id, mode, "", header)
}

// serveMediaAt asks for a preview at a size, as the viewer does with "large".
func serveMediaAt(t *testing.T, handler http.Handler, id, mode, size string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api/media/" + id + "/" + mode
	if size != "" {
		target += "?size=" + size
	}
	request := httptest.NewRequest("GET", target, nil)
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
	handler := s.LocalMediaHandler(MediaRoots{Archive: root})

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
	handler := s.LocalMediaHandler(MediaRoots{Archive: root})
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
	handler := s.LocalMediaHandler(MediaRoots{Archive: root, Posters: posters})
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
	handler := s.LocalMediaHandler(MediaRoots{Archive: root})
	if got := serveMedia(t, handler, "1", "original", nil); got.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("RAW original returned %d", got.Code)
	}
}

// Nothing on this path may write, so every method that could is refused.
func TestLocalMediaIsReadOnly(t *testing.T) {
	s := testStore(t)
	handler := s.LocalMediaHandler(MediaRoots{Archive: t.TempDir()})
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

// Each logical tree is served only from its own mount. A tree with no mount
// configured must answer 404 rather than reaching into another tree, because a
// preview drawn from the wrong file is what makes a reviewer delete the wrong
// one.
func TestLocalMediaKeepsEachTreeInItsOwnMount(t *testing.T) {
	s := testStore(t)
	archive := t.TempDir()
	shots := t.TempDir()
	if err := os.WriteFile(filepath.Join(archive, "clip.mp4"), []byte("archivebytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shots, "2020-11-26_IMG_3421.png"), []byte("shotbytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The archive holds a file of the same name, so serving the screenshot from
	// the archive root would silently succeed with the wrong bytes.
	if err := os.WriteFile(filepath.Join(archive, "2020-11-26_IMG_3421.png"), []byte("wrongtree"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/archive/clip.mp4',1,'video',12,'archive'),
		(2,'/screenshots/2020-11-26_IMG_3421.png',1,'image',9,'screenshots'),
		(3,'/upgrades/Takeout/x.mp4',1,'video',3,'takeout'),
		(4,'/disks/disk1/2021/clip.mov',1,'video',3,'shadow')`); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(MediaRoots{Archive: archive, Screenshots: shots})

	if got := serveMedia(t, handler, "2", "preview", nil); got.Code != 200 || got.Body.String() != "shotbytes" {
		t.Fatalf("screenshot served from the wrong mount: %d %q", got.Code, got.Body.String())
	}
	if got := serveMedia(t, handler, "1", "original", nil); got.Code != 200 || got.Body.String() != "archivebytes" {
		t.Fatalf("archive file not served: %d %q", got.Code, got.Body.String())
	}
	// Neither upgrades nor disks has a mount here, so both are simply absent.
	for _, id := range []string{"3", "4"} {
		if got := serveMedia(t, handler, id, "original", nil); got.Code != 404 {
			t.Fatalf("unmounted tree for asset %s returned %d", id, got.Code)
		}
	}
}

// A gallery tile must be a downscaled JPEG, not the original. The screenshot
// holding area routinely holds 13 MB PNGs, and a grid of 120 of those is what
// made the page unusable.
func TestLocalMediaScalesGalleryTiles(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	cache := t.TempDir()
	wide := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 2000; x++ {
			wide.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 90, A: 255})
		}
	}
	shot, err := os.Create(filepath.Join(root, "big.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(shot, wide); err != nil {
		t.Fatal(err)
	}
	shot.Close()
	info, err := os.Stat(filepath.Join(root, "big.png"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/screenshots/big.png',1,'image',?,'screenshots')", info.Size()); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(MediaRoots{Screenshots: root, Cache: cache})

	tile := serveMedia(t, handler, "1", "preview", nil)
	if tile.Code != 200 {
		t.Fatalf("tile request returned %d", tile.Code)
	}
	if got := tile.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("tile content type %q", got)
	}
	decoded, _, err := image.Decode(bytes.NewReader(tile.Body.Bytes()))
	if err != nil {
		t.Fatalf("tile did not decode: %v", err)
	}
	if decoded.Bounds().Dx() != gridPixels {
		t.Fatalf("tile is %d wide, wanted %d", decoded.Bounds().Dx(), gridPixels)
	}
	if tile.Body.Len() >= int(info.Size()) {
		t.Fatalf("tile (%d bytes) is no smaller than the original (%d)", tile.Body.Len(), info.Size())
	}
	// The second request must come from the cache, and match byte for byte.
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one cached tile, got %d (%v)", len(entries), err)
	}
	if again := serveMedia(t, handler, "1", "preview", nil); !bytes.Equal(again.Body.Bytes(), tile.Body.Bytes()) {
		t.Fatal("cached tile differs from the generated one")
	}
	// A browser holding a tile drawn before the last change to how tiles are
	// drawn revalidates with the file's own time, and must get the new tile
	// rather than being told to keep the old one.
	old := tileRevision.Add(-time.Hour)
	if err = os.Chtimes(filepath.Join(root, "big.png"), old, old); err != nil {
		t.Fatal(err)
	}
	redrawn := serveMedia(t, handler, "1", "preview", http.Header{"If-Modified-Since": {old.UTC().Format(http.TimeFormat)}})
	if redrawn.Code != 200 {
		t.Fatalf("a tile older than the renderer revalidated with %d", redrawn.Code)
	}
	if kept := serveMedia(t, handler, "1", "preview", http.Header{"If-Modified-Since": {redrawn.Header().Get("Last-Modified")}}); kept.Code != 304 {
		t.Fatalf("a current tile revalidated with %d, wanted 304", kept.Code)
	}
	// Asking for the large view still gets the untouched original.
	request := httptest.NewRequest("GET", "/api/media/1/preview?size=large", nil)
	request.SetPathValue("id", "1")
	request.SetPathValue("mode", "preview")
	large := httptest.NewRecorder()
	handler.ServeHTTP(large, request)
	if large.Code != 200 || large.Body.Len() != int(info.Size()) {
		t.Fatalf("large preview returned %d with %d bytes, wanted the %d-byte original", large.Code, large.Body.Len(), info.Size())
	}
}

// The cache half of a shadowed pair is the copy the user share actually
// resolves that path to, so it is previewable through the archive mount with no
// disk mount at all. The disk half is not, and saying so honestly is the point
// of the page that lists them.
func TestLocalMediaPreviewsTheVisibleHalfOfAShadowedPair(t *testing.T) {
	s := testStore(t)
	archive := t.TempDir()
	if err := os.MkdirAll(filepath.Join(archive, "2021/2021-12/2021-12-18"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archive, "2021/2021-12/2021-12-18/IMG_0016.mov"), []byte("cachecopy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/disks/cache/2021/2021-12/2021-12-18/IMG_0016.mov',1,'video',9,'shadow'),
		(2,'/disks/disk1/2021/2021-12/2021-12-18/IMG_0016.mov',1,'video',9,'shadow')`); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(MediaRoots{Archive: archive})

	if got := serveMedia(t, handler, "1", "original", nil); got.Code != 200 || got.Body.String() != "cachecopy" {
		t.Fatalf("visible half not served: %d %q", got.Code, got.Body.String())
	}
	if got := serveMedia(t, handler, "2", "original", nil); got.Code != 404 {
		t.Fatalf("hidden half returned %d, wanted an honest 404", got.Code)
	}

	// With the disks mounted, each half comes from its own disk rather than the
	// archive, so the fallback never shadows a real mount.
	disks := t.TempDir()
	for _, disk := range []string{"cache", "disk1"} {
		if err := os.MkdirAll(filepath.Join(disks, disk, "2021/2021-12/2021-12-18"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(disks, disk, "2021/2021-12/2021-12-18/IMG_0016.mov"), []byte(disk), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mounted := s.LocalMediaHandler(MediaRoots{Archive: archive, Disks: disks})
	for id, want := range map[string]string{"1": "cache", "2": "disk1"} {
		if got := serveMedia(t, mounted, id, "original", nil); got.Code != 200 || got.Body.String() != want {
			t.Fatalf("asset %s served %q, wanted %q", id, got.Body.String(), want)
		}
	}
}

// A RAW file carries the camera's own JPEG preview, so a tile can be built from
// it without decoding sensor data. The extractor is stubbed here because what is
// under test is the wiring - that the tile is downscaled, cached, and served as
// a JPEG - not exiftool.
func TestLocalMediaBuildsRawTilesFromTheEmbeddedPreview(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	cache := t.TempDir()
	tools := t.TempDir()

	// The embedded preview a camera writes is full-size, which is exactly why it
	// has to be downscaled rather than served as-is.
	embedded := image.NewRGBA(image.Rect(0, 0, 1616, 1080))
	for y := 0; y < 1080; y++ {
		for x := 0; x < 1616; x++ {
			embedded.Set(x, y, color.RGBA{R: uint8(x % 256), G: 40, B: uint8(y % 256), A: 255})
		}
	}
	var preview bytes.Buffer
	if err := jpeg.Encode(&preview, embedded, nil); err != nil {
		t.Fatal(err)
	}
	previewFile := filepath.Join(tools, "preview.jpg")
	if err := os.WriteFile(previewFile, preview.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	// Answers only -PreviewImage, so the tag fallback is exercised too, and only
	// when handed the descriptor at /dev/fd/3.
	extractor := filepath.Join(tools, "extract")
	script := "#!/bin/sh\nhead -c1 \"$3\" >/dev/null || exit 1\n[ \"$2\" = \"-PreviewImage\" ] || exit 1\ncat " + previewFile + "\n"
	if err := os.WriteFile(extractor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "A7408429.ARW"), []byte("sensor data, not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/A7408429.ARW',1,'raw',25,'archive')"); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(MediaRoots{Archive: root, Cache: cache, RawTool: extractor})

	tile := serveMedia(t, handler, "1", "preview", nil)
	if tile.Code != 200 || tile.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("raw tile returned %d %q", tile.Code, tile.Header().Get("Content-Type"))
	}
	decoded, _, err := image.Decode(bytes.NewReader(tile.Body.Bytes()))
	if err != nil {
		t.Fatalf("raw tile did not decode: %v", err)
	}
	if decoded.Bounds().Dx() != gridPixels {
		t.Fatalf("raw tile is %d wide, wanted %d", decoded.Bounds().Dx(), gridPixels)
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 1 {
		t.Fatalf("expected one cached raw tile, got %d", len(entries))
	}
	if again := serveMedia(t, handler, "1", "preview", nil); !bytes.Equal(again.Body.Bytes(), tile.Body.Bytes()) {
		t.Fatal("cached raw tile differs from the generated one")
	}
	// The viewer gets the embedded preview at its own size, not the tile.
	large := serveMediaAt(t, handler, "1", "preview", "large", nil)
	big, _, err := image.Decode(bytes.NewReader(large.Body.Bytes()))
	if err != nil {
		t.Fatalf("raw large preview did not decode: %v", err)
	}
	if big.Bounds().Dx() != 1616 {
		t.Fatalf("raw large preview is %d wide, wanted the embedded preview's 1616", big.Bounds().Dx())
	}
	// The original is still refused: nothing here transcodes sensor data.
	if got := serveMedia(t, handler, "1", "original", nil); got.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("raw original returned %d", got.Code)
	}
	// With no extractor configured the answer is an honest miss, not a tile.
	bare := s.LocalMediaHandler(MediaRoots{Archive: root, Cache: cache})
	if got := serveMedia(t, bare, "1", "preview", nil); got.Code != 404 {
		t.Fatalf("raw preview without an extractor returned %d", got.Code)
	}
}

// HEIC is the whole iPhone library and no browser decodes it. Unlike RAW it
// carries no embedded preview, so the frame extractor decodes it, and unlike a
// video the frame arrives at full resolution because a HEIF stream cannot be
// scaled on the way out. What is under test is that it still reaches the page as
// a tile-sized JPEG.
func TestLocalMediaDecodesHeicIntoATile(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	cache := t.TempDir()
	tools := t.TempDir()

	full := image.NewRGBA(image.Rect(0, 0, 2320, 3088))
	for y := 0; y < 3088; y++ {
		for x := 0; x < 2320; x++ {
			full.Set(x, y, color.RGBA{R: 30, G: uint8(x % 256), B: uint8(y % 256), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, full, nil); err != nil {
		t.Fatal(err)
	}
	frameFile := filepath.Join(tools, "frame.jpg")
	if err := os.WriteFile(frameFile, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	// Refuses a scale filter, exactly as the real decoder does for HEIF, so a
	// tile only appears if the caller asked for no scaling and shrank it itself.
	extractor := filepath.Join(tools, "frames")
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = \"-vf\" ] && exit 1; done\nhead -c1 /dev/fd/3 >/dev/null || exit 1\ncat " + frameFile + "\n"
	if err := os.WriteFile(extractor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "IMG_8156.HEIC"), []byte("heic container"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/IMG_8156.HEIC',1,'image',14,'archive')"); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(MediaRoots{Archive: root, Cache: cache, FFmpeg: extractor})

	tile := serveMedia(t, handler, "1", "preview", nil)
	if tile.Code != 200 || tile.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("heic tile returned %d %q", tile.Code, tile.Header().Get("Content-Type"))
	}
	decoded, _, err := image.Decode(bytes.NewReader(tile.Body.Bytes()))
	if err != nil {
		t.Fatalf("heic tile did not decode: %v", err)
	}
	if decoded.Bounds().Dy() != gridPixels {
		t.Fatalf("heic tile is %d tall, wanted %d", decoded.Bounds().Dy(), gridPixels)
	}
	if tile.Body.Len() >= encoded.Len() {
		t.Fatalf("heic tile (%d bytes) is no smaller than the decoded frame (%d)", tile.Body.Len(), encoded.Len())
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 1 {
		t.Fatalf("expected one cached heic tile, got %d", len(entries))
	}
	// The viewer draws a photograph full screen, so it gets a picture of that
	// size. Handing it the grid tile made every iPhone photo postage-stamp small.
	large := serveMediaAt(t, handler, "1", "preview", "large", nil)
	if large.Code != 200 || large.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("heic large preview returned %d %q", large.Code, large.Header().Get("Content-Type"))
	}
	big, _, err := image.Decode(bytes.NewReader(large.Body.Bytes()))
	if err != nil {
		t.Fatalf("heic large preview did not decode: %v", err)
	}
	if big.Bounds().Dy() != viewerPixels {
		t.Fatalf("heic large preview is %d tall, wanted %d", big.Bounds().Dy(), viewerPixels)
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 2 {
		t.Fatalf("expected the tile and the large preview cached apart, got %d entries", len(entries))
	}
	if again := serveMedia(t, handler, "1", "preview", nil); !bytes.Equal(again.Body.Bytes(), tile.Body.Bytes()) {
		t.Fatal("the grid tile changed after the large preview was made")
	}
	// Without an extractor the page gets an honest miss rather than a container
	// the <img> cannot read.
	bare := s.LocalMediaHandler(MediaRoots{Archive: root, Cache: cache})
	if got := serveMedia(t, bare, "1", "preview", nil); got.Code != 404 {
		t.Fatalf("heic preview without an extractor returned %d", got.Code)
	}
}

// The catalogued kind came from the earlier tool and is not always right: 86
// .MPG files arrived recorded as images. A tile has to follow the container the
// decoder will actually see, not the label.
func TestLocalMediaFramesAVideoTheCatalogueCallsAnImage(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	tools := t.TempDir()
	frame := image.NewRGBA(image.Rect(0, 0, 320, 240))
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, frame, nil); err != nil {
		t.Fatal(err)
	}
	frameFile := filepath.Join(tools, "frame.jpg")
	if err := os.WriteFile(frameFile, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	extractor := filepath.Join(tools, "frames")
	if err := os.WriteFile(extractor, []byte("#!/bin/sh\ncat "+frameFile+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "MOV00759.MPG"), []byte("mpeg program stream"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/MOV00759.MPG',1,'image',19,'archive')"); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(MediaRoots{Archive: root, Cache: t.TempDir(), FFmpeg: extractor})
	if got := serveMedia(t, handler, "1", "preview", nil); got.Code != 200 || got.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("mislabelled video returned %d %q", got.Code, got.Header().Get("Content-Type"))
	}
}

// A file can carry a RAW extension and hold an ordinary JPEG, which has no
// embedded preview because it is the picture. It still has to reach the page.
func TestLocalMediaTilesAJpegWearingARawExtension(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	tools := t.TempDir()
	picture := image.NewRGBA(image.Rect(0, 0, 3024, 4032))
	shot, err := os.Create(filepath.Join(root, "IMG_8405.DNG"))
	if err != nil {
		t.Fatal(err)
	}
	if err = jpeg.Encode(shot, picture, nil); err != nil {
		t.Fatal(err)
	}
	shot.Close()
	info, err := os.Stat(filepath.Join(root, "IMG_8405.DNG"))
	if err != nil {
		t.Fatal(err)
	}
	// Finds nothing, exactly as the real extractor does on a plain JPEG.
	extractor := filepath.Join(tools, "extract")
	if err = os.WriteFile(extractor, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/IMG_8405.DNG',1,'raw',?,'archive')", info.Size()); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(MediaRoots{Archive: root, Cache: t.TempDir(), RawTool: extractor})

	tile := serveMedia(t, handler, "1", "preview", nil)
	if tile.Code != 200 {
		t.Fatalf("mislabelled JPEG returned %d", tile.Code)
	}
	decoded, _, err := image.Decode(bytes.NewReader(tile.Body.Bytes()))
	if err != nil {
		t.Fatalf("tile did not decode: %v", err)
	}
	if decoded.Bounds().Dy() != gridPixels {
		t.Fatalf("tile is %d tall, wanted %d", decoded.Bounds().Dy(), gridPixels)
	}
}

// The physical copies behind a shadowed pair cannot be reached under their own
// names: the merged share resolves that path to the other copy, and some pairs
// differ only by letter case, which a case-insensitive client folds together. A
// flat directory of hardlinks named by asset id is how they are reached, and the
// two halves of a case pair must come back as different files.
func TestLocalMediaReachesShadowedCopiesThroughTheReviewFarm(t *testing.T) {
	s := testStore(t)
	farm := t.TempDir()
	if err := os.WriteFile(filepath.Join(farm, "1.mov"), []byte("upper case copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(farm, "2.mov"), []byte("lower case copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec(`INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES
		(1,'/disks/disk1/2021/2021-12/2021-12-18/IMG_0016.MOV',1,'video',15,'shadow'),
		(2,'/disks/disk1/2021/2021-12/2021-12-18/IMG_0016.mov',1,'video',15,'shadow'),
		(3,'/disks/disk1/2021/2021-12/2021-12-18/IMG_9999.MOV',1,'video',15,'shadow')`); err != nil {
		t.Fatal(err)
	}
	handler := s.LocalMediaHandler(MediaRoots{Review: farm})

	for id, want := range map[string]string{"1": "upper case copy", "2": "lower case copy"} {
		if got := serveMedia(t, handler, id, "original", nil); got.Code != 200 || got.Body.String() != want {
			t.Fatalf("asset %s served %d %q, wanted %q", id, got.Code, got.Body.String(), want)
		}
	}
	// A file with no link in the farm is still an honest miss.
	if got := serveMedia(t, handler, "3", "original", nil); got.Code != 404 {
		t.Fatalf("unlinked asset returned %d", got.Code)
	}
	// A real disk mount still wins, so the farm never shadows one.
	disks := t.TempDir()
	if err := os.MkdirAll(filepath.Join(disks, "disk1/2021/2021-12/2021-12-18"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disks, "disk1/2021/2021-12/2021-12-18/IMG_0016.MOV"), []byte("from the disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	mounted := s.LocalMediaHandler(MediaRoots{Review: farm, Disks: disks})
	if got := serveMedia(t, mounted, "1", "original", nil); got.Body.String() != "from the disk" {
		t.Fatalf("farm shadowed a real mount: %q", got.Body.String())
	}
}

// An iPhone HEIC is a grid of dozens of HEVC tiles, and by default every tile's
// decoder starts a thread per core. On Tower that is hundreds of threads for one
// photograph, past the container's process limit, and every such tile came back
// blank. One frame gains nothing from threading, so the decoder is held to one.
func TestFrameExtractorIsHeldToOneThread(t *testing.T) {
	tools := t.TempDir()
	extractor := filepath.Join(tools, "frames")
	// Succeeds only when both the decoder and the filter graph are held to one
	// thread, and the decoder option comes before the input it applies to.
	script := `#!/bin/sh
decoder=0; filters=0; previous=""
for a in "$@"; do
  [ "$previous" = "-threads" ] && [ "$a" = "1" ] && decoder=1
  [ "$previous" = "-filter_threads" ] && [ "$a" = "1" ] && filters=1
  [ "$a" = "-i" ] && [ "$decoder" = 0 ] && exit 1
  previous="$a"
done
[ "$decoder" = 1 ] && [ "$filters" = 1 ] || exit 1
printf frame
`
	if err := os.WriteFile(extractor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(tools, "clip.mov")
	if err := os.WriteFile(media, []byte("container"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(media)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, scale := range []bool{true, false} {
		out, err := runFrameExtractor(context.Background(), extractor, file, "1", scale, false)
		if err != nil || string(out) != "frame" {
			t.Fatalf("scale=%v: %q %v", scale, out, err)
		}
	}
}

func TestVideoFrameToneMapsAnHDRClip(t *testing.T) {
	tools := t.TempDir()
	// A prober that calls the clip HLG, beside an extractor that records the
	// filter it was given, as ffprobe sits beside ffmpeg.
	probe := filepath.Join(tools, "ffprobe")
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nhead -c1 /dev/fd/3 >/dev/null || exit 1\ncat "+filepath.Join(tools, "transfer")+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	extractor := filepath.Join(tools, "ffmpeg")
	if err := os.WriteFile(extractor, []byte("#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = \"-vf\" ] && printf %s \"$2\"; shift; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(tools, "clip.mov")
	if err := os.WriteFile(media, []byte("container"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := probeTool(extractor); got != probe {
		t.Fatalf("probe tool %q, want %q", got, probe)
	}
	if got := probeTool(filepath.Join(tools, "frames")); got != "" {
		t.Fatalf("an extractor that is not ffmpeg has no prober, got %q", got)
	}
	for _, c := range []struct {
		transfer string
		toneMap  bool
	}{
		{"arib-std-b67", true}, {"smpte2084", true}, {"bt709", false}, {"", false},
		// An iPhone clip carries Dolby Vision side data, and the prober then
		// ends the line with an empty field: this is its real output.
		{"arib-std-b67,", true}, {"bt709,", false},
	} {
		if err := os.WriteFile(filepath.Join(tools, "transfer"), []byte(c.transfer+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(media)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := videoFrame(context.Background(), extractor, file, "", "clip"+c.transfer, 9, 1)
		file.Close()
		if err != nil {
			t.Fatalf("%q: %v", c.transfer, err)
		}
		if got := strings.Contains(string(frame), "tonemap="); got != c.toneMap {
			t.Errorf("%q: filter %q, tone mapped %v, want %v", c.transfer, frame, got, c.toneMap)
		}
		if !strings.HasPrefix(string(frame), "scale=") {
			t.Errorf("%q: the frame is scaled first, got %q", c.transfer, frame)
		}
	}
}

// A file moved to the Bin has left its day folder, but the Log and the viewer
// still ask for it by its catalogue id: they are served the Bin's copy, and a
// file emptied from the Bin is simply gone.
func TestLocalMediaFollowsAFileIntoTheBin(t *testing.T) {
	b, s, root := binFixture(t)
	ctx := context.Background()
	handler := s.LocalMediaHandler(MediaRoots{Archive: root})
	p, err := b.Preview(ctx, []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Run(ctx, p.ID, "quarantine", ""); err != nil {
		t.Fatal(err)
	}
	if got := serveMedia(t, handler, "1", "original", nil); got.Code != 200 || got.Body.String() != "family original" {
		t.Fatalf("a file in the Bin: %d %q", got.Code, got.Body.String())
	}
	if _, err = b.Run(ctx, p.ID, "purge", "DELETE 2"); err != nil {
		t.Fatal(err)
	}
	if got := serveMedia(t, handler, "1", "original", nil); got.Code != 404 {
		t.Fatalf("a file emptied from the Bin: %d", got.Code)
	}
}
