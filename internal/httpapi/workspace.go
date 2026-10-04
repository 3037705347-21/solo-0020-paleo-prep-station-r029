package httpapi

import (
	"errors"
	"io"
	"net/http"

	"example.com/paleo-prep-station/internal/contracts"
)

// maxWorkspaceBytes bounds import payloads generously above the normal
// request body limit while still protecting the in-memory service.
const maxWorkspaceBytes = 16 << 20

func (api *API) exportWorkspace(w http.ResponseWriter, r *http.Request) {
	pkg, err := api.service.ExportWorkspace(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=paleo-workspace.json")
	writeJSON(w, http.StatusOK, pkg)
}

func (api *API) importWorkspace(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkspaceBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, contracts.Invalid("workspace_too_large", "导入包超过允许的大小上限"))
			return
		}
		writeError(w, contracts.Invalid("workspace_unreadable", "无法读取导入包内容"))
		return
	}
	report, err := api.service.ImportWorkspace(r.Context(), raw)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if report.Replaced {
		status = http.StatusCreated
	}
	writeJSON(w, status, report)
}
