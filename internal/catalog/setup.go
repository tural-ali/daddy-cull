package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"daddy-cull/next/internal/api"
)

// A Mac set up with install.sh keeps its settings in config.json in its state
// folder, beside the catalogue. Both processes start from it; the Setup page
// changes it and then has both start again to take it up, which launchd does
// the moment they stop. A server started with flags alone, such as a NAS in
// Docker, has no config file, and its Setup page only shows how it is set.

// SetupConfig is how Daddy Cull is set up: the folders it works with and what
// it brings photos in from.
type SetupConfig struct {
	// Library is the folder of day folders, YYYY/YYYY-MM/YYYY-MM-DD, that holds the photos.
	Library string `json:"library"`
	// Import is the folder new photos are dropped into, to be filed in the library under the day each was taken.
	Import string `json:"import"`
	// TakeoutInbox is the folder Google Takeout exports are dropped into, or empty for none.
	TakeoutInbox string `json:"takeoutInbox"`
	// Shared opens the day folders Cull makes to every user of this computer, rather than only its owner.
	Shared bool `json:"shared"`
	// ICloud is the download from iCloud Photos.
	ICloud SetupICloud `json:"icloud"`
	// Immich is the Immich server favourites are mirrored to, if any.
	Immich SetupImmich `json:"immich"`
	// Done says the setup has been finished once, so Cull opens on Today rather than on Setup.
	Done bool `json:"done"`
}

// SetupICloud is the download from iCloud Photos, which icloudpd does.
type SetupICloud struct {
	// On says whether photos are downloaded from iCloud.
	On bool `json:"on"`
	// AppleID is the Apple Account the photos are downloaded from.
	AppleID string `json:"appleId"`
	// Since is the first day downloaded, as YYYY-MM-DD, or empty for the whole library.
	Since string `json:"since"`
}

// SetupImmich is the Immich server favourites are mirrored to.
type SetupImmich struct {
	// URL is the server's address, such as http://immich.local:2283, or empty for none.
	URL string `json:"url"`
	// PathPrefix is the library folder as Immich's external library reads it, such as /mnt/photos.
	PathPrefix string `json:"pathPrefix"`
}

// SetupChange is a new setup from the Setup page.
type SetupChange struct {
	// Config is the whole setup, as GET /api/setup gives it.
	Config SetupConfig `json:"config"`
	// ImmichKey replaces Immich's API key when given; it is kept apart from the config and never shown again.
	ImmichKey *string `json:"immichKey,omitempty"`
}

// SetupTool is a program Cull uses, and whether this computer has it.
type SetupTool struct {
	// Name is the program's name.
	Name string `json:"name"`
	// Found says whether it is installed where Cull looks.
	Found bool `json:"found"`
	// For says what Cull uses it for.
	For string `json:"for"`
}

// SetupRun is what a scheduled download last did, as the daddy-cull command records it.
type SetupRun struct {
	// SignedIn says whether the download has a session it can use, for iCloud.
	SignedIn bool `json:"signedIn"`
	// LastRun is when it last ran, in RFC 3339, or empty if it never has.
	LastRun string `json:"lastRun"`
	// OK says whether that run finished without a problem.
	OK bool `json:"ok"`
	// Message is what the run said, such as how many photos it fetched or what went wrong.
	Message string `json:"message"`
}

// SetupView is the Setup page.
type SetupView struct {
	// Configurable says whether Cull was started from a config file it may change. Otherwise its folders are set where it is started, and they are only shown.
	Configurable bool `json:"configurable"`
	// Config is the setup now.
	Config SetupConfig `json:"config"`
	// ImmichKeySet says whether an Immich API key is kept.
	ImmichKeySet bool `json:"immichKeySet"`
	// Tools are the programs Cull uses.
	Tools []SetupTool `json:"tools"`
	// ICloud is what the iCloud download last did.
	ICloud SetupRun `json:"icloud"`
	// ApplePhotos is what the last export from Apple Photos did.
	ApplePhotos SetupRun `json:"applePhotos"`
	// Free is how many bytes are free where the library is, or 0 if that cannot be told.
	Free int64 `json:"free"`
	// Restarting says Cull is starting again to take up a change; read the page again in a few seconds.
	Restarting bool `json:"restarting,omitempty"`
}

// Setup is the config file, where there is one, and what the Setup page shows.
type Setup struct {
	// file is config.json; empty when Cull was started with flags alone.
	file string
	// fixed is the setup as the flags gave it, shown when there is no file.
	fixed SetupConfig
	// restart stops the process so launchd starts it again.
	restart func()
	// look finds a program.
	look func(string) (string, error)
}

