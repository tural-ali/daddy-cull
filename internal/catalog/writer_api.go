package catalog

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"daddy-cull/next/internal/api"
)

// The requests below are what the private writer takes. The web process has
// no write access to the archive: it checks each request and forwards it to
// the writer, which is the only process that moves a file. Every move is
// planned first and carried out second, so a caller sees what will happen
// before it does.

// BinSelection names the files to plan moving into the Bin.
type BinSelection struct {
	// IDs are the files' ids: in the catalogue for the Bin, or in the earlier
	// app's Bin, from GET /api/legacy-bin, for that Bin.
	IDs []int64 `json:"ids"`
	// Videos, for the Bin and in place of ids, are Live Photo videos whose
	// photo has gone, as paths in the archive under .live-photos, such as
	// .live-photos/2022/2022-08/2022-08-03/IMG_6390_HEVC.MOV. A video a photo
	// in the archive still has is refused: it goes with that photo.
	Videos []string `json:"videos,omitempty"`
}

// PlanStep carries out a step of a plan made earlier.
type PlanStep struct {
	// ID is the plan's id, as the preview returned it.
	ID string `json:"id"`
	// Action is what to do with the plan's files. quarantine moves them into
	// the Bin, restore puts them back where they were, and purge deletes them
	// for good, which also needs the delete permission and the confirmation.
	Action string `json:"action"`
	// Confirmation is required to purge: DELETE and the number of files, as
	// in DELETE 3.
	Confirmation string `json:"confirmation,omitempty"`
}

// ScreenshotChoice plans what to do with one screenshot in the holding area.
type ScreenshotChoice struct {
	// AssetID is the screenshot's id in the catalogue.
	AssetID int64 `json:"assetId"`
	// Action is keep, which copies it into the archive under the date in its
	// name, or remove, which moves it into the Bin.
	Action string `json:"action"`
}

// PlanRef names a plan made earlier.
type PlanRef struct {
	// ID is the plan's id, as the preview returned it.
	ID string `json:"id"`
}

// PlanPurge deletes the files of a plan in the Bin for good.
type PlanPurge struct {
	// ID is the plan's id.
	ID string `json:"id"`
	// Confirmation is DELETE and the number of files, as in DELETE 3.
	Confirmation string `json:"confirmation"`
}

// UpgradeChoice plans adding a better copy of an archive photo beside it.
type UpgradeChoice struct {
	// ArchiveAssetID is the photo in the archive, which stays where it is.
	ArchiveAssetID int64 `json:"archiveAssetId"`
	// SourceAssetID is the higher-resolution copy from Takeout, which is
	// copied into the archive beside it.
	SourceAssetID int64 `json:"sourceAssetId"`
}

// TrashSelection names cards on the Bin page, by the keys the Bin lists them
// with, for an action on all of them at once.
type TrashSelection struct {
	// Keys are the cards' keys from GET /api/trash. A card that holds several
	// files, such as a Live Photo, is acted on whole.
	Keys []string `json:"keys,omitempty"`
	// Confirmation is required to delete: DELETE and the number of cards, as
	// in DELETE 3.
	Confirmation string `json:"confirmation,omitempty"`
}

