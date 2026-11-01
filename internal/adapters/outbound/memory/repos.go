package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/facility-layout/internal/domain/aisle"
	"github.com/claudioed/facility-layout/internal/domain/site"
	"github.com/claudioed/facility-layout/internal/domain/structure"
	"github.com/claudioed/facility-layout/internal/domain/zone"
)

// SiteRepo is an in-memory implementation of ports.SiteRepo.
type SiteRepo struct {
	mu    sync.RWMutex
	sites map[string]*site.Site
}

// NewSiteRepo builds an empty SiteRepo.
func NewSiteRepo() *SiteRepo {
	return &SiteRepo{sites: make(map[string]*site.Site)}
}

// Save stores the site under its code.
func (r *SiteRepo) Save(_ context.Context, s *site.Site) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sites[s.Code()] = s
	return nil
}

// FindByCode returns the site, or (nil, nil) when it does not exist.
func (r *SiteRepo) FindByCode(_ context.Context, code string) (*site.Site, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sites[code]
	if !ok {
		return nil, nil
	}
	return s, nil
}

// List returns every site, ordered by code.
func (r *SiteRepo) List(_ context.Context) ([]*site.Site, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*site.Site, 0, len(r.sites))
	for _, s := range r.sites {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code() < out[j].Code() })
	return out, nil
}

// ListCapabilityHeartbeatDue implements ports.SiteCapabilityHeartbeatSource.
func (r *SiteRepo) ListCapabilityHeartbeatDue(_ context.Context, notAfter time.Time, limit int) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	codes := make([]string, 0)
	for _, s := range r.sites {
		last := s.CapabilityHeartbeatAt()
		if s.IsActive() && s.CapabilityRevision() > 0 && (last.IsZero() || !last.After(notAfter)) {
			codes = append(codes, s.Code())
		}
	}
	sort.Strings(codes)
	if limit > 0 && len(codes) > limit {
		codes = codes[:limit]
	}
	return codes, nil
}

// ZoneRepo is an in-memory implementation of ports.ZoneRepo.
type ZoneRepo struct {
	mu    sync.RWMutex
	zones map[string]*zone.Zone
}

// NewZoneRepo builds an empty ZoneRepo.
func NewZoneRepo() *ZoneRepo {
	return &ZoneRepo{zones: make(map[string]*zone.Zone)}
}

// Save stores the zone under its id.
func (r *ZoneRepo) Save(_ context.Context, z *zone.Zone) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.zones[z.ID()] = z
	return nil
}

// FindByID returns the zone, or (nil, nil) when it does not exist.
func (r *ZoneRepo) FindByID(_ context.Context, id string) (*zone.Zone, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	z, ok := r.zones[id]
	if !ok {
		return nil, nil
	}
	return z, nil
}

// ListBySite returns every zone in a site, ordered by id.
func (r *ZoneRepo) ListBySite(_ context.Context, siteCode string) ([]*zone.Zone, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*zone.Zone, 0)
	for _, z := range r.zones {
		if z.SiteCode() == siteCode {
			out = append(out, z)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// AisleRepo is an in-memory implementation of ports.AisleRepo.
type AisleRepo struct {
	mu     sync.RWMutex
	aisles map[string]*aisle.Aisle
}

// NewAisleRepo builds an empty AisleRepo.
func NewAisleRepo() *AisleRepo {
	return &AisleRepo{aisles: make(map[string]*aisle.Aisle)}
}

// Save stores the aisle under its id.
func (r *AisleRepo) Save(_ context.Context, a *aisle.Aisle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aisles[a.ID()] = a
	return nil
}

// FindByID returns the aisle, or (nil, nil) when it does not exist.
func (r *AisleRepo) FindByID(_ context.Context, id string) (*aisle.Aisle, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.aisles[id]
	if !ok {
		return nil, nil
	}
	return a, nil
}

// ListByZone returns every aisle in a zone, ordered by walk-order hint then code.
func (r *AisleRepo) ListByZone(_ context.Context, zoneID string) ([]*aisle.Aisle, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*aisle.Aisle, 0)
	for _, a := range r.aisles {
		if a.ZoneID() == zoneID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SequenceHint() != out[j].SequenceHint() {
			return out[i].SequenceHint() < out[j].SequenceHint()
		}
		return out[i].AisleCode() < out[j].AisleCode()
	})
	return out, nil
}

