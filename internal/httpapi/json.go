package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"example.com/paleo-prep-station/internal/contracts"
)

const maxRequestBytes = 1 << 20

// maxWorkspaceBytes limits an import package. Workspace snapshots are larger
// than single requests because they cover the whole business history.
const maxWorkspaceBytes = 16 << 20

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, contracts.Invalid("invalid_json", "请求体不是合法 JSON"))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, contracts.Invalid("invalid_json", "请求体只能包含一个 JSON 对象"))
		return false
	}
	return true
}

// readPackageBody reads an import package as raw JSON so the workspace parser
// owns all structural validation and error codes.
func readPackageBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkspaceBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, contracts.Invalid("workspace_package_too_large", "工作区包大小超出限制"))
			return nil, false
		}
		writeError(w, contracts.Invalid("invalid_json", "无法读取请求体"))
		return nil, false
	}
	return raw, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
