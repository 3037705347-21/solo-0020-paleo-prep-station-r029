package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
)

// Issue describes one concrete reason a package cannot be restored.
type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Subject string `json:"subject,omitempty"`
}

// PackageError is the explainable result of a rejected export package.
type PackageError struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Issues  []Issue `json:"issues,omitempty"`
}

func (e *PackageError) Error() string {
	text := e.Message
	if len(e.Issues) > 0 {
		text += "（共 " + itoa(len(e.Issues)) + " 项问题，首项：" + e.Issues[0].Message + "）"
	}
	return text
}

// Bundle holds the fully validated, ready-to-commit workspace state.
type Bundle struct {
	Specimens []domain.Specimen
	Plans     []domain.PrepPlan
	Progress  map[string][]domain.ProgressEntry
	Releases  []domain.ReleaseRecord
}

// Validate enforces the envelope and every business invariant shared with the
// four live workflows. It only returns a Bundle when the whole package is
// consistent, so callers never touch the running state on partial data.
func Validate(pkg Package) (Bundle, error) {
	if err := validateEnvelope(pkg); err != nil {
		return Bundle{}, err
	}

	issues := make([]Issue, 0)
	add := func(code, message, subject string) {
		issues = append(issues, Issue{Code: code, Message: message, Subject: subject})
	}

	specimens := make(map[string]domain.Specimen, len(pkg.Specimens))
	catalogs := make(map[string]string, len(pkg.Specimens))
	for _, dto := range pkg.Specimens {
		createdAt, createdErr := parseTimestamp(dto.CreatedAt)
		updatedAt, updatedErr := parseTimestamp(dto.UpdatedAt)
		if createdErr != nil {
			add("invalid_entity_timestamp", "标本 "+label(dto.ID, dto.CatalogNumber)+"：创建时间必须使用 RFC3339 格式", dto.ID)
			continue
		}
		if updatedErr != nil {
			add("invalid_entity_timestamp", "标本 "+label(dto.ID, dto.CatalogNumber)+"：更新时间必须使用 RFC3339 格式", dto.ID)
			continue
		}
		value, err := domain.RestoreSpecimen(
			dto.ID,
			dto.CatalogNumber,
			dto.FieldLabel,
			dto.TaxonHint,
			dto.MatrixKind,
			dto.JacketNotes,
			domain.SpecimenStatus(dto.Status),
			dto.Version,
			createdAt,
			updatedAt,
		)
		if err != nil {
			add(issueCode(err), "标本 "+label(dto.ID, dto.CatalogNumber)+"："+err.Error(), dto.ID)
			continue
		}
		if _, exists := specimens[value.ID]; exists {
			add("duplicate_specimen_id", "标本标识重复："+value.ID, value.ID)
		}
		if first, exists := catalogs[value.CatalogNumber]; exists && first != value.ID {
			add("duplicate_catalog_number", "目录号 "+value.CatalogNumber+" 被多个标本共用", value.CatalogNumber)
		} else {
			catalogs[value.CatalogNumber] = value.ID
		}
		specimens[value.ID] = value
	}

	plans := make(map[string]domain.PrepPlan, len(pkg.Plans))
	for _, dto := range pkg.Plans {
		createdAt, createdErr := parseTimestamp(dto.CreatedAt)
		updatedAt, updatedErr := parseTimestamp(dto.UpdatedAt)
		var approvedAt *time.Time
		if dto.ApprovedAt != nil {
			parsed, err := parseTimestamp(*dto.ApprovedAt)
			if err != nil {
				add("invalid_entity_timestamp", "方案 "+label(dto.ID, dto.Objective)+"：审批时间必须使用 RFC3339 格式", dto.ID)
				continue
			}
			approvedAt = &parsed
		}
		if createdErr != nil {
			add("invalid_entity_timestamp", "方案 "+label(dto.ID, dto.Objective)+"：创建时间必须使用 RFC3339 格式", dto.ID)
			continue
		}
		if updatedErr != nil {
			add("invalid_entity_timestamp", "方案 "+label(dto.ID, dto.Objective)+"：更新时间必须使用 RFC3339 格式", dto.ID)
			continue
		}
		value, err := domain.RestorePrepPlan(
			dto.ID,
			dto.SpecimenID,
			dto.Objective,
			dto.Method,
			dto.Adhesive,
			dto.TargetExposure,
			domain.PlanStatus(dto.Status),
			dto.ApprovedBy,
			approvedAt,
			dto.Version,
			createdAt,
			updatedAt,
		)
		if err != nil {
			add(issueCode(err), "方案 "+label(dto.ID, dto.Objective)+"："+err.Error(), dto.ID)
			continue
		}
		if _, exists := plans[value.ID]; exists {
			add("duplicate_plan_id", "修复方案标识重复："+value.ID, value.ID)
		}
		if _, ok := specimens[value.SpecimenID]; !ok {
			add("broken_entity_reference", "方案 "+value.ID+" 引用了不存在的标本 "+value.SpecimenID, value.ID)
		}
		plans[value.ID] = value
	}

	entries := make(map[string]domain.ProgressEntry, len(pkg.Progress))
	groups := make(map[string][]domain.ProgressEntry)
	for _, dto := range pkg.Progress {
		createdAt, err := parseTimestamp(dto.CreatedAt)
		if err != nil {
			add("invalid_entity_timestamp", "进度记录 "+label(dto.ID, dto.Stage)+"：记录时间必须使用 RFC3339 格式", dto.ID)
			continue
		}
		value, err := domain.RestoreProgressEntry(
			dto.ID,
			dto.PlanID,
			dto.Sequence,
			dto.Stage,
			dto.Percent,
			dto.Note,
			dto.RecordedBy,
			createdAt,
		)
		if err != nil {
			add(issueCode(err), "进度记录 "+label(dto.ID, dto.Stage)+"："+err.Error(), dto.ID)
			continue
		}
		if _, exists := entries[value.ID]; exists {
			add("duplicate_progress_id", "进度记录标识重复："+value.ID, value.ID)
		}
		if _, ok := plans[value.PlanID]; !ok {
			add("broken_entity_reference", "进度记录 "+value.ID+" 引用了不存在的方案 "+value.PlanID, value.ID)
		}
		entries[value.ID] = value
		groups[value.PlanID] = append(groups[value.PlanID], value)
	}

	releases := make(map[string]domain.ReleaseRecord, len(pkg.Releases))
	releaseCount := make(map[string]int)
	for _, dto := range pkg.Releases {
		createdAt, err := parseTimestamp(dto.CreatedAt)
		if err != nil {
			add("invalid_entity_timestamp", "放行记录 "+label(dto.ID, dto.Purpose)+"：记录时间必须使用 RFC3339 格式", dto.ID)
			continue
		}
		value, err := domain.RestoreReleaseRecord(
			dto.ID,
			dto.SpecimenID,
			dto.Purpose,
			dto.Recipient,
			dto.ApprovedBy,
			createdAt,
		)
		if err != nil {
			add(issueCode(err), "放行记录 "+label(dto.ID, dto.Purpose)+"："+err.Error(), dto.ID)
			continue
		}
		if _, exists := releases[value.ID]; exists {
			add("duplicate_release_id", "放行记录标识重复："+value.ID, value.ID)
		}
		if _, ok := specimens[value.SpecimenID]; !ok {
			add("broken_entity_reference", "放行记录 "+value.ID+" 引用了不存在的标本 "+value.SpecimenID, value.ID)
		}
		releases[value.ID] = value
		releaseCount[value.SpecimenID]++
	}

	// 进度连续性：每个方案内部序号连续、比例不回退、时间不倒挂。
	for planID, group := range groups {
		sort.Slice(group, func(i, j int) bool { return group[i].Sequence < group[j].Sequence })
		for index, entry := range group {
			if entry.Sequence != index+1 {
				add("progress_sequence_gap",
					"方案 "+planID+" 的进度序号必须从 1 连续递增，发现序号 "+itoa(entry.Sequence),
					planID)
				break
			}
		}
		for index := 1; index < len(group); index++ {
			if group[index].Percent < group[index-1].Percent {
				add("progress_regressed",
					"方案 "+planID+" 第 "+itoa(group[index].Sequence)+" 条进度比例回退",
					planID)
			}
			if group[index].CreatedAt.Before(group[index-1].CreatedAt) {
				add("progress_timestamp_order",
					"方案 "+planID+" 第 "+itoa(group[index].Sequence)+" 条进度时间早于上一条",
					planID)
			}
		}
	}

	// 方案与标本的跨实体一致性：未结束方案唯一、状态与进度互证。
	plansBySpecimen := make(map[string][]domain.PrepPlan)
	openPlans := make(map[string][]string)
	for _, id := range sortedPlanIDs(plans) {
		plan := plans[id]
		plansBySpecimen[plan.SpecimenID] = append(plansBySpecimen[plan.SpecimenID], plan)
		if plan.IsOpen() {
			openPlans[plan.SpecimenID] = append(openPlans[plan.SpecimenID], plan.ID)
		}
		if err := validatePlanProgress(plan, groups[plan.ID]); err != nil {
			add(issueCode(err), "方案 "+plan.ID+"："+err.Error(), plan.ID)
		}
	}
	for specimenID, ids := range openPlans {
		if len(ids) > 1 {
			add("open_plan_conflict", "标本 "+specimenID+" 同时存在多个未结束方案："+joinIDs(ids), specimenID)
		}
	}
	for _, specimen := range orderedSpecimens(specimens) {
		if err := validateSpecimenPlans(specimen, plansBySpecimen[specimen.ID], openPlans[specimen.ID]); err != nil {
			add(issueCode(err), "标本 "+specimen.ID+"："+err.Error(), specimen.ID)
		}
	}

	// 标本与放行记录互证，且每份标本最多一条放行记录。
	for specimenID, count := range releaseCount {
		if count > 1 {
			add("release_not_unique", "标本 "+specimenID+" 存在 "+itoa(count)+" 条放行记录，每份标本最多一条", specimenID)
		}
	}
	releaseBySpecimen := make(map[string]domain.ReleaseRecord, len(releases))
	for _, record := range releases {
		if _, exists := releaseBySpecimen[record.SpecimenID]; !exists {
			releaseBySpecimen[record.SpecimenID] = record
		}
	}
	for _, specimen := range orderedSpecimens(specimens) {
		_, hasRelease := releaseBySpecimen[specimen.ID]
		if err := validateSpecimenRelease(specimen, hasRelease, openPlans[specimen.ID]); err != nil {
			add(issueCode(err), "标本 "+specimen.ID+"："+err.Error(), specimen.ID)
		}
	}

	if len(issues) > 0 {
		return Bundle{}, &PackageError{
			Code:    "workspace_validation_failed",
			Message: "导入包未通过工作区一致性校验，当前工作区未被修改",
			Issues:  issues,
		}
	}

	specimenList := make([]domain.Specimen, 0, len(specimens))
	for _, value := range specimens {
		specimenList = append(specimenList, value)
	}
	sortSpecimens(specimenList)
	planList := make([]domain.PrepPlan, 0, len(plans))
	for _, value := range plans {
		planList = append(planList, value)
	}
	sortPlans(planList)
	releaseList := make([]domain.ReleaseRecord, 0, len(releases))
	for _, value := range releases {
		releaseList = append(releaseList, value)
	}
	sortReleases(releaseList)

	return Bundle{
		Specimens: specimenList,
		Plans:     planList,
		Progress:  groups,
		Releases:  releaseList,
	}, nil
}

