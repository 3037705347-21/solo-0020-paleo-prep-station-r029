// Package workspace defines the portable workspace package format and the
// rules for exporting and re-importing the complete station state.
package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"example.com/paleo-prep-station/internal/domain"
)

// FormatVersion is the only package format this build can read and write.
const FormatVersion = 1

// PackageKind identifies the payload as a paleo prep station workspace.
const PackageKind = "paleo-prep-station-workspace"

// Package is the complete portable envelope. It records the format version,
// the generation time and every business fact held by the service.
type Package struct {
	FormatVersion int           `json:"format_version"`
	Kind          string        `json:"kind"`
	GeneratedAt   time.Time     `json:"generated_at"`
	Specimens     []SpecimenDTO `json:"specimens"`
	Plans         []PlanDTO     `json:"plans"`
	Progress      []ProgressDTO `json:"progress"`
	Releases      []ReleaseDTO  `json:"releases"`
}

// SpecimenDTO mirrors domain.Specimen for the package payload. Timestamps use
// RFC3339 strings so malformed values become field-level validation issues
// instead of aborting the whole package decode.
type SpecimenDTO struct {
	ID            string `json:"id"`
	CatalogNumber string `json:"catalog_number"`
	FieldLabel    string `json:"field_label"`
	TaxonHint     string `json:"taxon_hint"`
	MatrixKind    string `json:"matrix_kind"`
	JacketNotes   string `json:"jacket_notes"`
	Status        string `json:"status"`
	Version       int    `json:"version"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// PlanDTO mirrors domain.PrepPlan for the package payload.
type PlanDTO struct {
	ID             string  `json:"id"`
	SpecimenID     string  `json:"specimen_id"`
	Objective      string  `json:"objective"`
	Method         string  `json:"method"`
	Adhesive       string  `json:"adhesive"`
	TargetExposure int     `json:"target_exposure"`
	Status         string  `json:"status"`
	ApprovedBy     string  `json:"approved_by"`
	ApprovedAt     *string `json:"approved_at"`
	Version        int     `json:"version"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

// ProgressDTO mirrors domain.ProgressEntry for the package payload.
type ProgressDTO struct {
	ID         string `json:"id"`
	PlanID     string `json:"plan_id"`
	Sequence   int    `json:"sequence"`
	Stage      string `json:"stage"`
	Percent    int    `json:"percent"`
	Note       string `json:"note"`
	RecordedBy string `json:"recorded_by"`
	CreatedAt  string `json:"created_at"`
}

// ReleaseDTO mirrors domain.ReleaseRecord for the package payload.
type ReleaseDTO struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
	Purpose    string `json:"purpose"`
	Recipient  string `json:"recipient"`
	ApprovedBy string `json:"approved_by"`
	CreatedAt  string `json:"created_at"`
}

// NewPackage assembles a versioned package from validated domain entities.
func NewPackage(
	generatedAt time.Time,
	specimens []domain.Specimen,
	plans []domain.PrepPlan,
	progress []domain.ProgressEntry,
	releases []domain.ReleaseRecord,
) Package {
	stamp := generatedAt.UTC()
	pkg := Package{
		FormatVersion: FormatVersion,
		Kind:          PackageKind,
		GeneratedAt:   stamp,
		Specimens:     make([]SpecimenDTO, 0, len(specimens)),
		Plans:         make([]PlanDTO, 0, len(plans)),
		Progress:      make([]ProgressDTO, 0, len(progress)),
		Releases:      make([]ReleaseDTO, 0, len(releases)),
	}
	for _, value := range specimens {
		pkg.Specimens = append(pkg.Specimens, specimenToDTO(value))
	}
	for _, value := range plans {
		pkg.Plans = append(pkg.Plans, planToDTO(value))
	}
	for _, value := range progress {
		pkg.Progress = append(pkg.Progress, progressToDTO(value))
	}
	for _, value := range releases {
		pkg.Releases = append(pkg.Releases, releaseToDTO(value))
	}
	return pkg
}

