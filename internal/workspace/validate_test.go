package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"example.com/paleo-prep-station/internal/domain"
)

func flattenProgress(groups map[string][]domain.ProgressEntry) []domain.ProgressEntry {
	values := make([]domain.ProgressEntry, 0)
	for _, group := range groups {
		values = append(values, group...)
	}
	return values
}

func issueCodes(err error) []string {
	pkgErr, ok := err.(*PackageError)
	if !ok {
		return nil
	}
	codes := make([]string, 0, len(pkgErr.Issues))
	for _, issue := range pkgErr.Issues {
		codes = append(codes, issue.Code)
	}
	return codes
}

func hasIssue(err error, code string) bool {
	for _, value := range issueCodes(err) {
		if value == code {
			return true
		}
	}
	return false
}

func TestValidateAcceptsEveryReachableWorkflowState(t *testing.T) {
	f := newFixture()
	cases := []struct {
		name      string
		specimens []SpecimenDTO
		plans     []PlanDTO
		progress  []ProgressDTO
		releases  []ReleaseDTO
	}{
		{
			name:      "registered specimen",
			specimens: []SpecimenDTO{f.specimen("s1", "registered", 1)},
		},
		{
			name:      "planned specimen with draft plan",
			specimens: []SpecimenDTO{f.specimen("s1", "planned", 2)},
			plans:     []PlanDTO{f.plan("p1", "s1", "draft", 1)},
		},
		{
			name:      "planned specimen retaining cancelled plan",
			specimens: []SpecimenDTO{f.specimen("s1", "planned", 2)},
			plans:     []PlanDTO{f.plan("p1", "s1", "cancelled", 2)},
		},
		{
			name:      "preparing specimen with approved plan",
			specimens: []SpecimenDTO{f.specimen("s1", "preparing", 3)},
			plans:     []PlanDTO{f.plan("p1", "s1", "approved", 2)},
		},
		{
			name:      "preparing specimen with active plan and partial progress",
			specimens: []SpecimenDTO{f.specimen("s1", "preparing", 3)},
			plans:     []PlanDTO{f.plan("p1", "s1", "active", 3)},
			progress:  []ProgressDTO{f.progress("g1", "p1", 1, 40)},
		},
		{
			name:      "stabilized specimen with completed plan",
			specimens: []SpecimenDTO{f.specimen("s1", "stabilized", 4)},
			plans:     []PlanDTO{f.plan("p1", "s1", "completed", 4)},
			progress: []ProgressDTO{
				f.progress("g1", "p1", 1, 40),
				f.progress("g2", "p1", 2, 100),
			},
		},
		{
			name:      "available specimen with release",
			specimens: []SpecimenDTO{f.specimen("s1", "available", 5)},
			plans:     []PlanDTO{f.plan("p1", "s1", "completed", 4)},
			progress: []ProgressDTO{
				f.progress("g1", "p1", 1, 100),
			},
			releases: []ReleaseDTO{f.release("r1", "s1")},
		},
		{
			name: "completed plan plus cancelled draft retained on specimen",
			specimens: []SpecimenDTO{
				f.specimen("s1", "stabilized", 4),
			},
			plans: []PlanDTO{
				f.plan("p1", "s1", "cancelled", 2),
				f.plan("p2", "s1", "completed", 4),
			},
			progress: []ProgressDTO{f.progress("g1", "p2", 1, 100)},
		},
		{
			name: "empty workspace",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundle, err := Validate(f.envelope(tc.specimens, tc.plans, tc.progress, tc.releases))
			if err != nil {
				t.Fatalf("expected valid workspace, got %v", err)
			}
			if len(bundle.Specimens) != len(tc.specimens) {
				t.Fatalf("expected %d specimens, got %d", len(tc.specimens), len(bundle.Specimens))
			}
		})
	}
}

