package takeout

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sidecar is the JSON file Google writes beside each photo, with what Google
// Photos knew about it.
type Sidecar struct {
	// Title is the photo's name as it was uploaded, which the file in the
	// export may have lost to Google's cutting long names short.
	Title string `json:"title"`
	// Description is the caption typed in Google Photos.
	Description string `json:"description"`
	// PhotoTakenTime is when the photo was taken, in seconds since 1970 as
	// a string.
	PhotoTakenTime *Timestamp `json:"photoTakenTime"`
	// GeoData is where it was taken, as Google Photos shows it: from the
	// camera, or set by hand. All zero when unknown.
	GeoData Geo `json:"geoData"`
	// GeoDataExif is where the camera said it was taken.
	GeoDataExif Geo `json:"geoDataExif"`
	// People are the faces named in it.
	People []struct {
		Name string `json:"name"`
	} `json:"people"`
	// Favorited is the star in Google Photos.
	Favorited bool `json:"favorited"`
}

// Timestamp is a time as Google writes it.
type Timestamp struct {
	Timestamp string `json:"timestamp"`
}

// Geo is a place on the map.
type Geo struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// Known reports whether the place was recorded: Google writes zeros when not.
func (g Geo) Known() bool { return g.Latitude != 0 || g.Longitude != 0 }

// ParseSidecar reads a photo's JSON. ok is false for a JSON that is not a
// photo's, such as an album's metadata.json, which has no taken time.
func ParseSidecar(body []byte) (Sidecar, bool) {
	var s Sidecar
	if json.Unmarshal(body, &s) != nil || s.PhotoTakenTime == nil || s.Title == "" {
		return Sidecar{}, false
	}
	return s, true
}

// Taken is when the photo was taken, if Google knew.
func (s Sidecar) Taken() (time.Time, bool) {
	if s.PhotoTakenTime == nil {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(s.PhotoTakenTime.Timestamp, 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0).UTC(), true
}

// Place is where the photo was taken, preferring what the camera recorded.
func (s Sidecar) Place() (Geo, bool) {
	if s.GeoDataExif.Known() {
		return s.GeoDataExif, true
	}
	return s.GeoData, s.GeoData.Known()
}

// PeopleNames are the named faces, in the order Google lists them.
func (s Sidecar) PeopleNames() []string {
	names := make([]string, 0, len(s.People))
	for _, p := range s.People {
		if p.Name != "" {
			names = append(names, p.Name)
		}
	}
	return names
}

// supplemental is what Google has called a photo's JSON since 2024, as in
// IMG_1234.JPG.supplemental-metadata.json. Before then it was IMG_1234.JPG.json.
const supplemental = ".supplemental-metadata"

// editedSuffixes mark the copy Google Photos saves when a photo is edited
// there, in English and German. It has no JSON of its own and shares the
// original's.
var editedSuffixes = []string{"-edited", "-bearbeitet"}

// numbered is a name Google gave a second file of the same name in one
// folder, as in IMG_1234(1).JPG, whose JSON is IMG_1234.JPG(1).json.
var numbered = regexp.MustCompile(`^(.*)\((\d+)\)(\.[^.]*)?$`)

// jsonNumber splits the (n) off the end of a JSON's stem.
var jsonNumber = regexp.MustCompile(`^(.*)\((\d+)\)$`)

// media is one reading of a media file's name: the name its JSON is named
// after, and the (n) Google added, if any.
type media struct {
	base   string
	number string
}

func readings(name string) []media {
	out := []media{{base: name}}
	if m := numbered.FindStringSubmatch(name); m != nil {
		out = append(out, media{base: m[1] + m[3], number: m[2]})
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for _, suffix := range editedSuffixes {
		if strings.HasSuffix(stem, suffix) {
			out = append(out, media{base: strings.TrimSuffix(stem, suffix) + ext})
		}
	}
	return out
}

// shortest is how much of a name Google keeps when it cuts one short.
const shortest = 40

// Pair finds each media file's JSON among the JSON files in the same folder.
// media are the media files' names; sidecars maps each JSON file's name to
// the title inside it. It answers the JSON's name for each media file that
// has one.
//
// Google names the JSON after the photo, cuts both short when the name is
// long, moves a (n) to the end, shares the original's JSON with an edited
// copy, and gives the video of a Live Photo none at all. Each is tried in
// turn, the surest first.
func Pair(mediaNames []string, sidecars map[string]string) map[string]string {
	jsons := make([]string, 0, len(sidecars))
	for name := range sidecars {
		if strings.HasSuffix(strings.ToLower(name), ".json") {
			jsons = append(jsons, name)
		}
	}
	sort.Strings(jsons)
	type parsed struct{ name, stem, number string }
	all := make([]parsed, 0, len(jsons))
	for _, name := range jsons {
		stem := name[:len(name)-len(".json")]
		p := parsed{name: name, stem: stem}
		if m := jsonNumber.FindStringSubmatch(stem); m != nil {
			p.stem, p.number = m[1], m[2]
		}
		all = append(all, p)
	}
	paired := map[string]string{}
	find := func(name string) (string, bool) {
		for _, r := range readings(name) {
			// Named in full, either way.
			for _, p := range all {
				if p.number == r.number && (p.stem == r.base || p.stem == r.base+supplemental) {
					return p.name, true
				}
			}
			// Cut short: the JSON's stem is the photo's name with part of
			// .supplemental-metadata, or the photo's name itself cut short.
			best, bestLength := "", 0
			for _, p := range all {
				if p.number != r.number {
					continue
				}
				fits := strings.HasPrefix(r.base+supplemental, p.stem) && len(p.stem) >= len(r.base)
				fits = fits || (len(p.stem) >= shortest && strings.HasPrefix(r.base, p.stem))
				if fits && len(p.stem) > bestLength {
					best, bestLength = p.name, len(p.stem)
				}
			}
			if best != "" {
				return best, true
			}
		}
		// The title inside says whose it is, when only one says so.
		for _, r := range readings(name) {
			if r.number != "" {
				continue
			}
			match := ""
			for _, p := range all {
				title := sidecars[p.name]
				if p.number == "" && (title == r.base || (len(r.base) >= shortest && strings.HasPrefix(title, strings.TrimSuffix(r.base, path.Ext(r.base))) && strings.EqualFold(path.Ext(title), path.Ext(r.base)))) {
					if match != "" {
						match = ""
						break
					}
					match = p.name
				}
			}
			if match != "" {
				return match, true
			}
		}
		return "", false
	}
	for _, name := range mediaNames {
		if json, ok := find(name); ok {
			paired[name] = json
		}
	}
	// The video of a Live Photo shares the still's name and has no JSON: it
	// was taken at the same moment, so it takes the still's.
	stills := map[string]string{}
	for _, name := range mediaNames {
		if json, ok := paired[name]; ok && !IsVideo(name) {
			stills[strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))] = json
		}
	}
	for _, name := range mediaNames {
		if _, ok := paired[name]; ok || !IsVideo(name) {
			continue
		}
		if json, ok := stills[strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))]; ok {
			paired[name] = json
		}
	}
	return paired
}

