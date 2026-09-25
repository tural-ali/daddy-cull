package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const socialHeader = "score\tpath\tevidence\tsize\twidth\theight\tduration\tletterbox_top\tletterbox_bottom\tposter\n"

func socialReport(rows ...string) string {
	return socialHeader + strings.Join(rows, "")
}

func TestImportSocialReportLinksExistingArchiveAssets(t *testing.T) {
	store := testStore(t)
	if _, err := store.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(7,'/archive/2021/2021-05/2021-05-14/clip.mp4',1,'video',4,'archive')"); err != nil {
		t.Fatal(err)
	}
	report := socialReport("12\t/host/archive/2021/2021-05/2021-05-14/clip.mp4\tuuid filename, low bpp\t2048\t480\t848\t15.0\t28\t28\t00001.jpg\n")
	count, err := store.ImportSocialReport(context.Background(), strings.NewReader(report), "/host/archive")
	if err != nil || count != 1 {
		t.Fatalf("import failed: %d %v", count, err)
	}
	page, err := store.SocialCandidates(context.Background(), "", 0, 50)
	if err != nil || page.Total != 1 || page.Likely != 1 {
		t.Fatalf("unexpected page: %+v %v", page, err)
	}
	item := page.Items[0]
	if item.Asset.ID != 7 {
		t.Fatalf("candidate should reuse the catalogued asset, got %d", item.Asset.ID)
	}
	if item.Band != "likely" || !item.Letterbox || !item.Poster || item.Day != "2021-05-14" {
		t.Fatalf("unexpected item: %+v", item)
	}
	// The archive row must not be duplicated by the import.
	var assets int
	if err = store.read.QueryRow("SELECT count(*) FROM assets WHERE source_id='archive'").Scan(&assets); err != nil || assets != 1 {
		t.Fatalf("archive assets: %d %v", assets, err)
	}
}

func TestImportSocialReportBandsAndFilters(t *testing.T) {
	store := testStore(t)
	report := socialReport(
		"14\t/host/archive/2021/2021-05/2021-05-14/a.mp4\tuuid filename\t10\t480\t848\t9\t20\t20\t00001.jpg\n",
		"7\t/host/archive/2021/2021-05/2021-05-15/b.mp4\tportrait\t10\t720\t1280\t9\t0\t0\t00002.jpg\n",
		"3\t/host/archive/2021/2021-05/2021-05-16/c.mp4\tlow bpp\t10\t720\t1280\t9\t30\t30\t\n",
	)
	if _, err := store.ImportSocialReport(context.Background(), strings.NewReader(report), "/host/archive"); err != nil {
		t.Fatal(err)
	}
	page, err := store.SocialCandidates(context.Background(), "", 0, 50)
	if err != nil || page.Total != 3 || page.Likely != 1 || page.Possible != 1 || page.Watch != 1 || page.Letterboxed != 2 {
		t.Fatalf("unexpected totals: %+v %v", page, err)
	}
	if page.Shown != 3 {
		t.Fatalf("unfiltered page should show every candidate, got %d", page.Shown)
	}
	if page.Items[0].Score != 14 {
		t.Fatalf("candidates should be ranked by score, got %d first", page.Items[0].Score)
	}
	// Shown has to follow the filter, or the pager offers a next page that is not there.
	// Likely social is strong evidence or a letterbox: a (14) and c (weak, but
	// letterboxed). b has some evidence and no letterbox, so it is a maybe.
	if page.Social != 2 || page.Unsure != 1 {
		t.Fatalf("likely social %d, not sure %d, want 2 and 1", page.Social, page.Unsure)
	}
	for band, want := range map[string]int{"likely": 1, "possible": 1, "watch": 1, "letterboxed": 2, "social": 2, "unsure": 1} {
		filtered, filterErr := store.SocialCandidates(context.Background(), band, 0, 50)
		if filterErr != nil || len(filtered.Items) != want {
			t.Fatalf("band %q returned %d items: %v", band, len(filtered.Items), filterErr)
		}
		if filtered.Shown != want || filtered.Total != 3 {
			t.Fatalf("band %q reported shown=%d total=%d, want shown=%d total=3", band, filtered.Shown, filtered.Total, want)
		}
	}
	if _, err = store.SocialCandidates(context.Background(), "nonsense", 0, 50); err != ErrInvalid {
		t.Fatalf("unknown band should be rejected, got %v", err)
	}
}

