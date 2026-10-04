package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

var fixedNow = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func mustSpecimen(t *testing.T, id, catalog string, status domain.SpecimenStatus, version int, stamp time.Time) domain.Specimen {
	t.Helper()
	value, err := domain.NewSpecimen(id, catalog, "北坡-01", "鱼龙", "泥岩", "备注", stamp)
	if err != nil {
		t.Fatalf("NewSpecimen: %v", err)
	}
	for value.Status != status {
		switch value.Status {
		case domain.SpecimenRegistered:
			if err := value.MarkPlanned(stamp); err != nil {
				t.Fatalf("MarkPlanned: %v", err)
			}
		case domain.SpecimenPlanned:
			if err := value.MarkPreparing(stamp); err != nil {
				t.Fatalf("MarkPreparing: %v", err)
			}
		case domain.SpecimenPreparing:
			if err := value.MarkStabilized(stamp); err != nil {
				t.Fatalf("MarkStabilized: %v", err)
			}
		case domain.SpecimenStabilized:
			if err := value.MarkAvailable(stamp); err != nil {
				t.Fatalf("MarkAvailable: %v", err)
			}
		default:
			t.Fatalf("cannot advance specimen to %s", status)
		}
	}
	value.Version = version
	return value
}

func plannedCopy(t *testing.T, specimen domain.Specimen, stamp time.Time) domain.Specimen {
	t.Helper()
	return mustSpecimen(t, specimen.ID, specimen.CatalogNumber, domain.SpecimenPlanned, 2, stamp)
}

func stabilizedCopy(t *testing.T, specimen domain.Specimen, stamp time.Time) domain.Specimen {
	t.Helper()
	return mustSpecimen(t, specimen.ID, specimen.CatalogNumber, domain.SpecimenStabilized, 4, stamp)
}

func TestParseAcceptances(t *testing.T) {
	pkg := BuildPackage(repository.Data{}, fixedNow)
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Format != FormatName || parsed.Version != FormatVersion {
		t.Fatalf("metadata lost: %s v%d", parsed.Format, parsed.Version)
	}
	if !parsed.ExportedAt.Equal(fixedNow) {
		t.Fatalf("exported time lost: %v", parsed.ExportedAt)
	}
}

func TestParseFailures(t *testing.T) {
	base := BuildPackage(repository.Data{}, fixedNow)

	cases := []struct {
		name     string
		raw      func() []byte
		wantCode string
	}{
		{name: "empty body", raw: func() []byte { return nil }, wantCode: "empty_workspace_package"},
		{name: "whitespace only", raw: func() []byte { return []byte("   ") }, wantCode: "empty_workspace_package"},
		{name: "broken json", raw: func() []byte { return []byte(`{"format":`) }, wantCode: "malformed_workspace_package"},
		{name: "not an object", raw: func() []byte { return []byte(`[]`) }, wantCode: "malformed_workspace_package"},
		{name: "two objects", raw: func() []byte {
			first, _ := json.Marshal(base)
			return append(first, first...)
		}, wantCode: "malformed_workspace_package"},
		{name: "unknown field", raw: func() []byte {
			return []byte(`{"format":"paleo-prep-station-workspace","version":1,"exported_at":"2026-03-01T09:00:00Z","specimens":[],"plans":[],"progress":[],"releases":[],"extra":1}`)
		}, wantCode: "malformed_workspace_package"},
		{name: "old format version", raw: func() []byte {
			old := base
			old.Version = 2
			raw, _ := json.Marshal(old)
			return raw
		}, wantCode: "unsupported_workspace_version"},
		{name: "unknown format name", raw: func() []byte {
			other := base
			other.Format = "other-lab"
			raw, _ := json.Marshal(other)
			return raw
		}, wantCode: "unsupported_workspace_format"},
		{name: "missing exported_at", raw: func() []byte {
			return []byte(`{"format":"paleo-prep-station-workspace","version":1,"specimens":[],"plans":[],"progress":[],"releases":[]}`)
		}, wantCode: "malformed_workspace_package"},
		{name: "bad exported_at", raw: func() []byte {
			return []byte(`{"format":"paleo-prep-station-workspace","version":1,"exported_at":"yesterday","specimens":[],"plans":[],"progress":[],"releases":[]}`)
		}, wantCode: "invalid_exported_at"},
		{name: "section missing", raw: func() []byte {
			return []byte(`{"format":"paleo-prep-station-workspace","version":1,"exported_at":"2026-03-01T09:00:00Z","plans":[],"progress":[],"releases":[]}`)
		}, wantCode: "malformed_workspace_package"},
		{name: "section wrong type", raw: func() []byte {
			return []byte(`{"format":"paleo-prep-station-workspace","version":1,"exported_at":"2026-03-01T09:00:00Z","specimens":{},"plans":[],"progress":[],"releases":[]}`)
		}, wantCode: "malformed_workspace_package"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.raw())
			appErr := requireAppError(t, err)
			if appErr.Code != tc.wantCode {
				t.Fatalf("want code %s, got %s (%s)", tc.wantCode, appErr.Code, appErr.Message)
			}
		})
	}
}

