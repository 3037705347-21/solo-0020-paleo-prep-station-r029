package domain

import (
	"strings"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

const restoredIDMaxRunes = 64

// ValidSpecimenStatus reports whether status is one of the specimen states
// produced by the registered workflow.
func ValidSpecimenStatus(status SpecimenStatus) bool {
	switch status {
	case SpecimenRegistered,
		SpecimenPlanned,
		SpecimenPreparing,
		SpecimenStabilized,
		SpecimenAvailable:
		return true
	default:
		return false
	}
}

// ValidPlanStatus reports whether status is one of the plan states produced by
// the four production workflows.
func ValidPlanStatus(status PlanStatus) bool {
	switch status {
	case PlanDraft,
		PlanApproved,
		PlanActive,
		PlanCompleted,
		PlanCancelled:
		return true
	default:
		return false
	}
}

func validateRestoredID(kind, id string) error {
	cleaned := strings.TrimSpace(id)
	if cleaned == "" {
		return contracts.Invalid("invalid_workspace_entity", kind+"缺少标识")
	}
	if cleaned != id {
		return contracts.Invalid("invalid_workspace_entity", kind+"标识不能包含首尾空白："+id)
	}
	if len([]rune(id)) > restoredIDMaxRunes {
		return contracts.Invalid("invalid_workspace_entity", kind+"标识长度超出限制："+id)
	}
	return nil
}

func restoredRequiredText(kind, field, value string, max int) error {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return contracts.Invalid("invalid_workspace_entity", kind+"的"+field+"不能为空")
	}
	if cleaned != value {
		return contracts.Invalid("invalid_workspace_entity", kind+"的"+field+"不能包含首尾空白")
	}
	if len([]rune(value)) > max {
		return contracts.Invalid("invalid_workspace_entity", kind+"的"+field+"长度超出限制")
	}
	return nil
}

func restoredOptionalText(kind, field, value string, max int) error {
	if strings.TrimSpace(value) != value {
		return contracts.Invalid("invalid_workspace_entity", kind+"的"+field+"不能包含首尾空白")
	}
	if len([]rune(value)) > max {
		return contracts.Invalid("invalid_workspace_entity", kind+"的"+field+"长度超出限制")
	}
	return nil
}

func restoredTimestamp(kind, field string, value time.Time) error {
	if value.IsZero() {
		return contracts.Invalid("invalid_workspace_entity", kind+"的"+field+"不能为空")
	}
	if value.Location() != time.UTC {
		return contracts.Invalid("invalid_workspace_entity", kind+"的"+field+"必须使用 UTC 时间")
	}
	return nil
}

func restoredVersion(kind string, version int) error {
	if version < 1 {
		return contracts.Invalid("invalid_workspace_entity", kind+"的版本号必须大于零")
	}
	return nil
}

// RestoreSpecimen validates a specimen coming from a workspace package against
// the same field invariants used by NewSpecimen, plus state and version rules
// that the live workflows maintain. The value is never modified.
func RestoreSpecimen(value Specimen) error {
	kind := "标本"
	if err := validateRestoredID(kind, value.ID); err != nil {
		return err
	}
	catalog, err := validateCatalogNumber(value.CatalogNumber)
	if err != nil {
		return err
	}
	if catalog != value.CatalogNumber {
		return contracts.Invalid("invalid_catalog_number", "目录号只能包含字母、数字、点、下划线和连字符，且必须为规范大写形式："+value.CatalogNumber)
	}
	if err := restoredRequiredText(kind, "野外标签", value.FieldLabel, 80); err != nil {
		return err
	}
	if err := restoredOptionalText(kind, "分类提示", value.TaxonHint, 120); err != nil {
		return err
	}
	if err := restoredRequiredText(kind, "基质类型", value.MatrixKind, 60); err != nil {
		return err
	}
	if err := restoredOptionalText(kind, "夹克备注", value.JacketNotes, 500); err != nil {
		return err
	}
	if !ValidSpecimenStatus(value.Status) {
		return contracts.Invalid("invalid_workspace_entity", "标本 "+value.ID+" 的状态未知："+string(value.Status))
	}
	if err := restoredVersion(kind, value.Version); err != nil {
		return err
	}
	if err := restoredTimestamp(kind, "创建时间", value.CreatedAt); err != nil {
		return err
	}
	if err := restoredTimestamp(kind, "更新时间", value.UpdatedAt); err != nil {
		return err
	}
	if value.UpdatedAt.Before(value.CreatedAt) {
		return contracts.Invalid("invalid_workspace_entity", "标本 "+value.ID+" 的更新时间早于创建时间")
	}
	return nil
}

