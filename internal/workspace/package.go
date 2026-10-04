// Package workspace defines the portable workspace package: a versioned,
// self-describing snapshot of every business fact managed by the station.
//
// Export builds a package from repository data; import parses and validates a
// package with the same invariants enforced by the four production workflows
// before the service swaps the current workspace. A package is accepted only
// when the entire content is structurally and semantically valid.
package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

const (
	// FormatName identifies workspace packages produced by this service.
	FormatName = "paleo-prep-station-workspace"
	// FormatVersion is the only package format version accepted today.
	FormatVersion = 1
)

// Package is the on-the-wire workspace envelope. It records the format name,
// schema version, generation time and every business fact in the workspace.
type Package struct {
	Format     string                 `json:"format"`
	Version    int                    `json:"version"`
	ExportedAt time.Time              `json:"exported_at"`
	Specimens  []domain.Specimen      `json:"specimens"`
	Plans      []domain.PrepPlan      `json:"plans"`
	Progress   []domain.ProgressEntry `json:"progress"`
	Releases   []domain.ReleaseRecord `json:"releases"`
}

// Counts summarizes the business facts covered by a package.
type Counts struct {
	Specimens int `json:"specimens"`
	Plans     int `json:"plans"`
	Progress  int `json:"progress"`
	Releases  int `json:"releases"`
}