// crossAisleKey uniquely identifies a cross-aisle regardless of which side
// was named "from" — the connection is symmetric.
type crossAisleKey struct {
	zoneID, a, b, atBay string
}

func newCrossAisleKey(zoneID, fromAisle, toAisle, atBay string) crossAisleKey {
	if fromAisle > toAisle {
		fromAisle, toAisle = toAisle, fromAisle
	}
	return crossAisleKey{zoneID: zoneID, a: fromAisle, b: toAisle, atBay: atBay}
}

// CrossAisleRepo is an in-memory implementation of ports.CrossAisleRepo.
type CrossAisleRepo struct {
	mu   sync.RWMutex
	rows map[crossAisleKey]*aisle.CrossAisle
}

// NewCrossAisleRepo builds an empty CrossAisleRepo.
func NewCrossAisleRepo() *CrossAisleRepo {
	return &CrossAisleRepo{rows: make(map[crossAisleKey]*aisle.CrossAisle)}
}

// Save stores the cross-aisle under its symmetric key.
func (r *CrossAisleRepo) Save(_ context.Context, c *aisle.CrossAisle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[newCrossAisleKey(c.ZoneID(), c.FromAisle(), c.ToAisle(), c.AtBay())] = c
	return nil
}

// FindByAisles returns the cross-aisle connecting fromAisle and toAisle at
// atBay, or (nil, nil) when none exists.
func (r *CrossAisleRepo) FindByAisles(_ context.Context, zoneID, fromAisle, toAisle, atBay string) (*aisle.CrossAisle, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.rows[newCrossAisleKey(zoneID, fromAisle, toAisle, atBay)]
	if !ok {
		return nil, nil
	}
	return c, nil
}

// ListByZone returns every cross-aisle in a zone, ordered by from-aisle,
// to-aisle, bay.
func (r *CrossAisleRepo) ListByZone(_ context.Context, zoneID string) ([]*aisle.CrossAisle, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*aisle.CrossAisle, 0)
	for _, c := range r.rows {
		if c.ZoneID() == zoneID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromAisle() != out[j].FromAisle() {
			return out[i].FromAisle() < out[j].FromAisle()
		}
		if out[i].ToAisle() != out[j].ToAisle() {
			return out[i].ToAisle() < out[j].ToAisle()
		}
		return out[i].AtBay() < out[j].AtBay()
	})
	return out, nil
}

// FixedStructureRepo is an in-memory implementation of ports.FixedStructureRepo.
type FixedStructureRepo struct {
	mu         sync.RWMutex
	structures map[string]*structure.FixedStructure
}

// NewFixedStructureRepo builds an empty FixedStructureRepo.
func NewFixedStructureRepo() *FixedStructureRepo {
	return &FixedStructureRepo{structures: make(map[string]*structure.FixedStructure)}
}

// Save stores the structure under its id.
func (r *FixedStructureRepo) Save(_ context.Context, f *structure.FixedStructure) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.structures[f.ID()] = f
	return nil
}

// FindByID returns the structure, or (nil, nil) when it does not exist.
func (r *FixedStructureRepo) FindByID(_ context.Context, id string) (*structure.FixedStructure, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.structures[id]
	if !ok {
		return nil, nil
	}
	return f, nil
}

// ListBySite returns every structure in a site, ordered by id.
func (r *FixedStructureRepo) ListBySite(_ context.Context, siteCode string) ([]*structure.FixedStructure, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*structure.FixedStructure, 0)
	for _, f := range r.structures {
		if f.SiteCode() == siteCode {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}
