package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/station"
)

// rawBody sends pre-encoded JSON without a second marshal round.
type rawBody struct{ data []byte }

func (r rawBody) marshal() ([]byte, error) { return r.data, nil }

func newTestServer() *httptest.Server {
	service := station.New(repository.NewMemory())
	return httptest.NewServer(NewRouter(service))
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		var raw []byte
		switch typed := body.(type) {
		case rawBody:
			raw = typed.data
		default:
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			raw = encoded
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform request %s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, payload
}

func seedSpecimenAndPlan(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	status, body := doJSON(t, client, http.MethodPost, baseURL+"/specimens", contracts.RegisterSpecimenRequest{
		CatalogNumber: "PPS-2026-100",
		FieldLabel:    "北坡-09",
		MatrixKind:    "泥岩",
	})
	if status != http.StatusCreated {
		t.Fatalf("register specimen: %d %s", status, body)
	}
	var specimen struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &specimen); err != nil {
		t.Fatalf("decode specimen: %v", err)
	}
	status, body = doJSON(t, client, http.MethodPost, baseURL+"/specimens/"+specimen.ID+"/plans", contracts.CreatePlanRequest{
		Objective: "清理围岩", Method: "气动笔剥离", TargetExposure: 60,
	})
	if status != http.StatusCreated {
		t.Fatalf("create plan: %d %s", status, body)
	}
	var plan struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(body, &plan)
	status, _ = doJSON(t, client, http.MethodPost, baseURL+"/plans/"+plan.ID+"/approval", contracts.ApprovalRequest{
		ApprovedBy: "curator-lin",
	})
	if status != http.StatusOK {
		t.Fatalf("approve plan: %d", status)
	}
	return specimen.ID
}

func TestExportEmptyWorkspace(t *testing.T) {
	server := newTestServer()
	defer server.Close()

	response, err := http.Get(server.URL + "/workspace/export")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var pkg struct {
		FormatVersion int    `json:"format_version"`
		GeneratedAt   string `json:"generated_at"`
		Specimens     []any  `json:"specimens"`
		Plans         []any  `json:"plans"`
		Progress      []any  `json:"progress"`
		Releases      []any  `json:"releases"`
	}
	if err := json.NewDecoder(response.Body).Decode(&pkg); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if pkg.FormatVersion != 1 || pkg.GeneratedAt == "" {
		t.Fatalf("unexpected envelope: %+v", pkg)
	}
	if pkg.Specimens == nil || pkg.Plans == nil || pkg.Progress == nil || pkg.Releases == nil {
		t.Fatal("exported collections must be arrays, not null")
	}
}

