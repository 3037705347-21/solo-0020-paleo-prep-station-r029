package domain

import (
	"strings"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

// RestoreSpecimen rebuilds a specimen from an exported workspace package.
// It applies the same field and status rules as live registration instead of
// trusting the package bytes, while preserving the historical identity,
// status, version and timestamps captured at the export point.
func RestoreSpecimen(
	id string,
	catalogNumber string,
	fieldLabel string,
	taxonHint string,
	matrixKind string,
	jacketNotes string,
	status SpecimenStatus,
	version int,
	createdAt time.Time,
	updatedAt time.Time,
) (Specimen, error) {
	identity, err := restoreID("标本", id)
	if err != nil {
		return Specimen{}, err
	}
	catalog, err := validateCatalogNumber(catalogNumber)
	if err != nil {
		return Specimen{}, err
	}
	field, err := requiredText("野外标签", fieldLabel, 80)
	if err != nil {
		return Specimen{}, err
	}
	taxon, err := optionalText("分类提示", taxonHint, 120)
	if err != nil {
		return Specimen{}, err
	}
	matrix, err := requiredText("基质类型", matrixKind, 60)
	if err != nil {
		return Specimen{}, err
	}
	notes, err := optionalText("夹克备注", jacketNotes, 500)
	if err != nil {
		return Specimen{}, err
	}
	if !validSpecimenStatus(status) {
		return Specimen{}, contracts.Invalid("invalid_specimen_status", "标本状态不在受支持的状态集合中")
	}
	created, updated, err := restoreLifecycle(version, createdAt, updatedAt)
	if err != nil {
		return Specimen{}, err
	}
	return Specimen{
		ID:            identity,
		CatalogNumber: catalog,
		FieldLabel:    field,
		TaxonHint:     taxon,
		MatrixKind:    matrix,
		JacketNotes:   notes,
		Status:        status,
		Version:       version,
		CreatedAt:     created,
		UpdatedAt:     updated,
	}, nil
}

// RestorePrepPlan rebuilds a plan and re-checks the approver/approval-time
// pairing that the live approval workflow writes atomically.
func RestorePrepPlan(
	id string,
	specimenID string,
	objective string,
	method string,
	adhesive string,
	targetExposure int,
	status PlanStatus,
	approvedBy string,
	approvedAt *time.Time,
	version int,
	createdAt time.Time,
	updatedAt time.Time,
) (PrepPlan, error) {
	identity, err := restoreID("修复方案", id)
	if err != nil {
		return PrepPlan{}, err
	}
	specimen, err := restoreID("标本", specimenID)
	if err != nil {
		return PrepPlan{}, contracts.Invalid("invalid_entity_reference", "修复方案必须引用一个标本")
	}
	goal, err := requiredText("修复目标", objective, 200)
	if err != nil {
		return PrepPlan{}, err
	}
	approach, err := requiredText("修复方法", method, 160)
	if err != nil {
		return PrepPlan{}, err
	}
	adhesiveValue, err := optionalText("黏结材料", adhesive, 120)
	if err != nil {
		return PrepPlan{}, err
	}
	if targetExposure < 1 || targetExposure > 100 {
		return PrepPlan{}, contracts.Invalid("invalid_target_exposure", "目标暴露比例必须位于 1 到 100 之间")
	}
	if !validPlanStatus(status) {
		return PrepPlan{}, contracts.Invalid("invalid_plan_status", "方案状态不在受支持的状态集合中")
	}
	approver := strings.TrimSpace(approvedBy)
	hasApprover := approver != ""
	hasApprovalAt := approvedAt != nil && !approvedAt.IsZero()
	if hasApprover != hasApprovalAt {
		return PrepPlan{}, contracts.Invalid("approval_fields_inconsistent", "审批人和审批时间必须同时存在或同时为空")
	}
	if (status == PlanDraft || status == PlanCancelled) && (hasApprover || hasApprovalAt) {
		return PrepPlan{}, contracts.Invalid("approval_fields_inconsistent", "草稿或已取消方案不能携带审批信息")
	}
	if (status == PlanApproved || status == PlanActive || status == PlanCompleted) && !(hasApprover && hasApprovalAt) {
		return PrepPlan{}, contracts.Invalid("approval_fields_inconsistent", "已审批方案必须同时携带审批人和审批时间")
	}
	if len([]rune(approver)) > 80 {
		return PrepPlan{}, contracts.Invalid("field_too_long", "审批人长度超出限制")
	}
	created, updated, err := restoreLifecycle(version, createdAt, updatedAt)
	if err != nil {
		return PrepPlan{}, err
	}
	plan := PrepPlan{
		ID:             identity,
		SpecimenID:     specimen,
		Objective:      goal,
		Method:         approach,
		Adhesive:       adhesiveValue,
		TargetExposure: targetExposure,
		Status:         status,
		Version:        version,
		CreatedAt:      created,
		UpdatedAt:      updated,
	}
	if hasApprovalAt {
		stamp := approvedAt.UTC()
		plan.ApprovedBy = approver
		plan.ApprovedAt = &stamp
	}
	return plan, nil
}

// RestoreProgressEntry rebuilds one ordered progress record.
func RestoreProgressEntry(
	id string,
	planID string,
	sequence int,
	stage string,
	percent int,
	note string,
	recordedBy string,
	createdAt time.Time,
) (ProgressEntry, error) {
	identity, err := restoreID("进度", id)
	if err != nil {
		return ProgressEntry{}, err
	}
	plan, err := restoreID("修复方案", planID)
	if err != nil {
		return ProgressEntry{}, contracts.Invalid("invalid_entity_reference", "进度记录必须引用一个修复方案")
	}
	if sequence < 1 {
		return ProgressEntry{}, contracts.Invalid("invalid_progress_sequence", "进度序号必须从 1 开始连续递增")
	}
	if err := validatePercent(percent); err != nil {
		return ProgressEntry{}, err
	}
	stageValue, err := requiredText("阶段名称", stage, 80)
	if err != nil {
		return ProgressEntry{}, err
	}
	noteValue, err := optionalText("进度说明", note, 500)
	if err != nil {
		return ProgressEntry{}, err
	}
	recorder, err := requiredText("记录人", recordedBy, 80)
	if err != nil {
		return ProgressEntry{}, err
	}
	if createdAt.IsZero() {
		return ProgressEntry{}, contracts.Invalid("invalid_entity_timestamp", "进度记录时间不能为空")
	}
	return ProgressEntry{
		ID:         identity,
		PlanID:     plan,
		Sequence:   sequence,
		Stage:      stageValue,
		Percent:    percent,
		Note:       noteValue,
		RecordedBy: recorder,
		CreatedAt:  createdAt.UTC(),
	}, nil
}

// RestoreReleaseRecord rebuilds a permanent release record.
func RestoreReleaseRecord(
	id string,
	specimenID string,
	purpose string,
	recipient string,
	approvedBy string,
	createdAt time.Time,
) (ReleaseRecord, error) {
	identity, err := restoreID("放行", id)
	if err != nil {
		return ReleaseRecord{}, err
	}
	specimen, err := restoreID("标本", specimenID)
	if err != nil {
		return ReleaseRecord{}, contracts.Invalid("invalid_entity_reference", "放行记录必须引用一个标本")
	}
	purposeValue, err := requiredText("借阅目的", purpose, 200)
	if err != nil {
		return ReleaseRecord{}, err
	}
	recipientValue, err := requiredText("接收方", recipient, 120)
	if err != nil {
		return ReleaseRecord{}, err
	}
	approver, err := requiredText("批准人", approvedBy, 80)
	if err != nil {
		return ReleaseRecord{}, err
	}
	if createdAt.IsZero() {
		return ReleaseRecord{}, contracts.Invalid("invalid_entity_timestamp", "放行记录时间不能为空")
	}
	return ReleaseRecord{
		ID:         identity,
		SpecimenID: specimen,
		Purpose:    purposeValue,
		Recipient:  recipientValue,
		ApprovedBy: approver,
		CreatedAt:  createdAt.UTC(),
	}, nil
}

func restoreID(kind, value string) (string, error) {
	cleaned := strings.TrimSpace(value)
	if cleaned == "" {
		return "", contracts.Invalid("invalid_entity_id", kind+"标识不能为空")
	}
	if len([]rune(cleaned)) > 128 {
		return "", contracts.Invalid("invalid_entity_id", kind+"标识长度超出限制")
	}
	return cleaned, nil
}

func restoreLifecycle(version int, createdAt, updatedAt time.Time) (time.Time, time.Time, error) {
	if version < 1 {
		return time.Time{}, time.Time{}, contracts.Invalid("invalid_entity_version", "实体版本号必须大于零")
	}
	if createdAt.IsZero() || updatedAt.IsZero() {
		return time.Time{}, time.Time{}, contracts.Invalid("invalid_entity_timestamp", "实体创建时间和更新时间不能为空")
	}
	created := createdAt.UTC()
	updated := updatedAt.UTC()
	if updated.Before(created) {
		return time.Time{}, time.Time{}, contracts.Invalid("entity_timestamp_order", "实体更新时间不能早于创建时间")
	}
	return created, updated, nil
}

func validSpecimenStatus(status SpecimenStatus) bool {
	switch status {
	case SpecimenRegistered, SpecimenPlanned, SpecimenPreparing, SpecimenStabilized, SpecimenAvailable:
		return true
	default:
		return false
	}
}

func validPlanStatus(status PlanStatus) bool {
	switch status {
	case PlanDraft, PlanApproved, PlanActive, PlanCompleted, PlanCancelled:
		return true
	default:
		return false
	}
}
