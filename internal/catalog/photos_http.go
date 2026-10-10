package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"daddy-cull/next/internal/api"
)

// PhotosAgentHeader carries the helper's shared secret. A header rather than a
// query parameter, so the key never lands in an access log or a proxy's URL
// history, and a custom one, so a browser cannot send it cross-site without a
// preflight this server never answers.
const PhotosAgentHeader = "X-Photos-Agent-Key"

// photosReportLimit bounds one report from the helper. It sends matches a few
// dozen at a time, so even with thumbnails a report is a megabyte or two.
const photosReportLimit = 8 << 20

// PhotosOverview is what the Apple Photos page shows before a check: how
// much is waiting to be carried across, from Cull's own records alone.
type PhotosOverview struct {
	// Delete counts files removed in Cull that Photos still holds, as far as
	// Cull knows.
	Delete int `json:"delete"`
	// Favourite counts hearts not yet given in Photos.
	Favourite int `json:"favourite"`
	// Held counts files kept back from the sync, each with its reason.
	Held int `json:"held"`
	// Undated counts removed and favourited files without a date to find
	// them by.
	Undated int `json:"undated"`
	// Restored lists files put back in Cull after Photos was told to delete
	// them, which need putting back in Photos by hand.
	Restored []PhotosRestored `json:"restored"`
	// Synced is what earlier syncs carried across.
	Synced PhotosSynced `json:"synced"`
}

// PhotosSynced is what earlier syncs carried across.
type PhotosSynced struct {
	// Deleted counts the catalogue items Photos was seen to delete.
	Deleted int `json:"deleted"`
	// Favourited counts the catalogue items Photos was seen to favourite.
	Favourited int `json:"favourited"`
	// Last is when a sync last finished, in RFC 3339 UTC, empty when none has.
	Last string `json:"last"`
}

// PhotosApply says which of a check's findings to carry out.
type PhotosApply struct {
	// Job is the check's id.
	Job string `json:"job"`
	// Delete lists the ids of the check's delete rows to carry out. Anything
	// left out is left alone.
	Delete []string `json:"delete"`
	// Favourite lists the ids of the check's favourite rows to carry out.
	// Anything left out is left alone.
	Favourite []string `json:"favourite"`
}

// PhotosJobRef names a sync.
type PhotosJobRef struct {
	// Job is the sync's id.
	Job string `json:"job"`
}

// PhotosForget names removed files that are gone from Photos already, by
// hand perhaps, so Cull stops asking.
type PhotosForget struct {
	// Keys lists catalogue keys from the overview's restored list, 1 to
	// 10,000 of them. A key not on that list is ignored.
	Keys []string `json:"keys"`
}

// PhotosForgotten counts the files forgotten.
type PhotosForgotten struct {
	// Forgotten counts the records dropped, which can be fewer than the keys
	// sent.
	Forgotten int `json:"forgotten"`
}

// PhotosCancel answers a heartbeat: whether the Mac should stop what it is
// doing.
type PhotosCancel struct {
	// Cancel is true when the job the heartbeat named was cancelled, replaced
	// or is no longer checking or applying; always false when it named none.
	Cancel bool `json:"cancel"`
}

// PhotosFailure says why the Mac could not finish a job.
type PhotosFailure struct {
	// Error is the reason, in words the page shows after "The Mac could not
	// finish:". Only the first 300 bytes are kept, and an empty one reads as
	// no reason given.
	Error string `json:"error"`
}

// Empty is a body with nothing in it, sent as {}.
type Empty struct{}

// Handler serves the Photos routes on their own, as Routes does on a shared
// mux.
func (h *PhotosHub) Handler() http.Handler {
	m := api.NewMux(api.NewBook())
	m.Guard(api.SameOrigin)
	h.Routes(m)
	return m
}