func validateEnvelope(pkg Package) error {
	if pkg.FormatVersion != FormatVersion {
		return &PackageError{
			Code:    "unsupported_format_version",
			Message: "导入包格式版本 " + itoa(pkg.FormatVersion) + " 不受支持，当前仅支持版本 " + itoa(FormatVersion),
		}
	}
	if pkg.Kind != PackageKind {
		return &PackageError{
			Code:    "unrecognized_workspace_kind",
			Message: "导入包类型标识不匹配，无法识别为修复制备台工作区",
		}
	}
	if pkg.GeneratedAt.IsZero() {
		return &PackageError{
			Code:    "workspace_generation_missing",
			Message: "导入包缺少有效的生成时间",
		}
	}
	return nil
}

// validatePlanProgress re-checks the same terminal rules LogProgress enforces.
func validatePlanProgress(plan domain.PrepPlan, group []domain.ProgressEntry) error {
	sort.Slice(group, func(i, j int) bool { return group[i].Sequence < group[j].Sequence })
	last := 0
	if len(group) > 0 {
		last = group[len(group)-1].Percent
	}
	for index, entry := range group {
		if entry.Percent == 100 && index != len(group)-1 {
			return conflict("progress_after_completion", "100% 只能出现在最后一条进度中")
		}
	}
	switch plan.Status {
	case domain.PlanDraft, domain.PlanCancelled:
		if len(group) > 0 {
			return conflict("plan_progress_invalid", "草稿或已取消方案不能携带进度记录")
		}
	case domain.PlanApproved:
		if len(group) > 0 {
			return conflict("plan_progress_invalid", "已审批但尚未开始的方案不能携带进度记录")
		}
	case domain.PlanActive:
		if len(group) == 0 {
			return conflict("plan_progress_invalid", "进行中的方案必须至少包含一条进度记录")
		}
		if last >= 100 {
			return conflict("plan_progress_invalid", "进度达到 100% 时方案必须为 completed")
		}
	case domain.PlanCompleted:
		if len(group) == 0 || last < 100 {
			return conflict("plan_progress_invalid", "已完成方案必须以 100% 的最后一条进度结束")
		}
	}
	return nil
}

