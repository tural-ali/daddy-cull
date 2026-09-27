package catalog

import (
	"context"
	"strings"
	"testing"
)

func generation(t *testing.T, s *Store) int64 {
	t.Helper()
	n, err := s.CatalogueGeneration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Files added or removed move the generation on; a person deciding on a page
// does not, because that page already shows it.
func TestCatalogueGenerationFollowsFilesNotDecisions(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	start := generation(t, s)
	for _, statement := range []string{
		"INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/2026/2026-09/2026-09-26/IMG_0001.HEIC',1,'image',100,'archive')",
		"INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(2,'/archive/2026/2026-09/2026-09-26/IMG_0002.HEIC',1,'image',200,'archive')",
	} {
		if _, err := s.write.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if got := generation(t, s); got != start+2 {
		t.Fatalf("after two files added: %d, want %d", got, start+2)
	}
	if _, err := s.Decide(ctx, Decision{RequestID: "page-decision-1", AssetID: 1, Status: "keep"}); err != nil {
		t.Fatal(err)
	}
	if got := generation(t, s); got != start+2 {
		t.Fatalf("a decision moved it: %d", got)
	}
	if _, err := s.write.Exec("DELETE FROM decisions WHERE asset_id=2; DELETE FROM assets WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	if got := generation(t, s); got != start+3 {
		t.Fatalf("after a file removed: %d, want %d", got, start+3)
	}
	root := "/mnt/user/family-archive"
	line := root + "/2026/2026-09/2026-09-26/IMG_0001.HEIC\t100\talex\t2026/09/26/IMG_0001.HEIC\t2026-09-28T00:00:00+00:00\n"
	// Asset 1 is kept, so the phone deletion leaves it and nothing changes.
	if _, err := s.MarkPhoneDeletions(ctx, root, strings.NewReader(line)); err != nil {
		t.Fatal(err)
	}
	if got := generation(t, s); got != start+3 {
		t.Fatalf("a deletion that marked nothing moved it: %d", got)
	}
	if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(3,'/archive/2026/2026-09/2026-09-26/IMG_0003.HEIC',1,'image',300,'archive')"); err != nil {
		t.Fatal(err)
	}
	line = root + "/2026/2026-09/2026-09-26/IMG_0003.HEIC\t300\talex\t2026/09/26/IMG_0003.HEIC\t2026-09-28T00:00:00+00:00\n"
	if _, err := s.MarkPhoneDeletions(ctx, root, strings.NewReader(line)); err != nil {
		t.Fatal(err)
	}
	if got := generation(t, s); got != start+5 {
		t.Fatalf("after a file added and marked by a phone deletion: %d, want %d", got, start+5)
	}
}
