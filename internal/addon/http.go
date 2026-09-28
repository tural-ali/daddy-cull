package addon

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"daddy-cull/next/internal/api"
)

// Guard stands in front of every API route. A request carrying an addon's
// key is let through only while that addon is on and has asked for the
// permission the route needs; a request to an addon's route is let through
// only while that addon is on.
//
// Cull has no accounts: anything that can reach it can use it, as its own
// pages do, which send no key. A key says which addon is asking, so turning
// an addon off stops it, and an addon cannot do more than it asked for.
func (r *Registry) Guard(route api.Route, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if header := req.Header.Get("Authorization"); header != "" {
			key, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || strings.TrimSpace(key) == "" {
				api.Fail(w, 401, "Send the addon's key as Authorization: Bearer <key>.")
				return
			}
			owner, known := r.keyOwner(strings.TrimSpace(key))
			if !known {
				api.Fail(w, 401, "Cull does not know this key. Turn the addon on in Addons, which writes its key into the addon's folder.")
				return
			}
			id := owner.manifest.ID
			if owner.problem != "" || !r.On(req.Context(), id) {
				api.Fail(w, 403, r.Name(id)+" is turned off in Addons.")
				return
			}
			if route.Needs != api.Read && !slices.Contains(owner.manifest.Permissions, route.Needs) {
				api.Fail(w, 403, r.Name(id)+" has not asked for the "+route.Needs+" permission this needs. Add it to the addon's "+ManifestFile+" and turn the addon off and on again.")
				return
			}
			req = req.WithContext(api.WithCaller(req.Context(), id, owner.manifest.Permissions))
		}
		if route.Addon != "" && !r.On(req.Context(), route.Addon) {
			api.Fail(w, 404, r.Name(route.Addon)+" is turned off. Turn it on in Addons.")
			return
		}
		next.ServeHTTP(w, req)
	})
}

// Choice is the body that turns an addon on or off.
type Choice struct {
	// On is true to turn the addon on and false to turn it off. It is
	// required.
	On *bool `json:"on"`
}

// Routes serves the list of addons and the switch that turns one on or off.
func (r *Registry) Routes(m *api.Mux) {
	m.Book().Tag("Addons", "Everything beyond reviewing dates, finding duplicates and the Bin is an addon. Cull's own come with it; an addon of your own is a folder holding an addon.json in the addons folder.")
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/addons", Tag: "Addons", Needs: api.Read,
		Summary: "List addons",
		Doc:     "Every addon, Cull's own first: what it is, whether it is on, and whether it has what it needs. An addon of your own shows here as soon as its folder is in the addons folder, before it is turned on.",
		Returns: []View{},
	}, func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, r.List(req.Context()))
	})
	m.HandleFunc(api.Route{
		Method: "POST", Path: "/api/addons/{id}", Tag: "Addons", Needs: api.Settings,
		Summary: "Turn an addon on or off",
		Doc:     "Turning an addon off hides its pages, stops its routes and background work, and refuses its key. Nothing it did is undone, and files it moved to the Bin stay there. Turning an addon of your own on for the first time writes its key into its folder.",
		Params:  []api.Param{api.Path("id", "The addon's id.", "screenshots")},
		Body:    Choice{},
		Returns: View{},
		Errors: []api.Error{
			{Status: 400, When: "The body is not {\"on\": true} or {\"on\": false}"},
			{Status: 404, When: "No addon has this id"},
			{Status: 409, When: "An addon of your own cannot be loaded; the message says why"},
		},
	}, func(w http.ResponseWriter, req *http.Request) {
		if !api.IsJSON(req) {
			api.Fail(w, 415, "Send JSON.")
			return
		}
		var choice Choice
		decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 256))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&choice); err != nil || choice.On == nil || decoder.Decode(new(any)) != io.EOF {
			api.Fail(w, 400, "Send {\"on\": true} or {\"on\": false}.")
			return
		}
		id := req.PathValue("id")
		switch err := r.Set(req.Context(), id, *choice.On); {
		case errors.Is(err, ErrUnknown):
			api.Fail(w, 404, "No addon is called "+id+".")
			return
		case errors.Is(err, ErrBroken):
			api.Fail(w, 409, err.Error())
			return
		case err != nil:
			api.Fail(w, 503, "The choice could not be saved. Try again in a moment.")
			return
		}
		for _, view := range r.List(req.Context()) {
			if view.ID == id {
				writeJSON(w, view)
				return
			}
		}
	})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(value)
}
