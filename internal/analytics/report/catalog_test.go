package report_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/facility-layout/internal/analytics/report"
)

// fakeStore is an in-memory implementation of both report ports used to
// exercise report derivation from a synthetic event sequence. It is a test
// double local to this package: the production stores live in the
// analyticsstore outbound adapter.
type fakeStore struct {
	seen map[string]bool
	rows map[report.RowKey]*report.Row
}

func newFakeStore() *fakeStore {
	return &fakeStore{seen: map[string]bool{}, rows: map[report.RowKey]*report.Row{}}
}

func dayBucket(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }

func (s *fakeStore) dup(eventId string) bool {
	if s.seen[eventId] {
		return true
	}
	s.seen[eventId] = true
	return false
}

func (s *fakeStore) row(scope string, at time.Time) *report.Row {
	k := report.RowKey{Scope: scope, DayBucket: dayBucket(at)}
	r, ok := s.rows[k]
	if !ok {
		r = &report.Row{Key: k}
		s.rows[k] = r
	}
	return r
}

func (s *fakeStore) ApplySiteRegistered(_ context.Context, eventId, scope string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	s.row(scope, at).SitesRegistered++
	return nil
}

func (s *fakeStore) ApplyZoneRegistered(_ context.Context, eventId, scope string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	s.row(scope, at).ZonesRegistered++
	return nil
}

func (s *fakeStore) ApplyAisleRegistered(_ context.Context, eventId, scope string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	s.row(scope, at).AislesRegistered++
	return nil
}

func (s *fakeStore) ApplyLocationTypeRegistered(_ context.Context, eventId, scope string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	s.row(scope, at).LocationTypesRegistered++
	return nil
}

func (s *fakeStore) ApplyPlacementRuleDefined(_ context.Context, eventId, scope string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	s.row(scope, at).PlacementRulesDefined++
	return nil
}

func (s *fakeStore) ApplyLocationSlotRegistered(_ context.Context, eventId, scope string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	s.row(scope, at).SlotsRegistered++
	return nil
}

func (s *fakeStore) ApplyLocationSlotDecommissioned(_ context.Context, eventId, scope string, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	s.row(scope, at).SlotsDecommissioned++
	return nil
}

func (s *fakeStore) ApplyFacilityLayoutImported(_ context.Context, eventId, scope string, submitted, imported, rejected int, at time.Time) error {
	if s.dup(eventId) {
		return nil
	}
	r := s.row(scope, at)
	r.BulkImports++
	r.ImportRowsSubmitted += submitted
	r.ImportRowsImported += imported
	r.ImportRowsRejected += rejected
	return nil
}

func (s *fakeStore) Query(_ context.Context, q report.ReportQuery) (report.CatalogReport, error) {
	out := report.CatalogReport{}
	for k, r := range s.rows {
		if k.DayBucket.Before(q.From) || !k.DayBucket.Before(q.To) {
			continue
		}
		if q.Scope != "" && k.Scope != q.Scope {
			continue
		}
		out.Rows = append(out.Rows, *r)
	}
	return out, nil
}

func (s *fakeStore) FreshnessLag(_ context.Context) (time.Duration, error) { return 0, nil }

func TestCatalogReport_DerivesFromEventSequence(t *testing.T) {
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	s := seedOneDayOfCatalogChanges(t, base)

	rep := queryDay(t, s, base)

	bucket := dayBucket(base)
	assertSiteRowGrew(t, rep, bucket)
	assertZoneRowGrew(t, rep, bucket)
	assertCatalogWideRowGrew(t, rep, bucket)
}

// seedOneDayOfCatalogChanges replays one day of catalog changes into a fresh
// store and returns it:
//
//   - WH1: one site, two zones
//   - zone WH1-STOR-AMB: one aisle, three slots, one decommission
//   - catalog-wide: two location types, one placement rule, one import
func seedOneDayOfCatalogChanges(t *testing.T, base time.Time) *fakeStore {
	t.Helper()
	s := newFakeStore()
	ctx := context.Background()
	must(t, s.ApplySiteRegistered(ctx, "e1", "WH1", base))
	must(t, s.ApplyZoneRegistered(ctx, "e2", "WH1", base))
	must(t, s.ApplyZoneRegistered(ctx, "e3", "WH1", base))
	must(t, s.ApplyAisleRegistered(ctx, "e4", "WH1-STOR-AMB", base))
	must(t, s.ApplyLocationSlotRegistered(ctx, "e5", "WH1-STOR-AMB", base))
	must(t, s.ApplyLocationSlotRegistered(ctx, "e6", "WH1-STOR-AMB", base))
	must(t, s.ApplyLocationSlotRegistered(ctx, "e7", "WH1-STOR-AMB", base))
	must(t, s.ApplyLocationSlotDecommissioned(ctx, "e8", "WH1-STOR-AMB", base))
	must(t, s.ApplyLocationTypeRegistered(ctx, "e9", "", base))
	must(t, s.ApplyLocationTypeRegistered(ctx, "e10", "", base))
	must(t, s.ApplyPlacementRuleDefined(ctx, "e11", "", base))
	must(t, s.ApplyFacilityLayoutImported(ctx, "e12", "", 10, 9, 1, base))
	return s
}

