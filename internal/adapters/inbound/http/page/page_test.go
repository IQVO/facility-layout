package page

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

type row struct {
	Sort string
	ID   string
}

func at(r row) Position { return Position(r) }

func TestLimit(t *testing.T) {
	cases := []struct {
		q    string
		want int
		err  error
	}{
		{"", DefaultLimit, nil},
		{"limit=1", 1, nil},
		{"limit=500", 500, nil},
		{"limit=0", 0, ErrInvalidLimit},
		{"limit=501", 0, ErrInvalidLimit},
		{"limit=-3", 0, ErrInvalidLimit},
		{"limit=abc", 0, ErrInvalidLimit},
		{"limit=", 0, ErrInvalidLimit},
		{"limit=5&limit=6", 0, ErrInvalidLimit},
		{"limit=&limit=7", 0, ErrInvalidLimit},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/x?"+c.q, nil)
		got, err := Limit(r)
		if got != c.want || !errors.Is(err, c.err) {
			t.Fatalf("%q: got (%d,%v) want (%d,%v)", c.q, got, err, c.want, c.err)
		}
	}
}

func TestCursorRoundTripAndRejection(t *testing.T) {
	p := Position{Sort: "2026-10-31T10:00:00Z", ID: "a/b c"}
	got, err := Decode(Encode(p))
	if err != nil || *got != p {
		t.Fatalf("round trip: %v %v", got, err)
	}
	for _, bad := range []string{"%%%", "bm90LWpzb24", "e30" /* {} has no id */, "e2k6IiJ9" /* empty id */} {
		if _, err := Decode(bad); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("%q must be rejected, got %v", bad, err)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/x?cursor=%25%25", nil)
	if _, err := After(r); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("After must reject a bad cursor, got %v", err)
	}
	if p, err := After(httptest.NewRequest(http.MethodGet, "/x", nil)); p != nil || err != nil {
		t.Fatalf("absent cursor is the first page")
	}
	if _, err := After(httptest.NewRequest(http.MethodGet, "/x?cursor="+Encode(p)+"&cursor="+Encode(p), nil)); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("a repeated cursor must be rejected, got %v", err)
	}
}

// walk simulates a repository that honours (sort,id) keyset order and walks every page.
func walk(all []row, limit int) (seen []row, pages int) {
	sort.Slice(all, func(i, j int) bool {
		if all[i].Sort != all[j].Sort {
			return all[i].Sort < all[j].Sort
		}
		return all[i].ID < all[j].ID
	})
	var after *Position
	for {
		var rows []row
		for _, r := range all {
			if after != nil && r.Sort < after.Sort || after != nil && r.Sort == after.Sort && r.ID <= after.ID {
				continue
			}
			rows = append(rows, r)
			if len(rows) == FetchSize(limit) {
				break
			}
		}
		pg := Of(rows, limit, at)
		seen = append(seen, pg.Items...)
		pages++
		if pg.NextCursor == "" {
			return
		}
		after, _ = Decode(pg.NextCursor)
	}
}

func TestWalkNoDuplicatesNoSkipsWithTies(t *testing.T) {
	// 11 rows, many sharing the same sort key: ties are the case offset/`from` paging gets wrong.
	var all []row
	for i := 0; i < 11; i++ {
		all = append(all, row{Sort: []string{"a", "a", "b"}[i%3], ID: string(rune('A' + i))})
	}
	for _, limit := range []int{1, 2, 3, 5, 10, 11, 12} {
		seen, _ := walk(append([]row(nil), all...), limit)
		if len(seen) != len(all) {
			t.Fatalf("limit %d: saw %d rows want %d", limit, len(seen), len(all))
		}
		dup := map[string]bool{}
		for _, r := range seen {
			if dup[r.ID] {
				t.Fatalf("limit %d: duplicate %s", limit, r.ID)
			}
			dup[r.ID] = true
		}
	}
}

func TestLastPageHasNoCursorAndItemsNeverNil(t *testing.T) {
	pg := Of[row](nil, 10, at)
	if pg.Items == nil || pg.NextCursor != "" {
		t.Fatalf("empty page must be {items: []} with no cursor: %+v", pg)
	}
	pg = Of([]row{{"a", "1"}, {"a", "2"}}, 2, at)
	if pg.NextCursor != "" || len(pg.Items) != 2 {
		t.Fatalf("exactly limit rows is the last page: %+v", pg)
	}
	pg = Of([]row{{"a", "1"}, {"a", "2"}, {"a", "3"}}, 2, at)
	if pg.NextCursor == "" || len(pg.Items) != 2 {
		t.Fatalf("limit+1 rows means a next page: %+v", pg)
	}
	p, _ := Decode(pg.NextCursor)
	if p.ID != "2" {
		t.Fatalf("cursor must point after the last RETURNED row, got %+v", p)
	}
}

func TestUnknownNamesTheFirstUndeclaredParameter(t *testing.T) {
	cases := []struct {
		q    string
		want string
	}{
		{"", ""},
		{"limit=5&cursor=x", ""},
		{"demandRef=o1&limit=5", ""},
		{"after=x", "after"},
		{"demandRef=o1&offset=3&after=x", "after"}, // sorted: after < offset
		{"x-schemathesis-unknown-property=42", "x-schemathesis-unknown-property"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/x?"+c.q, nil)
		if got := Unknown(r, "demandRef"); got != c.want {
			t.Fatalf("%q: got %q want %q", c.q, got, c.want)
		}
	}
}
