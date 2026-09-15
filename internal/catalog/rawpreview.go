package catalog

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// rawImage lists the sensor formats the archive holds. A browser cannot decode
// any of them, but every one carries a full-size JPEG preview written by the
// camera, so a tile can be made without demosaicing anything.
var rawImage = map[string]bool{".arw": true, ".dng": true, ".cr2": true, ".cr3": true, ".nef": true, ".raf": true, ".orf": true, ".rw2": true}

// rawTags are the embedded images to try, largest first. Every file in this
// archive answered PreviewImage when sampled, but a camera that writes only a
// thumbnail still gets a tile rather than a blank card.
var rawTags = []string{"PreviewImage", "JpgFromRaw", "ThumbnailImage"}

// rawPreview returns a gallery tile for a RAW file, cached on disk.
//
// The camera's own embedded preview is read rather than the sensor data, which
// is why this costs a fifth of a second on a 45 MB frame instead of two seconds:
// nothing is demosaiced, and only the bytes of the embedded JPEG are pulled over
// the mount. It is then reduced through the same path as every other tile, so a
// 200 KB preview lands at roughly 50 KB.
//
// As with video frames, the extractor is handed the already-validated descriptor
// rather than a path, so no second lookup can resolve anywhere else.
func rawPreview(ctx context.Context, tool string, file *os.File, cacheDir, subject string, size, mtime int64) ([]byte, error) {
	if tool == "" {
		return nil, fmt.Errorf("no raw extractor configured")
	}
	return cachedBytes(cacheDir, "raw", subject, size, mtime, func() ([]byte, error) {
		return withWorker(ctx, func() ([]byte, error) {
			for _, tag := range rawTags {
				embedded, err := runRawExtractor(ctx, tool, file, tag)
				if err == nil && len(embedded) > 0 {
					return shrinkToTile(bytes.NewReader(embedded))
				}
			}
			// Some files carry a RAW extension but hold an ordinary JPEG, which
			// has no embedded preview because it is the picture. Decoding the
			// file directly is both how that is detected and how it is served.
			if _, err := file.Seek(0, 0); err == nil {
				if tile, err := shrinkToTile(file); err == nil {
					return tile, nil
				}
			}
			return nil, fmt.Errorf("no embedded preview")
		})
	})
}

func runRawExtractor(ctx context.Context, tool string, file *os.File, tag string) ([]byte, error) {
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, previewTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, tool, "-b", "-"+tag, "/dev/fd/3")
	command.ExtraFiles = []*os.File{file}
	var out, errorOutput bytes.Buffer
	command.Stdout = &out
	command.Stderr = &errorOutput
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("raw preview extraction failed: %w: %s", err, errorOutput.String())
	}
	return out.Bytes(), nil
}