func TestValidateRejectsStructuralAndReferenceProblems(t *testing.T) {
	f := newFixture()
	cases := []struct {
		name      string
		pkg       Package
		wantCode  string
		wantIssue string
	}{
		{
			name: "old format version",
			pkg: func() Package {
				pkg := f.envelope(nil, nil, nil, nil)
				pkg.FormatVersion = 0
				return pkg
			}(),
			wantCode: "unsupported_format_version",
		},
		{
			name: "future format version",
			pkg: func() Package {
				pkg := f.envelope(nil, nil, nil, nil)
				pkg.FormatVersion = FormatVersion + 1
				return pkg
			}(),
			wantCode: "unsupported_format_version",
		},
		{
			name: "wrong kind",
			pkg: func() Package {
				pkg := f.envelope(nil, nil, nil, nil)
				pkg.Kind = "something-else"
				return pkg
			}(),
			wantCode: "unrecognized_workspace_kind",
		},
		{
			name:      "plan points to missing specimen",
			pkg:       f.envelope(nil, []PlanDTO{f.plan("p1", "ghost", "draft", 1)}, nil, nil),
			wantIssue: "broken_entity_reference",
		},
		{
			name: "progress points to missing plan",
			pkg: f.envelope(
				[]SpecimenDTO{f.specimen("s1", "registered", 1)},
				nil,
				[]ProgressDTO{f.progress("g1", "ghost", 1, 10)},
				nil,
			),
			wantIssue: "broken_entity_reference",
		},
		{
			name:      "release points to missing specimen",
			pkg:       f.envelope(nil, nil, nil, []ReleaseDTO{f.release("r1", "ghost")}),
			wantIssue: "broken_entity_reference",
		},
		{
			name: "duplicate specimen id",
			pkg: func() Package {
				first := f.specimen("s1", "registered", 1)
				second := f.specimen("s1", "registered", 1)
				second.CatalogNumber = "CAT-OTHER"
				return f.envelope([]SpecimenDTO{first, second}, nil, nil, nil)
			}(),
			wantIssue: "duplicate_specimen_id",
		},
		{
			name: "duplicate catalog number",
			pkg: func() Package {
				first := f.specimen("s1", "registered", 1)
				second := f.specimen("s2", "registered", 1)
				second.CatalogNumber = first.CatalogNumber
				return f.envelope([]SpecimenDTO{first, second}, nil, nil, nil)
			}(),
			wantIssue: "duplicate_catalog_number",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate(tc.pkg)
			if err == nil {
				t.Fatal("expected validation failure")
			}
			if tc.wantCode != "" {
				pkgErr, ok := err.(*PackageError)
				if !ok || pkgErr.Code != tc.wantCode {
					t.Fatalf("expected code %s, got %v", tc.wantCode, err)
				}
			}
			if tc.wantIssue != "" && !hasIssue(err, tc.wantIssue) {
				t.Fatalf("expected issue %s, got %v", tc.wantIssue, err)
			}
		})
	}
}

