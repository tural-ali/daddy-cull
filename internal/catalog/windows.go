package catalog

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
)

// Stable keys, rather than offsets, keep progress when earlier cards leave.
func window[T any](items []T, limit int, after string, key func(T) string) ([]T, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", ErrInvalid
	}
	cursor := ""
	if after != "" {
		raw, err := base64.RawURLEncoding.DecodeString(after)
		if err != nil || len(raw) > 1024 {
			return nil, "", ErrInvalid
		}
		cursor = string(raw)
	}
	sort.SliceStable(items, func(i, j int) bool { return key(items[i]) < key(items[j]) })
	start := sort.Search(len(items), func(i int) bool { return key(items[i]) > cursor })
	end := min(start+limit, len(items))
	next := ""
	if end < len(items) {
		next = base64.RawURLEncoding.EncodeToString([]byte(key(items[end-1])))
	}
	return items[start:end], next, nil
}

// TrashGroupSize counts the whole batch an action affects, across page boundaries.
type TrashGroupSize struct {
	// Files counts every card in the batch.
	Files int `json:"files"`
	// Bytes is the total stored size in bytes.
	Bytes int64 `json:"bytes"`
}

// TrashPage is a bounded view of the Bin with full-library and batch totals.
type TrashPage struct {
	// Items are the cards on this page.
	Items []TrashItem `json:"items"`
	// Total counts all matching cards, across every page.
	Total int `json:"total"`
	// Bytes is the total stored size in bytes.
	Bytes int64 `json:"bytes"`
	// Next is the continuation cursor, empty on the last page.
	Next string `json:"next"`
	// Groups gives the complete action size for batches represented on this page.
	Groups map[string]TrashGroupSize `json:"groups"`
}

func trashKey(item TrashItem) string {
	return fmt.Sprintf("%020d:%s", int64(9999999999)-instant(item.RemovedAt).Unix(), item.Key)
}
func trashWindow(items []TrashItem, limit int, after string) (TrashPage, error) {
	p := TrashPage{Total: len(items), Groups: map[string]TrashGroupSize{}}
	groups := map[string]TrashGroupSize{}
	for _, item := range items {
		p.Bytes += item.Size
		g := groups[item.Group]
		g.Files++
		g.Bytes += item.Size
		groups[item.Group] = g
	}
	var err error
	p.Items, p.Next, err = window(items, limit, after, trashKey)
	for _, item := range p.Items {
		p.Groups[item.Group] = groups[item.Group]
	}
	return p, err
}

func pageLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit, ok := intParam(w, r, "limit", 50)
	if !ok {
		return 0, false
	}
	if limit < 1 || limit > 100 {
		failFor(w, ErrInvalid, "limit must be 1 to 100.")
		return 0, false
	}
	return limit, true
}
