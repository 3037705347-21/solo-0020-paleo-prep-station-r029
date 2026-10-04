package station

import (
	"context"
	"errors"

	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/workspace"
)

// errAbortCommit stops a mutation from committing without surfacing as a
// failure: it marks the idempotent repeated-import path.
var errAbortCommit = errors.New("workspace import is a no-op")

// ImportReport explains the outcome of an import, including whether a
// repeated import left the workspace unchanged.
type ImportReport struct {
	Replaced    bool   `json:"replaced"`
	Unchanged   bool   `json:"unchanged"`
	Specimens   int    `json:"specimens"`
	Plans       int    `json:"plans"`
	Progress    int    `json:"progress"`
	Releases    int    `json:"releases"`
	Fingerprint string `json:"fingerprint"`
}

// ExportWorkspace reads every business fact and packages it with the current
// format version and a generation timestamp.
func (s *Service) ExportWorkspace(ctx context.Context) (workspace.Package, error) {
	if err := ctx.Err(); err != nil {
		return workspace.Package{}, err
	}
	var pkg workspace.Package
	err := s.repo.Read(func(m *repository.Mutation) error {
		specimens := m.ListSpecimens()
		plans := m.ListPlans()
		groups := m.ListProgressGroups()
		releases := m.ListReleases()
		progress := make([]domain.ProgressEntry, 0)
		for _, plan := range plans {
			progress = append(progress, groups[plan.ID]...)
		}
		pkg = workspace.NewPackage(s.now(), specimens, plans, progress, releases)
		return nil
	})
	return pkg, err
}

// ImportWorkspace validates the whole package before committing. Validation
// runs outside the commit closure and replacement happens inside a single
// mutation, so any structural or business failure leaves the running
// workspace byte-for-byte untouched.
func (s *Service) ImportWorkspace(ctx context.Context, raw []byte) (ImportReport, error) {
	if err := ctx.Err(); err != nil {
		return ImportReport{}, err
	}
	pkg, err := workspace.Decode(raw)
	if err != nil {
		return ImportReport{}, err
	}
	bundle, err := workspace.Validate(pkg)
	if err != nil {
		return ImportReport{}, err
	}
	incoming := bundle.Fingerprint()

	report := ImportReport{
		Specimens:   len(bundle.Specimens),
		Plans:       len(bundle.Plans),
		Releases:    len(bundle.Releases),
		Fingerprint: incoming,
	}
	for _, group := range bundle.Progress {
		report.Progress += len(group)
	}

	err = s.repo.Mutate(func(m *repository.Mutation) error {
		current := s.snapshotBundle(m)
		if current.Fingerprint() == incoming {
			report.Replaced = false
			report.Unchanged = true
			return errAbortCommit
		}
		m.ReplaceAll(bundle.Specimens, bundle.Plans, bundle.Progress, bundle.Releases)
		report.Replaced = true
		report.Unchanged = false
		return nil
	})
	if errors.Is(err, errAbortCommit) {
		return report, nil
	}
	return report, err
}

// snapshotBundle builds a fingerprintable view of the live state inside a
// mutation for idempotent repeated-import detection.
func (s *Service) snapshotBundle(m *repository.Mutation) workspace.Bundle {
	specimens := m.ListSpecimens()
	plans := m.ListPlans()
	groups := m.ListProgressGroups()
	releases := m.ListReleases()
	return workspace.Bundle{
		Specimens: specimens,
		Plans:     plans,
		Progress:  groups,
		Releases:  releases,
	}
}