func TestValidateRejectsOpenPlanAndProgressViolations(t *testing.T) {
	f := newFixture()
	completedPlan := f.plan("p1", "s1", "completed", 4)
	cases := []struct {
		name      string
		specimens []SpecimenDTO
		plans     []PlanDTO
		progress  []ProgressDTO
		wantIssue string
	}{
		{
			name:      "two open plans on one specimen",
			specimens: []SpecimenDTO{f.specimen("s1", "planned", 2)},
			plans: []PlanDTO{
				f.plan("p1", "s1", "draft", 1),
				f.plan("p2", "s1", "approved", 2),
			},
			wantIssue: "open_plan_conflict",
		},
		{
			name:      "progress sequence gap",
			specimens: []SpecimenDTO{f.specimen("s1", "preparing", 3)},
			plans:     []PlanDTO{f.plan("p1", "s1", "active", 3)},
			progress: []ProgressDTO{
				f.progress("g1", "p1", 1, 20),
				f.progress("g2", "p1", 3, 40),
			},
			wantIssue: "progress_sequence_gap",
		},
		{
			name:      "progress regression",
			specimens: []SpecimenDTO{f.specimen("s1", "preparing", 3)},
			plans:     []PlanDTO{f.plan("p1", "s1", "active", 3)},
			progress: []ProgressDTO{
				f.progress("g1", "p1", 1, 60),
				f.progress("g2", "p1", 2, 40),
			},
			wantIssue: "progress_regressed",
		},
		{
			name:      "progress after one hundred percent",
			specimens: []SpecimenDTO{f.specimen("s1", "stabilized", 4)},
			plans:     []PlanDTO{completedPlan},
			progress: []ProgressDTO{
				f.progress("g1", "p1", 1, 100),
				f.progress("g2", "p1", 2, 100),
			},
			wantIssue: "progress_after_completion",
		},
		{
			name:      "completed plan missing terminal progress",
			specimens: []SpecimenDTO{f.specimen("s1", "stabilized", 4)},
			plans:     []PlanDTO{completedPlan},
			progress: []ProgressDTO{
				f.progress("g1", "p1", 1, 80),
			},
			wantIssue: "plan_progress_invalid",
		},
		{
			name:      "active plan with one hundred percent progress",
			specimens: []SpecimenDTO{f.specimen("s1", "preparing", 3)},
			plans:     []PlanDTO{f.plan("p1", "s1", "active", 3)},
			progress: []ProgressDTO{
				f.progress("g1", "p1", 1, 100),
			},
			wantIssue: "plan_progress_invalid",
		},
		{
			name:      "draft carrying progress",
			specimens: []SpecimenDTO{f.specimen("s1", "planned", 2)},
			plans:     []PlanDTO{f.plan("p1", "s1", "draft", 1)},
			progress:  []ProgressDTO{f.progress("g1", "p1", 1, 10)},
			wantIssue: "plan_progress_invalid",
		},
		{
			name:      "preparing specimen without working plan",
			specimens: []SpecimenDTO{f.specimen("s1", "preparing", 3)},
			plans:     []PlanDTO{f.plan("p1", "s1", "draft", 1)},
			wantIssue: "specimen_plan_state_conflict",
		},
		{
			name:      "stabilized specimen without completed plan",
			specimens: []SpecimenDTO{f.specimen("s1", "stabilized", 4)},
			plans:     nil,
			wantIssue: "specimen_plan_state_conflict",
		},
		{
			name:      "registered specimen carrying a cancelled plan",
			specimens: []SpecimenDTO{f.specimen("s1", "registered", 1)},
			plans:     []PlanDTO{f.plan("p1", "s1", "cancelled", 2)},
			wantIssue: "specimen_plan_state_conflict",
		},
		{
			name:      "planned specimen carrying a completed plan",
			specimens: []SpecimenDTO{f.specimen("s1", "planned", 2)},
			plans: []PlanDTO{
				f.plan("p1", "s1", "completed", 4),
			},
			progress:  []ProgressDTO{f.progress("g1", "p1", 1, 100)},
			wantIssue: "specimen_plan_state_conflict",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate(f.envelope(tc.specimens, tc.plans, tc.progress, nil))
			if err == nil {
				t.Fatal("expected validation failure")
			}
			if !hasIssue(err, tc.wantIssue) {
				t.Fatalf("expected issue %s, got %v", tc.wantIssue, err)
			}
		})
	}
}

func TestValidateRejectsReleaseViolations(t *testing.T) {
	f := newFixture()
	completedPlan := f.plan("p1", "s1", "completed", 4)
	finalProgress := []ProgressDTO{f.progress("g1", "p1", 1, 100)}

	t.Run("two releases for one specimen", func(t *testing.T) {
		_, err := Validate(f.envelope(
			[]SpecimenDTO{f.specimen("s1", "available", 5)},
			[]PlanDTO{completedPlan},
			finalProgress,
			[]ReleaseDTO{f.release("r1", "s1"), f.release("r2", "s1")},
		))
		if !hasIssue(err, "release_not_unique") {
			t.Fatalf("expected release_not_unique, got %v", err)
		}
	})

	t.Run("release on non-available specimen", func(t *testing.T) {
		_, err := Validate(f.envelope(
			[]SpecimenDTO{f.specimen("s1", "stabilized", 4)},
			[]PlanDTO{completedPlan},
			finalProgress,
			[]ReleaseDTO{f.release("r1", "s1")},
		))
		if !hasIssue(err, "specimen_release_state_conflict") {
			t.Fatalf("expected specimen_release_state_conflict, got %v", err)
		}
	})

	t.Run("available specimen without release", func(t *testing.T) {
		_, err := Validate(f.envelope(
			[]SpecimenDTO{f.specimen("s1", "available", 5)},
			[]PlanDTO{completedPlan},
			finalProgress,
			nil,
		))
		if !hasIssue(err, "specimen_release_state_conflict") {
			t.Fatalf("expected specimen_release_state_conflict, got %v", err)
		}
	})
}

