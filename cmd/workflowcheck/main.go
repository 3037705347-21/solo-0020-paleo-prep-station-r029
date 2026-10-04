package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/httpapi"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/station"
)

type specimenView struct {
	ID            string `json:"id"`
	CatalogNumber string `json:"catalog_number"`
	Status        string `json:"status"`
}

type planView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
	Status     string `json:"status"`
}

type planSnapshot struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent int `json:"percent"`
	} `json:"progress"`
}

type releaseView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
}

type workspacePackage struct {
	Format     string            `json:"format"`
	Version    int               `json:"version"`
	ExportedAt string            `json:"exported_at"`
	Specimens  []json.RawMessage `json:"specimens"`
	Plans      []json.RawMessage `json:"plans"`
	Progress   []json.RawMessage `json:"progress"`
	Releases   []json.RawMessage `json:"releases"`
}

type importReport struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Counts  struct {
		Specimens int `json:"specimens"`
		Plans     int `json:"plans"`
		Progress  int `json:"progress"`
		Releases  int `json:"releases"`
	} `json:"counts"`
	Previous struct {
		Specimens int `json:"specimens"`
	} `json:"previous_counts"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to check")
	flag.Parse()

	if err := run(*workflow); err != nil {
		fmt.Fprintln(os.Stderr, "workflow check failed:", err)
		os.Exit(1)
	}
	fmt.Printf("workflow check passed: %s\n", *workflow)
}

func run(workflow string) error {
	handler := httpapi.NewRouter(station.New(repository.NewMemory()))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	switch workflow {
	case "register-specimen":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		if specimen.Status != "registered" {
			return fmt.Errorf("expected registered status, got %s", specimen.Status)
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.ID != specimen.ID {
			return errors.New("registered specimen cannot be read back")
		}
		return nil

	case "approve-prep-plan":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		plan, err := createPlan(client, server.URL, specimen.ID)
		if err != nil {
			return err
		}
		var approved planView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/plans/"+plan.ID+"/approval",
			contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
			http.StatusOK,
			&approved,
		); err != nil {
			return err
		}
		if approved.Status != "approved" {
			return fmt.Errorf("expected approved plan, got %s", approved.Status)
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "preparing" {
			return fmt.Errorf("expected preparing specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "log-preparation":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 35, "矩阵剥离"); err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var snapshot planSnapshot
		if err := requestJSON(client, http.MethodGet, server.URL+"/plans/"+plan.ID, nil, http.StatusOK, &snapshot); err != nil {
			return err
		}
		if snapshot.Plan.Status != "completed" || len(snapshot.Progress) != 2 {
			return errors.New("plan did not complete with exactly two progress entries")
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "stabilized" {
			return fmt.Errorf("expected stabilized specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "release-for-study":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var release releaseView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/specimens/"+specimen.ID+"/release",
			contracts.ReleaseRequest{
				Purpose:    "比较解剖研究",
				Recipient:  "地层研究组",
				ApprovedBy: "keeper-zhou",
			},
			http.StatusCreated,
			&release,
		); err != nil {
			return err
		}
		if release.SpecimenID != specimen.ID {
			return errors.New("release record references the wrong specimen")
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.Status != "available" {
			return fmt.Errorf("expected available specimen, got %s", fetched.Status)
		}
		return nil

	case "export-import-workspace":
		return checkExportImport(client, server.URL)

	case "import-guard":
		return checkImportGuard(client, server.URL)
	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

func checkExportImport(client *http.Client, baseURL string) error {
	specimen, plan, err := completedPlanSetup(client, baseURL)
	if err != nil {
		return err
	}
	if err := recordProgress(client, baseURL, plan.ID, 100, "表面加固"); err != nil {
		return err
	}
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:    "比较解剖研究",
			Recipient:  "地层研究组",
			ApprovedBy: "keeper-zhou",
		},
		http.StatusCreated,
		nil,
	); err != nil {
		return err
	}

	first, err := exportWorkspace(client, baseURL)
	if err != nil {
		return err
	}
	if first.Format != "paleo-prep-station-workspace" || first.Version != 1 {
		return fmt.Errorf("export package metadata wrong: %s v%d", first.Format, first.Version)
	}
	if first.ExportedAt == "" {
		return errors.New("export package is missing generation time")
	}
	if len(first.Specimens) != 1 || len(first.Plans) != 1 ||
		len(first.Progress) != 1 || len(first.Releases) != 1 {
		return fmt.Errorf("export package does not cover all business facts: %+v", first)
	}

	repeatReport, err := importWorkspace(client, baseURL, mustMarshal(first), http.StatusOK)
	if err != nil {
		return err
	}
	if repeatReport.Status != "unchanged" {
		return fmt.Errorf("re-importing the same package should be unchanged, got %s", repeatReport.Status)
	}

	// Drift the live workspace: a rejected duplicate keeps facts identical,
	// while a new specimen makes the workspace genuinely different.
	if err := registerDuplicateSpecimenExpectConflict(client, baseURL, specimen.CatalogNumber); err != nil {
		return err
	}
	extraSpecimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return err
	}

	// Restore the earlier point in time: the extra specimen must disappear.
	report, err := importWorkspace(client, baseURL, mustMarshal(first), http.StatusOK)
	if err != nil {
		return err
	}
	if report.Status != "replaced" {
		return fmt.Errorf("restoring an older point in time should report replaced, got %s", report.Status)
	}
	if report.Counts.Specimens != 1 || report.Previous.Specimens != 2 {
		return fmt.Errorf("restore counts wrong: %+v", report)
	}
	var removed specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+extraSpecimen.ID, nil, http.StatusNotFound, &removed); err != nil {
		return fmt.Errorf("specimen removed by restore should be gone: %w", err)
	}

	// After restoration the existing queries and workflows use the same rules:
	// the completed plan reads back consistently and no duplicate release is
	// possible on the available specimen.
	var fetched specimenView
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
		return err
	}
	if fetched.Status != "available" {
		return fmt.Errorf("restored specimen should be available, got %s", fetched.Status)
	}
	var snapshot planSnapshot
	if err := requestJSON(client, http.MethodGet, baseURL+"/plans/"+plan.ID, nil, http.StatusOK, &snapshot); err != nil {
		return err
	}
	if snapshot.Plan.Status != "completed" || len(snapshot.Progress) != 1 {
		return errors.New("restored plan snapshot is inconsistent")
	}
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{Purpose: "再次借阅", Recipient: "外单位", ApprovedBy: "keeper-zou"},
		http.StatusConflict,
		nil,
	); err != nil {
		return fmt.Errorf("second release after restore must still conflict: %w", err)
	}
	return nil
}

func checkImportGuard(client *http.Client, baseURL string) error {
	// Populate the live workspace first: failed imports must not touch it.
	if _, err := registerSpecimen(client, baseURL); err != nil {
		return err
	}

	emptyReport, status, body, err := importRaw(client, baseURL, nil)
	if err != nil {
		return err
	}
	if status != http.StatusBadRequest || emptyReport.Code != "empty_workspace_package" {
		return fmt.Errorf("empty body should be an explainable 400, got %d %s: %s", status, emptyReport.Code, body)
	}

	// A fully completed package gives progress and release sections to corrupt.
	completed, completedPlan, err := completedPlanSetup(client, baseURL)
	if err != nil {
		return err
	}
	if err := recordProgress(client, baseURL, completedPlan.ID, 35, "矩阵剥离"); err != nil {
		return err
	}
	if err := recordProgress(client, baseURL, completedPlan.ID, 100, "表面加固"); err != nil {
		return err
	}
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+completed.ID+"/release",
		contracts.ReleaseRequest{Purpose: "比较解剖研究", Recipient: "地层研究组", ApprovedBy: "keeper-zhou"},
		http.StatusCreated,
		nil,
	); err != nil {
		return err
	}
	// A planned specimen carries one legitimate draft plan, so adding another
	// open plan isolates the open-plan uniqueness rule.
	plannedSpecimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return err
	}
	if _, err := createPlan(client, baseURL, plannedSpecimen.ID); err != nil {
		return err
	}
	base, err := exportWorkspace(client, baseURL)
	if err != nil {
		return err
	}

	cases := []struct {
		name     string
		mutate   func(*workspacePackage)
		wantCode string
	}{
		{
			name:     "old format version",
			mutate:   func(p *workspacePackage) { p.Version = 99 },
			wantCode: "unsupported_workspace_version",
		},
		{
			name:     "unknown format name",
			mutate:   func(p *workspacePackage) { p.Format = "other-system" },
			wantCode: "unsupported_workspace_format",
		},
		{
			name: "dangling plan reference",
			mutate: func(p *workspacePackage) {
				var plan map[string]any
				if err := json.Unmarshal(p.Plans[0], &plan); err != nil {
					panic(err)
				}
				plan["specimen_id"] = "specimen-missing"
				p.Plans[0] = mustMarshal(plan)
			},
			wantCode: "dangling_plan_reference",
		},
		{
			name: "two open plans on one specimen",
			mutate: func(p *workspacePackage) {
				var specimen domainSpecimenView
				raw := findRaw(p.Specimens, "id", plannedSpecimen.ID)
				if err := json.Unmarshal(raw, &specimen); err != nil {
					panic(err)
				}
				extra := map[string]any{
					"id":              "plan-extra",
					"specimen_id":     plannedSpecimen.ID,
					"objective":       "第二个目标",
					"method":          "另一种方法",
					"adhesive":        "",
					"target_exposure": 60,
					"status":          "draft",
					"version":         1,
					"created_at":      specimen.UpdatedAt,
					"updated_at":      specimen.UpdatedAt,
				}
				p.Plans = append(p.Plans, mustMarshal(extra))
			},
			wantCode: "multiple_open_plans",
		},
		{
			name: "progress sequence gap",
			mutate: func(p *workspacePackage) {
				for index := range p.Progress {
					var entry map[string]any
					if err := json.Unmarshal(p.Progress[index], &entry); err != nil {
						panic(err)
					}
					if entry["plan_id"] == completedPlan.ID {
						entry["sequence"] = 9
						p.Progress[index] = mustMarshal(entry)
					}
				}
			},
			wantCode: "progress_sequence_gap",
		},
		{
			name: "regressed progress",
			mutate: func(p *workspacePackage) {
				extra := map[string]any{
					"id":          "progress-regressed",
					"plan_id":     completedPlan.ID,
					"sequence":    3,
					"stage":       "回退",
					"percent":     10,
					"note":        "",
					"recorded_by": "preparator-chen",
					"created_at":  "2026-01-04T00:00:00Z",
				}
				p.Progress = append(p.Progress, mustMarshal(extra))
			},
			wantCode: "progress_not_monotonic",
		},
		{
			name: "duplicate release for one specimen",
			mutate: func(p *workspacePackage) {
				extra := map[string]any{
					"id":          "release-extra",
					"specimen_id": completed.ID,
					"purpose":     "第二次借阅",
					"recipient":   "外单位",
					"approved_by": "keeper-zou",
					"created_at":  "2026-02-02T00:00:00Z",
				}
				p.Releases = append(p.Releases, mustMarshal(extra))
			},
			wantCode: "duplicate_release_record",
		},
		{
			name: "completed plan without final 100 percent",
			mutate: func(p *workspacePackage) {
				for index := range p.Progress {
					var entry map[string]any
					if err := json.Unmarshal(p.Progress[index], &entry); err != nil {
						panic(err)
					}
					if entry["plan_id"] == completedPlan.ID && entry["percent"] == float64(100) {
						entry["percent"] = 80
						p.Progress[index] = mustMarshal(entry)
					}
				}
			},
			wantCode: "progress_terminal_mismatch",
		},
	}

	for _, tc := range cases {
		pkgCopy := clonePackage(base)
		tc.mutate(&pkgCopy)
		report, gotStatus, rawBody, err := importRaw(client, baseURL, mustMarshal(pkgCopy))
		if err != nil {
			return fmt.Errorf("%s: %w", tc.name, err)
		}
		if gotStatus != http.StatusBadRequest || report.Code != tc.wantCode {
			return fmt.Errorf("%s: expected 400/%s, got %d/%s body=%s", tc.name, tc.wantCode, gotStatus, report.Code, rawBody)
		}
		// Every rejected import must leave the current workspace untouched.
		current, err := exportWorkspace(client, baseURL)
		if err != nil {
			return fmt.Errorf("%s: %w", tc.name, err)
		}
		if string(mustMarshal(factsOf(current))) != string(mustMarshal(factsOf(base))) {
			return fmt.Errorf("%s: failed import changed the current workspace", tc.name)
		}
	}

	// Corrupted content (invalid JSON) is rejected without a partial apply.
	report, status, _, err := importRaw(client, baseURL, []byte(`{"format":`))
	if err != nil {
		return err
	}
	if status != http.StatusBadRequest || report.Code != "malformed_workspace_package" {
		return fmt.Errorf("corrupted package should be 400/malformed, got %d/%s", status, report.Code)
	}

	// An empty workspace package is a valid point-in-time and must replace all.
	empty := workspacePackage{
		Format:     "paleo-prep-station-workspace",
		Version:    1,
		ExportedAt: "2026-02-01T00:00:00Z",
		Specimens:  []json.RawMessage{},
		Plans:      []json.RawMessage{},
		Progress:   []json.RawMessage{},
		Releases:   []json.RawMessage{},
	}
	replaced, err := importWorkspace(client, baseURL, mustMarshal(empty), http.StatusOK)
	if err != nil {
		return err
	}
	if replaced.Status != "replaced" || replaced.Counts.Specimens != 0 || replaced.Previous.Specimens != 3 {
		return fmt.Errorf("empty workspace should report replaced with zero counts, got %+v", replaced)
	}
	var list struct {
		Items []specimenView `json:"items"`
	}
	if err := requestJSON(client, http.MethodGet, baseURL+"/specimens", nil, http.StatusOK, &list); err != nil {
		return err
	}
	if len(list.Items) != 0 {
		return errors.New("workspace should be empty after importing an empty package")
	}

	// Importing the empty package again is idempotent, and the old catalog
	// number becomes usable again because the workspace was replaced wholesale.
	again, err := importWorkspace(client, baseURL, mustMarshal(empty), http.StatusOK)
	if err != nil {
		return err
	}
	if again.Status != "unchanged" {
		return fmt.Errorf("re-importing empty workspace should be unchanged, got %s", again.Status)
	}
	if _, err := registerSpecimen(client, baseURL); err != nil {
		return fmt.Errorf("catalog number should be reusable on the empty workspace: %w", err)
	}
	return nil
}

type domainSpecimenView struct {
	UpdatedAt string `json:"updated_at"`
}

func findRaw(items []json.RawMessage, key, want string) json.RawMessage {
	for _, item := range items {
		var probe map[string]any
		if err := json.Unmarshal(item, &probe); err != nil {
			panic(err)
		}
		if value, _ := probe[key].(string); value == want {
			return item
		}
	}
	panic("entity with " + key + "=" + want + " not found")
}

type workspaceFacts struct {
	Specimens []json.RawMessage `json:"specimens"`
	Plans     []json.RawMessage `json:"plans"`
	Progress  []json.RawMessage `json:"progress"`
	Releases  []json.RawMessage `json:"releases"`
}

func factsOf(pkg workspacePackage) workspaceFacts {
	return workspaceFacts{
		Specimens: pkg.Specimens,
		Plans:     pkg.Plans,
		Progress:  pkg.Progress,
		Releases:  pkg.Releases,
	}
}

func clonePackage(pkg workspacePackage) workspacePackage {
	return workspacePackage{
		Format:     pkg.Format,
		Version:    pkg.Version,
		ExportedAt: pkg.ExportedAt,
		Specimens:  append([]json.RawMessage(nil), pkg.Specimens...),
		Plans:      append([]json.RawMessage(nil), pkg.Plans...),
		Progress:   append([]json.RawMessage(nil), pkg.Progress...),
		Releases:   append([]json.RawMessage(nil), pkg.Releases...),
	}
}

func mustMarshal(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func exportWorkspace(client *http.Client, baseURL string) (workspacePackage, error) {
	var pkg workspacePackage
	if err := requestJSON(client, http.MethodGet, baseURL+"/workspace/export", nil, http.StatusOK, &pkg); err != nil {
		return workspacePackage{}, err
	}
	return pkg, nil
}

func importWorkspace(client *http.Client, baseURL string, body []byte, wantStatus int) (importReport, error) {
	var report importReport
	if err := requestRawJSON(client, http.MethodPost, baseURL+"/workspace/import", body, wantStatus, &report); err != nil {
		return importReport{}, err
	}
	return report, nil
}

func importRaw(client *http.Client, baseURL string, body []byte) (errorBody, int, string, error) {
	request, err := http.NewRequest(http.MethodPost, baseURL+"/workspace/import", bytes.NewReader(body))
	if err != nil {
		return errorBody{}, 0, "", err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return errorBody{}, 0, "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return errorBody{}, 0, "", err
	}
	var bodyView errorBody
	_ = json.Unmarshal(raw, &bodyView)
	return bodyView, response.StatusCode, string(raw), nil
}

func registerDuplicateSpecimenExpectConflict(client *http.Client, baseURL, catalogNumber string) error {
	return requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: catalogNumber,
			FieldLabel:    "冲突标签",
			MatrixKind:    "粉砂岩",
		},
		http.StatusConflict,
		nil,
	)
}

func registerSpecimenWith(client *http.Client, baseURL string, request contracts.RegisterSpecimenRequest) (specimenView, error) {
	var specimen specimenView
	err := requestRawJSON(client, http.MethodPost, baseURL+"/specimens", mustMarshal(request), http.StatusCreated, &specimen)
	return specimen, err
}

func registerSpecimen(client *http.Client, baseURL string) (specimenView, error) {
	catalogSequence++
	return registerSpecimenWith(client, baseURL, contracts.RegisterSpecimenRequest{
		CatalogNumber: fmt.Sprintf("PPS-2026-%03d", catalogSequence),
		FieldLabel:    fmt.Sprintf("北坡-%02d", catalogSequence),
		TaxonHint:     "大型脊椎动物",
		MatrixKind:    "粉砂岩",
		JacketNotes:   "夹克边角完好",
	})
}

var catalogSequence int

func createPlan(client *http.Client, baseURL, specimenID string) (planView, error) {
	var plan planView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "暴露主要关节面",
			Method:         "手工显微剥离",
			Adhesive:       "可逆树脂",
			TargetExposure: 80,
		},
		http.StatusCreated,
		&plan,
	)
	return plan, err
}

func completedPlanSetup(client *http.Client, baseURL string) (specimenView, planView, error) {
	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	); err != nil {
		return specimenView{}, planView{}, err
	}
	return specimen, approved, nil
}

func recordProgress(client *http.Client, baseURL, planID string, percent int, stage string) error {
	var ignored struct{}
	return requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+planID+"/progress",
		contracts.ProgressRequest{
			Stage:      stage,
			Percent:    percent,
			Note:       "本地链路检查",
			RecordedBy: "preparator-chen",
		},
		http.StatusCreated,
		&ignored,
	)
}

func requestJSON(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	target any,
) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("request %s %s returned %d: %s", method, url, response.StatusCode, string(raw))
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, url, err)
	}
	return nil
}

func requestRawJSON(
	client *http.Client,
	method string,
	url string,
	payload []byte,
	wantStatus int,
	target any,
) error {
	request, err := http.NewRequest(method, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("request %s %s returned %d: %s", method, url, response.StatusCode, string(raw))
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, url, err)
	}
	return nil
}
