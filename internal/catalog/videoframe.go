package catalog

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// The kind names the second generation of frames: the first was cut
	// without tone mapping, so an HDR clip's frame came out grey.
	return cachedBytes(cacheDir, "frame2", subject, size, mtime, func() ([]byte, error) {
		return withWorker(ctx, func() ([]byte, error) {
			hdr := isHDR(ctx, probeTool(tool), file)
			// A second past the start avoids the black or half-faded opening
			// frame most phone clips begin with; a clip shorter than that falls
			// back to its first.
			frame, err := runFrameExtractor(ctx, tool, file, "1", true, hdr)
			if err != nil || len(frame) == 0 {
				frame, err = runFrameExtractor(ctx, tool, file, "0", true, hdr)
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
			full, err := runFrameExtractor(ctx, tool, file, "", false, false)
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

// hdrTransfers are the transfer characteristics of an HDR clip: HLG, which an
// iPhone records, and PQ. A frame cut from either straight into an SDR JPEG
// comes out grey and flat, its values never meant for that range.
var hdrTransfers = map[string]bool{"arib-std-b67": true, "smpte2084": true}

// toneMapFilter brings an HDR frame down to SDR the way a player would show
// it: linear light, BT.709 primaries, the Hable curve, then back to video
// range. It follows the scale, so the work is done on the small frame.
const toneMapFilter = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p"

// probeTool is the prober that ships beside the extractor, ffprobe next to
// ffmpeg, or empty when there is none to be found.
func probeTool(tool string) string {
	dir, base := filepath.Split(tool)
	if !strings.HasSuffix(base, "ffmpeg") {
		return ""
	}
	probe := strings.TrimSuffix(base, "ffmpeg") + "ffprobe"
	if dir == "" {
		resolved, err := exec.LookPath(probe)
		if err != nil {
			return ""
		}
		return resolved
	}
	probe = filepath.Join(dir, probe)
	if _, err := os.Stat(probe); err != nil {
		return ""
	}
	return probe
}

// isHDR reports whether the clip's first video stream is tagged HDR. A clip the
// prober cannot read counts as SDR: its frame still comes out, at worst dull.
func isHDR(ctx context.Context, probe string, file *os.File) bool {
	if probe == "" {
		return false
	}
	if _, err := file.Seek(0, 0); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, probe, "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=color_transfer", "-of", "csv=p=0", "/dev/fd/3")
	command.ExtraFiles = []*os.File{file}
	out, err := command.Output()
	if err != nil {
		return false
	}
	return hdrTransfers[strings.TrimSpace(string(out))]
}

// runFrameExtractor decodes one frame to JPEG on standard output. seconds is the
// offset to seek to, empty for none, and scale asks the decoder to size the
// frame itself, which only works for streams it filters simply. toneMap brings
// an HDR frame down to SDR on the way, and needs scale.
func runFrameExtractor(ctx context.Context, tool string, file *os.File, seconds string, scale, toneMap bool) ([]byte, error) {
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
		filter := fmt.Sprintf("scale='min(%d,iw)':-2", gridPixels)
		if toneMap {
			filter += "," + toneMapFilter
		}
		arguments = append(arguments, "-vf", filter)
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