// The media an export holds that the library takes: the same as the
// archive's.
var (
	stills = map[string]bool{"heic": true, "heif": true, "jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true, "avif": true, "tif": true, "tiff": true}
	raws   = map[string]bool{"arw": true, "dng": true, "cr2": true, "nef": true, "raf": true, "orf": true}
	videos = map[string]bool{"mov": true, "mp4": true, "m4v": true, "avi": true, "mkv": true, "3gp": true, "mpg": true, "mpeg": true}
)

func extension(name string) string { return strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")) }

// IsMedia reports whether a file is a photo or a video the library takes.
func IsMedia(name string) bool {
	ext := extension(name)
	return stills[ext] || raws[ext] || videos[ext]
}

// IsVideo reports whether a file is a video.
func IsVideo(name string) bool { return videos[extension(name)] }

// Kind is image, raw or video, as the catalogue records it.
func Kind(name string) string {
	switch ext := extension(name); {
	case videos[ext]:
		return "video"
	case raws[ext]:
		return "raw"
	default:
		return "image"
	}
}

// IsSidecar reports whether a file is a JSON file.
func IsSidecar(name string) bool { return extension(name) == "json" }

var (
	compactTime = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})(\d{2})(\d{2})[_-](\d{2})(\d{2})(\d{2})(?:\d{3})?(?:[^0-9]|$)`)
	spacedTime  = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})-(\d{2})-(\d{2})[ _T-](\d{2})[.:-](\d{2})[.:-](\d{2})(?:[^0-9]|$)`)
)

// NameTime is the time a camera or phone wrote into the file's name, as in
// PXL_20210314_101530123.jpg or 2019-08-14 12.30.00.jpg. It is the clock of
// the place the photo was taken, so it comes back as that wall-clock time
// in UTC, as the catalogue records days.
func NameTime(name string) (time.Time, bool) {
	for _, pattern := range []*regexp.Regexp{compactTime, spacedTime} {
		m := pattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05", m[1]+"-"+m[2]+"-"+m[3]+" "+m[4]+":"+m[5]+":"+m[6])
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