func TestImportSocialReportRejectsPathsOutsideTheArchive(t *testing.T) {
	store := testStore(t)
	report := socialReport("12\t/somewhere/else/clip.mp4\tuuid filename\t10\t480\t848\t9\t0\t0\t00001.jpg\n")
	if _, err := store.ImportSocialReport(context.Background(), strings.NewReader(report), "/host/archive"); err == nil {
		t.Fatal("a candidate outside the archive must be refused")
	}
}

func TestImportSocialReportRejectsPosterPathEscape(t *testing.T) {
	store := testStore(t)
	report := socialReport("12\t/host/archive/2021/2021-05/2021-05-14/clip.mp4\tuuid\t10\t480\t848\t9\t0\t0\t../../etc/passwd\n")
	if _, err := store.ImportSocialReport(context.Background(), strings.NewReader(report), "/host/archive"); err == nil {
		t.Fatal("a poster name containing a path must be refused")
	}
}

// The detector walked the whole share, Bin included, so its report names files
// the Bin already holds. Offering one would ask for a decision on a file that is
// already out of the archive, and the Bin would refuse to take it a second time.
func TestImportSocialReportSkipsWhatTheBinHolds(t *testing.T) {
	store := testStore(t)
	report := socialReport(
		"6\t/host/archive/.culled/2021-12-29/b0426c5a.MOV\thex32 filename\t10\t1080\t1920\t18\t0\t0\t01093.jpg\n",
		"8\t/host/archive/2021/2021-05/2021-05-15/b.mp4\tportrait\t10\t480\t848\t9\t0\t0\t00002.jpg\n",
	)
	count, err := store.ImportSocialReport(context.Background(), strings.NewReader(report), "/host/archive")
	if err != nil || count != 1 {
		t.Fatalf("import: %d %v", count, err)
	}
	page, err := store.SocialCandidates(context.Background(), "", 0, 50)
	if err != nil || page.Total != 1 || page.Items[0].Name != "b.mp4" {
		t.Fatalf("a file in the Bin must not be offered: %+v %v", page, err)
	}
	var binAssets int
	if err = store.read.QueryRow("SELECT count(*) FROM assets WHERE relative_path LIKE '%/.culled/%'").Scan(&binAssets); err != nil || binAssets != 0 {
		t.Fatalf("a Bin file must not enter the catalogue: %d %v", binAssets, err)
	}
}

func TestImportSocialReportReplacesPreviousRows(t *testing.T) {
	store := testStore(t)
	first := socialReport("12\t/host/archive/2021/2021-05/2021-05-14/a.mp4\tuuid\t10\t480\t848\t9\t0\t0\t00001.jpg\n")
	if _, err := store.ImportSocialReport(context.Background(), strings.NewReader(first), "/host/archive"); err != nil {
		t.Fatal(err)
	}
	second := socialReport("8\t/host/archive/2021/2021-05/2021-05-15/b.mp4\tportrait\t10\t480\t848\t9\t0\t0\t00002.jpg\n")
	if _, err := store.ImportSocialReport(context.Background(), strings.NewReader(second), "/host/archive"); err != nil {
		t.Fatal(err)
	}
	page, err := store.SocialCandidates(context.Background(), "", 0, 50)
	if err != nil || page.Total != 1 || page.Items[0].Name != "b.mp4" {
		t.Fatalf("a re-import should replace derived rows: %+v %v", page, err)
	}
}

