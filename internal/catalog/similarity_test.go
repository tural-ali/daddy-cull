package catalog

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func comparisonPicture(light uint8) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 90, 80))
	for y := range 80 {
		for x := range 90 {
			gray := uint8(35+((x/10+y/10)%4)*45) + light
			img.Set(x, y, color.RGBA{gray, gray, gray, 255})
		}
	}
	return img
}

func TestVisualFingerprintGuardsAgainstWeakAndDifferentEvidence(t *testing.T) {
	a, b := fingerprint(comparisonPicture(0)), fingerprint(comparisonPicture(8))
	if a.hash == "" || !similarVisual(a, b) {
		t.Fatal("mild exposure changes should be suggestions", a, b)
	}
	other := b
	other.aspect = 2
	if similarVisual(a, other) {
		t.Fatal("different shapes should not match")
	}
	other = b
	other.red += 90
	if similarVisual(a, other) {
		t.Fatal("different colours should not match")
	}
	flat := image.NewRGBA(image.Rect(0, 0, 90, 80))
	if fingerprint(flat).hash != "" {
		t.Fatal("uniform pictures do not prove similarity")
	}
	if visuallyClose("not hex", a.hash) || visuallyClose("", "") {
		t.Fatal("invalid evidence accepted")
	}
}

func TestBurstBuildsVisualEvidenceWithoutExactDuplicateProof(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "2020-01-02"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, comparisonPicture(uint8(i*8))); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("2020-01-02/DSC_%d.png", i)
		if err := os.WriteFile(filepath.Join(root, name), encoded.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,1577923200,'image',?)", i, "/archive/"+name, encoded.Len()); err != nil {
			t.Fatal(err)
		}
		target := shapeTarget{int64(i), "/archive/" + name, int64(encoded.Len())}
		if err := s.indexSimilarity(ctx, MediaRoots{Archive: root, Cache: t.TempDir()}, target); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	found, err := s.Burst(ctx, 1)
	if err != nil || len(found) != 2 || found[1].ComparisonReason != "Similar visual fingerprint" {
		t.Fatal(found, err)
	}
	var count int
	if err = s.read.QueryRow("SELECT count(*) FROM asset_evidence WHERE full_hash IS NOT NULL").Scan(&count); err != nil || count != 0 {
		t.Fatal("similarity leaked into exact evidence", count, err)
	}
	report, err := s.DuplicateWindow(ctx, 50, "")
	if err != nil || len(report.Groups) != 0 {
		t.Fatal("visual suggestion became an exact duplicate", report, err)
	}
	// A replacement invalidates the fingerprint even before the worker runs.
	s.write.Exec("UPDATE assets SET size_bytes=size_bytes+1 WHERE id=2")
	found, err = s.Burst(ctx, 1)
	if err != nil || len(found) != 1 {
		t.Fatal("stale similarity used after replacement", found, err)
	}
}

func TestBurstStaysInExactFolderAndRanksCaptureProximity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, row := range []struct {
		id          int
		name, stamp string
	}{
		{1, "DSC_100.JPG", "2020:01:02 12:00:00"},
		{2, "DSC_999.JPG", "2020:01:02 12:00:07"},
		{3, "nested/DSC_101.JPG", "2020:01:02 12:00:01"},
		{4, "DSC_200.JPG", "2020:01:02 12:00:02"},
	} {
		s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,1577923200,'image',100)", row.id, "/archive/2020-01-02/"+row.name)
		s.write.Exec("INSERT INTO exposures VALUES(?,100,?,'','Sony')", row.id, row.stamp)
	}
	if err := s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	found, err := s.Burst(ctx, 1)
	if err != nil || len(found) != 3 || found[1].ID != 4 || found[2].ID != 2 {
		t.Fatal(found, err)
	}
}

