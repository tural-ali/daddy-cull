package catalog

import (
	"context"
	"testing"
)

func TestPagesAndFilters(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Seed(ctx, 1003); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"", "image", "raw", "video"} {
		seen := map[int64]bool{}
		token := ""
		for {
			p, err := s.Page(ctx, token, kind, "", 17)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range p.Assets {
				if seen[a.ID] || (kind != "" && a.Kind != kind) {
					t.Fatal("duplicate or incorrect filter")
				}
				seen[a.ID] = true
			}
			if p.Next == "" {
				break
			}
			token = p.Next
		}
		var want int
		q := "SELECT count(*) FROM assets"
		args := []any{}
		if kind != "" {
			q += " WHERE kind=?"
			args = append(args, kind)
		}
		if err := s.read.QueryRow(q, args...).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if len(seen) != want {
			t.Fatalf("%s: got %d want %d", kind, len(seen), want)
		}
	}
	if err := s.Seed(ctx, 2); err == nil {
		t.Fatal("overwrote existing catalogue")
	}
}

func TestCursorValidationAndAppendBoundary(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.Seed(ctx, 20); err != nil {
		t.Fatal(err)
	}
	p, err := s.Page(ctx, "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Page(ctx, p.Next, "video", "", 10); err == nil {
		t.Fatal("accepted cursor for different filter")
	}
	for _, token := range []string{"!", "e30"} {
		if _, err = s.Page(ctx, token, "", "", 10); err == nil {
			t.Fatal("accepted malformed cursor")
		}
	}
	if _, err = s.Page(ctx, "", "", "", 201); err == nil {
		t.Fatal("accepted unbounded page")
	}
	if _, err = s.write.Exec("INSERT INTO assets VALUES(21,'new',1577836804,'image',1,1,'archive',NULL)"); err != nil {
		t.Fatal(err)
	}
	p, err = s.Page(ctx, p.Next, "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Assets) != 10 || p.Next != "" {
		t.Fatalf("append leaked into snapshot: %+v", p)
	}
	for _, a := range p.Assets {
		if a.ID == 21 {
			t.Fatal("new asset leaked")
		}
	}
}
