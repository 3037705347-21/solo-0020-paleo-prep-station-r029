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

func TestExportImportRoundTripAndRepeatedImport(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemory()
	service := New(repo)

	if _, err := service.RegisterSpecimen(ctx, contracts.RegisterSpecimenRequest{
		CatalogNumber: "PPS-RT01",
		FieldLabel:    "北坡-01",
		MatrixKind:    "泥岩",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	pkg, err := service.ExportWorkspace(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if pkg.Format != workspace.FormatName || pkg.Version != workspace.FormatVersion {
		t.Fatalf("export metadata wrong: %s v%d", pkg.Format, pkg.Version)
	}
	if pkg.ExportedAt.IsZero() || len(pkg.Specimens) != 1 {
		t.Fatalf("export content wrong: %+v", pkg)
	}
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	report, err := service.ImportWorkspace(ctx, raw)
	if err != nil {
		t.Fatalf("re-import exported package: %v", err)
	}
	if report.Status != "unchanged" {
		t.Fatalf("re-importing same facts must be unchanged, got %s", report.Status)
	}

	specimens, err := service.ListSpecimens(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(specimens) != 1 || specimens[0].CatalogNumber != "PPS-RT01" {
		t.Fatalf("workspace changed after unchanged import: %+v", specimens)
	}
}

func TestFailedImportKeepsWorkspaceIntact(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemory()
	service := New(repo)

	created, err := service.RegisterSpecimen(ctx, contracts.RegisterSpecimenRequest{
		CatalogNumber: "PPS-KEEP",
		FieldLabel:    "保留标签",
		MatrixKind:    "砂岩",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	cases := map[string][]byte{
		"empty":         nil,
		"malformed":     []byte(`{"format":`),
		"old version":   []byte(`{"format":"paleo-prep-station-workspace","version":42,"exported_at":"2026-03-01T00:00:00Z","specimens":[],"plans":[],"progress":[],"releases":[]}`),
		"dangling plan": []byte(`{"format":"paleo-prep-station-workspace","version":1,"exported_at":"2026-03-01T00:00:00Z","specimens":[],"plans":[{"id":"plan-x","specimen_id":"specimen-gone","objective":"o","method":"m","target_exposure":50,"status":"draft","version":1,"created_at":"2026-03-01T00:00:00Z","updated_at":"2026-03-01T00:00:00Z"}],"progress":[],"releases":[]}`),
	}
	for name, body := range cases {
		if _, err := service.ImportWorkspace(ctx, body); err == nil {
			t.Fatalf("%s import must fail", name)
		}
		got, err := service.GetSpecimen(ctx, created.ID)
		if err != nil {
			t.Fatalf("%s: original specimen vanished: %v", name, err)
		}
		if got.CatalogNumber != "PPS-KEEP" || got.Status != domain.SpecimenRegistered {
			t.Fatalf("%s: original specimen altered: %+v", name, got)
		}
	}
}

func TestImportReplacesWholeWorkspaceIncludingEmpty(t *testing.T) {
	ctx := context.Background()
	repo := repository.NewMemory()
	service := New(repo)

	if _, err := service.RegisterSpecimen(ctx, contracts.RegisterSpecimenRequest{
		CatalogNumber: "PPS-OLD",
		FieldLabel:    "旧标签",
		MatrixKind:    "泥岩",
	}); err != nil {
		t.Fatalf("register old: %v", err)
	}

	empty := workspace.Package{
		Format:     workspace.FormatName,
		Version:    workspace.FormatVersion,
		ExportedAt: mustTime(t, "2026-03-01T00:00:00Z"),
		Specimens:  []domain.Specimen{},
		Plans:      []domain.PrepPlan{},
		Progress:   []domain.ProgressEntry{},
		Releases:   []domain.ReleaseRecord{},
	}
	raw, _ := json.Marshal(empty)
	report, err := service.ImportWorkspace(ctx, raw)
	if err != nil {
		t.Fatalf("import empty: %v", err)
	}
	if report.Status != "replaced" || report.Counts.Specimens != 0 || report.Previous.Specimens != 1 {
		t.Fatalf("empty import report wrong: %+v", report)
	}
	values, err := service.ListSpecimens(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(values) != 0 {
		t.Fatalf("workspace should be empty, got %+v", values)
	}

	// The old catalog number is reusable on the replaced empty workspace, and
	// the same registration workflow still enforces its rules.
	if _, err := service.RegisterSpecimen(ctx, contracts.RegisterSpecimenRequest{
		CatalogNumber: "PPS-OLD",
		FieldLabel:    "新标签",
		MatrixKind:    "泥岩",
	}); err != nil {
		t.Fatalf("catalog number should be reusable after restore: %v", err)
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return parsed
}