func TestNormalisedFilenamesAloneNeverSuggestABurst(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := 1; i <= 2; i++ {
		s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,1577923200,'image',100)", i, fmt.Sprintf("/archive/2020-01-02/2024-11-20(%d).jpg", i))
	}
	if err := s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	related, err := s.Related(ctx, 1)
	if err != nil || len(related) != 2 {
		t.Fatal("fixture must reproduce legacy filename grouping", related, err)
	}
	found, err := s.Burst(ctx, 1)
	if err != nil || len(found) != 1 {
		t.Fatal("filename grouping leaked into comparison suggestions", found, err)
	}
}

func TestBurstDoesNotDependOnLegacyFilenameBucketLimit(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := 1; i <= 201; i++ {
		s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,1577923200,'image',100)", i, fmt.Sprintf("/archive/2020-01-02/2024-11-20(%d).jpg", i))
	}
	s.write.Exec("INSERT INTO exposures VALUES(1,100,'2020:01:02 12:00:00','','Sony'),(2,100,'2020:01:02 12:00:03','','Sony')")
	if err := s.IndexRelated(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Related(ctx, 1); err == nil {
		t.Fatal("fixture must exceed the legacy filename bucket limit")
	}
	found, err := s.Burst(ctx, 1)
	if err != nil || len(found) != 2 || found[1].ID != 2 {
		t.Fatal("real burst was lost inside a large filename bucket", found, err)
	}
}

func TestComparisonAnalysisIsBoundedAndRetriesUnavailablePreviews(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := 1; i <= 100; i++ {
		s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(?,?,1577923200,'image',100)", i, fmt.Sprintf("/archive/2020-01-02/DSC_%d.JPG", i))
	}
	targets, err := s.similarityTargets(ctx, 1, 100)
	if err != nil || len(targets) != comparisonWindowSize {
		t.Fatal(len(targets), err)
	}
	if err = s.indexSimilarity(ctx, MediaRoots{}, targets[0]); err != nil {
		t.Fatal(err)
	}
	targets, err = s.similarityTargets(ctx, 1, 100)
	if err != nil || len(targets) != 79 {
		t.Fatal("unavailable preview retried immediately", len(targets), err)
	}
	s.write.Exec("UPDATE photo_similarity SET checked_at=? WHERE asset_id=1", time.Now().Add(-25*time.Hour).Unix())
	targets, err = s.similarityTargets(ctx, 1, 100)
	if err != nil || len(targets) != 80 {
		t.Fatal("unavailable preview never retried", len(targets), err)
	}
	s.comparisonRunning.Store(true)
	found, err := s.Burst(ctx, 1)
	if err != nil || len(found) != 1 || !found[0].ComparisonPending || s.comparisonPriority.Load() != 1 {
		t.Fatal("comparison did not schedule local analysis", found, err)
	}
}

func TestSimilarityWorkerFinishesPriorityAndStopsWithoutChangingMedia(t *testing.T) {
	s := testStore(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "2020-01-02"), 0700); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, comparisonPicture(0)); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "2020-01-02/DSC_001.png")
	if err := os.WriteFile(name, encoded.Bytes(), 0400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.write.Exec("INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes) VALUES(1,'/archive/2020-01-02/DSC_001.png',1577923200,'image',?)", encoded.Len()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.comparisonPriority.Store(1)
	done := make(chan struct{})
	go func() { s.KeepSimilarities(ctx, MediaRoots{Archive: root, Cache: t.TempDir()}); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var count int
		err := s.read.QueryRow("SELECT count(*) FROM photo_similarity WHERE fingerprint!=''").Scan(&count)
		if err != nil {
			cancel()
			<-done
			t.Fatal(err)
		}
		if count == 1 && s.comparisonPriority.Load() == 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("priority work never settled")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignored shutdown")
	}
	if s.comparisonRunning.Load() {
		t.Fatal("worker still marked running")
	}
	after, err := os.ReadFile(name)
	if err != nil || !bytes.Equal(after, encoded.Bytes()) {
		t.Fatal("preview analysis changed original media", err)
	}
}