// queryDay returns the day-granularity report covering [base-24h, base+24h).
func queryDay(t *testing.T, s *fakeStore, base time.Time) report.CatalogReport {
	t.Helper()
	rep, err := s.Query(context.Background(), report.ReportQuery{
		From:        base.Add(-24 * time.Hour),
		To:          base.Add(24 * time.Hour),
		Granularity: report.GranularityDay,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	return rep
}

// assertSiteRowGrowth checks the WH1-scoped row of the day bucket.
func assertSiteRowGrew(t *testing.T, rep report.CatalogReport, bucket time.Time) {
	t.Helper()
	site := findRow(rep, report.RowKey{Scope: "WH1", DayBucket: bucket})
	if site == nil {
		t.Fatal("no WH1 row")
	}
	if site.SitesRegistered != 1 {
		t.Errorf("SitesRegistered = %d, want 1", site.SitesRegistered)
	}
	if site.ZonesRegistered != 2 {
		t.Errorf("ZonesRegistered = %d, want 2", site.ZonesRegistered)
	}
}

// assertZoneRowGrew checks the WH1-STOR-AMB-scoped row of the day bucket.
func assertZoneRowGrew(t *testing.T, rep report.CatalogReport, bucket time.Time) {
	t.Helper()
	zone := findRow(rep, report.RowKey{Scope: "WH1-STOR-AMB", DayBucket: bucket})
	if zone == nil {
		t.Fatal("no zone row")
	}
	if zone.AislesRegistered != 1 {
		t.Errorf("AislesRegistered = %d, want 1", zone.AislesRegistered)
	}
	if zone.SlotsRegistered != 3 {
		t.Errorf("SlotsRegistered = %d, want 3", zone.SlotsRegistered)
	}
	if zone.SlotsDecommissioned != 1 {
		t.Errorf("SlotsDecommissioned = %d, want 1", zone.SlotsDecommissioned)
	}
}

// assertCatalogWideRowGrew checks the catalog-wide (empty-scope) row of the
// day bucket.
func assertCatalogWideRowGrew(t *testing.T, rep report.CatalogReport, bucket time.Time) {
	t.Helper()
	catalog := findRow(rep, report.RowKey{Scope: "", DayBucket: bucket})
	if catalog == nil {
		t.Fatal("no catalog-wide row")
	}
	if catalog.LocationTypesRegistered != 2 {
		t.Errorf("LocationTypesRegistered = %d, want 2", catalog.LocationTypesRegistered)
	}
	if catalog.PlacementRulesDefined != 1 {
		t.Errorf("PlacementRulesDefined = %d, want 1", catalog.PlacementRulesDefined)
	}
	if catalog.BulkImports != 1 || catalog.ImportRowsSubmitted != 10 || catalog.ImportRowsImported != 9 || catalog.ImportRowsRejected != 1 {
		t.Errorf("import tallies = %+v", catalog)
	}
}

func TestCatalogReport_FiltersAndIdempotency(t *testing.T) {
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	ctx := context.Background()

	tests := []struct {
		name  string
		query report.ReportQuery
		want  int // number of rows expected
	}{
		{"no filter", report.ReportQuery{From: base.Add(-24 * time.Hour), To: base.Add(24 * time.Hour), Granularity: report.GranularityDay}, 2},
		{"scope filter", report.ReportQuery{From: base.Add(-24 * time.Hour), To: base.Add(24 * time.Hour), Scope: "WH1", Granularity: report.GranularityDay}, 1},
		{"window excludes all", report.ReportQuery{From: base.Add(30 * 24 * time.Hour), To: base.Add(60 * 24 * time.Hour), Granularity: report.GranularityDay}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newFakeStore()
			// Apply the same slot registration twice with the same eventId → counts once.
			must(t, s.ApplyLocationSlotRegistered(ctx, "dup", "WH1", base))
			must(t, s.ApplyLocationSlotRegistered(ctx, "dup", "WH1", base))
			must(t, s.ApplyAisleRegistered(ctx, "other", "WH2-STOR-FRZ", base))

			rep, err := s.Query(ctx, tt.query)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(rep.Rows) != tt.want {
				t.Errorf("rows = %d, want %d", len(rep.Rows), tt.want)
			}
			if tt.name == "no filter" {
				wh1 := findRow(rep, report.RowKey{Scope: "WH1", DayBucket: dayBucket(base)})
				if wh1 == nil || wh1.SlotsRegistered != 1 {
					t.Errorf("dedupe failed: WH1 slots = %v", wh1)
				}
			}
		})
	}
}

func findRow(rep report.CatalogReport, k report.RowKey) *report.Row {
	for i := range rep.Rows {
		if rep.Rows[i].Key == k {
			return &rep.Rows[i]
		}
	}
	return nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
}
