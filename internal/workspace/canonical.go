package workspace

import (
	"sort"

	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

// canonicalData returns a dataset whose slices are grouped and ordered
// deterministically so it can be marshaled or compared byte-for-byte.
func canonicalData(data repository.Data) repository.Data {
	specimens := append([]domain.Specimen(nil), data.Specimens...)
	sort.Slice(specimens, func(i, j int) bool { return specimens[i].ID < specimens[j].ID })

	plans := append([]domain.PrepPlan(nil), data.Plans...)
	sort.Slice(plans, func(i, j int) bool { return plans[i].ID < plans[j].ID })

	progress := append([]domain.ProgressEntry(nil), data.Progress...)
	sort.Slice(progress, func(i, j int) bool {
		if progress[i].PlanID != progress[j].PlanID {
			return progress[i].PlanID < progress[j].PlanID
		}
		return progress[i].Sequence < progress[j].Sequence
	})

	releases := append([]domain.ReleaseRecord(nil), data.Releases...)
	sort.Slice(releases, func(i, j int) bool { return releases[i].ID < releases[j].ID })

	return repository.Data{
		Specimens: specimens,
		Plans:     plans,
		Progress:  progress,
		Releases:  releases,
	}
}

func hasCancelledPlan(plans []domain.PrepPlan, specimenID string) bool {
	for _, plan := range plans {
		if plan.SpecimenID == specimenID && plan.Status == domain.PlanCancelled {
			return true
		}
	}
	return false
}

// singleOpenPlan returns the specimen's open plan when there is exactly one.
func singleOpenPlan(plans []domain.PrepPlan, specimenID string) (domain.PrepPlan, bool) {
	var found domain.PrepPlan
	count := 0
	for _, plan := range plans {
		if plan.SpecimenID == specimenID && plan.IsOpen() {
			found = plan
			count++
		}
	}
	return found, count == 1
}
