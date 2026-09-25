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

func TestScreenshotPageSplitsReviewedFromWaiting(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	for _, name := range []string{"2020-01-02_a.png", "2020-01-03_b.png", "2020-01-04_c.mov"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("pixels"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if _, err := s.ImportScreenshotDirectory(ctx, root); err != nil {
		t.Fatal(err)
	}
	all, err := s.ScreenshotPage(ctx, "", "", 0, 100)
	if err != nil || all.Total != 3 || all.Unreviewed != 3 || all.Reviewed != 0 {
		t.Fatalf("before keeping: %+v %v", all, err)
	}
	if _, err = s.Decide(ctx, Decision{RequestID: "0123456789abcdef0123456789abcdef", AssetID: all.Items[0].ID, Status: "keep"}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.ScreenshotPage(ctx, "", "", 0, 100)
	if err != nil || waiting.Total != 2 || len(waiting.Items) != 2 || waiting.Reviewed != 1 || waiting.Bytes != 12 {
		t.Fatalf("not reviewed: %+v %v", waiting, err)
	}
	reviewed, err := s.ScreenshotPage(ctx, "", "reviewed", 0, 100)
	if err != nil || reviewed.Total != 1 || len(reviewed.Items) != 1 || reviewed.Items[0].ID != all.Items[0].ID || reviewed.Items[0].Status != "keep" {
		t.Fatalf("reviewed: %+v %v", reviewed, err)
	}
	videos, err := s.ScreenshotPage(ctx, "video", "all", 0, 100)
	if err != nil || videos.Total != 1 || videos.Unreviewed != 1 || videos.Reviewed != 0 {
		t.Fatalf("videos: %+v %v", videos, err)
	}
	if _, err = s.ScreenshotPage(ctx, "", "kept", 0, 100); err == nil {
		t.Fatal("an unknown filter was accepted")
	}
}
