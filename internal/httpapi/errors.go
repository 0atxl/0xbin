package httpapi

import (
	"encoding/json"
	"net/http"
)

// Error is the stable public API error envelope.
type Error struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes one public API error without exposing internals.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func writeError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Error{Error: ErrorDetail{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
