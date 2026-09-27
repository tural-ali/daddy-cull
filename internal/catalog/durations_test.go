package catalog

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Each video's running time is read once, through the read-only mount, and
// shown on its tile. A clip the prober cannot read is not tried again until it
// changes; one that cannot be reached, or sits in the Bin, is left alone.
func TestVideoDurationsAreReadOnce(t *testing.T) {
	tool, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	probe := probeTool(tool)
	if probe == "" {
		t.Skip("ffprobe not installed")
	}
	ctx := context.Background()
	s := testStore(t)
	root := t.TempDir()
	day := filepath.Join(root, "2025", "2025-04", "2025-04-08")
	if err = os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	clip := func(name string, args ...string) {
		out, err := exec.Command(tool, append([]string{"-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=64x48:rate=25"}, append(args, filepath.Join(day, name))...)...).CombinedOutput()
		if err != nil {
			t.Fatal(name, err, string(out))
		}
	}
	clip("IMG_8157.MOV", "-t", "2.4", "-pix_fmt", "yuv420p")
	clip("MOV03466.AVI", "-t", "1", "-c:v", "mjpeg")
	clip("CLIP0001.MPG", "-t", "3")
	if err = os.WriteFile(filepath.Join(day, "BROKEN.MOV"), []byte("not a video at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	binned := filepath.Join(day, "BINNED.MOV")
	if err = os.WriteFile(binned, []byte("in the Bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id         int64
		name, kind string
	}{{1, "IMG_8157.MOV", "video"}, {2, "MOV03466.AVI", "video"}, {3, "CLIP0001.MPG", "image"}, {4, "BROKEN.MOV", "video"}, {5, "GONE.MOV", "video"}, {6, "BINNED.MOV", "video"}, {7, "IMG_8156.DNG", "raw"}} {
		if _, err = s.write.ExecContext(ctx, "INSERT INTO assets(id,relative_path,captured_at,kind,size_bytes,source_id) VALUES(?,?,1,?,100,'archive')", row.id, "/archive/2025/2025-04/2025-04-08/"+row.name, row.kind); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.write.ExecContext(ctx, "INSERT INTO file_state(asset_id,state,plan_id) VALUES(6,'bin','plan')"); err != nil {
		t.Fatal(err)
	}
	roots := MediaRoots{Archive: root, FFmpeg: tool}

	read, err := s.FillDurations(ctx, roots, probe)
	if err != nil {
		t.Fatal(err)
	}
	if read != 4 {
		t.Fatalf("recorded %d, want the three clips and the broken file", read)
	}
	seconds := map[int64]float64{}
	rows, err := s.read.QueryContext(ctx, "SELECT asset_id,seconds FROM video_durations")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var value float64
		if err = rows.Scan(&id, &value); err != nil {
			t.Fatal(err)
		}
		seconds[id] = value
	}
	rows.Close()
	near := func(id int64, want float64) {
		t.Helper()
		if got, ok := seconds[id]; !ok || math.Abs(got-want) > 0.3 {
			t.Fatalf("asset %d: %v (recorded %v), want about %v", id, got, ok, want)
		}
	}
	near(1, 2.4)
	near(2, 1)
	near(3, 3)
	if got, ok := seconds[4]; !ok || got != 0 {
		t.Fatalf("the broken file is recorded as unreadable: %v %v", got, ok)
	}
	for _, id := range []int64{5, 6, 7} {
		if _, ok := seconds[id]; ok {
			t.Fatalf("asset %d should not have been read", id)
		}
	}

	if read, err = s.FillDurations(ctx, roots, probe); err != nil || read != 0 {
		t.Fatalf("a second pass reads nothing: %d %v", read, err)
	}
	// A file replaced by another of a different size is read again, and its
	// old running time is not shown in the meantime.
	if _, err = s.write.ExecContext(ctx, "UPDATE assets SET size_bytes=200 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	assets := []*Asset{{ID: 1, Path: "/archive/2025/2025-04/2025-04-08/IMG_8157.MOV"}, {ID: 2, Path: "/archive/2025/2025-04/2025-04-08/MOV03466.AVI"}, {ID: 4, Path: "/archive/2025/2025-04/2025-04-08/BROKEN.MOV"}}
	if err = s.markDurations(ctx, assets); err != nil {
		t.Fatal(err)
	}
	if assets[0].Duration != 0 || math.Abs(assets[1].Duration-1) > 0.3 || assets[2].Duration != 0 {
		t.Fatalf("marked %v %v %v", assets[0].Duration, assets[1].Duration, assets[2].Duration)
	}
	if read, err = s.FillDurations(ctx, roots, probe); err != nil || read != 1 {
		t.Fatalf("the changed file is read again: %d %v", read, err)
	}
	if err = s.markDurations(ctx, assets); err != nil || math.Abs(assets[0].Duration-2.4) > 0.3 {
		t.Fatalf("after the re-read: %v %v", assets[0].Duration, err)
	}
}
