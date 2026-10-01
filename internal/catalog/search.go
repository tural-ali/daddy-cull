package catalog

import (
	"context"
	"daddy-cull/next/internal/api"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// SearchQuery limits a read-only library search to an explicit set of filters.
type SearchQuery struct {
	Text, Kind, Status, From, To, After string
	Favourite                           bool
	Limit                               int
}
type searchCursor struct {
	Query    string
	Time, ID int64
}

func (s *Store) Search(ctx context.Context, q SearchQuery) (Page, error) {
	p := Page{Assets: []Asset{}}
	if len(q.Text) > 200 || q.Limit < 1 || q.Limit > 100 {
		return p, ErrInvalid
	}
	if q.Kind != "" && q.Kind != "image" && q.Kind != "raw" && q.Kind != "video" {
		return p, ErrInvalid
	}
	if q.Status != "" && q.Status != "unreviewed" && q.Status != "keep" && q.Status != "later" {
		return p, ErrInvalid
	}
	signature, _ := json.Marshal([]any{q.Text, q.Kind, q.Status, q.From, q.To, q.Favourite})
	c := searchCursor{Query: string(signature), Time: -1}
	if q.After != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.After)
		if err != nil || len(raw) > 2048 || json.Unmarshal(raw, &c) != nil || c.Query != string(signature) {
			return p, ErrInvalid
		}
	}
	query := assetSelect + ` WHERE a.source_id='archive' AND (a.captured_at,a.id)>(?,?)
 AND NOT EXISTS(SELECT 1 FROM file_state fs WHERE fs.asset_id=a.id AND fs.state!='restored')
 AND NOT EXISTS(SELECT 1 FROM missing_assets m WHERE m.asset_id=a.id)
 AND a.id NOT IN (` + liveClipAssets + `)`
	args := []any{c.Time, c.ID}
	if q.Text != "" {
		query += ` AND (instr(lower(a.relative_path),?)>0 OR EXISTS(SELECT 1 FROM exposures x WHERE x.asset_id=a.id AND x.size_bytes=a.size_bytes AND instr(lower(x.model),?)>0))`
		text := strings.ToLower(strings.TrimSpace(q.Text))
		args = append(args, text, text)
	}
	if q.Kind != "" {
		query += " AND a.kind=?"
		args = append(args, q.Kind)
	}
	if q.Status != "" {
		query += " AND COALESCE(d.status,'unreviewed')=?"
		args = append(args, q.Status)
	}
	if q.Favourite {
		query += " AND d.favourite=1"
	}
	for _, date := range []struct {
		value, op string
		next      bool
	}{{q.From, ">=", false}, {q.To, "<", true}} {
		if date.value == "" {
			continue
		}
		at, err := time.Parse("2006-01-02", date.value)
		if err != nil {
			return p, ErrInvalid
		}
		if date.next {
			at = at.AddDate(0, 0, 1)
		}
		query += " AND a.captured_at" + date.op + "?"
		args = append(args, at.Unix())
	}
	if q.From != "" && q.To != "" && q.From > q.To {
		return p, ErrInvalid
	}
	query += " ORDER BY a.captured_at,a.id LIMIT ?"
	args = append(args, q.Limit+1)
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var a Asset
		if err = scanAsset(rows, &a); err != nil {
			rows.Close()
			return p, err
		}
		p.Assets = append(p.Assets, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	if len(p.Assets) > q.Limit {
		p.Assets = p.Assets[:q.Limit]
		last := p.Assets[len(p.Assets)-1]
		raw, _ := json.Marshal(searchCursor{Query: string(signature), Time: last.CapturedAt, ID: last.ID})
		p.Next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return p, nil
}

func (s *Store) searchRoutes(m *api.Mux) {
	m.HandleFunc(api.Route{Method: "GET", Path: "/api/search", Tag: "Library", Needs: api.Read, Summary: "Search the library", Doc: "Search filenames, folders and indexed camera models. Results are bounded and in capture order. A cursor belongs to its filters.", Params: []api.Param{
		api.Query("q", "string", "Filename, folder or indexed camera model, up to 200 characters."), api.Query("kind", "string", "image, raw or video."), api.Query("status", "string", "unreviewed, keep or later."), api.Query("favourite", "boolean", "1 to show favourites only."), api.Query("from", "string", "First capture date, YYYY-MM-DD."), api.Query("to", "string", "Last capture date, inclusive, YYYY-MM-DD."), api.Query("after", "string", "The previous page's next cursor."), api.Query("limit", "integer", "1 to 100 results, 50 by default.")}, Returns: Page{}, Errors: []api.Error{{Status: 400, When: "Invalid filters or cursor"}, {Status: 500, When: "The catalogue could not be read"}}}, func(w http.ResponseWriter, r *http.Request) {
		limit, ok := pageLimit(w, r)
		if !ok {
			return
		}
		q := r.URL.Query()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		page, err := s.Search(ctx, SearchQuery{Text: q.Get("q"), Kind: q.Get("kind"), Status: q.Get("status"), Favourite: q.Get("favourite") == "1", From: q.Get("from"), To: q.Get("to"), After: q.Get("after"), Limit: limit})
		if err != nil {
			failFor(w, err, "Use valid search filters and the cursor returned by this search.")
			return
		}
		writeJSON(w, page)
	})
}
