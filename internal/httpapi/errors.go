package httpapi

import (
	"context"
	"errors"
	"net/http"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/workspace"
)

type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Issues  []workspace.Issue `json:"issues,omitempty"`
}

func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		writeJSON(w, http.StatusRequestTimeout, errorBody{
			Code:    "request_canceled",
			Message: "请求已取消",
		})
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, errorBody{
			Code:    "request_timeout",
			Message: "请求处理超时",
		})
		return
	}
	var workspaceErr *workspace.PackageError
	if errors.As(err, &workspaceErr) {
		writeJSON(w, http.StatusBadRequest, errorBody{
			Code:    workspaceErr.Code,
			Message: workspaceErr.Message,
			Issues:  workspaceErr.Issues,
		})
		return
	}
	if appErr, ok := contracts.AsError(err); ok {
		writeJSON(w, statusForKind(appErr.Kind), errorBody{
			Code:    appErr.Code,
			Message: appErr.Message,
		})
		return
	}
	writeJSON(w, http.StatusInternalServerError, errorBody{
		Code:    "internal_error",
		Message: "服务暂时无法处理该请求",
	})
}

func statusForKind(kind contracts.ErrorKind) int {
	switch kind {
	case contracts.KindInvalid:
		return http.StatusBadRequest
	case contracts.KindNotFound:
		return http.StatusNotFound
	case contracts.KindConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
