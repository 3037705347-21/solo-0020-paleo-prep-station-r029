package httpapi

import (
	"net/http"
	"time"
)

func (api *API) exportWorkspace(w http.ResponseWriter, r *http.Request) {
	pkg, err := api.service.ExportWorkspace(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	w.Header().Set("Content-Disposition", `attachment; filename="workspace-v1-`+stamp+`.json"`)
	writeJSON(w, http.StatusOK, pkg)
}

func (api *API) importWorkspace(w http.ResponseWriter, r *http.Request) {
	raw, ok := readPackageBody(w, r)
	if !ok {
		return
	}
	report, err := api.service.ImportWorkspace(r.Context(), raw)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}
