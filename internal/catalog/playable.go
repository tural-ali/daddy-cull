package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Old cameras filed their clips as AVI (Motion JPEG with µ-law sound) and MPG
// (MPEG-2), which no browser plays. Such a clip is played from an MP4 copy
// made on first view: H.264 and AAC, with the index at the front so playback
// starts before the whole copy has arrived. The copy lives in the preview
// cache, keyed like every other derived file, and the archive is only read.

// playableTimeout bounds one conversion. The longest clip in the archive, a
// 448 MB AVI, converts in well under a minute on a home NAS.
const playableTimeout = 5 * time.Minute

// converting holds one lock per copy, so a player's burst of range requests
// waits for the conversion already running rather than starting another.
var converting sync.Map

// playableCopy returns the path of an MP4 copy of a clip, converting it first
// when the cache has none. The clip arrives as an open descriptor proven to be
// inside its mount, and the converter sees nothing else from the request.
func playableCopy(ctx context.Context, tool string, file *os.File, cacheDir, subject string, size, mtime int64) (string, error) {
	if tool == "" || cacheDir == "" {
		return "", fmt.Errorf("no converter or cache configured")
	}
	sum := sha256.Sum256([]byte("play1:" + subject + ":" + strconv.FormatInt(size, 10) + ":" + strconv.FormatInt(mtime, 10)))
	key := filepath.Join(cacheDir, hex.EncodeToString(sum[:])[:32]+".mp4")
	lock, _ := converting.LoadOrStore(key, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	if _, err := os.Stat(key); err == nil {
		return key, nil
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", err
	}
	// Written under a temporary name so a reader never gets a half-made copy.
	temporary, err := os.CreateTemp(cacheDir, "tmp-*.mp4")
	if err != nil {
		return "", err
	}
	temporary.Close()
	defer os.Remove(temporary.Name())
	_, err = withWorker(ctx, func() ([]byte, error) {
		if _, err := file.Seek(0, 0); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, playableTimeout)
		defer cancel()
		command := exec.CommandContext(ctx, tool, "-nostdin", "-loglevel", "error", "-y", "-threads", "4",
			"-i", "/dev/fd/3", "-map", "0:v:0", "-map", "0:a:0?",
			// Even dimensions, which yuv420p requires; old cameras recorded
			// some odd sizes.
			"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", "-pix_fmt", "yuv420p",
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-c:a", "aac", "-b:a", "128k",
			"-movflags", "+faststart", "-f", "mp4", temporary.Name())
		command.ExtraFiles = []*os.File{file}
		if out, err := command.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("convert: %v: %s", err, out)
		}
		return nil, nil
	})
	if err != nil {
		return "", err
	}
	if err = os.Rename(temporary.Name(), key); err != nil {
		return "", err
	}
	return key, nil
}
