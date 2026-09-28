package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"daddy-cull/next/internal/api"
)

// DecisionEvent is sent whenever a choice is saved, from any page, tab or
// addon.
type DecisionEvent struct {
	// AssetID is the file the choice is about.
	AssetID int64 `json:"assetId"`
	// Status is the file's status now: unreviewed, keep, later or cull.
	Status string `json:"status"`
	// Favourite is whether the file has a heart now.
	Favourite bool `json:"favourite"`
	// PreviousStatus and PreviousFavourite are how it was before.
	PreviousStatus    string `json:"previousStatus"`
	PreviousFavourite bool   `json:"previousFavourite"`
	// At is when the choice was saved, in RFC 3339 and UTC.
	At string `json:"at"`
}

// eventPoll is how often the stream looks for news. Choices are saved by
// this process but also by the private writer and by imports run beside it,
// so the tables are read rather than trusted to announce themselves.
var eventPoll = 2 * time.Second

// eventBatch bounds how many decisions one look sends, so a caller resuming
// from long ago catches up in steps rather than in one burst.
const eventBatch = 500

// EventRoutes serves the event stream. addons, when not nil, is announced
// each time an addon is turned on or off.
func (s *Store) EventRoutes(m *api.Mux, addons func() <-chan struct{}) {
	m.Book().Tag("Events", "What happens in Cull as it happens, so an addon can follow along without asking again and again.")
	m.HandleFunc(api.Route{
		Method: "GET", Path: "/api/events", Tag: "Events", Needs: api.Read,
		Summary: "Follow what happens",
		Doc: "A stream of server-sent events. `catalogue` is sent first and whenever files arrive in or leave the catalogue, carrying the new generation as in GET /api/catalogue. " +
			"`decision` is sent for every choice saved, carrying a DecisionEvent, with an id: send the last id seen as the Last-Event-ID header, as EventSource does by itself, to carry on from there after a break. " +
			"Without one the stream starts from now. `addons` is sent when an addon is turned on or off, with no data; read GET /api/addons again. A comment is sent every 25 seconds to keep the connection open.",
		Params:   []api.Param{api.Query("after", "integer", "Where to start the decisions from, for a caller that cannot send Last-Event-ID. The header wins when both are sent.")},
		Produces: "text/event-stream",
		Errors:   []api.Error{{Status: 400, When: "Last-Event-ID or after is not a number."}, {Status: 503, When: "The catalogue could not be read."}},
	}, func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			api.Fail(w, 503, "This connection cannot stream.")
			return
		}
		ctx := r.Context()
		after := int64(-1)
		for _, raw := range []string{r.URL.Query().Get("after"), r.Header.Get("Last-Event-ID")} {
			if raw == "" {
				continue
			}
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 {
				api.Fail(w, 400, "Last-Event-ID and after are the id of a decision event.")
				return
			}
			after = n
		}
		if after < 0 {
			var err error
			if after, err = s.lastDecisionEvent(ctx); err != nil {
				api.Fail(w, 503, "The catalogue could not be read. Try again in a moment.")
				return
			}
		}
		generation, err := s.CatalogueGeneration(ctx)
		if err != nil {
			api.Fail(w, 503, "The catalogue could not be read. Try again in a moment.")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		send := func(event, id string, data any) bool {
			if id != "" {
				fmt.Fprintf(w, "id: %s\n", id)
			}
			body := []byte("{}")
			if data != nil {
				body, _ = json.Marshal(data)
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body); err != nil {
				return false
			}
			flusher.Flush()
			return true
		}
		if !send("catalogue", "", Generation{Generation: generation}) {
			return
		}
		poll := time.NewTicker(eventPoll)
		defer poll.Stop()
		alive := time.NewTicker(25 * time.Second)
		defer alive.Stop()
		var changed <-chan struct{}
		if addons != nil {
			changed = addons()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-alive.C:
				if _, err := fmt.Fprint(w, ": still here\n\n"); err != nil {
					return
				}
				flusher.Flush()
			case <-changed:
				changed = addons()
				if !send("addons", "", nil) {
					return
				}
			case <-poll.C:
				events, last, err := s.decisionEventsAfter(ctx, after)
				if err == nil {
					for i, event := range events {
						if !send("decision", strconv.FormatInt(last[i], 10), event) {
							return
						}
						after = last[i]
					}
				}
				if now, err := s.CatalogueGeneration(ctx); err == nil && now != generation {
					generation = now
					if !send("catalogue", "", Generation{Generation: generation}) {
						return
					}
				}
			}
		}
	})
}

func (s *Store) lastDecisionEvent(ctx context.Context) (int64, error) {
	var n int64
	err := s.read.QueryRowContext(ctx, "SELECT coalesce(max(rowid),0) FROM decision_events").Scan(&n)
	return n, err
}

// decisionEventsAfter lists the decisions saved since the one numbered after,
// oldest first, with each one's number.
func (s *Store) decisionEventsAfter(ctx context.Context, after int64) ([]DecisionEvent, []int64, error) {
	rows, err := s.read.QueryContext(ctx, "SELECT rowid,asset_id,status,favourite,previous_status,previous_favourite,created_at FROM decision_events WHERE rowid>? ORDER BY rowid LIMIT ?", after, eventBatch)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var events []DecisionEvent
	var ids []int64
	for rows.Next() {
		var id int64
		var event DecisionEvent
		if err := rows.Scan(&id, &event.AssetID, &event.Status, &event.Favourite, &event.PreviousStatus, &event.PreviousFavourite, &event.At); err != nil {
			return nil, nil, err
		}
		if at, err := time.Parse(time.DateTime, event.At); err == nil {
			event.At = at.UTC().Format(time.RFC3339)
		}
		events = append(events, event)
		ids = append(ids, id)
	}
	return events, ids, rows.Err()
}