// NewSetup is the Setup page for config file, or for the flags alone when file
// is empty.
func NewSetup(file string, fixed SetupConfig, restart func()) *Setup {
	return &Setup{file: file, fixed: fixed, restart: restart, look: exec.LookPath}
}

// ReadSetup reads a config file.
func ReadSetup(file string) (SetupConfig, error) {
	var config SetupConfig
	body, err := os.ReadFile(file)
	if err != nil {
		return config, err
	}
	if err := json.Unmarshal(body, &config); err != nil {
		return config, fmt.Errorf("%s is not a Daddy Cull config file: %w", file, err)
	}
	return config, nil
}

// StateDir is the folder the config file is in, which holds the catalogue,
// the iCloud session and what the scheduled downloads record.
func StateDir(file string) string { return filepath.Dir(file) }

// ICloudDownloads is the folder icloudpd downloads into for this config file.
func ICloudDownloads(file string) string { return filepath.Join(StateDir(file), "iCloud") }

// ApplePhotosFolder is the folder in Import that Apple Photos exports into.
const ApplePhotosFolder = "Apple Photos"

// FolderMode is the mode the day folders are given.
func (c SetupConfig) FolderMode() os.FileMode {
	if c.Shared {
		return 0o777
	}
	return 0o755
}

// Check says what is wrong with a setup, or nil.
func (c SetupConfig) Check() error {
	folders := []struct{ name, path string }{{"library", c.Library}, {"Import folder", c.Import}}
	if c.TakeoutInbox != "" {
		folders = append(folders, struct{ name, path string }{"Google Takeout folder", c.TakeoutInbox})
	}
	for _, folder := range folders {
		if folder.path == "" {
			return fmt.Errorf("choose a folder for the %s", folder.name)
		}
		if !filepath.IsAbs(folder.path) {
			return fmt.Errorf("the %s needs a full path, starting with /, such as /Users/sam/Pictures", folder.name)
		}
	}
	for i, a := range folders {
		for _, b := range folders[i+1:] {
			x, y := filepath.Clean(a.path), filepath.Clean(b.path)
			if x == y || within(x, y) || within(y, x) {
				return fmt.Errorf("the %s and the %s must be separate folders, neither inside the other", a.name, b.name)
			}
		}
	}
	if c.ICloud.On && strings.TrimSpace(c.ICloud.AppleID) == "" {
		return errors.New("give the Apple Account to download from, or turn the iCloud download off")
	}
	if c.ICloud.Since != "" {
		if _, err := time.Parse("2006-01-02", c.ICloud.Since); err != nil {
			return errors.New("give the first day to download as YYYY-MM-DD, or leave it empty for the whole library")
		}
	}
	if c.Immich.URL != "" {
		if !strings.HasPrefix(c.Immich.URL, "http://") && !strings.HasPrefix(c.Immich.URL, "https://") {
			return errors.New("Immich's address starts with http:// or https://, such as http://immich.local:2283")
		}
		if !strings.HasPrefix(c.Immich.PathPrefix, "/") {
			return errors.New("give the folder Immich reads the library from, such as /mnt/photos")
		}
	}
	return nil
}

// Save checks a setup, makes its folders, and writes it, with the Immich key
// when one is given.
func (s *Setup) Save(change SetupChange) error {
	if s.file == "" {
		return errors.New("Cull was started without a config file, so its folders are set where it is started")
	}
	config := change.Config
	config.ICloud.AppleID = strings.TrimSpace(config.ICloud.AppleID)
	config.Immich.URL = strings.TrimRight(strings.TrimSpace(config.Immich.URL), "/")
	for _, folder := range []*string{&config.Library, &config.Import, &config.TakeoutInbox} {
		*folder = strings.TrimSpace(*folder)
		if *folder != "" {
			*folder = filepath.Clean(*folder)
		}
	}
	if err := config.Check(); err != nil {
		return err
	}
	for _, folder := range []string{config.Library, config.Import, config.TakeoutInbox} {
		if folder == "" {
			continue
		}
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return fmt.Errorf("%s could not be made: %w", folder, err)
		}
	}
	body, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := writeQuietly(s.file, append(body, '\n')); err != nil {
		return err
	}
	if change.ImmichKey != nil {
		key := strings.TrimSpace(*change.ImmichKey)
		if strings.ContainsAny(key, "\n\r\"'`$\\ ") {
			return errors.New("an Immich API key is letters and digits only")
		}
		secrets := filepath.Join(StateDir(s.file), "secrets.env")
		if err := writeQuietly(secrets, []byte("IMMICH_KEY="+key+"\n")); err != nil {
			return err
		}
	}
	return nil
}