func TestExportImportRoundTripOverHTTP(t *testing.T) {
	source := newTestServer()
	defer source.Close()
	client := source.Client()

	specimenID := seedSpecimenAndPlan(t, client, source.URL)
	status, exported := doJSON(t, client, http.MethodGet, source.URL+"/workspace/export", nil)
	if status != http.StatusOK {
		t.Fatalf("export status: %d", status)
	}

	target := newTestServer()
	defer target.Close()
	status, reportRaw := doJSON(t, target.Client(), http.MethodPost, target.URL+"/workspace/import", rawBody{exported})
	if status != http.StatusCreated {
		t.Fatalf("first import expected 201, got %d: %s", status, reportRaw)
	}
	var report struct {
		Replaced  bool `json:"replaced"`
		Unchanged bool `json:"unchanged"`
		Specimens int  `json:"specimens"`
	}
	if err := json.Unmarshal(reportRaw, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if !report.Replaced || report.Unchanged || report.Specimens != 1 {
		t.Fatalf("unexpected first import report: %+v", report)
	}

	status, fetched := doJSON(t, target.Client(), http.MethodGet, target.URL+"/specimens/"+specimenID, nil)
	if status != http.StatusOK {
		t.Fatalf("restored specimen lookup: %d", status)
	}
	var specimen struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(fetched, &specimen)
	if specimen.Status != "preparing" {
		t.Fatalf("expected preparing after restore, got %s", specimen.Status)
	}

	// Repeating the same import is a 200 no-op.
	status, repeatRaw := doJSON(t, target.Client(), http.MethodPost, target.URL+"/workspace/import", rawBody{exported})
	if status != http.StatusOK {
		t.Fatalf("repeat import expected 200, got %d: %s", status, repeatRaw)
	}
	if err := json.Unmarshal(repeatRaw, &report); err != nil {
		t.Fatalf("decode repeat report: %v", err)
	}
	if report.Replaced || !report.Unchanged {
		t.Fatalf("repeat import should be unchanged: %+v", report)
	}
}

func TestImportRejectsCorruptAndOldPackagesWithoutStateChange(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	client := server.Client()
	specimenID := seedSpecimenAndPlan(t, client, server.URL)
	_, exported := doJSON(t, client, http.MethodGet, server.URL+"/workspace/export", nil)

	cases := []struct {
		name    string
		payload []byte
	}{
		{"garbage", []byte("<<<not json>>>")},
		{"truncated", exported[:len(exported)/2]},
		{"old version", bytes.Replace(exported, []byte(`"format_version":1`), []byte(`"format_version":0`), 1)},
		{"future version", bytes.Replace(exported, []byte(`"format_version":1`), []byte(`"format_version":99`), 1)},
		{"unknown field", bytes.TrimSuffix(exported, []byte("}"))},
	}
	cases[4].payload = append(cases[4].payload, []byte(`,"surprise":1}`)...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := doJSON(t, client, http.MethodPost, server.URL+"/workspace/import", rawBody{tc.payload})
			if status != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", status, body)
			}
			var errBody struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(body, &errBody); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if errBody.Code == "" {
				t.Fatal("expected stable error code")
			}
		})
	}

	status, body := doJSON(t, client, http.MethodGet, server.URL+"/specimens/"+specimenID, nil)
	if status != http.StatusOK {
		t.Fatalf("original workspace must remain intact, got %d: %s", status, body)
	}
}

func TestImportRejectsBusinessViolations(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	client := server.Client()
	specimenID := seedSpecimenAndPlan(t, client, server.URL)
	_, exported := doJSON(t, client, http.MethodGet, server.URL+"/workspace/export", nil)

	// Corrupt the specimen status so its plan pairing becomes impossible.
	tampered := bytes.Replace(exported, []byte(`"status":"preparing"`), []byte(`"status":"available"`), 1)
	status, body := doJSON(t, client, http.MethodPost, server.URL+"/workspace/import", rawBody{tampered})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 for inconsistent state, got %d: %s", status, body)
	}
	var errBody struct {
		Code   string `json:"code"`
		Issues []struct {
			Code string `json:"code"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if errBody.Code != "workspace_validation_failed" || len(errBody.Issues) == 0 {
		t.Fatalf("expected validation issues, got %s", body)
	}

	status, _ = doJSON(t, client, http.MethodGet, server.URL+"/specimens/"+specimenID, nil)
	if status != http.StatusOK {
		t.Fatalf("specimen should remain readable, got %d", status)
	}
}

func TestImportEmptyPackageClearsWorkspace(t *testing.T) {
	server := newTestServer()
	defer server.Close()
	client := server.Client()
	specimenID := seedSpecimenAndPlan(t, client, server.URL)

	emptyPackage := []byte(`{"format_version":1,"kind":"paleo-prep-station-workspace","generated_at":"2026-09-01T08:00:00Z","specimens":[],"plans":[],"progress":[],"releases":[]}`)
	status, body := doJSON(t, client, http.MethodPost, server.URL+"/workspace/import", rawBody{emptyPackage})
	if status != http.StatusCreated {
		t.Fatalf("expected 201 on clearing import, got %d: %s", status, body)
	}
	status, _ = doJSON(t, client, http.MethodGet, server.URL+"/specimens/"+specimenID, nil)
	if status != http.StatusNotFound {
		t.Fatalf("expected specimen gone after clear, got %d", status)
	}
	status, listBody := doJSON(t, client, http.MethodGet, server.URL+"/specimens", nil)
	if status != http.StatusOK {
		t.Fatalf("list after clear: %d", status)
	}
	var list struct {
		Items []any `json:"items"`
	}
	_ = json.Unmarshal(listBody, &list)
	if len(list.Items) != 0 {
		t.Fatalf("expected zero items, got %d", len(list.Items))
	}
}
