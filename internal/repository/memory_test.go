package repository

import (
	"errors"
	"testing"
	"time"

	"example.com/paleo-prep-station/internal/domain"
)

var assertErr = errors.New("sentinel transaction failure")

func TestExportDataIsSortedAndComplete(t *testing.T) {
	repo := NewMemory()
	stamp := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	specimen, err := domain.NewSpecimen("specimen-1", "PPS-001", "标签", "", "泥岩", "", stamp)
	if err != nil {
		t.Fatalf("NewSpecimen: %v", err)
	}
	if err := repo.Mutate(func(m *Mutation) error {
		m.PutSpecimen(specimen)
		return nil
	}); err != nil {
		t.Fatalf("mutate: %v", err)
	}

	var data Data
	if err := repo.Read(func(m *Mutation) error {
		data = m.ExportData()
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data.Specimens) != 1 || data.Specimens[0].ID != "specimen-1" {
		t.Fatalf("export missing specimen: %+v", data)
	}
	if data.Plans == nil || data.Progress == nil || data.Releases == nil {
		t.Fatal("empty sections must still be non-nil slices")
	}
}

func TestFailedMutationDoesNotReplace(t *testing.T) {
	repo := NewMemory()
	stamp := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	original, err := domain.NewSpecimen("specimen-keep", "PPS-KEEP", "标签", "", "泥岩", "", stamp)
	if err != nil {
		t.Fatalf("NewSpecimen: %v", err)
	}
	if err := repo.Mutate(func(m *Mutation) error {
		m.PutSpecimen(original)
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err = repo.Mutate(func(m *Mutation) error {
		m.ReplaceAll(Data{Specimens: []domain.Specimen{}})
		if _, ok := m.GetSpecimen("specimen-keep"); ok {
			t.Fatal("replacement should be visible inside the transaction")
		}
		return assertErr
	})
	if err != assertErr {
		t.Fatalf("expected sentinel error, got %v", err)
	}

	var found domain.Specimen
	if err := repo.Read(func(m *Mutation) error {
		var ok bool
		found, ok = m.GetSpecimen("specimen-keep")
		if !ok {
			t.Fatal("original workspace was destroyed by the failed transaction")
		}
		return nil
	}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if found.CatalogNumber != "PPS-KEEP" {
		t.Fatalf("wrong surviving specimen: %+v", found)
	}
}

func TestReplaceAllReinstallsProgressInSequence(t *testing.T) {
	repo := NewMemory()
	first := domain.ProgressEntry{ID: "p1", PlanID: "plan-1", Sequence: 1, Percent: 30, Stage: "a", RecordedBy: "r", CreatedAt: time.Now().UTC()}
	second := domain.ProgressEntry{ID: "p2", PlanID: "plan-1", Sequence: 2, Percent: 100, Stage: "b", RecordedBy: "r", CreatedAt: time.Now().UTC()}
	err := repo.Mutate(func(m *Mutation) error {
		m.ReplaceAll(Data{Progress: []domain.ProgressEntry{second, first}})
		return nil
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if err := repo.Read(func(m *Mutation) error {
		entries := m.ProgressForPlan("plan-1")
		if len(entries) != 2 || entries[0].Sequence != 1 || entries[1].Sequence != 2 {
			t.Fatalf("progress not reinstalled in sequence: %+v", entries)
		}
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
}
