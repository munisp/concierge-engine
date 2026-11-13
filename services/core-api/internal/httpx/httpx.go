// Package httpx: JSON helpers and the platform error envelope.
package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
)

type ErrorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

var (
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
	ErrValidation = errors.New("validation")
	ErrForbidden  = errors.New("forbidden")
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, err error) {
	var env ErrorEnvelope
	status := http.StatusInternalServerError
	code := "internal"
	switch {
	case errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, ErrValidation):
		status, code = http.StatusUnprocessableEntity, "validation"
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	}
	env.Error.Code = code
	env.Error.Message = err.Error()
	WriteJSON(w, status, env)
}

func Decode(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
