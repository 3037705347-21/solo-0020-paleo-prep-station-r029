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

	case "backup-restore":
		return checkBackupRestore(server.URL, client)
	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

func checkBackupRestore(sourceURL string, client *http.Client) error {
	specimen, plan, err := completedPlanSetup(client, sourceURL)
	if err != nil {
		return err
	}
	if err := recordProgress(client, sourceURL, plan.ID, 45, "矩阵剥离"); err != nil {
		return err
	}

	backup, err := rawRequest(client, http.MethodGet, sourceURL+"/workspace/export", nil)
	if err != nil {
		return err
	}
	var envelope struct {
		FormatVersion int `json:"format_version"`
		Specimens     []struct {
			ID string `json:"id"`
		} `json:"specimens"`
		Plans []struct {
			ID string `json:"id"`
		} `json:"plans"`
		Progress []struct {
			PlanID  string `json:"plan_id"`
			Percent int    `json:"percent"`
		} `json:"progress"`
	}
	if err := json.Unmarshal(backup, &envelope); err != nil {
		return err
	}
	if envelope.FormatVersion != 1 || len(envelope.Specimens) != 1 || len(envelope.Plans) != 1 || len(envelope.Progress) != 1 {
		return fmt.Errorf("export package missing full business facts: %s", backup)
	}

	// Restore the backup into a brand-new, empty service instance.
	target := httptest.NewServer(httpapi.NewRouter(station.New(repository.NewMemory())))
	defer target.Close()
	report, err := rawRequest(client, http.MethodPost, target.URL+"/workspace/import", backup)
	if err != nil {
		return fmt.Errorf("restore into empty service: %w", err)
	}
	var outcome struct {
		Replaced  bool `json:"replaced"`
		Unchanged bool `json:"unchanged"`
		Specimens int  `json:"specimens"`
		Plans     int  `json:"plans"`
		Progress  int  `json:"progress"`
	}
	if err := json.Unmarshal(report, &outcome); err != nil {
		return err
	}
	if !outcome.Replaced || outcome.Unchanged || outcome.Specimens != 1 || outcome.Plans != 1 || outcome.Progress != 1 {
		return fmt.Errorf("restore should replace empty workspace, got %s", report)
	}

	var restoredPlan planSnapshot
	if err := requestJSON(client, http.MethodGet, target.URL+"/plans/"+plan.ID, nil, http.StatusOK, &restoredPlan); err != nil {
		return err
	}
	if restoredPlan.Plan.Status != "active" || len(restoredPlan.Progress) != 1 || restoredPlan.Progress[0].Percent != 45 {
		return fmt.Errorf("restored plan snapshot inconsistent: %+v", restoredPlan)
	}

	// Importing the same backup again is an idempotent no-op while the
	// target still matches the captured point in time.
	repeat, err := rawRequest(client, http.MethodPost, target.URL+"/workspace/import", backup)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(repeat, &outcome); err != nil {
		return err
	}
	if outcome.Replaced || !outcome.Unchanged {
		return fmt.Errorf("repeated import must be an unchanged no-op, got %s", repeat)
	}

	// The preparation workflow continues under the same rules after restore.
	if err := recordProgress(client, target.URL, plan.ID, 100, "表面加固"); err != nil {
		return fmt.Errorf("continue preparation after restore: %w", err)
	}
	var restoredSpecimen specimenView
	if err := requestJSON(client, http.MethodGet, target.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &restoredSpecimen); err != nil {
		return err
	}
	if restoredSpecimen.Status != "stabilized" {
		return fmt.Errorf("expected stabilized after restored completion, got %s", restoredSpecimen.Status)
	}

	// Corrupt content is rejected and never disturbs the restored data.
	if _, err := rawRequest(client, http.MethodPost, target.URL+"/workspace/import", []byte("{broken")); err == nil {
		return errors.New("corrupt import unexpectedly accepted")
	}
	if err := requestJSON(client, http.MethodGet, target.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &restoredSpecimen); err != nil {
		return fmt.Errorf("workspace changed after failed import: %w", err)
	}
	if restoredSpecimen.Status != "stabilized" {
		return fmt.Errorf("workspace status changed after failed import: %s", restoredSpecimen.Status)
	}
	return nil
}

func rawRequest(client *http.Client, method, url string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return raw, fmt.Errorf("request %s %s returned %d: %s", method, url, response.StatusCode, string(raw))
	}
	return raw, nil
}

func registerSpecimen(client *http.Client, baseURL string) (specimenView, error) {
	var specimen specimenView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: "PPS-2026-001",
			FieldLabel:    "北坡-07",
			TaxonHint:     "大型脊椎动物",
			MatrixKind:    "粉砂岩",
			JacketNotes:   "夹克边角完好",
		},
		http.StatusCreated,
		&specimen,
	)
	return specimen, err
}

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