// BuildPackage wraps all current business facts into an export envelope.
func BuildPackage(data repository.Data, now time.Time) Package {
	canonical := canonicalData(data)
	return Package{
		Format:     FormatName,
		Version:    FormatVersion,
		ExportedAt: now.UTC(),
		Specimens:  nonNil(canonical.Specimens),
		Plans:      nonNil(canonical.Plans),
		Progress:   nonNil(canonical.Progress),
		Releases:   nonNil(canonical.Releases),
	}
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

type rawEnvelope struct {
	Format     json.RawMessage `json:"format"`
	Version    json.RawMessage `json:"version"`
	ExportedAt json.RawMessage `json:"exported_at"`
	Specimens  json.RawMessage `json:"specimens"`
	Plans      json.RawMessage `json:"plans"`
	Progress   json.RawMessage `json:"progress"`
	Releases   json.RawMessage `json:"releases"`
}

// Parse decodes a workspace package without mutating any service state. It
// enforces a single JSON object, no unknown fields, the supported format and
// version, a valid generation time and four array sections. Entity-level and
// cross-entity rules are checked by Validate.
func Parse(raw []byte) (Package, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope rawEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		if isEmptyBody(raw) {
			return Package{}, contracts.Invalid("empty_workspace_package", "导入内容为空，无法识别工作区包")
		}
		return Package{}, contracts.Invalid("malformed_workspace_package", "工作区包不是合法 JSON 或结构无法识别："+err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Package{}, contracts.Invalid("malformed_workspace_package", "工作区包只能包含一个 JSON 对象")
	}

	format, err := decodeRequiredString("format", envelope.Format)
	if err != nil {
		return Package{}, err
	}
	if format != FormatName {
		return Package{}, contracts.Invalid(
			"unsupported_workspace_format",
			"无法识别的工作区包格式："+format,
		)
	}
	version, err := decodeRequiredInt("version", envelope.Version)
	if err != nil {
		return Package{}, err
	}
	if version != FormatVersion {
		return Package{}, contracts.Invalid(
			"unsupported_workspace_version",
			fmt.Sprintf("工作区包格式版本 %d 不受支持，当前仅支持版本 %d", version, FormatVersion),
		)
	}
	exportedAtText, err := decodeRequiredString("exported_at", envelope.ExportedAt)
	if err != nil {
		return Package{}, err
	}
	exportedAt, err := time.Parse(time.RFC3339, exportedAtText)
	if err != nil || exportedAt.IsZero() {
		return Package{}, contracts.Invalid("invalid_exported_at", "工作区包的生成时间不是有效的 RFC3339 时间")
	}

	specimens, err := decodeArray[domain.Specimen]("specimens", envelope.Specimens)
	if err != nil {
		return Package{}, err
	}
	plans, err := decodeArray[domain.PrepPlan]("plans", envelope.Plans)
	if err != nil {
		return Package{}, err
	}
	progress, err := decodeArray[domain.ProgressEntry]("progress", envelope.Progress)
	if err != nil {
		return Package{}, err
	}
	releases, err := decodeArray[domain.ReleaseRecord]("releases", envelope.Releases)
	if err != nil {
		return Package{}, err
	}
	return Package{
		Format:     format,
		Version:    version,
		ExportedAt: exportedAt.UTC(),
		Specimens:  normalizeSpecimens(specimens),
		Plans:      normalizePlans(plans),
		Progress:   normalizeProgress(progress),
		Releases:   normalizeReleases(releases),
	}, nil
}

func normalizeSpecimens(values []domain.Specimen) []domain.Specimen {
	for index := range values {
		values[index].CreatedAt = values[index].CreatedAt.UTC()
		values[index].UpdatedAt = values[index].UpdatedAt.UTC()
	}
	return values
}

func normalizePlans(values []domain.PrepPlan) []domain.PrepPlan {
	for index := range values {
		values[index].CreatedAt = values[index].CreatedAt.UTC()
		values[index].UpdatedAt = values[index].UpdatedAt.UTC()
		if values[index].ApprovedAt != nil {
			stamp := values[index].ApprovedAt.UTC()
			values[index].ApprovedAt = &stamp
		}
	}
	return values
}

func normalizeProgress(values []domain.ProgressEntry) []domain.ProgressEntry {
	for index := range values {
		values[index].CreatedAt = values[index].CreatedAt.UTC()
	}
	return values
}

func normalizeReleases(values []domain.ReleaseRecord) []domain.ReleaseRecord {
	for index := range values {
		values[index].CreatedAt = values[index].CreatedAt.UTC()
	}
	return values
}

// Validate applies every workspace invariant to a parsed package and returns
// the canonical data ready to replace the current workspace. The rules mirror
// those enforced by specimen registration, plan approval, progress logging
// and release workflows.
func Validate(pkg Package) (repository.Data, error) {
	if pkg.Format != FormatName || pkg.Version != FormatVersion {
		return repository.Data{}, contracts.Invalid("unsupported_workspace_version", "工作区包格式或版本不受支持")
	}
	if pkg.ExportedAt.IsZero() {
		return repository.Data{}, contracts.Invalid("invalid_exported_at", "工作区包缺少生成时间")
	}

	specimenIDs := make(map[string]domain.Specimen, len(pkg.Specimens))
	catalogNumbers := make(map[string]string, len(pkg.Specimens))
	for _, value := range pkg.Specimens {
		if err := domain.RestoreSpecimen(value); err != nil {
			return repository.Data{}, err
		}
		if _, exists := specimenIDs[value.ID]; exists {
			return repository.Data{}, contracts.Invalid("duplicate_specimen_id", "工作区包包含重复标本标识："+value.ID)
		}
		if other, exists := catalogNumbers[value.CatalogNumber]; exists {
			return repository.Data{}, contracts.Invalid(
				"duplicate_catalog_number",
				"目录号 "+value.CatalogNumber+" 同时出现在标本 "+other+" 与 "+value.ID+" 中",
			)
		}
		specimenIDs[value.ID] = value
		catalogNumbers[value.CatalogNumber] = value.ID
	}

	planIDs := make(map[string]domain.PrepPlan, len(pkg.Plans))
	openPlans := make(map[string]int)
	completedPlans := make(map[string]int)
	for _, value := range pkg.Plans {
		if err := domain.RestorePlan(value); err != nil {
			return repository.Data{}, err
		}
		if _, exists := planIDs[value.ID]; exists {
			return repository.Data{}, contracts.Invalid("duplicate_plan_id", "工作区包包含重复方案标识："+value.ID)
		}
		if _, ok := specimenIDs[value.SpecimenID]; !ok {
			return repository.Data{}, contracts.Invalid(
				"dangling_plan_reference",
				"修复方案 "+value.ID+" 引用了不存在的标本 "+value.SpecimenID,
			)
		}
		planIDs[value.ID] = value
		if value.IsOpen() {
			openPlans[value.SpecimenID]++
		}
		if value.IsCompleted() {
			completedPlans[value.SpecimenID]++
		}
	}
	for specimenID, count := range openPlans {
		if count > 1 {
			return repository.Data{}, contracts.Invalid(
				"multiple_open_plans",
				"标本 "+specimenID+" 存在 "+itoa(count)+" 个未结束方案，同一时间最多允许一个",
			)
		}
	}
	for specimenID, count := range completedPlans {
		if count > 1 {
			return repository.Data{}, contracts.Invalid(
				"multiple_completed_plans",
				"标本 "+specimenID+" 存在 "+itoa(count)+" 个已完成方案，每份标本至多一个完成方案",
			)
		}
	}

	progressByPlan := make(map[string][]domain.ProgressEntry, len(pkg.Plans))
	progressIDs := make(map[string]struct{}, len(pkg.Progress))
	for _, value := range pkg.Progress {
		if err := domain.RestoreProgressEntry(value); err != nil {
			return repository.Data{}, err
		}
		if _, exists := progressIDs[value.ID]; exists {
			return repository.Data{}, contracts.Invalid("duplicate_progress_id", "工作区包包含重复进度标识："+value.ID)
		}
		progressIDs[value.ID] = struct{}{}
		if _, ok := planIDs[value.PlanID]; !ok {
			return repository.Data{}, contracts.Invalid(
				"dangling_progress_reference",
				"进度记录 "+value.ID+" 引用了不存在的修复方案 "+value.PlanID,
			)
		}
		progressByPlan[value.PlanID] = append(progressByPlan[value.PlanID], value)
	}
	for planID, entries := range progressByPlan {
		plan := planIDs[planID]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Sequence < entries[j].Sequence })
		for index, entry := range entries {
			if entry.Sequence != index+1 {
				return repository.Data{}, contracts.Invalid(
					"progress_sequence_gap",
					fmt.Sprintf("修复方案 %s 的进度序号必须从 1 连续递增，第 %d 条记录序号为 %d", planID, index+1, entry.Sequence),
				)
			}
			if index > 0 && entry.Percent < entries[index-1].Percent {
				return repository.Data{}, contracts.Invalid(
					"progress_not_monotonic",
					fmt.Sprintf("修复方案 %s 的进度比例在序号 %d 处出现回退", planID, entry.Sequence),
				)
			}
		}
		lastPercent := 0
		if len(entries) > 0 {
			lastPercent = entries[len(entries)-1].Percent
		}
		switch plan.Status {
		case domain.PlanDraft, domain.PlanCancelled:
			if len(entries) > 0 {
				return repository.Data{}, contracts.Invalid(
					"progress_state_mismatch",
					"修复方案 "+planID+" 处于 "+string(plan.Status)+" 状态，却携带进度记录",
				)
			}
		case domain.PlanApproved:
			if len(entries) > 0 {
				return repository.Data{}, contracts.Invalid(
					"progress_state_mismatch",
					"修复方案 "+planID+" 尚未开始制备，却携带进度记录",
				)
			}
		case domain.PlanActive:
			if len(entries) == 0 {
				return repository.Data{}, contracts.Invalid(
					"progress_state_mismatch",
					"修复方案 "+planID+" 已开始制备，却缺少进度记录",
				)
			}
			if lastPercent >= 100 {
				return repository.Data{}, contracts.Invalid(
					"progress_terminal_mismatch",
					"修复方案 "+planID+" 的最后进度已达到 100%，方案应当已完成",
				)
			}
		case domain.PlanCompleted:
			if len(entries) == 0 {
				return repository.Data{}, contracts.Invalid(
					"progress_terminal_mismatch",
					"修复方案 "+planID+" 已完成，却缺少进度记录",
				)
			}
			if lastPercent != 100 {
				return repository.Data{}, contracts.Invalid(
					"progress_terminal_mismatch",
					"修复方案 "+planID+" 已完成，但最后一条进度比例为 "+itoa(lastPercent)+"% 而不是 100%",
				)
			}
		}
	}

	releaseBySpecimen := make(map[string]domain.ReleaseRecord, len(pkg.Releases))
	releaseIDs := make(map[string]struct{}, len(pkg.Releases))
	for _, value := range pkg.Releases {
		if err := domain.RestoreReleaseRecord(value); err != nil {
			return repository.Data{}, err
		}
		if _, exists := releaseIDs[value.ID]; exists {
			return repository.Data{}, contracts.Invalid("duplicate_release_id", "工作区包包含重复放行标识："+value.ID)
		}
		releaseIDs[value.ID] = struct{}{}
		if _, ok := specimenIDs[value.SpecimenID]; !ok {
			return repository.Data{}, contracts.Invalid(
				"dangling_release_reference",
				"放行记录 "+value.ID+" 引用了不存在的标本 "+value.SpecimenID,
			)
		}
		if existing, exists := releaseBySpecimen[value.SpecimenID]; exists {
			return repository.Data{}, contracts.Invalid(
				"duplicate_release_record",
				"标本 "+value.SpecimenID+" 同时存在放行记录 "+existing.ID+" 与 "+value.ID,
			)
		}
		releaseBySpecimen[value.SpecimenID] = value
	}

	for id, specimen := range specimenIDs {
		_, hasRelease := releaseBySpecimen[id]
		openCount := openPlans[id]
		completedCount := completedPlans[id]
		switch {
		case hasRelease:
			if specimen.Status != domain.SpecimenAvailable {
				return repository.Data{}, contracts.Invalid(
					"specimen_state_mismatch",
					"标本 "+id+" 已有放行记录，状态必须是 available，实际为 "+string(specimen.Status),
				)
			}
			if openCount > 0 {
				return repository.Data{}, contracts.Invalid(
					"specimen_state_mismatch",
					"标本 "+id+" 已放行，却仍存在未结束方案",
				)
			}
			if completedCount == 0 {
				return repository.Data{}, contracts.Invalid(
					"specimen_state_mismatch",
					"标本 "+id+" 已放行，却缺少已完成的制备方案",
				)
			}
		case specimen.Status == domain.SpecimenRegistered:
			if completedCount > 0 || openCount > 0 || hasCancelledPlan(pkg.Plans, id) {
				return repository.Data{}, contracts.Invalid(
					"specimen_state_mismatch",
					"标本 "+id+" 为 registered，却已经存在修复方案",
				)
			}
		case specimen.Status == domain.SpecimenPlanned:
			plan, hasOpen := singleOpenPlan(pkg.Plans, id)
			switch {
			case completedCount > 0:
				return repository.Data{}, contracts.Invalid(
					"specimen_state_mismatch",
					"标本 "+id+" 为 planned，却已经存在完成方案",
				)
			case hasOpen && plan.Status == domain.PlanDraft:
				// A live draft plan is the normal planned state.
			case !hasOpen && hasCancelledPlan(pkg.Plans, id):
				// The only plan(s) were cancelled before approval; the
				// specimen stays planned and a new draft can be created.
			default:
				return repository.Data{}, contracts.Invalid(
					"specimen_state_mismatch",
					"标本 "+id+" 为 planned，必须存在一个草稿方案，或仅保留已取消的草稿",
				)
			}
		case specimen.Status == domain.SpecimenPreparing:
			plan, ok := singleOpenPlan(pkg.Plans, id)
			if !ok || completedCount > 0 {
				return repository.Data{}, contracts.Invalid(
					"specimen_state_mismatch",
					"标本 "+id+" 为 preparing，必须恰好存在一个已审批或进行中的方案",
				)
			}
			if plan.Status != domain.PlanApproved && plan.Status != domain.PlanActive {
				return repository.Data{}, contracts.Invalid(
					"specimen_state_mismatch",
					"标本 "+id+" 为 preparing，但开放方案状态为 "+string(plan.Status),
				)
			}
		case specimen.Status == domain.SpecimenStabilized:
			if openCount > 0 || completedCount != 1 {
				return repository.Data{}, contracts.Invalid(
					"specimen_state_mismatch",
					"标本 "+id+" 为 stabilized，必须不存在开放方案且恰好有一个完成方案",
				)
			}
		case specimen.Status == domain.SpecimenAvailable:
			return repository.Data{}, contracts.Invalid(
				"specimen_state_mismatch",
				"标本 "+id+" 为 available，却缺少放行记录",
			)
		}
	}

	data := repository.Data{
		Specimens: pkg.Specimens,
		Plans:     pkg.Plans,
		Progress:  pkg.Progress,
		Releases:  pkg.Releases,
	}
	return canonicalData(data), nil
}