// validateSpecimenPlans verifies the specimen/plan state combination that the
// live plan workflows can only produce together in one mutation. The check is
// set-based because a specimen can legitimately retain cancelled drafts and
// completed plans at the same time.
func validateSpecimenPlans(
	specimen domain.Specimen,
	plans []domain.PrepPlan,
	openPlanIDs []string,
) error {
	if len(plans) == 0 {
		if specimen.Status != domain.SpecimenRegistered {
			return conflict("specimen_plan_state_conflict", "没有任何方案的标本只能处于 registered")
		}
		return nil
	}
	hasApproved := false
	hasActive := false
	hasCompleted := false
	for _, plan := range plans {
		switch plan.Status {
		case domain.PlanApproved:
			hasApproved = true
		case domain.PlanActive:
			hasActive = true
		case domain.PlanCompleted:
			hasCompleted = true
		}
	}
	working := hasApproved || hasActive
	switch specimen.Status {
	case domain.SpecimenRegistered:
		return conflict("specimen_plan_state_conflict", "方案创建与 planned 标记必须同时发生，registered 标本不能携带任何方案")
	case domain.SpecimenPlanned:
		if working || hasCompleted {
			return conflict("specimen_plan_state_conflict", "planned 标本只能持有草稿或已取消方案")
		}
		if len(openPlanIDs) > 1 {
			return conflict("open_plan_conflict", "一份标本同时最多存在一个未结束方案")
		}
	case domain.SpecimenPreparing:
		if !working {
			return conflict("specimen_plan_state_conflict", "preparing 标本必须持有一个已审批或进行中的未结束方案")
		}
		if hasApproved && hasActive {
			return conflict("specimen_plan_state_conflict", "同一标本不能同时存在已审批与进行中的未结束方案")
		}
		if len(openPlanIDs) > 1 {
			return conflict("open_plan_conflict", "一份标本同时最多存在一个未结束方案")
		}
	case domain.SpecimenStabilized, domain.SpecimenAvailable:
		if len(openPlanIDs) > 0 {
			return conflict("open_plan_conflict", "已稳定或已放行标本不能存在未结束方案")
		}
		if !hasCompleted {
			return conflict("specimen_plan_state_conflict", "已稳定或已放行标本必须至少有一个已完成方案")
		}
	}
	return nil
}