func specimenToDTO(value domain.Specimen) SpecimenDTO {
	return SpecimenDTO{
		ID:            value.ID,
		CatalogNumber: value.CatalogNumber,
		FieldLabel:    value.FieldLabel,
		TaxonHint:     value.TaxonHint,
		MatrixKind:    value.MatrixKind,
		JacketNotes:   value.JacketNotes,
		Status:        string(value.Status),
		Version:       value.Version,
		CreatedAt:     value.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:     value.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func planToDTO(value domain.PrepPlan) PlanDTO {
	dto := PlanDTO{
		ID:             value.ID,
		SpecimenID:     value.SpecimenID,
		Objective:      value.Objective,
		Method:         value.Method,
		Adhesive:       value.Adhesive,
		TargetExposure: value.TargetExposure,
		Status:         string(value.Status),
		ApprovedBy:     value.ApprovedBy,
		Version:        value.Version,
		CreatedAt:      value.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:      value.UpdatedAt.Format(time.RFC3339Nano),
	}
	if value.ApprovedAt != nil {
		stamp := value.ApprovedAt.Format(time.RFC3339Nano)
		dto.ApprovedAt = &stamp
	}
	return dto
}

func progressToDTO(value domain.ProgressEntry) ProgressDTO {
	return ProgressDTO{
		ID:         value.ID,
		PlanID:     value.PlanID,
		Sequence:   value.Sequence,
		Stage:      value.Stage,
		Percent:    value.Percent,
		Note:       value.Note,
		RecordedBy: value.RecordedBy,
		CreatedAt:  value.CreatedAt.Format(time.RFC3339Nano),
	}
}

func releaseToDTO(value domain.ReleaseRecord) ReleaseDTO {
	return ReleaseDTO{
		ID:         value.ID,
		SpecimenID: value.SpecimenID,
		Purpose:    value.Purpose,
		Recipient:  value.Recipient,
		ApprovedBy: value.ApprovedBy,
		CreatedAt:  value.CreatedAt.Format(time.RFC3339Nano),
	}
}

// Decode parses a package strictly: unknown keys, multiple JSON values and
// malformed timestamps are all rejected as corrupted content.
func Decode(raw []byte) (Package, error) {
	var pkg Package
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pkg); err != nil {
		return Package{}, &PackageError{
			Code:    "workspace_corrupt",
			Message: "导入包不是合法的工作区 JSON：" + trimJSONError(err),
		}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return Package{}, &PackageError{
			Code:    "workspace_corrupt",
			Message: "导入包只能包含一个 JSON 对象",
		}
	} else if !errors.Is(err, io.EOF) {
		return Package{}, &PackageError{
			Code:    "workspace_corrupt",
			Message: "导入包尾部包含无法解析的内容：" + trimJSONError(err),
		}
	}
	return pkg, nil
}

// UnmarshalJSON enforces the envelope fields so that a renamed or truncated
// package is reported structurally rather than silently treated as empty.
func (p *Package) UnmarshalJSON(data []byte) error {
	var envelope struct {
		FormatVersion *json.RawMessage `json:"format_version"`
		Kind          *json.RawMessage `json:"kind"`
		GeneratedAt   *json.RawMessage `json:"generated_at"`
		Specimens     *json.RawMessage `json:"specimens"`
		Plans         *json.RawMessage `json:"plans"`
		Progress      *json.RawMessage `json:"progress"`
		Releases      *json.RawMessage `json:"releases"`
	}
	if err := strictObject(data, &envelope); err != nil {
		return err
	}
	if err := requireField("format_version", envelope.FormatVersion, &p.FormatVersion); err != nil {
		return err
	}
	if err := requireField("kind", envelope.Kind, &p.Kind); err != nil {
		return err
	}
	if err := requireTimeField("generated_at", envelope.GeneratedAt, &p.GeneratedAt); err != nil {
		return err
	}
	if err := requireArrayField("specimens", envelope.Specimens, &p.Specimens); err != nil {
		return err
	}
	if err := requireArrayField("plans", envelope.Plans, &p.Plans); err != nil {
		return err
	}
	if err := requireArrayField("progress", envelope.Progress, &p.Progress); err != nil {
		return err
	}
	if err := requireArrayField("releases", envelope.Releases, &p.Releases); err != nil {
		return err
	}
	return nil
}

// UnmarshalJSON keeps DTO decoding strict as well.
func (d *SpecimenDTO) UnmarshalJSON(data []byte) error {
	type alias SpecimenDTO
	var value alias
	if err := strictObject(data, &value); err != nil {
		return err
	}
	*d = SpecimenDTO(value)
	return nil
}

func (d *PlanDTO) UnmarshalJSON(data []byte) error {
	type alias PlanDTO
	var value alias
	if err := strictObject(data, &value); err != nil {
		return err
	}
	*d = PlanDTO(value)
	return nil
}

func (d *ProgressDTO) UnmarshalJSON(data []byte) error {
	type alias ProgressDTO
	var value alias
	if err := strictObject(data, &value); err != nil {
		return err
	}
	*d = ProgressDTO(value)
	return nil
}

func (d *ReleaseDTO) UnmarshalJSON(data []byte) error {
	type alias ReleaseDTO
	var value alias
	if err := strictObject(data, &value); err != nil {
		return err
	}
	*d = ReleaseDTO(value)
	return nil
}

func strictObject(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func requireField[T any](name string, raw *json.RawMessage, target *T) error {
	if raw == nil {
		return structuralField(name, "缺少必需字段")
	}
	decoder := json.NewDecoder(bytes.NewReader(*raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return structuralField(name, "字段类型不正确："+trimJSONError(err))
	}
	return nil
}

func requireTimeField(name string, raw *json.RawMessage, target *time.Time) error {
	var text string
	if err := requireField(name, raw, &text); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text))
	if err != nil {
		return structuralField(name, "时间必须使用 RFC3339 格式")
	}
	*target = parsed.UTC()
	return nil
}

func requireArrayField[T any](name string, raw *json.RawMessage, target *[]T) error {
	if raw == nil {
		return structuralField(name, "缺少必需的集合字段")
	}
	trimmed := bytes.TrimSpace(*raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return structuralField(name, "集合字段不能为 null")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return structuralField(name, "集合字段类型不正确："+trimJSONError(err))
	}
	return nil
}

func structuralField(name, message string) error {
	return &jsonError{field: name, message: message}
}

type jsonError struct {
	field   string
	message string
}

func (e *jsonError) Error() string {
	return "字段 " + e.field + "：" + e.message
}

func trimJSONError(err error) string {
	text := err.Error()
	if idx := strings.LastIndex(text, "·"); idx >= 0 {
		text = text[idx+3:]
	}
	return strings.TrimSpace(text)
}