func TestDecodeRejectsCorruptContent(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"not json", "this is not json"},
		{"json array instead of object", "[]"},
		{"json number instead of object", "42"},
		{"unknown envelope field", `{"format_version":1,"kind":"paleo-prep-station-workspace","generated_at":"2026-09-01T08:00:00Z","specimens":[],"plans":[],"progress":[],"releases":[],"extra":1}`},
		{"two json objects", `{"format_version":1,"kind":"paleo-prep-station-workspace","generated_at":"2026-09-01T08:00:00Z","specimens":[],"plans":[],"progress":[],"releases":[]}{}`},
		{"missing format version", `{"kind":"paleo-prep-station-workspace","generated_at":"2026-09-01T08:00:00Z","specimens":[],"plans":[],"progress":[],"releases":[]}`},
		{"missing generated time", `{"format_version":1,"kind":"paleo-prep-station-workspace","specimens":[],"plans":[],"progress":[],"releases":[]}`},
		{"null collections", `{"format_version":1,"kind":"paleo-prep-station-workspace","generated_at":"2026-09-01T08:00:00Z","specimens":null,"plans":[],"progress":[],"releases":[]}`},
		{"malformed generated time", `{"format_version":1,"kind":"paleo-prep-station-workspace","generated_at":"not-a-time","specimens":[],"plans":[],"progress":[],"releases":[]}`},
		{"wrong field type", `{"format_version":"1","kind":"paleo-prep-station-workspace","generated_at":"2026-09-01T08:00:00Z","specimens":[],"plans":[],"progress":[],"releases":[]}`},
		{"unknown nested specimen field", `{"format_version":1,"kind":"paleo-prep-station-workspace","generated_at":"2026-09-01T08:00:00Z","specimens":[{"id":"s1","catalog_number":"CAT-1","field_label":"北坡","matrix_kind":"泥岩","status":"registered","version":1,"created_at":"2026-09-01T08:00:00Z","updated_at":"2026-09-01T08:00:00Z","ghost":true}],"plans":[],"progress":[],"releases":[]}`},
		{"truncated object", `{"format_version":1,"kind":"paleo-pr`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode([]byte(tc.raw)); err == nil {
				t.Fatal("expected decode failure")
			}
		})
	}
}

func TestDecodeThenValidateReportsFieldLevelIssues(t *testing.T) {
	f := newFixture()
	raw := func() []byte {
		pkg := f.envelope(
			[]SpecimenDTO{f.specimen("s1", "available", 5)},
			[]PlanDTO{f.plan("p1", "s1", "completed", 4)},
			[]ProgressDTO{f.progress("g1", "p1", 1, 100)},
			[]ReleaseDTO{},
		)
		data, _ := json.Marshal(pkg)
		return data
	}()
	pkg, err := Decode(raw)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if _, err := Validate(pkg); !hasIssue(err, "specimen_release_state_conflict") {
		t.Fatalf("expected field-level issue, got %v", err)
	}
}

func TestMalformedNestedTimestampIsAnExplainableIssue(t *testing.T) {
	f := newFixture()
	bad := f.specimen("s1", "registered", 1)
	bad.CreatedAt = "yesterday"
	_, err := Validate(f.envelope([]SpecimenDTO{bad}, nil, nil, []ReleaseDTO{}))
	if err == nil || !hasIssue(err, "invalid_entity_timestamp") {
		t.Fatalf("expected invalid_entity_timestamp issue, got %v", err)
	}
}

func TestRoundTripIsStable(t *testing.T) {
	f := newFixture()
	original := f.envelope(
		[]SpecimenDTO{f.specimen("s1", "available", 5)},
		[]PlanDTO{f.plan("p1", "s1", "completed", 4)},
		[]ProgressDTO{f.progress("g1", "p1", 1, 100)},
		[]ReleaseDTO{f.release("r1", "s1")},
	)
	bundle, err := Validate(original)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	rebuilt := NewPackage(f.now.Add(time.Hour), bundle.Specimens, bundle.Plans, flattenProgress(bundle.Progress), bundle.Releases)
	if _, err := Validate(rebuilt); err != nil {
		t.Fatalf("rebuilt package failed validation: %v", err)
	}
	if bundle.Fingerprint() == "" || len(bundle.Fingerprint()) != 64 {
		t.Fatalf("unexpected fingerprint %q", bundle.Fingerprint())
	}
}

func TestValidationIssuesAreExplainable(t *testing.T) {
	f := newFixture()
	_, err := Validate(f.envelope(
		[]SpecimenDTO{f.specimen("s1", "available", 5)},
		nil,
		nil,
		nil,
	))
	pkgErr, ok := err.(*PackageError)
	if !ok {
		t.Fatalf("expected PackageError, got %T", err)
	}
	if !strings.Contains(pkgErr.Error(), "未被修改") {
		t.Fatalf("error should promise the workspace is untouched: %v", err)
	}
	if len(pkgErr.Issues) == 0 || pkgErr.Issues[0].Message == "" {
		t.Fatal("expected concrete issue messages")
	}
}
