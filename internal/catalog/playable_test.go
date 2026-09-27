package catalog

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// An old camera's AVI plays from an MP4 copy made on first view, answered in
// byte ranges like any clip, and the copy is made once.
func TestOldCameraClipPlaysFromConvertedCopy(t *testing.T) {
	tool, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	s := testStore(t)
	root, cache := t.TempDir(), t.TempDir()
	clip := filepath.Join(root, "MOV03466.AVI")
	// Motion JPEG with µ-law sound at an odd size, as the 2011 camera wrote it.
	if out, err := exec.Command(tool, "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=321x241:duration=1:rate=10", "-f", "lavfi", "-i", "sine=duration=1", "-c:v", "mjpeg", "-c:a", "pcm_mulaw", clip).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	if _, err = s.write.ExecContext(context.Background(), "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/MOV03466.AVI',1,'video',10,'archive')"); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(clip)

	refused := serveMedia(t, s.LocalMediaHandler(MediaRoots{Archive: root}), "1", "original", nil)
	if refused.Code != 415 {
		t.Fatalf("with no converter the clip is refused, got %d", refused.Code)
	}

	handler := s.LocalMediaHandler(MediaRoots{Archive: root, Cache: cache, FFmpeg: tool})
	got := serveMedia(t, handler, "1", "original", nil)
	if got.Code != 200 || got.Header().Get("Content-Type") != "video/mp4" || !bytes.Contains(got.Body.Bytes()[:32], []byte("ftyp")) {
		t.Fatalf("converted clip: %d %q", got.Code, got.Header().Get("Content-Type"))
	}
	ranged := serveMedia(t, handler, "1", "original", http.Header{"Range": {"bytes=0-99"}})
	if ranged.Code != 206 || ranged.Body.Len() != 100 {
		t.Fatalf("byte range: %d %d", ranged.Code, ranged.Body.Len())
	}
	copies, _ := filepath.Glob(filepath.Join(cache, "*.mp4"))
	if len(copies) != 1 {
		t.Fatal("copies", copies)
	}
	leftovers, _ := filepath.Glob(filepath.Join(cache, "tmp-*"))
	if len(leftovers) != 0 {
		t.Fatal("temporary files left", leftovers)
	}
	if after, _ := os.ReadFile(clip); !bytes.Equal(after, original) {
		t.Fatal("the archive file changed")
	}
}
