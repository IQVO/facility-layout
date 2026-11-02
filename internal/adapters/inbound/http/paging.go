package http

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/claudioed/facility-layout/internal/adapters/inbound/http/page"
)

// pageParams reads `limit` and `cursor` for a keyset-paginated collection GET
// (REST guideline section 3) and rejects any query parameter the collection
// does not declare (section 4.2). On a bad request it writes the 400 problem
// and returns ok=false.
func pageParams(w http.ResponseWriter, r *http.Request, filters ...string) (int, *page.Position, bool) {
	if name := page.Unknown(r, filters...); name != "" {
		writeProblem(w, http.StatusBadRequest,
			problemInfo{"unknown-query-parameter", "Unknown query parameter"},
			fmt.Sprintf("query parameter %q is not accepted by this collection", name), r.URL.Path)
		return 0, nil, false
	}
	limit, err := page.Limit(r)
	if err != nil {
		writeProblem(w, http.StatusBadRequest,
			problemInfo{"invalid-limit", "Invalid limit"}, err.Error(), r.URL.Path)
		return 0, nil, false
	}
	after, err := page.After(r)
	if err != nil {
		writeProblem(w, http.StatusBadRequest,
			problemInfo{"invalid-cursor", "Invalid cursor"}, err.Error(), r.URL.Path)
		return 0, nil, false
	}
	return limit, after, true
}

// keysetPage orders rows by (Sort, ID), keeps only the rows strictly after the
// cursor position and cuts the page with page.Of. Ties on the sort key are
// broken by the id, so a page boundary never skips or repeats a row.
func keysetPage[T any](rows []T, limit int, after *page.Position, at func(T) page.Position) page.Page[T] {
	sorted := make([]T, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool { return positionLess(at(sorted[i]), at(sorted[j])) })
	start := 0
	if after != nil {
		start = sort.Search(len(sorted), func(i int) bool { return positionLess(*after, at(sorted[i])) })
	}
	rest := sorted[start:]
	if len(rest) > page.FetchSize(limit) {
		rest = rest[:page.FetchSize(limit)]
	}
	return page.Of(rest, limit, at)
}

func positionLess(a, b page.Position) bool {
	if a.Sort != b.Sort {
		return a.Sort < b.Sort
	}
	return a.ID < b.ID
}

// writePage pages rows and writes the {items,nextCursor} envelope.
func writePage[T any](w http.ResponseWriter, rows []T, limit int, after *page.Position, at func(T) page.Position) {
	writeJSON(w, http.StatusOK, keysetPage(rows, limit, after, at))
}

func byID(id string) page.Position { return page.Position{Sort: id, ID: id} }

func siteAt(s siteResponse) page.Position { return byID(s.SiteCode) }

// siteRoleDefaultAt keeps the domain's fixed table order (ADR-0037): the
// sort key is the row's zero-padded position in that static table.
func siteRoleDefaultAt(rows []siteRoleTransferDefaultResponse) func(siteRoleTransferDefaultResponse) page.Position {
	rank := make(map[string]int, len(rows))
	for i, row := range rows {
		rank[row.SiteRole] = i
	}
	return func(d siteRoleTransferDefaultResponse) page.Position {
		return page.Position{Sort: fmt.Sprintf("%04d", rank[d.SiteRole]), ID: d.SiteRole}
	}
}

func zoneAt(z zoneResponse) page.Position { return byID(z.ZoneID) }

// aisleAt keeps aisles in SequenceHint walk order; the hint is never
// negative, so a zero-padded decimal sorts like the integer.
func aisleAt(a aisleResponse) page.Position {
	return page.Position{Sort: fmt.Sprintf("%010d", a.SequenceHint), ID: a.AisleID}
}

func crossAisleAt(c crossAisleResponse) page.Position {
	return byID(c.ZoneID + "|" + c.FromAisle + "|" + c.ToAisle + "|" + c.AtBay)
}

func fixedStructureAt(f fixedStructureResponse) page.Position { return byID(f.ID) }

func locationTypeAt(t locationTypeResponse) page.Position { return byID(t.Name) }

func placementRuleAt(p placementRuleResponse) page.Position { return byID(p.RuleID) }

func locationSlotAt(s locationSlotResponse) page.Position { return byID(s.LocationCode) }
