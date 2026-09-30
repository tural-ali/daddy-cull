package catalog

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"daddy-cull/next/internal/api"
)

// A RAW and the JPEG and HEIC exported from it are one photo on the day page,
// but they are different files: the camera wrote the RAW, an editor wrote
// the exports at another size and crop, maybe much later. The viewer's Info
// shows what each file says about itself, read from the file on request with
// the metadata reader, so switching between the formats shows which is which.

// detailsTimeout bounds one read. The reader stops after the headers.
const detailsTimeout = 10 * time.Second

// FileDetails is what one file records about itself.
type FileDetails struct {
	// Camera is the make and model that took it, such as SONY ILCE-7M4.
	Camera string `json:"camera,omitempty"`
	// Lens is the lens it was taken with, such as FE 24-105mm F4 G OSS.
	Lens string `json:"lens,omitempty"`
	// Shutter is the exposure time as the camera writes it, such as 1/250.
	Shutter string `json:"shutter,omitempty"`
	// Aperture is the f-number, such as 4.5.
	Aperture float64 `json:"aperture,omitempty"`
	// ISO is the sensitivity, such as 100.
	ISO int `json:"iso,omitempty"`
	// Focal is the focal length in millimetres, such as 46.
	Focal float64 `json:"focal,omitempty"`
	// Software is what wrote the file last, such as the camera's firmware or
	// the editor it was exported from.
	Software string `json:"software,omitempty"`
	// Width and Height are the picture's size as it is shown, turned by its
	// orientation.
	Width int `json:"width,omitempty"`
	// Height is the picture's height as it is shown.
	Height int `json:"height,omitempty"`
	// Located is whether the file records where it was taken.
	Located bool `json:"located"`
	// Modified is when the file itself last changed, which for an export is
	// usually when it was exported.
	Modified time.Time `json:"modified"`
}

// DetailsRoute answers GET /api/assets/{id}/details from the file itself.
func (s *Store) DetailsRoute(m *api.Mux, roots MediaRoots) {
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/assets/{id}/details", Tag: "Media", Needs: api.Read,
		Summary: "Read what a file records about itself",
		Doc:     "The camera, lens, exposure, size and software a file records, read from the file now, so each file of a RAW and its exports can be told apart. A file in the Bin is read from the Bin.",
		Params:  []api.Param{api.PathInt("id", "The file's id in the catalogue.", "1")},
		Returns: FileDetails{},
		Errors:  []api.Error{{Status: 404, When: "There is no such file, or it cannot be read now."}},
	}, func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			api.Fail(w, 404, "There is no such file.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), detailsTimeout)
		defer cancel()
		details, ok := s.fileDetails(ctx, roots, id)
		if !ok {
			api.Fail(w, 404, "The file could not be read. It may have moved since the last scan.")
			return
		}
		writeJSON(w, details)
	})
}

// fileDetails finds the file as the media handler does, the Bin's copy for a
// file in the Bin, and reads it.
func (s *Store) fileDetails(ctx context.Context, roots MediaRoots, id int64) (FileDetails, bool) {
	if roots.RawTool == "" {
		return FileDetails{}, false
	}
	var relative, state, planID string
	if s.read.QueryRowContext(ctx, "SELECT a.relative_path,COALESCE(fs.state,''),COALESCE(fs.plan_id,'') FROM assets a LEFT JOIN file_state fs ON fs.asset_id=a.id WHERE a.id=?", id).Scan(&relative, &state, &planID) != nil {
		return FileDetails{}, false
	}
	servedID := id
	if state == "bin" || state == "quarantining" {
		if binned, _, ok := s.binnedAsset(ctx, planID, relative); ok {
			relative, servedID = binned, 0
		}
	}
	mountRoot, inMount, known := roots.root(relative, servedID)
	if !known {
		return FileDetails{}, false
	}
	root, err := os.OpenRoot(mountRoot)
	if err != nil {
		return FileDetails{}, false
	}
	defer root.Close()
	file, err := root.Open(inMount)
	if err != nil {
		return FileDetails{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return FileDetails{}, false
	}
	// A # asks for the number rather than the reader's wording of it; the
	// exposure time keeps its wording, the fraction a camera shows.
	command := exec.CommandContext(ctx, roots.RawTool, "-j", "-fast", "-Make", "-Model", "-LensModel", "-Lens", "-ExposureTime", "-FNumber#", "-ISO#",
		"-FocalLength#", "-Software", "-ImageWidth#", "-ImageHeight#", "-Orientation#", "-Rotation#", "-GPSLatitude#", "/dev/fd/3")
	command.ExtraFiles = []*os.File{file}
	out, err := command.Output()
	if ctx.Err() != nil || len(out) == 0 {
		return FileDetails{}, false
	}
	details := parseDetails(out)
	details.Modified = info.ModTime().UTC()
	return details, true
}

// parseDetails reads the metadata reader's answer. A tag the file does not
// have is left out; the reader writes numbers where it can and text where it
// cannot, so every value is read as either.
func parseDetails(out []byte) FileDetails {
	var answers []map[string]any
	if json.Unmarshal(out, &answers) != nil || len(answers) != 1 {
		return FileDetails{}
	}
	answer := answers[0]
	text := func(key string) string {
		switch value := answer[key].(type) {
		case string:
			return strings.TrimSpace(value)
		case float64:
			return strconv.FormatFloat(value, 'f', -1, 64)
		}
		return ""
	}
	number := func(key string) float64 {
		value, err := strconv.ParseFloat(text(key), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
			return 0
		}
		return value
	}
	var details FileDetails
	maker, model := text("Make"), text("Model")
	switch {
	case model == "":
		details.Camera = maker
	case maker == "" || strings.HasPrefix(strings.ToLower(model), strings.ToLower(firstWord(maker))):
		details.Camera = model
	default:
		details.Camera = firstWord(maker) + " " + model
	}
	details.Lens = text("LensModel")
	if details.Lens == "" {
		details.Lens = text("Lens")
	}
	details.Shutter = text("ExposureTime")
	details.Aperture = math.Round(number("FNumber")*10) / 10
	details.ISO = int(math.Round(number("ISO")))
	details.Focal = math.Round(number("FocalLength")*10) / 10
	details.Software = text("Software")
	_, details.Located = answer["GPSLatitude"]
	// The size is read the way the grid reads it, turned by its orientation.
	shape, _ := json.Marshal([]map[string]any{{"ImageWidth": answer["ImageWidth"], "ImageHeight": answer["ImageHeight"], "Orientation": answer["Orientation"], "Rotation": answer["Rotation"]}})
	details.Width, details.Height = parseShape(shape)
	return details
}

// firstWord is a maker's short name, NIKON for NIKON CORPORATION, so a
// model that does not repeat it reads as NIKON Z 6 and one that does, such as
// Canon EOS R5, is not written Canon Canon EOS R5.
func firstWord(maker string) string {
	name, _, _ := strings.Cut(maker, " ")
	return name
}