// CountsOf returns the number of business facts of each kind in the data.
func CountsOf(data repository.Data) Counts {
	return Counts{
		Specimens: len(data.Specimens),
		Plans:     len(data.Plans),
		Progress:  len(data.Progress),
		Releases:  len(data.Releases),
	}
}

// EqualData reports whether two datasets cover exactly the same business
// facts. Both inputs are canonicalized before comparison, so order on input
// does not matter.
func EqualData(left, right repository.Data) bool {
	a := canonicalData(left)
	b := canonicalData(right)
	return reflect.DeepEqual(a.Specimens, b.Specimens) &&
		reflect.DeepEqual(a.Plans, b.Plans) &&
		reflect.DeepEqual(a.Progress, b.Progress) &&
		reflect.DeepEqual(a.Releases, b.Releases)
}

func decodeRequiredString(field string, raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", contracts.Invalid("malformed_workspace_package", "工作区包缺少必需字段："+field)
	}
	var value string
	if err := strictUnmarshal(raw, &value); err != nil {
		return "", contracts.Invalid("malformed_workspace_package", "字段 "+field+" 必须是字符串")
	}
	return value, nil
}

func decodeRequiredInt(field string, raw json.RawMessage) (int, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return 0, contracts.Invalid("malformed_workspace_package", "工作区包缺少必需字段："+field)
	}
	var value int
	if err := strictUnmarshal(raw, &value); err != nil {
		return 0, contracts.Invalid("malformed_workspace_package", "字段 "+field+" 必须是整数")
	}
	return value, nil
}

func decodeArray[T any](field string, raw json.RawMessage) ([]T, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, contracts.Invalid("malformed_workspace_package", "工作区包缺少必需字段："+field)
	}
	if trimmed[0] != '[' {
		return nil, contracts.Invalid("malformed_workspace_package", "字段 "+field+" 必须是数组")
	}
	var values []T
	if err := strictUnmarshal(trimmed, &values); err != nil {
		return nil, contracts.Invalid("malformed_workspace_package", "字段 "+field+" 包含无法识别的内容："+err.Error())
	}
	if values == nil {
		values = []T{}
	}
	return values, nil
}

func strictUnmarshal(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return fmt.Errorf("内容后存在多余数据")
	}
	return nil
}

func isEmptyBody(raw []byte) bool {
	return len(bytes.TrimSpace(raw)) == 0
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}