func TestValidateHappyPaths(t *testing.T) {
	stamp := fixedNow

	t.Run("empty workspace", func(t *testing.T) {
		data, err := Validate(BuildPackage(repository.Data{}, stamp))
		if err != nil {
			t.Fatalf("empty workspace must validate: %v", err)
		}
		if CountsOf(data) != (Counts{}) {
			t.Fatalf("empty workspace counts wrong: %+v", CountsOf(data))
		}
	})

	t.Run("registered specimen", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-1", "PPS-001", domain.SpecimenRegistered, 1, stamp)
		pkg := BuildPackage(repository.Data{Specimens: []domain.Specimen{specimen}}, stamp)
		if _, err := Parse(mustJSON(t, pkg)); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := Validate(pkg); err != nil {
			t.Fatalf("registered specimen must validate: %v", err)
		}
	})

	t.Run("planned with draft", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-2", "PPS-002", domain.SpecimenPlanned, 2, stamp)
		plan, err := domain.NewPrepPlan("plan-2", specimen, "目标", "方法", "", 50, stamp)
		if err != nil {
			t.Fatalf("NewPrepPlan: %v", err)
		}
		if _, err := Validate(packageOf(specimen, plan, nil, nil)); err != nil {
			t.Fatalf("planned+draft must validate: %v", err)
		}
	})

	t.Run("cancelled draft keeps specimen planned", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-c", "PPS-C01", domain.SpecimenPlanned, 2, stamp)
		plan, err := domain.NewPrepPlan("plan-c", specimen, "目标", "方法", "", 50, stamp)
		if err != nil {
			t.Fatalf("NewPrepPlan: %v", err)
		}
		if err := plan.Cancel(stamp); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		if _, err := Validate(packageOf(specimen, plan, nil, nil)); err != nil {
			t.Fatalf("planned with cancelled-only plan must validate: %v", err)
		}
	})

	t.Run("preparing with active progress", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-3", "PPS-003", domain.SpecimenPreparing, 3, stamp)
		plan, err := domain.NewPrepPlan("plan-3", plannedCopy(t, specimen, stamp), "目标", "方法", "", 50, stamp)
		if err != nil {
			t.Fatalf("NewPrepPlan: %v", err)
		}
		if err := plan.Approve("curator-lin", stamp); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if err := plan.Begin(stamp); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		entry, err := domain.NewProgressEntry("progress-1", plan, nil, "剥离", 40, "", "prep-chen", stamp)
		if err != nil {
			t.Fatalf("NewProgressEntry: %v", err)
		}
		if _, err := Validate(packageOf(specimen, plan, []domain.ProgressEntry{entry}, nil)); err != nil {
			t.Fatalf("active plan with progress must validate: %v", err)
		}
	})

	t.Run("stabilized with completed plan", func(t *testing.T) {
		data := completedData(t, "PPS-004", "specimen-4", "plan-4", false)
		if _, err := Validate(BuildPackage(data, stamp)); err != nil {
			t.Fatalf("stabilized+completed must validate: %v", err)
		}
	})

	t.Run("available with release", func(t *testing.T) {
		data := completedData(t, "PPS-005", "specimen-5", "plan-5", true)
		if _, err := Validate(BuildPackage(data, stamp)); err != nil {
			t.Fatalf("available+release must validate: %v", err)
		}
	})
}

