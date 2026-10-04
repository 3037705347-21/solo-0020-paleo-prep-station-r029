package workspace

import (
	"time"
)

type fixture struct {
	now time.Time
}

func newFixture() fixture {
	base, _ := time.Parse(time.RFC3339Nano, "2026-09-01T08:00:00Z")
	return fixture{now: base}
}

func (f fixture) envelope(specimens []SpecimenDTO, plans []PlanDTO, progress []ProgressDTO, releases []ReleaseDTO) Package {
	return Package{
		FormatVersion: FormatVersion,
		Kind:          PackageKind,
		GeneratedAt:   f.now,
		Specimens:     specimens,
		Plans:         plans,
		Progress:      progress,
		Releases:      releases,
	}
}

func (f fixture) specimen(id string, status string, version int) SpecimenDTO {
	return SpecimenDTO{
		ID:            id,
		CatalogNumber: "CAT-" + id,
		FieldLabel:    "北坡-" + id,
		TaxonHint:     "大型脊椎动物",
		MatrixKind:    "粉砂岩",
		JacketNotes:   "夹克完好",
		Status:        status,
		Version:       version,
		CreatedAt:     f.now.Format(time.RFC3339Nano),
		UpdatedAt:     f.now.Add(time.Duration(version) * time.Hour).Format(time.RFC3339Nano),
	}
}

func (f fixture) plan(id, specimenID, status string, version int) PlanDTO {
	plan := PlanDTO{
		ID:             id,
		SpecimenID:     specimenID,
		Objective:      "暴露主要关节面",
		Method:         "手工显微剥离",
		Adhesive:       "可逆树脂",
		TargetExposure: 80,
		Status:         status,
		ApprovedBy:     "",
		ApprovedAt:     nil,
		Version:        version,
		CreatedAt:      f.now.Format(time.RFC3339Nano),
		UpdatedAt:      f.now.Add(time.Duration(version) * time.Hour).Format(time.RFC3339Nano),
	}
	if status == "approved" || status == "active" || status == "completed" {
		plan.ApprovedBy = "curator-lin"
		stamp := f.now.Add(30 * time.Minute).Format(time.RFC3339Nano)
		plan.ApprovedAt = &stamp
	}
	return plan
}

func (f fixture) progress(id, planID string, sequence, percent int) ProgressDTO {
	return ProgressDTO{
		ID:         id,
		PlanID:     planID,
		Sequence:   sequence,
		Stage:      "阶段-" + string(rune('A'+sequence-1)),
		Percent:    percent,
		Note:       "链路记录",
		RecordedBy: "preparator-chen",
		CreatedAt:  f.now.Add(time.Duration(sequence) * 24 * time.Hour).Format(time.RFC3339Nano),
	}
}

func (f fixture) release(id, specimenID string) ReleaseDTO {
	return ReleaseDTO{
		ID:         id,
		SpecimenID: specimenID,
		Purpose:    "比较解剖研究",
		Recipient:  "地层研究组",
		ApprovedBy: "keeper-zhou",
		CreatedAt:  f.now.Add(10 * 24 * time.Hour).Format(time.RFC3339Nano),
	}
}
