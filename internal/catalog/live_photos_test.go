package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// A clip is a photo's by name: filed in .live-photos with or without _HEVC,
// or beside the photo with it. Every name is a synthetic fixture.
func TestLiveClipsArePairedByName(t *testing.T) {
	for _, c := range []struct{ clip, photo string }{
		{".live-photos/2022/2022-01/2022-01-07/IMG_6947_HEVC.MOV", "2022/2022-01/2022-01-07/img_6947"},
		{".live-photos/2020/2020-08/2020-08-16/IMG_2809.MP4", "2020/2020-08/2020-08-16/img_2809"},
		{"2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV", "2026/2026-09/2026-09-26/img_5472"},
		{"2026/2026-09/2026-09-26/IMG_5472_hevc.mp4", "2026/2026-09/2026-09-26/img_5472"},
		// A plain video beside a photo of the same name is a video of its own.
		{"2021/2021-09/2021-09-03/IMG_0091.MOV", ""},
		{".live-photos/2022/2022-01/2022-01-07/IMG_6947.AAE", ""},
		{".live-photos/IMG_1_HEVC.MOV", ""},
		{"IMG_1_HEVC.MOV", ""},
		{".live-photos/2022/2022-01/2022-01-07/_HEVC.MOV", ""},
	} {
		dir, stem, ok := livePhotoOf(c.clip)
		got := ""
		if ok {
			got = dir + "/" + stem
		}
		if got != c.photo {
			t.Errorf("%s: paired with %q, want %q", c.clip, got, c.photo)
		}
	}
}

// liveArchive lays out a synthetic archive: a Live Photo whose clip graduated
// beside it, a HEIC and JPEG of one exposure whose clip is filed in
// .live-photos, a real video that shares a photo's name, and a filed clip
// whose photo is not in the archive.
func liveArchive(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"2026/2026-09/2026-09-26/IMG_5472.HEIC":                   "still",
		"2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV":               "inline clip",
		"2026/2026-09/2026-09-26/IMG_0026.HEIC":                   "still",
		"2026/2026-09/2026-09-26/IMG_0026.MOV":                    "a video of its own",
		"2022/2022-01/2022-01-07/IMG_6947.HEIC":                   "still",
		"2022/2022-01/2022-01-07/IMG_6947.JPG":                    "still",
		".live-photos/2022/2022-01/2022-01-07/IMG_6947_HEVC.MOV":  "filed clip",
		".live-photos/2022/2022-01/2022-01-07/IMG_9999_HEVC.MOV":  "orphan clip",
		".live-photos/2026/2026-09/2026-09-26/IMG_5472.MP4":       "second clip",
		".live-photos/2026/2026-09/2026-09-26/.IMG_5472_HEVC.MOV": "hidden",
	} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func assetID(t *testing.T, s *Store, rel string) int64 {
	t.Helper()
	var id int64
	if err := s.read.QueryRow("SELECT id FROM assets WHERE relative_path=?", "/archive/"+rel).Scan(&id); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return id
}

// dayNames lists the files a day page shows for one year, marking each Live
// Photo.
func dayNames(t *testing.T, s *Store, md string) ([]string, TodayData) {
	t.Helper()
	data, err := s.Today(context.Background(), md)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, year := range data.Years {
		for _, a := range year.Assets {
			name := filepath.Base(a.Path)
			if a.Live {
				name += " live"
			}
			names = append(names, name)
		}
	}
	return names, data
}

// A Live Photo shows once, as its photo, with the clip behind a Live badge,
// however the clip was filed; the clip is never a file to decide on alone.
// Filing the clip into .live-photos later changes nothing on the page.
func TestLivePhotosShowAsTheirPhoto(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	root := liveArchive(t)
	if _, err := s.ScanArchive(ctx, root); err != nil {
		t.Fatal(err)
	}
	photo := assetID(t, s, "2026/2026-09/2026-09-26/IMG_5472.HEIC")
	clip := assetID(t, s, "2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV")

	names, data := dayNames(t, s, "09-26")
	if want := []string{"IMG_0026.HEIC", "IMG_0026.MOV", "IMG_5472.HEIC live"}; !slices.Equal(sorted(names), want) {
		t.Fatalf("the day shows %v, want %v", names, want)
	}
	if data.Clips[clip] != photo {
		t.Errorf("a link to the clip does not open its photo: %v", data.Clips)
	}
	if names, _ = dayNames(t, s, "01-07"); !slices.Equal(sorted(names), []string{"IMG_6947.HEIC live", "IMG_6947.JPG live"}) {
		t.Errorf("the exposure's two files show %v", names)
	}
	var revision int64
	s.read.QueryRow("SELECT COALESCE((SELECT revision FROM decisions WHERE asset_id=?),0)", clip).Scan(&revision)
	if _, err := s.Decide(ctx, Decision{AssetID: clip, RequestID: "decide-the-clip", ExpectedRevision: revision, Status: "cull"}); !errors.Is(err, ErrLiveClip) {
		t.Fatalf("the clip was decided on by itself: %v", err)
	}
	if _, err := s.Decide(ctx, Decision{AssetID: photo, RequestID: "decide-the-photo", Status: "keep"}); err != nil {
		t.Fatal(err)
	}

	// The clip plays by the photo's id: the original .MOV before the .MP4,
	// and the filed one for the photo whose clip is in .live-photos.
	handler := s.LiveClipHandler(MediaRoots{Archive: root})
	play := func(id int64) (int, string) {
		request := httptest.NewRequest("GET", "/api/media/x/live", nil)
		request.SetPathValue("id", strconv.FormatInt(id, 10))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}
	if code, body := play(photo); code != http.StatusOK || body != "inline clip" {
		t.Errorf("the photo's clip: %d %q", code, body)
	}
	if code, body := play(assetID(t, s, "2022/2022-01/2022-01-07/IMG_6947.JPG")); code != http.StatusOK || body != "filed clip" {
		t.Errorf("the filed clip: %d %q", code, body)
	}
	if code, _ := play(assetID(t, s, "2026/2026-09/2026-09-26/IMG_0026.HEIC")); code != http.StatusNotFound {
		t.Errorf("a photo with no clip played %d", code)
	}

	// The host files the stray clip where it belongs.
	if err := os.Rename(filepath.Join(root, "2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV"), filepath.Join(root, ".live-photos/2026/2026-09/2026-09-26/IMG_5472_HEVC.MOV")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanArchive(ctx, root); err != nil {
		t.Fatal(err)
	}
	if names, _ = dayNames(t, s, "09-26"); !slices.Equal(sorted(names), []string{"IMG_0026.HEIC", "IMG_0026.MOV", "IMG_5472.HEIC live"}) {
		t.Fatalf("after filing the clip the day shows %v", names)
	}
	if code, body := play(photo); code != http.StatusOK || body != "inline clip" {
		t.Errorf("the filed clip does not play: %d %q", code, body)
	}
}

func sorted(names []string) []string {
	out := slices.Clone(names)
	slices.Sort(out)
	return out
}