func TestValidateEntityRules(t *testing.T) {
	stamp := fixedNow
	specimen := mustSpecimen(t, "specimen-x", "PPS-X01", domain.SpecimenRegistered, 1, stamp)

	duplicate := specimen
	duplicate.ID = "specimen-x2"
	pkg := BuildPackage(repository.Data{Specimens: []domain.Specimen{specimen, duplicate}}, stamp)
	if _, err := Validate(pkg); err == nil || codeOf(err) != "duplicate_catalog_number" {
		t.Fatalf("duplicate catalog expected, got %v", err)
	}

	duplicate = specimen
	duplicate.CatalogNumber = "PPS-X02"
	pkg = BuildPackage(repository.Data{Specimens: []domain.Specimen{specimen, duplicate}}, stamp)
	raw := mustJSON(t, pkg)
	var decoded Package
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, err := Validate(decoded); err == nil || codeOf(err) != "duplicate_specimen_id" {
		t.Fatalf("duplicate specimen id expected, got %v", err)
	}

	bad := specimen
	bad.MatrixKind = ""
	pkg = BuildPackage(repository.Data{Specimens: []domain.Specimen{bad}}, stamp)
	if _, err := Validate(pkg); err == nil {
		t.Fatal("specimen with empty matrix kind must be rejected")
	}

	bad = specimen
	bad.Version = 0
	pkg = BuildPackage(repository.Data{Specimens: []domain.Specimen{bad}}, stamp)
	if _, err := Validate(pkg); err == nil || codeOf(err) != "invalid_workspace_entity" {
		t.Fatalf("zero version must be rejected, got %v", err)
	}
}

