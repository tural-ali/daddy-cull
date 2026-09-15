package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// previewWorkers bounds how many decoders run at once. A gallery asks for a
// hundred tiles in one burst, and an unbounded fan-out would start a hundred
// subprocesses reading a network mount.
var previewWorkers = make(chan struct{}, 4)

// previewTimeout keeps one unreadable file from holding a worker forever.
const previewTimeout = 20 * time.Second

// cachedBytes returns a generated preview, building it with produce only when
// the cache has no copy.
//
// The key includes size and modification time, so a file replaced on disk yields
// a new key rather than a stale tile, and kind separates the several previews
// that can be derived from one asset. A cache directory that cannot be written
// is not an error: the preview is simply rebuilt each time. Nothing is ever
// written back to a media mount.
func cachedBytes(cacheDir, kind string, id, size, mtime int64, produce func() ([]byte, error)) ([]byte, error) {
	key := ""
	if cacheDir != "" {
		sum := sha256.Sum256([]byte(kind + ":" + strconv.FormatInt(id, 10) + ":" + strconv.FormatInt(size, 10) + ":" + strconv.FormatInt(mtime, 10)))
		key = filepath.Join(cacheDir, hex.EncodeToString(sum[:])[:32]+".jpg")
		if cached, err := os.ReadFile(key); err == nil {
			return cached, nil
		}
	}
	built, err := produce()
	if err != nil {
		return nil, err
	}
	if key != "" && os.MkdirAll(cacheDir, 0o700) == nil {
		// Written under a temporary name so a concurrent reader never sees a
		// half-written tile.
		if temporary, tempErr := os.CreateTemp(cacheDir, "tmp-*"); tempErr == nil {
			_, writeErr := temporary.Write(built)
			temporary.Close()
			if writeErr == nil {
				os.Rename(temporary.Name(), key)
			} else {
				os.Remove(temporary.Name())
			}
		}
	}
	return built, nil
}

// withWorker runs produce while holding one of the bounded decoder slots.
func withWorker(ctx context.Context, produce func() ([]byte, error)) ([]byte, error) {
	select {
	case previewWorkers <- struct{}{}:
		defer func() { <-previewWorkers }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return produce()
}

// shrinkToTile reduces a full-size image to the size the gallery draws, as a
// JPEG. Some decoders cannot scale on the way out, so their output arrives at
// full resolution and is reduced here instead.
func shrinkToTile(source io.Reader) ([]byte, error) {
	decoded, _, err := image.Decode(source)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if err = jpeg.Encode(&buffer, downscale(decoded, gridPixels), &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