// writeQuietly replaces file whole, readable by its owner only.
func writeQuietly(file string, body []byte) error {
	temp := file + ".new"
	if err := os.WriteFile(temp, body, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temp, 0o600); err != nil {
		os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, file); err != nil {
		os.Remove(temp)
		return err
	}
	return nil
}

// View is the Setup page.
func (s *Setup) View() SetupView {
	view := SetupView{Config: s.fixed, Configurable: s.file != ""}
	if s.file != "" {
		if config, err := ReadSetup(s.file); err == nil {
			view.Config = config
		}
		if body, err := os.ReadFile(filepath.Join(StateDir(s.file), "secrets.env")); err == nil {
			view.ImmichKeySet = strings.Contains(string(body), "IMMICH_KEY=") && !strings.Contains(string(body), "IMMICH_KEY=\n")
		}
		view.ICloud = readRun(filepath.Join(StateDir(s.file), "icloud-status.json"))
		view.ApplePhotos = readRun(filepath.Join(StateDir(s.file), "apple-photos-status.json"))
	}
	for _, tool := range []SetupTool{
		{Name: "exiftool", For: "Reads when each photo was taken, and the preview inside RAW files"},
		{Name: "ffmpeg", For: "Makes previews of videos"},
		{Name: "icloudpd", For: "Downloads photos from iCloud"},
		{Name: "osxphotos", For: "Exports photos from Apple Photos on this Mac"},
	} {
		_, err := s.look(tool.Name)
		tool.Found = err == nil
		view.Tools = append(view.Tools, tool)
	}
	if view.Config.Library != "" {
		var stat syscall.Statfs_t
		if syscall.Statfs(view.Config.Library, &stat) == nil {
			view.Free = int64(stat.Bavail) * int64(stat.Bsize)
		}
	}
	return view
}

func readRun(file string) SetupRun {
	var run SetupRun
	if body, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(body, &run)
	}
	return run
}

// Watch stops the process when the config file changes, so launchd starts
// it again with the new setup. The writer uses it: the Setup page is served
// by the web process, which stops itself.
func Watch(ctx context.Context, file string, stop func()) {
	first, err := os.Stat(file)
	if err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		now, err := os.Stat(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil && (!now.ModTime().Equal(first.ModTime()) || now.Size() != first.Size()) {
			stop()
			return
		}
	}
}

// Routes serves the Setup page.
func (s *Setup) Routes(m *api.Mux) {
	m.Book().Tag("Setup", "How Cull is set up on this computer: the library, the Import folder, and what photos are brought in from. A Mac set up with install.sh can change it here; a server started with flags only shows it.")
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/setup", Tag: "Setup", Needs: api.Read,
		Summary: "See how Cull is set up",
		Doc:     "The folders, the downloads from iCloud and Apple Photos and how they last went, the programs Cull uses and whether each is installed, and the space left where the library is. The Immich key is never shown, only whether one is kept.",
		Returns: SetupView{},
	}, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.View())
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/setup", Tag: "Setup", Needs: api.Settings,
		Summary: "Change how Cull is set up",
		Doc:     "Checks the setup, makes any folder that is missing, saves it and starts Cull again to take it up, which takes a few seconds. Only Cull's own Setup and Settings pages may change it: an addon's key is refused, whatever it asked for.",
		Body:    SetupChange{}, Returns: SetupView{}, Status: http.StatusAccepted,
		Errors: []api.Error{{Status: 400, When: "The setup is not complete or its folders overlap. The body says what to change."}, {Status: 403, When: "An addon asked."}, {Status: 409, When: "Cull was started without a config file."}, notJSON, offSite},
	}, func(w http.ResponseWriter, r *http.Request) {
		if api.Caller(r.Context()) != "" {
			api.Fail(w, 403, "Only Cull's own Setup page can change the folders.")
			return
		}
		if s.file == "" {
			api.Fail(w, 409, "Cull was started without a config file, so its folders are set where it is started.")
			return
		}
		var change SetupChange
		if !decodeBody(w, r, 1<<16, &change, "Send the setup as GET /api/setup gives it, under config.") {
			return
		}
		if err := s.Save(change); err != nil {
			api.Fail(w, 400, err.Error())
			return
		}
		view := s.View()
		view.Restarting = true
		accepted(w, view)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// The answer goes first; then Cull stops, and launchd starts it again.
		go func() {
			time.Sleep(300 * time.Millisecond)
			s.restart()
		}()
	})
}