func TestValidateReferencesAndInvariants(t *testing.T) {
	stamp := fixedNow

	t.Run("plan references missing specimen", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-p", "PPS-P01", domain.SpecimenPlanned, 2, stamp)
		plan, _ := domain.NewPrepPlan("plan-p", specimen, "目标", "方法", "", 50, stamp)
		plan.SpecimenID = "specimen-gone"
		_, err := Validate(packageOf(specimen, plan, nil, nil))
		if err == nil || codeOf(err) != "dangling_plan_reference" {
			t.Fatalf("want dangling_plan_reference, got %v", err)
		}
	})

	t.Run("two open plans", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-o", "PPS-O01", domain.SpecimenPlanned, 2, stamp)
		plan1, _ := domain.NewPrepPlan("plan-o1", specimen, "目标", "方法", "", 50, stamp)
		plan2, _ := domain.NewPrepPlan("plan-o2", specimen, "目标二", "方法二", "", 60, stamp.Add(time.Second))
		_, err := Validate(packageOf(specimen, []domain.PrepPlan{plan1, plan2}, nil, nil))
		if err == nil || codeOf(err) != "multiple_open_plans" {
			t.Fatalf("want multiple_open_plans, got %v", err)
		}
	})

	t.Run("progress references missing plan", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-g", "PPS-G01", domain.SpecimenPreparing, 3, stamp)
		plan, _ := domain.NewPrepPlan("plan-g", plannedCopy(t, specimen, stamp), "目标", "方法", "", 50, stamp)
		_ = plan.Approve("curator-lin", stamp)
		_ = plan.Begin(stamp)
		entry, _ := domain.NewProgressEntry("progress-g", plan, nil, "剥离", 40, "", "prep-chen", stamp)
		entry.PlanID = "plan-gone"
		_, err := Validate(packageOf(specimen, plan, []domain.ProgressEntry{entry}, nil))
		if err == nil || codeOf(err) != "dangling_progress_reference" {
			t.Fatalf("want dangling_progress_reference, got %v", err)
		}
	})

	t.Run("progress sequence gap", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-s", "PPS-S01", domain.SpecimenPreparing, 3, stamp)
		plan, _ := domain.NewPrepPlan("plan-s", plannedCopy(t, specimen, stamp), "目标", "方法", "", 50, stamp)
		_ = plan.Approve("curator-lin", stamp)
		_ = plan.Begin(stamp)
		first, _ := domain.NewProgressEntry("progress-s1", plan, nil, "剥离", 30, "", "prep-chen", stamp)
		second, _ := domain.NewProgressEntry("progress-s2", plan, []domain.ProgressEntry{first}, "加固", 60, "", "prep-chen", stamp)
		second.Sequence = 3
		_, err := Validate(packageOf(specimen, plan, []domain.ProgressEntry{first, second}, nil))
		if err == nil || codeOf(err) != "progress_sequence_gap" {
			t.Fatalf("want progress_sequence_gap, got %v", err)
		}
	})

	t.Run("progress regression", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-r", "PPS-R01", domain.SpecimenPreparing, 3, stamp)
		plan, _ := domain.NewPrepPlan("plan-r", plannedCopy(t, specimen, stamp), "目标", "方法", "", 50, stamp)
		_ = plan.Approve("curator-lin", stamp)
		_ = plan.Begin(stamp)
		first, _ := domain.NewProgressEntry("progress-r1", plan, nil, "剥离", 60, "", "prep-chen", stamp)
		second := domain.ProgressEntry{
			ID: "progress-r2", PlanID: plan.ID, Sequence: 2,
			Stage: "回退", Percent: 40, Note: "", RecordedBy: "prep-chen",
			CreatedAt: stamp.Add(time.Minute),
		}
		_, err := Validate(packageOf(specimen, plan, []domain.ProgressEntry{first, second}, nil))
		if err == nil || codeOf(err) != "progress_not_monotonic" {
			t.Fatalf("want progress_not_monotonic, got %v", err)
		}
	})

	t.Run("completed plan without 100 percent", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-t", "PPS-T01", domain.SpecimenStabilized, 4, stamp)
		plan, _ := domain.NewPrepPlan("plan-t", plannedCopy(t, specimen, stamp), "目标", "方法", "", 50, stamp)
		_ = plan.Approve("curator-lin", stamp)
		_ = plan.Begin(stamp)
		entry, _ := domain.NewProgressEntry("progress-t1", plan, nil, "剥离", 80, "", "prep-chen", stamp)
		_ = plan.Complete(stamp)
		_, err := Validate(packageOf(specimen, plan, []domain.ProgressEntry{entry}, nil))
		if err == nil || codeOf(err) != "progress_terminal_mismatch" {
			t.Fatalf("want progress_terminal_mismatch, got %v", err)
		}
	})

	t.Run("two releases on one specimen", func(t *testing.T) {
		data := completedData(t, "PPS-U01", "specimen-u", "plan-u", true)
		record, _ := domain.NewReleaseRecord("release-u2", stabilizedCopy(t, data.Specimens[0], stamp), "目的二", "接收方二", "keeper-zou", stamp.Add(time.Hour))
		data.Releases = append(data.Releases, record)
		_, err := Validate(BuildPackage(data, stamp))
		if err == nil || codeOf(err) != "duplicate_release_record" {
			t.Fatalf("want duplicate_release_record, got %v", err)
		}
	})

	t.Run("release on missing specimen", func(t *testing.T) {
		specimen := mustSpecimen(t, "specimen-v", "PPS-V01", domain.SpecimenAvailable, 5, stamp)
		record, _ := domain.NewReleaseRecord("release-v", stabilizedCopy(t, specimen, stamp), "目的", "接收方", "keeper-zou", stamp)
		record.SpecimenID = "specimen-gone"
		_, err := Validate(packageOf(specimen, nil, nil, []domain.ReleaseRecord{record}))
		if err == nil || codeOf(err) != "dangling_release_reference" {
			t.Fatalf("want dangling_release_reference, got %v", err)
		}
	})

	t.Run("available without release", func(t *testing.T) {
		data := completedData(t, "PPS-W01", "specimen-w", "plan-w", false)
		data.Specimens[0] = mustSpecimen(t, "specimen-w", "PPS-W01", domain.SpecimenAvailable, 5, stamp)
		_, err := Validate(BuildPackage(data, stamp))
		if err == nil || codeOf(err) != "specimen_state_mismatch" {
			t.Fatalf("want specimen_state_mismatch, got %v", err)
		}
	})

	t.Run("release but specimen not available", func(t *testing.T) {
		data := completedData(t, "PPS-Z01", "specimen-z", "plan-z", false)
		record, _ := domain.NewReleaseRecord("release-z", stabilizedCopy(t, data.Specimens[0], stamp), "目的", "接收方", "keeper-zou", stamp)
		data.Releases = append(data.Releases, record)
		_, err := Validate(BuildPackage(data, stamp))
		if err == nil || codeOf(err) != "specimen_state_mismatch" {
			t.Fatalf("want specimen_state_mismatch, got %v", err)
		}
	})
}

