package repository

import (
	"sort"
	"sync"

	"example.com/paleo-prep-station/internal/domain"
)

type state struct {
	specimens map[string]domain.Specimen
	plans     map[string]domain.PrepPlan
	progress  map[string][]domain.ProgressEntry
	releases  map[string]domain.ReleaseRecord
}

type MemoryRepository struct {
	mu    sync.Mutex
	state state
}

func NewMemory() *MemoryRepository {
	return &MemoryRepository{
		state: state{
			specimens: make(map[string]domain.Specimen),
			plans:     make(map[string]domain.PrepPlan),
			progress:  make(map[string][]domain.ProgressEntry),
			releases:  make(map[string]domain.ReleaseRecord),
		},
	}
}

func (r *MemoryRepository) Read(fn func(*Mutation) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fn(cloneMutation(r.state))
}

func (r *MemoryRepository) Mutate(fn func(*Mutation) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tx := cloneMutation(r.state)
	if err := fn(tx); err != nil {
		return err
	}
	r.state = tx.snapshot()
	return nil
}

func cloneMutation(source state) *Mutation {
	copied := state{
		specimens: make(map[string]domain.Specimen, len(source.specimens)),
		plans:     make(map[string]domain.PrepPlan, len(source.plans)),
		progress:  make(map[string][]domain.ProgressEntry, len(source.progress)),
		releases:  make(map[string]domain.ReleaseRecord, len(source.releases)),
	}
	for key, value := range source.specimens {
		copied.specimens[key] = value
	}
	for key, value := range source.plans {
		copied.plans[key] = value
	}
	for key, values := range source.progress {
		copied.progress[key] = append([]domain.ProgressEntry(nil), values...)
	}
	for key, value := range source.releases {
		copied.releases[key] = value
	}
	return &Mutation{state: copied}
}

type Mutation struct {
	state state
}

func (m *Mutation) snapshot() state {
	return m.state
}

// Data is the complete, transportable set of business facts held by the
// repository. Slices are always stored in a deterministic order so that two
// workspaces with identical facts compare equal.
type Data struct {
	Specimens []domain.Specimen
	Plans     []domain.PrepPlan
	Progress  []domain.ProgressEntry
	Releases  []domain.ReleaseRecord
}

// ExportData copies every business fact in the transaction into a Data value.
func (m *Mutation) ExportData() Data {
	specimens := m.ListSpecimens()
	plans := m.listPlans()
	progress := make([]domain.ProgressEntry, 0)
	for _, plan := range plans {
		progress = append(progress, m.ProgressForPlan(plan.ID)...)
	}
	releases := make([]domain.ReleaseRecord, 0, len(m.state.releases))
	for _, value := range m.state.releases {
		releases = append(releases, value)
	}
	sort.Slice(releases, func(i, j int) bool {
		if releases[i].CreatedAt.Equal(releases[j].CreatedAt) {
			return releases[i].ID < releases[j].ID
		}
		return releases[i].CreatedAt.Before(releases[j].CreatedAt)
	})
	return Data{
		Specimens: specimens,
		Plans:     plans,
		Progress:  progress,
		Releases:  releases,
	}
}

// ReplaceAll discards the transaction contents and installs the given facts.
// Progress entries are grouped by plan and ordered by sequence. Callers must
// validate data before calling this method; it performs no rule checks.
func (m *Mutation) ReplaceAll(data Data) {
	next := state{
		specimens: make(map[string]domain.Specimen, len(data.Specimens)),
		plans:     make(map[string]domain.PrepPlan, len(data.Plans)),
		progress:  make(map[string][]domain.ProgressEntry),
		releases:  make(map[string]domain.ReleaseRecord, len(data.Releases)),
	}
	for _, value := range data.Specimens {
		next.specimens[value.ID] = value
	}
	for _, value := range data.Plans {
		next.plans[value.ID] = value
	}
	for _, value := range data.Progress {
		entries := next.progress[value.PlanID]
		entries = append(entries, value)
		next.progress[value.PlanID] = entries
	}
	for planID := range next.progress {
		entries := next.progress[planID]
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Sequence < entries[j].Sequence
		})
		next.progress[planID] = entries
	}
	for _, value := range data.Releases {
		next.releases[value.ID] = value
	}
	m.state = next
}

func (m *Mutation) listPlans() []domain.PrepPlan {
	values := make([]domain.PrepPlan, 0, len(m.state.plans))
	for _, value := range m.state.plans {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

func (m *Mutation) GetSpecimen(id string) (domain.Specimen, bool) {
	value, ok := m.state.specimens[id]
	return value, ok
}

func (m *Mutation) FindSpecimenByCatalog(catalogNumber string) (domain.Specimen, bool) {
	for _, value := range m.state.specimens {
		if value.CatalogNumber == catalogNumber {
			return value, true
		}
	}
	return domain.Specimen{}, false
}

func (m *Mutation) ListSpecimens() []domain.Specimen {
	values := make([]domain.Specimen, 0, len(m.state.specimens))
	for _, value := range m.state.specimens {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

func (m *Mutation) PutSpecimen(value domain.Specimen) {
	m.state.specimens[value.ID] = value
}

func (m *Mutation) GetPlan(id string) (domain.PrepPlan, bool) {
	value, ok := m.state.plans[id]
	return value, ok
}

func (m *Mutation) ListPlansForSpecimen(specimenID string) []domain.PrepPlan {
	values := make([]domain.PrepPlan, 0)
	for _, value := range m.state.plans {
		if value.SpecimenID == specimenID {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].CreatedAt.Equal(values[j].CreatedAt) {
			return values[i].ID < values[j].ID
		}
		return values[i].CreatedAt.Before(values[j].CreatedAt)
	})
	return values
}

func (m *Mutation) PutPlan(value domain.PrepPlan) {
	m.state.plans[value.ID] = value
}

func (m *Mutation) HasOpenPlan(specimenID string) bool {
	for _, value := range m.state.plans {
		if value.SpecimenID == specimenID && value.IsOpen() {
			return true
		}
	}
	return false
}

func (m *Mutation) ProgressForPlan(planID string) []domain.ProgressEntry {
	return append([]domain.ProgressEntry(nil), m.state.progress[planID]...)
}

func (m *Mutation) AppendProgress(value domain.ProgressEntry) {
	m.state.progress[value.PlanID] = append(m.state.progress[value.PlanID], value)
}

func (m *Mutation) ReleaseForSpecimen(specimenID string) (domain.ReleaseRecord, bool) {
	for _, value := range m.state.releases {
		if value.SpecimenID == specimenID {
			return value, true
		}
	}
	return domain.ReleaseRecord{}, false
}

func (m *Mutation) PutRelease(value domain.ReleaseRecord) {
	m.state.releases[value.ID] = value
}
