package station

import (
	"context"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/workspace"
)

// ImportReport is the explainable outcome of replacing a workspace.
type ImportReport struct {
	Status     string           `json:"status"`
	Message    string           `json:"message"`
	Counts     workspace.Counts `json:"counts"`
	ExportedAt time.Time        `json:"exported_at"`
	Previous   workspace.Counts `json:"previous_counts"`
}

// ExportWorkspace reads every business fact and packages it with format
// metadata and a generation time.
func (s *Service) ExportWorkspace(ctx context.Context) (workspace.Package, error) {
	if err := ctx.Err(); err != nil {
		return workspace.Package{}, err
	}
	var pkg workspace.Package
	err := s.repo.Read(func(m *repository.Mutation) error {
		pkg = workspace.BuildPackage(m.ExportData(), s.now())
		return nil
	})
	return pkg, err
}

// ImportWorkspace parses and validates the package before touching the current
// workspace. Validation runs on the incoming content only; the repository
// swap happens inside one mutation, and any parse or validation failure is
// returned without calling ReplaceAll so the existing workspace survives
// intact.
func (s *Service) ImportWorkspace(ctx context.Context, raw []byte) (ImportReport, error) {
	if err := ctx.Err(); err != nil {
		return ImportReport{}, err
	}

	pkg, err := workspace.Parse(raw)
	if err != nil {
		return ImportReport{}, err
	}
	data, err := workspace.Validate(pkg)
	if err != nil {
		return ImportReport{}, err
	}

	var report ImportReport
	err = s.repo.Mutate(func(m *repository.Mutation) error {
		previous := m.ExportData()
		previousCounts := workspace.CountsOf(previous)
		unchanged := workspace.EqualData(previous, data)
		m.ReplaceAll(data)
		report = ImportReport{
			Counts:     workspace.CountsOf(data),
			ExportedAt: pkg.ExportedAt.UTC(),
			Previous:   previousCounts,
		}
		if unchanged {
			report.Status = "unchanged"
			report.Message = "导入的工作区与当前工作区包含完全相同的业务事实，状态保持不变"
		} else {
			report.Status = "replaced"
			report.Message = "工作区已整体替换为导入包内容；导入前内容未通过任何写入，原工作区未被破坏"
		}
		return nil
	})
	if err != nil {
		if _, ok := contracts.AsError(err); ok {
			return ImportReport{}, err
		}
		return ImportReport{}, contracts.Internal("workspace_import_failed", "工作区导入失败", err)
	}
	return report, nil
}