// RestorePlan validates a plan coming from a workspace package, including the
// approval information pairing enforced by PrepPlan.Approve.
func RestorePlan(value PrepPlan) error {
	kind := "修复方案"
	if err := validateRestoredID(kind, value.ID); err != nil {
		return err
	}
	if err := validateRestoredID(kind, value.SpecimenID); err != nil {
		return contracts.Invalid("invalid_workspace_entity", "修复方案 "+value.ID+" 的标本标识无效")
	}
	if err := restoredRequiredText(kind, "修复目标", value.Objective, 200); err != nil {
		return err
	}
	if err := restoredRequiredText(kind, "修复方法", value.Method, 160); err != nil {
		return err
	}
	if err := restoredOptionalText(kind, "黏结材料", value.Adhesive, 120); err != nil {
		return err
	}
	if err := validatePercent(value.TargetExposure); err != nil {
		return contracts.Invalid("invalid_target_exposure", "修复方案 "+value.ID+" 的目标暴露比例必须位于 0 到 100 之间")
	}
	if value.TargetExposure == 0 {
		return contracts.Invalid("invalid_target_exposure", "修复方案 "+value.ID+" 的目标暴露比例必须大于零")
	}
	if !ValidPlanStatus(value.Status) {
		return contracts.Invalid("invalid_workspace_entity", "修复方案 "+value.ID+" 的状态未知："+string(value.Status))
	}
	if err := restoredVersion(kind, value.Version); err != nil {
		return err
	}
	if err := restoredTimestamp(kind, "创建时间", value.CreatedAt); err != nil {
		return err
	}
	if err := restoredTimestamp(kind, "更新时间", value.UpdatedAt); err != nil {
		return err
	}
	if value.UpdatedAt.Before(value.CreatedAt) {
		return contracts.Invalid("invalid_workspace_entity", "修复方案 "+value.ID+" 的更新时间早于创建时间")
	}
	if value.Status == PlanDraft || value.Status == PlanCancelled {
		if value.ApprovedBy != "" || value.ApprovedAt != nil {
			return contracts.Invalid("invalid_workspace_entity", "修复方案 "+value.ID+" 尚未审批，却携带审批信息")
		}
		return nil
	}
	if err := restoredRequiredText(kind, "审批人", value.ApprovedBy, 80); err != nil {
		return err
	}
	if value.ApprovedAt == nil {
		return contracts.Invalid("invalid_workspace_entity", "修复方案 "+value.ID+" 缺少审批时间")
	}
	if err := restoredTimestamp(kind, "审批时间", *value.ApprovedAt); err != nil {
		return err
	}
	if value.ApprovedAt.Before(value.CreatedAt) {
		return contracts.Invalid("invalid_workspace_entity", "修复方案 "+value.ID+" 的审批时间早于创建时间")
	}
	return nil
}

// RestoreProgressEntry validates one progress entry from a workspace package.
// Sequence continuity and plan linkage are checked by the workspace package
// validator, which has visibility into the whole workspace.
func RestoreProgressEntry(value ProgressEntry) error {
	kind := "进度记录"
	if err := validateRestoredID(kind, value.ID); err != nil {
		return err
	}
	if err := validateRestoredID(kind, value.PlanID); err != nil {
		return contracts.Invalid("invalid_workspace_entity", "进度记录 "+value.ID+" 的方案标识无效")
	}
	if value.Sequence < 1 {
		return contracts.Invalid("invalid_workspace_entity", "进度记录 "+value.ID+" 的序号必须大于零")
	}
	if err := validatePercent(value.Percent); err != nil {
		return contracts.Invalid("invalid_percent", "进度记录 "+value.ID+" 的比例必须位于 0 到 100 之间")
	}
	if err := restoredRequiredText(kind, "阶段名称", value.Stage, 80); err != nil {
		return err
	}
	if err := restoredOptionalText(kind, "进度说明", value.Note, 500); err != nil {
		return err
	}
	if err := restoredRequiredText(kind, "记录人", value.RecordedBy, 80); err != nil {
		return err
	}
	if err := restoredTimestamp(kind, "创建时间", value.CreatedAt); err != nil {
		return err
	}
	return nil
}

// RestoreReleaseRecord validates one release record from a workspace package.
// Specimen linkage and uniqueness are checked by the workspace package
// validator.
func RestoreReleaseRecord(value ReleaseRecord) error {
	kind := "放行记录"
	if err := validateRestoredID(kind, value.ID); err != nil {
		return err
	}
	if err := validateRestoredID(kind, value.SpecimenID); err != nil {
		return contracts.Invalid("invalid_workspace_entity", "放行记录 "+value.ID+" 的标本标识无效")
	}
	if err := restoredRequiredText(kind, "借阅目的", value.Purpose, 200); err != nil {
		return err
	}
	if err := restoredRequiredText(kind, "接收方", value.Recipient, 120); err != nil {
		return err
	}
	if err := restoredRequiredText(kind, "批准人", value.ApprovedBy, 80); err != nil {
		return err
	}
	if err := restoredTimestamp(kind, "创建时间", value.CreatedAt); err != nil {
		return err
	}
	return nil
}
