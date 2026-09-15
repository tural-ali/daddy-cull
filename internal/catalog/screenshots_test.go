package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestImportScreenshotDirectoryIndexesMediaWithoutOpeningContent(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	for name, content := range map[string]string{"2020-01-02_capture.png": "pixels", "2020-01-02_capture.png.xmp": "sidecar", ".hidden.jpg": "hidden", "notes.txt": "text"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.ImportScreenshotDirectory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Bytes != 6 {
		t.Fatalf("unexpected import: %+v", result)
	}
	items, err := s.ScreenshotBacklog(context.Background(), "", 100)
	if err != nil || len(items) != 1 || items[0].Day != "2020-01-02" || items[0].Asset.Source != "screenshots" {
		t.Fatalf("unexpected backlog: %+v %v", items, err)
	}
	if _, err = os.Stat(filepath.Join(root, "2020-01-02_capture.png")); err != nil {
		t.Fatalf("source changed: %v", err)
	}
}
