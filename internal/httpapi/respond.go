package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/kevinnguyen/diaper-raffle/internal/store"
)

// maxBodyBytes caps request bodies. A roster is text a person typed, so this
// is generous by two orders of magnitude and still bounds memory.
const maxBodyBytes = 1 << 20 // 1 MiB

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON sends v with the given status.
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		s.log.ErrorContext(r.Context(), "encode response", "error", err, "path", r.URL.Path)
		http.Error(w, `{"error":{"code":"internal","message":"Something went wrong."}}`,
			http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body)
}

// writeError turns a domain error into a status code and a message that is
// safe to show a guest. Unrecognised errors are logged in full and reported
// generically.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "internal"
	message := "Something went wrong. Try again."

	switch {
	case errors.Is(err, store.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
		message = cleanMessage(err, "That does not exist.")
	case errors.Is(err, store.ErrInvalid):
		status, code = http.StatusBadRequest, "invalid"
		message = cleanMessage(err, "That request does not make sense.")
	case errors.Is(err, store.ErrConflict):
		status, code = http.StatusConflict, "conflict"
		message = cleanMessage(err, "That conflicts with something already saved.")
	default:
		s.log.ErrorContext(r.Context(), "request failed",
			"error", err, "method", r.Method, "path", r.URL.Path)
	}

	s.writeJSON(w, r, status, errorBody{errorDetail{Code: code, Message: message}})
}

// cleanMessage strips the sentinel prefix off a wrapped domain error, leaving
// the human-readable half.
func cleanMessage(err error, fallback string) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i >= 0 && i+2 < len(msg) {
		msg = msg[i+2:]
	}
	if msg == "" {
		return fallback
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}

// badRequest reports a client mistake without going through the store's error
// vocabulary.
func (s *Server) badRequest(w http.ResponseWriter, r *http.Request, message string) {
	s.writeJSON(w, r, http.StatusBadRequest, errorBody{errorDetail{Code: "invalid", Message: message}})
}

// decodeJSON reads a JSON request body, rejecting unknown fields so a typo in
// a field name fails loudly instead of being silently ignored.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return fmt.Errorf("that is too much text to send at once")
		}
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) {
			return fmt.Errorf("the request body is not valid JSON")
		}
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return fmt.Errorf("field %q has the wrong type", typeErr.Field)
		}
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the request body is empty")
		}
		return fmt.Errorf("the request body could not be read")
	}

	// A second value in the body means the caller sent something we did not
	// understand; better to say so than to act on half of it.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("the request body must contain a single JSON object")
	}
	return nil
}

// pathInt reads an integer path segment.
func pathInt(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", raw)
	}
	return n, nil
}