func TestSocialPosterHandlerServesOnlyCataloguedNames(t *testing.T) {
	store := testStore(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "00001.jpg"), []byte("jpegbytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.jpg"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := socialReport(
		"12\t/host/archive/2021/2021-05/2021-05-14/a.mp4\tuuid\t10\t480\t848\t9\t0\t0\t00001.jpg\n",
		"11\t/host/archive/2021/2021-05/2021-05-15/b.mp4\tuuid\t10\t480\t848\t9\t0\t0\t\n",
	)
	if _, err := store.ImportSocialReport(context.Background(), strings.NewReader(report), "/host/archive"); err != nil {
		t.Fatal(err)
	}
	page, _ := store.SocialCandidates(context.Background(), "", 0, 50)
	withPoster, withoutPoster := int64(0), int64(0)
	for _, item := range page.Items {
		if item.Poster {
			withPoster = item.Asset.ID
		} else {
			withoutPoster = item.Asset.ID
		}
	}
	handler := store.SocialPosterHandler(root)
	serve := func(target string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("GET", target, nil)
		request.SetPathValue("id", strings.TrimPrefix(target, "/api/social-poster/"))
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	if got := serve("/api/social-poster/" + strconv.FormatInt(withPoster, 10)); got.Code != http.StatusOK || got.Body.String() != "jpegbytes" {
		t.Fatalf("poster should be served: %d %q", got.Code, got.Body.String())
	}
	if got := serve("/api/social-poster/" + strconv.FormatInt(withoutPoster, 10)); got.Code != http.StatusNotFound {
		t.Fatalf("a candidate with no poster must 404, got %d", got.Code)
	}
	if got := serve("/api/social-poster/9999"); got.Code != http.StatusNotFound {
		t.Fatalf("an unknown candidate must 404, got %d", got.Code)
	}
}

// A decision is the only way off this page, so the list, the band counts and the
// pager all have to honour it; otherwise a reviewer is handed the same clip twice.
func TestSocialCandidatesDropDecidedFiles(t *testing.T) {
	store := testStore(t)
	report := socialReport(
		"14\t/host/archive/2021/2021-05/2021-05-14/a.mp4\tuuid filename\t10\t480\t848\t9\t20\t20\t00001.jpg\n",
		"12\t/host/archive/2021/2021-05/2021-05-15/b.mp4\tuuid filename\t10\t480\t848\t9\t0\t0\t00002.jpg\n",
		"7\t/host/archive/2021/2021-05/2021-05-16/c.mp4\tportrait\t10\t720\t1280\t9\t0\t0\t00003.jpg\n",
	)
	ctx := context.Background()
	if _, err := store.ImportSocialReport(ctx, strings.NewReader(report), "/host/archive"); err != nil {
		t.Fatal(err)
	}
	page, err := store.SocialCandidates(ctx, "", 0, 50)
	if err != nil || page.Total != 3 || page.Likely != 2 || page.Kept != 0 || page.Marked != 0 {
		t.Fatalf("unexpected starting page: %+v %v", page, err)
	}
	keep, mark := page.Items[0], page.Items[1]
	if _, err = store.DecideBatch(ctx, []Decision{
		{RequestID: "keep-request-0001", AssetID: keep.Asset.ID, ExpectedRevision: keep.Asset.Revision, Status: "keep"},
		{RequestID: "cull-request-0001", AssetID: mark.Asset.ID, ExpectedRevision: mark.Asset.Revision, Status: "cull"},
	}); err != nil {
		t.Fatal(err)
	}
	page, err = store.SocialCandidates(ctx, "", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Shown != 1 || len(page.Items) != 1 {
		t.Fatalf("decided candidates should leave the list, got total=%d shown=%d items=%d", page.Total, page.Shown, len(page.Items))
	}
	if page.Kept != 1 || page.Marked != 1 {
		t.Fatalf("page should report the decisions, got kept=%d marked=%d", page.Kept, page.Marked)
	}
	if page.Likely != 0 || page.Letterboxed != 0 || page.Possible != 1 {
		t.Fatalf("band counts should follow the decisions: %+v", page)
	}
	if page.Bytes != page.Items[0].Asset.Size {
		t.Fatalf("byte total should cover only what is left, got %d", page.Bytes)
	}
	filtered, filterErr := store.SocialCandidates(ctx, "likely", 0, 50)
	if filterErr != nil || len(filtered.Items) != 0 || filtered.Shown != 0 {
		t.Fatalf("a decided band should be empty, got %d items shown=%d: %v", len(filtered.Items), filtered.Shown, filterErr)
	}
	// Undoing the decision has to bring the clip back, or a misclick is permanent.
	if _, err = store.DecideBatch(ctx, []Decision{
		{RequestID: "undo-request-0001", AssetID: keep.Asset.ID, ExpectedRevision: keep.Asset.Revision + 1, Status: "unreviewed"},
	}); err != nil {
		t.Fatal(err)
	}
	page, err = store.SocialCandidates(ctx, "", 0, 50)
	if err != nil || page.Total != 2 || page.Kept != 0 {
		t.Fatalf("an undone keep should return to the list: %+v %v", page, err)
	}
}