func TestEqualDataIgnoresOrder(t *testing.T) {
	left := completedData(t, "PPS-E01", "specimen-e", "plan-e", true)
	right := repository.Data{
		Releases:  append([]domain.ReleaseRecord(nil), left.Releases...),
		Progress:  append([]domain.ProgressEntry(nil), left.Progress...),
		Plans:     append([]domain.PrepPlan(nil), left.Plans...),
		Specimens: append([]domain.Specimen(nil), left.Specimens...),
	}
	if !EqualData(left, right) {
		t.Fatal("reversed slice order must still compare equal")
	}
	right.Releases[0].Purpose = "changed"
	if EqualData(left, right) {
		t.Fatal("changed business fact must compare different")
	}
}

func TestRoundTripStaysIdentical(t *testing.T) {
	data := completedData(t, "PPS-Q01", "specimen-q", "plan-q", true)
	pkg := BuildPackage(data, fixedNow)
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	restored, err := Validate(parsed)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !EqualData(restored, data) {
		t.Fatalf("round trip changed business facts\nwant %+v\ngot  %+v", data, restored)
	}
}

func completedData(t *testing.T, catalog, specimenID, planID string, released bool) repository.Data {
	t.Helper()
	stamp := fixedNow
	status := domain.SpecimenStabilized
	version := 4
	if released {
		status = domain.SpecimenAvailable
		version = 5
	}
	specimen := mustSpecimen(t, specimenID, catalog, status, version, stamp)
	plan, err := domain.NewPrepPlan(planID, mustSpecimen(t, specimenID, catalog, domain.SpecimenPlanned, 2, stamp), "目标", "方法", "树脂", 90, stamp)
	if err != nil {
		t.Fatalf("NewPrepPlan: %v", err)
	}
	if err := plan.Approve("curator-lin", stamp); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := plan.Begin(stamp); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	entry, err := domain.NewProgressEntry("progress-"+planID, plan, nil, "完成", 100, "", "prep-chen", stamp)
	if err != nil {
		t.Fatalf("NewProgressEntry: %v", err)
	}
	if err := plan.Complete(stamp); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	data := repository.Data{
		Specimens: []domain.Specimen{specimen},
		Plans:     []domain.PrepPlan{plan},
		Progress:  []domain.ProgressEntry{entry},
	}
	if released {
		stable := mustSpecimen(t, specimenID, catalog, domain.SpecimenStabilized, 4, stamp)
		record, err := domain.NewReleaseRecord("release-"+planID, stable, "研究目的", "研究组", "keeper-zou", stamp)
		if err != nil {
			t.Fatalf("NewReleaseRecord: %v", err)
		}
		data.Releases = []domain.ReleaseRecord{record}
	}
	return data
}

func packageOf(specimen domain.Specimen, plans any, progress any, releases any) Package {
	pkg := Package{
		Format:     FormatName,
		Version:    FormatVersion,
		ExportedAt: fixedNow,
		Specimens:  []domain.Specimen{specimen},
	}
	switch value := plans.(type) {
	case nil:
		pkg.Plans = []domain.PrepPlan{}
	case domain.PrepPlan:
		pkg.Plans = []domain.PrepPlan{value}
	case []domain.PrepPlan:
		pkg.Plans = value
	}
	switch value := progress.(type) {
	case nil:
		pkg.Progress = []domain.ProgressEntry{}
	case []domain.ProgressEntry:
		pkg.Progress = value
	}
	switch value := releases.(type) {
	case nil:
		pkg.Releases = []domain.ReleaseRecord{}
	case []domain.ReleaseRecord:
		pkg.Releases = value
	}
	return pkg
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func requireAppError(t *testing.T, err error) *contracts.Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	appErr, ok := contracts.AsError(err)
	if !ok {
		t.Fatalf("expected contracts.Error, got %T: %v", err, err)
	}
	if strings.TrimSpace(appErr.Message) == "" {
		t.Fatal("error message must be explainable")
	}
	return appErr
}

func codeOf(err error) string {
	appErr, ok := contracts.AsError(err)
	if !ok {
		return ""
	}
	return appErr.Code
}