// Routes serves the Apple Photos page's routes and Cull Sync's. The page's
// POSTs pass the same JSON and same-origin checks as every other write in the
// app; the helper's routes need the shared key instead, because the helper is
// not a browser and has no origin to check.
func (h *PhotosHub) Routes(m *api.Mux) {
	m.Book().Tag("Apple Photos", "Carrying what was removed and favourited in Cull across to Apple Photos, through Cull Sync on a Mac. Photos is only ever changed through its own interface, after the person agrees on the Mac.")
	m.Book().Tag("Cull Sync", "The routes Cull Sync, the Mac helper, calls with the key it was set up with. They are listed so nothing is hidden; an addon has no reason to call them.")
	writeJSON := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(value)
	}
	fail := api.Fail
	failFor := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, ErrPhotosDisabled):
			fail(w, 503, "Cull Sync is not set up yet. Choose Set up Cull Sync to install it on the Mac.")
		case errors.Is(err, ErrPhotosStale):
			fail(w, 409, "This sync has moved on, perhaps in another tab. The page has been refreshed.")
		case errors.Is(err, ErrInvalid):
			fail(w, 400, "That request did not match this sync.")
		default:
			fail(w, 503, "The catalogue could not be read. Try again in a moment.")
		}
	}
	decode := func(w http.ResponseWriter, r *http.Request, limit int64, into any) bool {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(into); err != nil {
			fail(w, 400, "The request could not be read.")
			return false
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			fail(w, 400, "One request body expected.")
			return false
		}
		return true
	}
	route := func(method, path, needs, summary, doc string, body, returns any, errs ...api.Error) api.Route {
		return api.Route{Method: method, Path: path, Addon: AddonApplePhotos, Tag: "Apple Photos", Needs: needs, Summary: summary, Doc: doc, Body: body, Returns: returns, Errors: errs}
	}
	job := api.Path("job", "The sync's id, from the check that started it.", "job")
	notSetUp := api.Error{Status: 503, When: "Cull Sync is not set up yet, or the catalogue could not be read."}
	stale := api.Error{Status: 409, When: "The sync has moved on since, perhaps in another tab. The body carries it as it stands."}

	// The page.
	m.HandleFunc(route("GET", "/api/photos", api.Read, "Get Cull Sync's state",
		"Whether Cull Sync is set up and online, and the sync in hand, if any.", nil, PhotosStatus{}),
		func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, h.Status()) })
	m.HandleFunc(route("GET", "/api/photos/scores", api.Read, "Count Apple's scores held",
		"How many Photos items the Mac's photos-scores script has sent across, and when the last page arrived.", nil, PhotosScoresState{}),
		func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			state, err := h.s.PhotosScoresState(ctx)
			if err != nil {
				failFor(w, err)
				return
			}
			writeJSON(w, 200, state)
		})
	m.HandleFunc(route("GET", "/api/photos/overview", api.Read, "Count what is waiting for Photos",
		"From Cull's own records, without asking the Mac: what would be deleted and favourited, and what is held back.", nil, PhotosOverview{}, notSetUp),
		func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			plan, err := h.s.PhotosPlan(ctx)
			if err != nil {
				failFor(w, err)
				return
			}
			deleted, favourited, last := h.s.PhotosSyncCounts(ctx)
			restored := plan.Restored
			if restored == nil {
				restored = []PhotosRestored{}
			}
			writeJSON(w, 200, PhotosOverview{Delete: len(plan.Delete), Favourite: len(plan.Favourite), Held: len(plan.Held), Undated: plan.Undated,
				Restored: restored, Synced: PhotosSynced{Deleted: deleted, Favourited: favourited, Last: last}})
		})
	withJob := route("GET", "/api/photos/jobs/{job}", api.Read, "Get a sync",
		"A check or an apply and how far it has got, with what the Mac found.", nil, PhotosJobView{}, api.Error{Status: 404, When: "The sync is no longer held."})
	withJob.Params = []api.Param{job}
	m.HandleFunc(withJob, func(w http.ResponseWriter, r *http.Request) {
		view, ok := h.Job(r.PathValue("job"))
		if !ok {
			fail(w, 404, "That sync is no longer held. Check again.")
			return
		}
		writeJSON(w, 200, view)
	})
	thumb := route("GET", "/api/photos/thumb/{job}/{n}", api.Read, "Get a match's thumbnail",
		"The thumbnail the Mac sent of a photo it matched in Photos, so a person can see it is the right one.", nil, nil, api.Error{Status: 404, When: "The sync or the match is no longer held."})
	thumb.Params = []api.Param{job, api.PathInt("n", "The match's place in the sync's findings, from 1.", "1")}
	thumb.Produces = "image/jpeg"
	m.HandleFunc(thumb, func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		thumb, ok := h.Thumb(r.PathValue("job"), n)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(thumb)
	})
	m.HandleFunc(route("POST", "/api/photos/check", api.Review, "Check Photos",
		"Asks the Mac to find in Photos each file waiting to be carried across. Nothing in Photos changes. The body is empty.", nil, PhotosJobView{},
		api.Error{Status: 409, When: "A sync is already running. The body carries it."}, notSetUp),
		func(w http.ResponseWriter, r *http.Request) {
			if !sameOriginJSON(w, r) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			view, err := h.StartCheck(ctx)
			if errors.Is(err, ErrPhotosBusy) {
				writeJSON(w, 409, map[string]any{"error": "A sync is already running.", "job": view})
				return
			}
			if err != nil {
				failFor(w, err)
				return
			}
			writeJSON(w, 200, view)
		})
	m.HandleFunc(route("POST", "/api/photos/apply", api.Delete, "Carry a check's findings across",
		"Asks the Mac to put the chosen matches in an album to delete from, and to favourite the others, once the person agrees on the Mac. It needs delete, as it ends in Photos deleting files.", PhotosApply{}, PhotosJobView{}, stale, notSetUp),
		func(w http.ResponseWriter, r *http.Request) {
			if !sameOriginJSON(w, r) {
				return
			}
			var body PhotosApply
			if !decode(w, r, 1<<20, &body) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			view, err := h.Apply(ctx, body.Job, body.Delete, body.Favourite)
			if errors.Is(err, ErrPhotosStale) && view.ID != "" {
				writeJSON(w, 409, map[string]any{"error": "Everything chosen has changed in Cull since the check. Check again.", "job": view})
				return
			}
			if err != nil {
				failFor(w, err)
				return
			}
			writeJSON(w, 200, view)
		})
	m.HandleFunc(route("POST", "/api/photos/cancel", api.Review, "Cancel a sync",
		"Stops a sync the Mac has not started changing Photos for.", PhotosJobRef{}, PhotosJobView{},
		api.Error{Status: 409, When: "The Mac is already changing Photos; it is stopped from the dialog on the Mac."}, notSetUp),
		func(w http.ResponseWriter, r *http.Request) {
			if !sameOriginJSON(w, r) {
				return
			}
			var body PhotosJobRef
			if !decode(w, r, 4096, &body) {
				return
			}
			view, err := h.Cancel(body.Job)
			if err != nil {
				if view.ID != "" {
					writeJSON(w, 409, map[string]any{"error": "The Mac is already changing Photos. Use the dialog on the Mac to stop it.", "job": view})
					return
				}
				failFor(w, err)
				return
			}
			writeJSON(w, 200, view)
		})
	m.HandleFunc(route("POST", "/api/photos/forget", api.Review, "Stop asking Photos about files",
		"For removed files that are gone from Photos already, perhaps by hand, so no check looks for them again.", PhotosForget{}, PhotosForgotten{}, notSetUp),
		func(w http.ResponseWriter, r *http.Request) {
			if !sameOriginJSON(w, r) {
				return
			}
			var body PhotosForget
			if !decode(w, r, 1<<20, &body) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			n, err := h.s.ForgetPhotosDeletions(ctx, body.Keys)
			if err != nil {
				failFor(w, err)
				return
			}
			writeJSON(w, 200, PhotosForgotten{Forgotten: n})
		})

	// Setting the helper up. The page asks for a one-time code with a POST
	// that passes the usual same-origin checks, so no other site can get one,
	// and only the Mac that runs the command ever sees the key.
	m.HandleFunc(route("POST", "/api/photos/setup", api.Settings, "Get a setup command",
		"A one-line command to run on the Mac, which installs Cull Sync with a key of its own. The code in it works once, for a few minutes. The body is {}.", Empty{}, PhotosSetupView{},
		api.Error{Status: 503, When: "This server was built without Cull Sync."}, api.Error{Status: 400, When: "The address the page was opened at cannot be used in the command."}),
		func(w http.ResponseWriter, r *http.Request) {
			if !sameOriginJSON(w, r) {
				return
			}
			var body Empty
			if !decode(w, r, 1024, &body) {
				return
			}
			view, err := h.NewSetup(photosBase(r))
			switch {
			case errors.Is(err, ErrPhotosSetupUnavailable):
				fail(w, 503, "This server was built without Cull Sync, so it cannot install it.")
			case err != nil:
				fail(w, 400, "The address this page was opened at cannot be used for the command. Open Daddy Cull by its usual address and try again.")
			default:
				writeJSON(w, 200, view)
			}
		})
	setup := route("GET", "/api/photos/setup/{id}", api.Read, "Get a setup command's state",
		"Whether the command has been run yet, and whether Cull Sync then came online.", nil, PhotosSetupView{}, api.Error{Status: 404, When: "The command is no longer held."})
	setup.Params = []api.Param{api.Path("id", "The setup's id.", "setup")}
	m.HandleFunc(setup, func(w http.ResponseWriter, r *http.Request) {
		view, ok := h.Setup(r.PathValue("id"))
		if !ok {
			fail(w, 404, "That setup command is no longer held. Get a new one.")
			return
		}
		writeJSON(w, 200, view)
	})
	// The command's fetch. It answers with a script even when it refuses,
	// because the answer goes straight into bash: a script that says why reads
	// better in Terminal than curl's bare status code, and it changes nothing.
	install := route("GET", "/api/photos/install/{code}", api.Read, "Get the installer",
		"The script the setup command pipes into bash. It works once: fetching it uses the code up.", nil, nil)
	install.Params = []api.Param{api.Path("code", "The one-time code from the setup command.", "code")}
	install.Produces, install.Tag, install.Internal = "text/plain", "Cull Sync", true
	m.HandleFunc(install, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// GET routes answer HEAD too. A HEAD, from a link preview perhaps,
		// must not burn the code and so throw away the key it would carry.
		if r.Method == http.MethodHead {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		script, err := h.Install(ctx, r.PathValue("code"), photosBase(r))
		var refusal photosInstallRefusal
		switch {
		case errors.As(err, &refusal):
			w.Write(photosRefusalScript(refusal))
		case err != nil:
			log.Printf("photos: could not build the Cull Sync installer: %v", err)
			w.Write(photosRefusalScript("Daddy Cull could not build the installer. Try again with a new command from the Apple Photos page."))
		default:
			w.Write(script)
		}
	})

	// The helper. A helper whose key was replaced, or a server that never
	// handed one out, gets the same answer: set it up again from the page.
	agentOnly := func(next http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !h.authorised(r.Header.Get(PhotosAgentHeader)) {
				fail(w, 403, "The key in sync.conf is not the one this server handed out. Set Cull Sync up again from the Apple Photos page.")
				return
			}
			if r.Method == http.MethodPost && !api.IsJSON(r) {
				fail(w, 415, "JSON required.")
				return
			}
			next(w, r)
		})
	}
	wrongKey := api.Error{Status: 403, When: "The " + PhotosAgentHeader + " header is not the key this server handed out."}
	agent := func(method, path, summary, doc string, body, returns any, params ...api.Param) api.Route {
		needs := api.Read
		if method == "POST" {
			needs = api.Review
		}
		return api.Route{Method: method, Path: path, Addon: AddonApplePhotos, Tag: "Cull Sync", Needs: needs, Internal: true, Summary: summary, Doc: doc, Body: body, Returns: returns, Params: params, Errors: []api.Error{wrongKey}}
	}
	work := agent("GET", "/api/photos/agent/work", "Wait for work",
		"Held open until there is a job for the Mac, or answered 204 when there is none for a while.", nil, PhotosTask{})
	m.Handle(work, agentOnly(func(w http.ResponseWriter, r *http.Request) {
		task, err := h.Claim(r.Context())
		if err != nil {
			// The helper hung up, or the server is shutting down.
			return
		}
		if task == nil {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, 200, task)
	}))
	m.Handle(agent("POST", "/api/photos/agent/heartbeat", "Say the Mac is online",
		"Sent every 15 seconds, and as a job moves on, with the helper's version, its Photos access and how far the job in hand has got. The answer says whether to abandon that job, because it was cancelled or replaced.",
		PhotosHeartbeat{}, PhotosCancel{}), agentOnly(func(w http.ResponseWriter, r *http.Request) {
		var beat PhotosHeartbeat
		if !decode(w, r, 8192, &beat) {
			return
		}
		writeJSON(w, 200, PhotosCancel{Cancel: h.Seen(beat)})
	}))
	m.Handle(agent("POST", "/api/photos/agent/jobs/{job}/matches", "Report matches found in Photos",
		"Part of a check's answer: the Photos assets that answer to some entries, with thumbnails, and the entries not found, with why where the Mac knows. A report for an entry already reported replaces it, so a retry is safe. Answers 409 once the job is no longer checking, and 400 for an entry the job does not hold.",
		PhotosMatchReport{}, Done{}, job), agentOnly(func(w http.ResponseWriter, r *http.Request) {
		var report PhotosMatchReport
		if !decode(w, r, photosReportLimit, &report) {
			return
		}
		if err := h.Matches(r.PathValue("job"), report); err != nil {
			failFor(w, err)
			return
		}
		writeJSON(w, 200, Done{OK: true})
	}))
	m.Handle(agent("POST", "/api/photos/agent/jobs/{job}/checked", "Report a check finished",
		"Ends a check, so the page shows what was found; an entry never reported is taken as not in Photos. The body is {}. Sending it again once the check is planned is harmless; answers 409 when the job has moved on otherwise.",
		Empty{}, Done{}, job), agentOnly(func(w http.ResponseWriter, r *http.Request) {
		var body Empty
		if !decode(w, r, 1024, &body) {
			return
		}
		if err := h.Checked(r.PathValue("job")); err != nil {
			failFor(w, err)
			return
		}
		writeJSON(w, 200, Done{OK: true})
	}))
	m.Handle(agent("POST", "/api/photos/agent/jobs/{job}/failed", "Report a job failed",
		"Says the Mac could not finish a check or an apply, with the reason the page shows. The job fails; after a failed apply, a later report of what changed is still recorded. Answers 409 when the job is not checking or applying.",
		PhotosFailure{}, Done{}, job), agentOnly(func(w http.ResponseWriter, r *http.Request) {
		var body PhotosFailure
		if !decode(w, r, 8192, &body) {
			return
		}
		if err := h.Failed(r.PathValue("job"), body.Error); err != nil {
			failFor(w, err)
			return
		}
		writeJSON(w, 200, Done{OK: true})
	}))
	m.Handle(agent("POST", "/api/photos/agent/scores", "Send what Photos thinks of its pictures",
		"Records one page of Apple's scores, faces, labels and captions, read from the Photos library by mac/photos-scores.py. Pages carry the run they belong to; when the last page of a run is in, items from earlier runs are dropped. Day pages then carry a hint beside each file Photos knows by name and day. A page with any item out of shape is refused whole with 400.",
		PhotosScoresReport{}, PhotosScoresStored{}), agentOnly(func(w http.ResponseWriter, r *http.Request) {
		var report PhotosScoresReport
		if !decode(w, r, 8<<20, &report) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		stored, err := h.Scores(ctx, report)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				log.Printf("photos: could not record Apple's scores: %v", err)
			}
			failFor(w, err)
			return
		}
		writeJSON(w, 200, stored)
	}))
	m.Handle(agent("POST", "/api/photos/agent/jobs/{job}/applied", "Report what changed in Photos",
		"Ends an apply with what Photos was seen to do to each chosen entry. What was done is recorded so it is never offered again, and the job is done. Only entries the apply asked for are accepted; a report already recorded is answered ok again, so a retry is safe.",
		PhotosAppliedReport{}, Done{}, job), agentOnly(func(w http.ResponseWriter, r *http.Request) {
		var report PhotosAppliedReport
		if !decode(w, r, 4<<20, &report) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := h.Applied(ctx, r.PathValue("job"), report); err != nil {
			if !errors.Is(err, ErrPhotosStale) && !errors.Is(err, ErrInvalid) {
				log.Printf("photos: could not record what the Mac changed: %v", err)
			}
			failFor(w, err)
			return
		}
		writeJSON(w, 200, Done{OK: true})
	}))
}

// photosBase is the scheme and host this request reached the server by. For
// the page that is how the person opened it, and for the install command it is
// the address in the command, so either way it is an address that works.
func photosBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
