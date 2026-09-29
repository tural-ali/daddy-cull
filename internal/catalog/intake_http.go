package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"daddy-cull/next/internal/api"
)

// IntakeView is the Import folder as the Settings page shows it.
type IntakeView struct {
	// Configured says whether the writer was started with an Import folder.
	Configured bool `json:"configured"`
	// Status is what the writer last found there, or null before its first look.
	Status *IntakeStatus `json:"status"`
}

// IntakeRoutes serves the Import folder's status, and asks the writer to look
// at it now.
func (s *Store) IntakeRoutes(m *api.Mux, upstream, secret string) {
	m.Book().Tag("Import", "The Import folder on this computer, where new photos are dropped: the private writer files each one under the day it was taken and catalogues it for review. A photo the library already holds is moved aside into its Already in the library folder, never deleted.")
	notConfigured := api.Error{Status: 409, When: "The writer was started without an Import folder."}
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/intake", Tag: "Import", Needs: api.Read,
		Summary: "See what the Import folder holds",
		Doc:     "Whether there is an Import folder, and what the writer found there when it last looked, which it does every minute: how many photos it filed, how many it set aside as already in the library, how many are still arriving and how many files it does not take.",
		Returns: IntakeView{}, Errors: []api.Error{unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		status, err := s.Intake(ctx)
		if err != nil {
			failFor(w, err, "")
			return
		}
		writeJSON(w, IntakeView{Configured: status != nil, Status: status})
	})
	client := &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/intake/run", Tag: "Import", Needs: api.Import,
		Summary: "Look at the Import folder now",
		Doc:     "Has the writer look at the Import folder now rather than within the minute, and answers once it has filed what it found. A file still being copied in is left for the next look.",
		Returns: IntakeStatus{}, Errors: []api.Error{notConfigured, offSite, writerDown, unreadable},
	}, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		status, err := s.Intake(ctx)
		cancel()
		if err != nil {
			failFor(w, err, "")
			return
		}
		if status == nil {
			api.Fail(w, 409, "There is no Import folder yet. Choose one in Settings, under Folders.")
			return
		}
		if upstream == "" || len(secret) < 32 {
			api.Fail(w, 503, "The private writer is not running, so nothing can be filed.")
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), "POST", upstream+"/intake/run", nil)
		if err != nil {
			api.Fail(w, 503, "The private writer is not running, so nothing can be filed.")
			return
		}
		request.Header.Set("X-Bin-Key", secret)
		response, err := client.Do(request)
		if err != nil {
			api.Fail(w, 503, "The writer's answer did not arrive. Read the status again before retrying.")
			return
		}
		defer response.Body.Close()
		var ran IntakeStatus
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&ran) != nil {
			api.Fail(w, 503, "The writer's answer did not arrive. Read the status again before retrying.")
			return
		}
		writeJSON(w, ran)
	})
}
