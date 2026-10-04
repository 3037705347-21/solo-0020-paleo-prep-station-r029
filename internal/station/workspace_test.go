package station

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/workspace"
)

func newTestService() *Service {
	service := New(repository.NewMemory())
	base, _ := time.Parse(time.RFC3339Nano, "2026-09-01T08:00:00Z")
	cursor := base
	service.now = func() time.Time {
		cursor = cursor.Add(time.Minute)
		return cursor
	}
	return service
}

func registerActiveSpecimen(t *testing.T, service *Service) (string, string) {
	t.Helper()
	specimen, err := service.RegisterSpecimen(context.Background(), contracts.RegisterSpecimenRequest{
		CatalogNumber: "PPS-2026-001",
		FieldLabel:    "北坡-07",
		TaxonHint:     "大型脊椎动物",
		MatrixKind:    "粉砂岩",
		JacketNotes:   "夹克边角完好",
	})
	if err != nil {
		t.Fatalf("register specimen: %v", err)
	}
	plan, err := service.CreatePlan(context.Background(), specimen.ID, contracts.CreatePlanRequest{
		Objective:      "暴露主要关节面",
		Method:         "手工显微剥离",
		Adhesive:       "可逆树脂",
		TargetExposure: 80,
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := service.ApprovePlan(context.Background(), plan.ID, contracts.ApprovalRequest{
		ApprovedBy: "curator-lin",
	}); err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	if _, err := service.LogProgress(context.Background(), plan.ID, contracts.ProgressRequest{
		Stage:      "矩阵剥离",
		Percent:    50,
		Note:       "首轮暴露",
		RecordedBy: "preparator-chen",
	}); err != nil {
		t.Fatalf("log progress: %v", err)
	}
	return specimen.ID, plan.ID
}

func mustExport(t *testing.T, service *Service) []byte {
	t.Helper()
	pkg, err := service.ExportWorkspace(context.Background())
	if err != nil {
		t.Fatalf("export workspace: %v", err)
	}
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("marshal package: %v", err)
	}
	return raw
}

func TestExportRecordsEnvelopeAndAllFacts(t *testing.T) {
	service := newTestService()
	specimenID, planID := registerActiveSpecimen(t, service)

	pkg, err := service.ExportWorkspace(context.Background())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if pkg.FormatVersion != workspace.FormatVersion {
		t.Fatalf("expected format version %d, got %d", workspace.FormatVersion, pkg.FormatVersion)
	}
	if pkg.Kind != workspace.PackageKind {
		t.Fatalf("unexpected kind %q", pkg.Kind)
	}
	if pkg.GeneratedAt.IsZero() {
		t.Fatal("expected generation timestamp")
	}
	if len(pkg.Specimens) != 1 || pkg.Specimens[0].ID != specimenID {
		t.Fatalf("expected one specimen %s, got %+v", specimenID, pkg.Specimens)
	}
	if len(pkg.Plans) != 1 || pkg.Plans[0].ID != planID || pkg.Plans[0].Status != "active" {
		t.Fatalf("expected active plan %s, got %+v", planID, pkg.Plans)
	}
	if len(pkg.Progress) != 1 || pkg.Progress[0].PlanID != planID || pkg.Progress[0].Percent != 50 {
		t.Fatalf("expected one 50%% progress entry, got %+v", pkg.Progress)
	}
}

func TestImportRestoresWorkspaceAndWorkflowsContinue(t *testing.T) {
	source := newTestService()
	specimenID, planID := registerActiveSpecimen(t, source)
	raw := mustExport(t, source)

	target := newTestService()
	report, err := target.ImportWorkspace(context.Background(), raw)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !report.Replaced || report.Unchanged {
		t.Fatalf("expected replacement report, got %+v", report)
	}
	if report.Specimens != 1 || report.Plans != 1 || report.Progress != 1 || report.Releases != 0 {
		t.Fatalf("unexpected counts: %+v", report)
	}

	specimen, err := target.GetSpecimen(context.Background(), specimenID)
	if err != nil {
		t.Fatalf("read restored specimen: %v", err)
	}
	if specimen.Status != domain.SpecimenPreparing {
		t.Fatalf("expected preparing specimen, got %s", specimen.Status)
	}
	snapshot, err := target.GetPlan(context.Background(), planID)
	if err != nil {
		t.Fatalf("read restored plan: %v", err)
	}
	if snapshot.Plan.Status != domain.PlanActive || len(snapshot.Progress) != 1 {
		t.Fatalf("unexpected restored plan snapshot: %+v", snapshot)
	}

	// The four live workflows must apply the same rules after restore:
	// completion and release can proceed from the imported midpoint.
	if _, err := target.LogProgress(context.Background(), planID, contracts.ProgressRequest{
		Stage:      "表面加固",
		Percent:    100,
		Note:       "完成制备",
		RecordedBy: "preparator-chen",
	}); err != nil {
		t.Fatalf("final progress after restore: %v", err)
	}
	specimen, _ = target.GetSpecimen(context.Background(), specimenID)
	if specimen.Status != domain.SpecimenStabilized {
		t.Fatalf("expected stabilized specimen, got %s", specimen.Status)
	}
	if _, err := target.ReleaseSpecimen(context.Background(), specimenID, contracts.ReleaseRequest{
		Purpose:    "比较解剖研究",
		Recipient:  "地层研究组",
		ApprovedBy: "keeper-zhou",
	}); err != nil {
		t.Fatalf("release after restore: %v", err)
	}
	specimen, _ = target.GetSpecimen(context.Background(), specimenID)
	if specimen.Status != domain.SpecimenAvailable {
		t.Fatalf("expected available specimen, got %s", specimen.Status)
	}
}

func TestRepeatedImportIsIdempotent(t *testing.T) {
	source := newTestService()
	specimenID, _ := registerActiveSpecimen(t, source)
	raw := mustExport(t, source)

	target := newTestService()
	first, err := target.ImportWorkspace(context.Background(), raw)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if !first.Replaced {
		t.Fatal("first import must replace the empty workspace")
	}

	second, err := target.ImportWorkspace(context.Background(), raw)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.Replaced || !second.Unchanged {
		t.Fatalf("repeated import must be a no-op, got %+v", second)
	}

	// A freshly exported package carries a new generation time but must
	// still be recognized as the same business facts.
	resident, err := target.ExportWorkspace(context.Background())
	if err != nil {
		t.Fatalf("export resident: %v", err)
	}
	residentRaw, _ := json.Marshal(resident)
	third, err := target.ImportWorkspace(context.Background(), residentRaw)
	if err != nil {
		t.Fatalf("resident re-import: %v", err)
	}
	if third.Replaced || !third.Unchanged {
		t.Fatalf("resident package must be a no-op, got %+v", third)
	}
	_ = specimenID
}

func TestFailedImportNeverTouchesCurrentWorkspace(t *testing.T) {
	source := newTestService()
	specimenID, planID := registerActiveSpecimen(t, source)
	good := mustExport(t, source)

	target := newTestService()
	if _, err := target.ImportWorkspace(context.Background(), good); err != nil {
		t.Fatalf("seed import: %v", err)
	}

	attempt := func(name string, payload []byte) {
		t.Helper()
		if _, err := target.ImportWorkspace(context.Background(), payload); err == nil {
			t.Fatalf("%s: expected import failure", name)
		}
		if _, err := target.GetSpecimen(context.Background(), specimenID); err != nil {
			t.Fatalf("%s: original specimen vanished: %v", name, err)
		}
		snapshot, err := target.GetPlan(context.Background(), planID)
		if err != nil {
			t.Fatalf("%s: original plan vanished: %v", name, err)
		}
		if snapshot.Plan.Status != domain.PlanActive || len(snapshot.Progress) != 1 {
			t.Fatalf("%s: original plan mutated: %+v", name, snapshot)
		}
	}

	attempt("corrupt bytes", []byte("{not-json"))
	attempt("truncated package", good[:len(good)/2])

	var old struct {
		FormatVersion int               `json:"format_version"`
		Kind          string            `json:"kind"`
		GeneratedAt   string            `json:"generated_at"`
		Specimens     []json.RawMessage `json:"specimens"`
		Plans         []json.RawMessage `json:"plans"`
		Progress      []json.RawMessage `json:"progress"`
		Releases      []json.RawMessage `json:"releases"`
	}
	_ = json.Unmarshal(good, &old)
	oldPackage := map[string]any{
		"format_version": 0,
		"kind":           workspace.PackageKind,
		"generated_at":   "2026-09-01T08:00:00Z",
		"specimens":      old.Specimens,
		"plans":          old.Plans,
		"progress":       old.Progress,
		"releases":       old.Releases,
	}
	oldRaw, _ := json.Marshal(oldPackage)
	attempt("old format version", oldRaw)

	// A business-rule violation (duplicate catalog numbers) must also
	// abort before replacing any state.
	var pkg workspace.Package
	if err := json.Unmarshal(good, &pkg); err != nil {
		t.Fatalf("unmarshal good package: %v", err)
	}
	duplicate := pkg.Specimens[0]
	duplicate.ID = "specimen-other"
	pkg.Specimens = append(pkg.Specimens, duplicate)
	dupRaw, _ := json.Marshal(pkg)
	attempt("duplicate catalog number", dupRaw)
}

func TestImportedStateEnforcesSameRules(t *testing.T) {
	source := newTestService()
	specimenID, planID := registerActiveSpecimen(t, source)
	if _, err := source.LogProgress(context.Background(), planID, contracts.ProgressRequest{
		Stage: "表面加固", Percent: 100, RecordedBy: "preparator-chen",
	}); err != nil {
		t.Fatalf("complete plan: %v", err)
	}
	raw := mustExport(t, source)

	target := newTestService()
	if _, err := target.ImportWorkspace(context.Background(), raw); err != nil {
		t.Fatalf("import: %v", err)
	}

	// A completed plan cannot accept more progress.
	if _, err := target.LogProgress(context.Background(), planID, contracts.ProgressRequest{
		Stage: "多余记录", Percent: 100, RecordedBy: "preparator-chen",
	}); err == nil {
		t.Fatal("expected conflict appending progress to completed plan")
	}

	// Duplicate catalog number is still rejected.
	if _, err := target.RegisterSpecimen(context.Background(), contracts.RegisterSpecimenRequest{
		CatalogNumber: "PPS-2026-001",
		FieldLabel:    "北坡-07",
		MatrixKind:    "粉砂岩",
	}); err == nil {
		t.Fatal("expected duplicate catalog conflict")
	}

	// Release works once and only once.
	release, err := target.ReleaseSpecimen(context.Background(), specimenID, contracts.ReleaseRequest{
		Purpose: "比较解剖研究", Recipient: "地层研究组", ApprovedBy: "keeper-zhou",
	})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := target.ReleaseSpecimen(context.Background(), specimenID, contracts.ReleaseRequest{
		Purpose: "再次借阅", Recipient: "其他组", ApprovedBy: "keeper-zhou",
	}); err == nil {
		t.Fatal("expected duplicate release conflict")
	}
	if release.SpecimenID != specimenID {
		t.Fatalf("release references wrong specimen: %+v", release)
	}
}

func TestEmptyWorkspaceRoundTrip(t *testing.T) {
	empty := newTestService()
	raw := mustExport(t, empty)

	var pkg workspace.Package
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatalf("unmarshal empty package: %v", err)
	}
	if pkg.FormatVersion != 1 || len(pkg.Specimens) != 0 || len(pkg.Plans) != 0 ||
		len(pkg.Progress) != 0 || len(pkg.Releases) != 0 {
		t.Fatalf("empty package must carry empty collections: %+v", pkg)
	}

	report, err := newTestService().ImportWorkspace(context.Background(), raw)
	if err != nil {
		t.Fatalf("import empty into empty: %v", err)
	}
	if report.Replaced || !report.Unchanged {
		t.Fatalf("empty into empty must be a no-op, got %+v", report)
	}

	// An empty package is a valid, explainable restore point that clears
	// a populated workspace when imported deliberately.
	populated := newTestService()
	registerActiveSpecimen(t, populated)
	clearing, err := populated.ImportWorkspace(context.Background(), raw)
	if err != nil {
		t.Fatalf("import empty into populated: %v", err)
	}
	if !clearing.Replaced || clearing.Specimens != 0 || clearing.Plans != 0 {
		t.Fatalf("expected replacement with zero counts, got %+v", clearing)
	}
	values, err := populated.ListSpecimens(context.Background())
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("expected cleared workspace, got %d specimens", len(values))
	}
}
