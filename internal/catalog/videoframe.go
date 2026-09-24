package catalog

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// browserBlindStill lists still formats a browser will not decode but the frame
// extractor will. HEIC is the whole iPhone library, so without this the archive's
// largest single group of photographs has no tile at all.
var browserBlindStill = map[string]bool{".heic": true, ".heif": true}

// videoFrame extracts a single still from a video and returns it as a JPEG,
// cached on disk so each file is decoded once rather than once per view.
//
// The video is handed to the decoder as an already-open file descriptor rather
// than a path. The descriptor was opened through os.Root, so it is proven to be
// inside the media mount, and passing it directly means no second path lookup
// can resolve anywhere else in between.
//
// The decoder is given no input other than that descriptor: it never sees a
// request value, and it only ever reads.
func videoFrame(ctx context.Context, tool string, file *os.File, cacheDir, subject string, size, mtime int64) ([]byte, error) {
	if tool == "" {
		return nil, fmt.Errorf("no frame extractor configured")
	}
	return cachedBytes(cacheDir, "frame", subject, size, mtime, func() ([]byte, error) {
		return withWorker(ctx, func() ([]byte, error) {
			// A second past the start avoids the black or half-faded opening
			// frame most phone clips begin with; a clip shorter than that falls
			// back to its first.
			frame, err := runFrameExtractor(ctx, tool, file, "1", true)
			if err != nil || len(frame) == 0 {
				frame, err = runFrameExtractor(ctx, tool, file, "0", true)
			}
			if err != nil {
				return nil, err
			}
			if len(frame) == 0 {
				return nil, fmt.Errorf("no frame decoded")
			}
			return frame, nil
		})
	})
}

// stillFrame decodes a single-image format the browser cannot read, such as
// HEIC, into a JPEG whose longest edge is at most pixels.
//
// It cannot ask the decoder to scale on the way out: a HEIF image is assembled
// from tiles through a complex filtergraph, and the decoder refuses a scale
// filter on a stream fed from one. So the frame comes back at full resolution
// and is reduced here, through the same path as every other tile.
func stillFrame(ctx context.Context, tool string, file *os.File, cacheDir, subject string, size, mtime int64, pixels int) ([]byte, error) {
	if tool == "" {
		return nil, fmt.Errorf("no frame extractor configured")
	}
	return cachedBytes(cacheDir, previewKind("still", pixels), subject, size, mtime, func() ([]byte, error) {
		return withWorker(ctx, func() ([]byte, error) {
			full, err := runFrameExtractor(ctx, tool, file, "", false)
			if err != nil {
				return nil, err
			}
			if len(full) == 0 {
				return nil, fmt.Errorf("no image decoded")
			}
			return shrinkTo(bytes.NewReader(full), pixels)
		})
	})
}

// runFrameExtractor decodes one frame to JPEG on standard output. seconds is the
// offset to seek to, empty for none, and scale asks the decoder to size the
// frame itself, which only works for streams it filters simply.
func runFrameExtractor(ctx context.Context, tool string, file *os.File, seconds string, scale bool) ([]byte, error) {
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	// One thread for the decoder and one for the filter graph. An iPhone HEIC is
	// a grid of dozens of HEVC tiles, each of which would otherwise start a
	// thread per core: hundreds for one photograph, past a container's process
	// limit, and slower besides, since starting them costs more than one frame
	// gains. Parallelism comes from the preview workers instead.
	arguments := []string{"-nostdin", "-loglevel", "error", "-filter_threads", "1", "-threads", "1"}
	if seconds != "" {
		arguments = append(arguments, "-ss", seconds)
	}
	arguments = append(arguments, "-i", "/dev/fd/3", "-frames:v", "1")
	if scale {
		// Downscaling in the decoder rather than afterwards keeps a 4K frame
		// from being carried through memory at full size. -2 keeps the height
		// even, which the JPEG encoder requires for subsampled chroma.
		arguments = append(arguments, "-vf", fmt.Sprintf("scale='min(%d,iw)':-2", gridPixels))
	}
	arguments = append(arguments, "-f", "mjpeg", "-")

	command := exec.CommandContext(ctx, tool, arguments...)
	command.ExtraFiles = []*os.File{file}
	var out, errorOutput bytes.Buffer
	command.Stdout = &out
	command.Stderr = &errorOutput
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("frame extraction failed: %w: %s", err, errorOutput.String())
	}
	return out.Bytes(), nil
}
