package catalog

import (
	"context"
	"strings"
	"testing"
)

func TestImportEvidenceMatchesKnownAssetWithoutChangingDecisions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/2020/2020-01/2020-01-02/A.JPG',1,'image',100,'archive')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.ExecContext(ctx, "INSERT INTO decisions VALUES(1,'keep',1,4)"); err != nil {
		t.Fatal(err)
	}
	line := `{"path":"/archive/2020/2020-01/2020-01-02/A.JPG","source":"archive","size":100,"mtime":123,"partialSignature":"0123456789abcdef0123456789abcdef","fullHash":"abcdef0123456789abcdef0123456789","perceptualHash":"0123456789abcdef","hashedAt":"2026-09-07T10:00:00Z"}` + "\n"
	if err := s.ImportEvidence(ctx, strings.NewReader(line)); err != nil {
		t.Fatal(err)
	}
	var fullHash, status string
	var revision int
	if err := s.read.QueryRowContext(ctx, "SELECT e.full_hash,d.status,d.revision FROM asset_evidence e JOIN decisions d ON d.asset_id=e.asset_id WHERE e.asset_id=1").Scan(&fullHash, &status, &revision); err != nil {
		t.Fatal(err)
	}
	if fullHash != "abcdef0123456789abcdef0123456789" || status != "keep" || revision != 4 {
		t.Fatalf("unexpected imported state: %s %s %d", fullHash, status, revision)
	}
}

func TestImportEvidenceRejectsStaleOrUnknownAssetsAtomically(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(1,'/archive/A.JPG',1,'image',100,'archive')"); err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("{\"path\":\"/archive/A.JPG\",\"source\":\"archive\",\"size\":99,\"fullHash\":\"abcdef0123456789abcdef0123456789\"}\n")
	if err := s.ImportEvidence(ctx, input); err == nil {
		t.Fatal("accepted evidence for a changed file")
	}
	var count int
	if err := s.read.QueryRowContext(ctx, "SELECT count(*) FROM asset_evidence").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial evidence import: %d %v", count, err)
	}
}