// needsDeleteToPurge stands in front of a route whose body may ask to purge,
// which needs the delete permission as well as the one the route names.
func needsDeleteToPurge(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if api.Allowed(r.Context(), api.Delete) {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
		if err != nil {
			api.Fail(w, 413, "The request is too large.")
			return
		}
		var step PlanStep
		if json.Unmarshal(body, &step) == nil && step.Action == "purge" {
			api.Fail(w, 403, "Deleting for good needs the delete permission.")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

var (
	writerDown  = api.Error{Status: 503, When: "The private writer is not running or its result could not be confirmed. Read the list again before retrying."}
	planRefused = api.Error{Status: 409, When: "The plan no longer matches the files on disk, or is part-way through another step. The body says why, with the plan as it stands."}
	badRequest  = api.Error{Status: 400, When: "The body is not the JSON described."}
	tooLarge    = api.Error{Status: 413, When: "The body is larger than the writer takes."}
)

// WriterRoutes serves the routes that move files, each forwarded to the
// private writer at upstream with secret.
func (s *Store) WriterRoutes(m *api.Mux, upstream, secret string) {
	bin := BinGateway(upstream, secret)
	moveErrors := []api.Error{badRequest, planRefused, tooLarge, writerDown}
	m.Handle(api.Route{
		Method: "GET", Path: "/api/bin", Tag: "Bin", Needs: api.Read,
		Summary: "List moves into the Bin",
		Doc:     "Every plan made for moving files into the Bin, with where each stands. A move is recorded before it starts, so one cut short is listed here too, and can be finished or put back.",
		Returns: []*BinPlan{}, Errors: []api.Error{writerDown},
	}, bin)
	m.Handle(api.Route{
		Method: "POST", Path: "/api/bin/preview", Tag: "Bin", Needs: api.Bin,
		Summary: "Plan moving files into the Bin",
		Doc:     "Checks each file and says exactly what would move where, sidecars included. Nothing moves until the plan is carried out.",
		Body:    BinSelection{}, Returns: BinPlan{}, Errors: moveErrors,
	}, bin)
	m.Handle(api.Route{
		Method: "POST", Path: "/api/bin/execute", Tag: "Bin", Needs: api.Bin,
		Summary: "Carry out a Bin plan",
		Doc:     "Moves the plan's files into the Bin, puts them back, or deletes them for good. Each file is checked against the plan again first. Once started it runs to the end even if the caller goes away.",
		Body:    PlanStep{}, Returns: BinPlan{}, Errors: append([]api.Error{{Status: 403, When: "An addon asked to purge without the delete permission."}}, moveErrors...),
	}, needsDeleteToPurge(bin))

	legacy := LegacyBinGateway(upstream, secret)
	m.Handle(api.Route{
		Method: "POST", Path: "/api/legacy-bin/preview", Addon: AddonClassic, Tag: "Daddy Cull classic", Needs: api.Bin,
		Summary: "Plan restoring or deleting files from the earlier app's Bin",
		Doc:     "Takes 1 to 20 ids from GET /api/legacy-bin and plans every file of their batches, sidecars included, checking and fingerprinting each where it is now, even if it has since moved to another disk. Nothing moves until the plan is carried out, to restore or to purge.",
		Body:    BinSelection{}, Returns: LegacyBinPlan{}, Errors: moveErrors,
	}, legacy)
	m.Handle(api.Route{
		Method: "POST", Path: "/api/legacy-bin/execute", Addon: AddonClassic, Tag: "Daddy Cull classic", Needs: api.Bin,
		Summary: "Carry out a plan for the earlier app's Bin",
		Doc:     "The action is restore or purge; purge also needs the delete permission and the confirmation.",
		Body:    PlanStep{}, Returns: LegacyBinPlan{}, Errors: moveErrors,
	}, needsDeleteToPurge(legacy))

	shots := ScreenshotGateway(upstream, secret)
	m.Handle(api.Route{
		Method: "POST", Path: "/api/screenshot-actions/preview", Addon: AddonScreenshots, Tag: "Screenshots", Needs: api.Bin,
		Summary: "Plan keeping or removing a screenshot",
		Doc:     "Keeping plans a copy into the archive under the date in the file's name, under a free name; removing plans a move into the Bin.",
		Body:    ScreenshotChoice{}, Returns: ScreenshotPlan{}, Errors: moveErrors,
	}, shots)
	m.Handle(api.Route{
		Method: "POST", Path: "/api/screenshot-actions/execute", Addon: AddonScreenshots, Tag: "Screenshots", Needs: api.Bin,
		Summary: "Carry out a screenshot plan",
		Doc:     "Keeping copies the files into the archive, checks each copy and then takes them out of the holding area; removing moves them into the Bin. The screenshot must still be waiting for review. A plan cut short is finished by asking again. Answers the plan, kept or bin.",
		Body:    PlanRef{}, Returns: ScreenshotPlan{}, Errors: moveErrors,
	}, shots)
	m.Handle(api.Route{
		Method: "POST", Path: "/api/screenshot-actions/undo", Addon: AddonScreenshots, Tag: "Screenshots", Needs: api.Bin,
		Summary: "Bring a removed screenshot back from the Bin",
		Doc:     "Moves the plan's files back to where they were in the holding area, so the screenshot waits for review again.",
		Body:    PlanRef{}, Returns: ScreenshotPlan{}, Errors: moveErrors,
	}, shots)
	m.Handle(api.Route{
		Method: "POST", Path: "/api/screenshot-actions/purge", Addon: AddonScreenshots, Tag: "Screenshots", Needs: api.Delete,
		Summary: "Delete a removed screenshot for good",
		Doc:     "Deletes the files of a removal plan in the Bin, sidecars included, each checked against the plan first. The confirmation counts the plan's files. Answers the plan, purged or purged_recovered.",
		Body:    PlanPurge{}, Returns: ScreenshotPlan{}, Errors: moveErrors,
	}, shots)

	upgrades := UpgradeGateway(upstream, secret)
	m.Handle(api.Route{
		Method: "POST", Path: "/api/upgrade-actions/preview", Addon: AddonUpgrades, Tag: "Takeout upgrades", Needs: api.Import,
		Summary: "Plan adding a better copy",
		Doc:     "Checks that the pair is a known upgrade not already added, that both files are still there, and fingerprints the Takeout copy. Answers where the copy would go, beside the archive photo as NAME (hi-res).ext. Nothing is copied until the plan is carried out.",
		Body:    UpgradeChoice{}, Returns: UpgradePlan{}, Errors: moveErrors,
	}, upgrades)
	m.Handle(api.Route{
		Method: "POST", Path: "/api/upgrade-actions/execute", Addon: AddonUpgrades, Tag: "Takeout upgrades", Needs: api.Import,
		Summary: "Carry out an upgrade plan",
		Doc:     "Copies the Takeout file into the archive beside the old one, checks the copy and records it as added. Neither the archive photo nor the Takeout file is moved or deleted. A plan cut short is finished by asking again.",
		Body:    PlanRef{}, Returns: UpgradePlan{}, Errors: moveErrors,
	}, upgrades)

	trash := TrashGateway(upstream, secret)
	trashErrors := []api.Error{badRequest, {Status: 409, When: "A card changed since it was listed, or the confirmation does not match. The body says which."}, tooLarge, writerDown}
	for _, route := range []api.Route{
		{Path: "/api/trash/restore", Needs: api.Bin, Summary: "Restore cards from the Bin", Doc: "Puts every file of each card back where it was, whichever part of Cull put it in the Bin."},
		{Path: "/api/trash/restore-file", Needs: api.Bin, Summary: "Restore one file of a card", Doc: "Puts back one file of a card that holds several, such as the still of a Live Photo. Exactly one key."},
		{Path: "/api/trash/delete", Needs: api.Delete, Summary: "Delete cards from the Bin", Doc: "Deletes the cards' files. With a grace period set, they wait that long on disk and can still be restored from the Log; without one they are gone at once."},
		{Path: "/api/trash/purge-now", Needs: api.Delete, Summary: "Delete cards now", Doc: "Deletes cards already waiting out their grace period, straight away."},
		{Path: "/api/trash/empty", Needs: api.Delete, Summary: "Empty the Bin", Doc: "Deletes every card in the Bin. The confirmation counts every card; no keys are sent."},
	} {
		route.Method, route.Tag, route.Body, route.Returns, route.Errors = "POST", "Bin", TrashSelection{}, TrashResult{}, trashErrors
		m.Handle(route, trash)
	}
}

// MediaRoutes serves the files themselves, from the read-only mounts in roots
// or, without any, from the thumbnail service at upstream.
func (s *Store) MediaRoutes(m *api.Mux, roots MediaRoots, upstream, posters string) {
	book := m.Book()
	book.Tag("Media", "The files themselves, and smaller copies of them for grids. Every route here is read-only and serves byte ranges, so video can be played and seeked in place.")
	mode := api.Param{Name: "mode", In: "path", Type: "string", Required: true, Enum: []string{"preview", "original"}, Example: "preview",
		Doc: "preview is a picture small enough for a grid, a frame for a video; original is the file as it is, converted to a playable video where the browser cannot play it."}
	size := api.Query("size", "string", "large asks for a bigger preview, for the viewer.")
	size.Enum = []string{"large"}
	notFound := api.Error{Status: 404, When: "There is no such file, or it cannot be shown."}
	local := roots.Archive != "" || roots.Screenshots != "" || roots.Upgrades != "" || roots.Disks != "" || roots.Review != ""
	media := api.Route{
		Method: "GET", Path: "/api/media/{id}/{mode}", Tag: "Media", Needs: api.Read,
		Summary:  "Get a file",
		Doc:      "A file of the catalogue by its id: a preview for a grid or the original. The response's type is the file's own.",
		Params:   []api.Param{api.PathInt("id", "The file's id in the catalogue.", "1"), mode, size},
		Produces: "image/*, video/*", Errors: []api.Error{notFound},
	}
	switch {
	case local:
		m.Handle(media, s.LocalMediaHandler(roots))
		m.Handle(api.Route{
			Method: "GET", Path: "/api/media/{id}/live", Tag: "Media", Needs: api.Read,
			Summary:  "Get a Live Photo's video",
			Doc:      "The clip of a Live Photo, by the photo's id: a file marked live in any list. The original .MOV where there is one, converted to a playable video where the browser cannot play it.",
			Params:   []api.Param{api.PathInt("id", "The photo's id in the catalogue.", "1")},
			Produces: "video/*", Errors: []api.Error{notFound},
		}, s.LiveClipHandler(roots))
		s.DetailsRoute(m, roots)
		s.ReadSidecarsFrom(roots)
		m.Handle(api.Route{
			Method: "GET", Path: "/api/bin-media/{id}/{mode}", Tag: "Media", Needs: api.Read,
			Summary:  "Get a file from the earlier app's Bin",
			Doc:      "The Bin page shows these with everything else, so this answers whether the Daddy Cull classic addon is on or not.",
			Params:   []api.Param{api.PathInt("id", "The file's id in the earlier app's Bin, from GET /api/legacy-bin.", "1"), mode, size},
			Produces: "image/*, video/*", Errors: []api.Error{notFound},
		}, s.LegacyBinMediaHandler(roots))
		m.Handle(api.Route{
			Method: "GET", Path: "/api/binned-media/{source}/{plan}/{index}/{mode}", Tag: "Media", Needs: api.Read,
			Summary: "Get a file waiting in the Bin",
			Doc:     "A file moved into the Bin, by the plan that moved it and its place in the plan. A file that has left the Bin is not served.",
			Params: []api.Param{
				{Name: "source", In: "path", Type: "string", Required: true, Enum: []string{"bin", "shot"}, Example: "bin", Doc: "Which part of Cull moved it: bin for the review, shot for Screenshots."},
				api.Path("plan", "The plan's id, 32 hexadecimal characters.", "0123456789abcdef0123456789abcdef"),
				api.PathInt("index", "The file's place in the plan, from 0.", "0"),
				mode, size,
			},
			Produces: "image/*, video/*", Errors: []api.Error{notFound},
		}, s.BinnedMediaHandler(roots))
	case upstream != "":
		m.Handle(media, s.MediaHandler(upstream))
	}
	if posters != "" {
		m.Handle(api.Route{
			Method: "GET", Path: "/api/social-poster/{id}", Addon: AddonSocial, Tag: "Media", Needs: api.Read,
			Summary:  "Get a video's poster frame",
			Doc:      "The frame the detection scan kept for a video saved from social apps, as a JPEG.",
			Params:   []api.Param{api.PathInt("id", "The video's id in the catalogue.", "1")},
			Produces: "image/jpeg", Errors: []api.Error{notFound},
		}, s.SocialPosterHandler(posters))
	}
}