// validateSpecimenRelease mirrors the release workflow gating plus the
// terminal-state pairing of release record and available specimen.
func validateSpecimenRelease(
	specimen domain.Specimen,
	hasRelease bool,
	openPlanIDs []string,
) error {
	if hasRelease && specimen.Status != domain.SpecimenAvailable {
		return conflict("specimen_release_state_conflict", "存在放行记录的标本必须处于 available 终态")
	}
	if specimen.Status == domain.SpecimenAvailable && !hasRelease {
		return conflict("specimen_release_state_conflict", "available 标本必须携带一条放行记录")
	}
	if hasRelease && len(openPlanIDs) > 0 {
		return conflict("open_plan_blocks_release", "已放行标本不能仍存在未结束方案")
	}
	return nil
}

func sortedPlanIDs(values map[string]domain.PrepPlan) []string {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Fingerprint returns a stable hash of all business facts, used to report
// whether re-importing the same package changes the running workspace.
func (b Bundle) Fingerprint() string {
	hasher := sha256.New()
	writeLength := func(values ...int) {
		for _, value := range values {
			hasher.Write([]byte(itoa(value)))
			hasher.Write([]byte{0})
		}
	}
	writeLength(len(b.Specimens), len(b.Plans), len(b.Releases))
	for _, value := range b.Specimens {
		hasher.Write([]byte(value.ID))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.CatalogNumber))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.Status))
		hasher.Write([]byte{0})
		hasher.Write([]byte(itoa(value.Version)))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.CreatedAt.Format(time.RFC3339Nano)))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.UpdatedAt.Format(time.RFC3339Nano)))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.FieldLabel))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.TaxonHint))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.MatrixKind))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.JacketNotes))
		hasher.Write([]byte{'|'})
	}
	planIDs := make([]string, 0, len(b.Plans))
	for _, plan := range b.Plans {
		planIDs = append(planIDs, plan.ID)
		approvedAt := ""
		if plan.ApprovedAt != nil {
			approvedAt = plan.ApprovedAt.Format(time.RFC3339Nano)
		}
		hasher.Write([]byte(plan.ID))
		hasher.Write([]byte{0})
		hasher.Write([]byte(plan.SpecimenID))
		hasher.Write([]byte{0})
		hasher.Write([]byte(plan.Status))
		hasher.Write([]byte{0})
		hasher.Write([]byte(itoa(plan.TargetExposure)))
		hasher.Write([]byte{0})
		hasher.Write([]byte(plan.ApprovedBy))
		hasher.Write([]byte{0})
		hasher.Write([]byte(approvedAt))
		hasher.Write([]byte{0})
		hasher.Write([]byte(itoa(plan.Version)))
		hasher.Write([]byte{0})
		hasher.Write([]byte(plan.Objective))
		hasher.Write([]byte{0})
		hasher.Write([]byte(plan.Method))
		hasher.Write([]byte{0})
		hasher.Write([]byte(plan.Adhesive))
		hasher.Write([]byte{'|'})
	}
	sort.Strings(planIDs)
	writeLength(len(b.Progress))
	for _, planID := range planIDs {
		group := append([]domain.ProgressEntry(nil), b.Progress[planID]...)
		sort.Slice(group, func(i, j int) bool {
			if group[i].Sequence != group[j].Sequence {
				return group[i].Sequence < group[j].Sequence
			}
			return group[i].CreatedAt.Before(group[j].CreatedAt)
		})
		hasher.Write([]byte(planID))
		hasher.Write([]byte{0})
		writeLength(len(group))
		for _, entry := range group {
			hasher.Write([]byte(entry.ID))
			hasher.Write([]byte{0})
			hasher.Write([]byte(itoa(entry.Sequence)))
			hasher.Write([]byte{0})
			hasher.Write([]byte(itoa(entry.Percent)))
			hasher.Write([]byte{0})
			hasher.Write([]byte(entry.CreatedAt.Format(time.RFC3339Nano)))
			hasher.Write([]byte{0})
			hasher.Write([]byte(entry.Stage))
			hasher.Write([]byte{0})
			hasher.Write([]byte(entry.Note))
			hasher.Write([]byte{0})
			hasher.Write([]byte(entry.RecordedBy))
			hasher.Write([]byte{'|'})
		}
	}
	for _, value := range b.Releases {
		hasher.Write([]byte(value.ID))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.SpecimenID))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.CreatedAt.Format(time.RFC3339Nano)))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.Purpose))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.Recipient))
		hasher.Write([]byte{0})
		hasher.Write([]byte(value.ApprovedBy))
		hasher.Write([]byte{'|'})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func issueCode(err error) string {
	if appErr, ok := contracts.AsError(err); ok {
		return appErr.Code
	}
	return "workspace_validation_failed"
}

func parseTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func conflict(code, message string) error {
	return contracts.Conflict(code, message)
}

func label(id, fallback string) string {
	if id != "" {
		return id
	}
	if fallback != "" {
		return fallback
	}
	return "<未提供标识>"
}

func joinIDs(ids []string) string {
	result := ""
	for index, id := range ids {
		if index > 0 {
			result += "、"
		}
		result += id
	}
	return result
}

func orderedSpecimens(values map[string]domain.Specimen) []domain.Specimen {
	list := make([]domain.Specimen, 0, len(values))
	for _, value := range values {
		list = append(list, value)
	}
	sortSpecimens(list)
	return list
}

func sortSpecimens(values []domain.Specimen) {
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
}

func sortPlans(values []domain.PrepPlan) {
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
}

func sortReleases(values []domain.ReleaseRecord) {
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
