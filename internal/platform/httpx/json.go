package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// MaxJSONBody is the default maximum JSON request body size.
const MaxJSONBody = 1 << 20

// HandlerFunc is an http handler that returns an error.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handle adapts a HandlerFunc to http.Handler, rendering errors uniformly.
func Handle(fn HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			WriteError(w, r, err)
		}
	})
}

// WriteError renders an error response without leaking internal details.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	e := AsError(err)
	if e.Status >= 500 {
		LoggerFrom(r).Error("request failed", "error", err.Error(), "status", e.Status)
	} else {
		LoggerFrom(r).Debug("request rejected", "error_code", e.Code, "status", e.Status)
	}
	type body struct {
		Code      string            `json:"code"`
		Message   string            `json:"message"`
		Details   map[string]string `json:"details,omitempty"`
		RequestID string            `json:"request_id,omitempty"`
	}
	WriteJSON(w, e.Status, map[string]any{"error": body{e.Code, e.Message, e.Details, RequestID(r.Context())}})
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// OK writes {"data": v}.
func OK(w http.ResponseWriter, v any) error {
	WriteJSON(w, http.StatusOK, map[string]any{"data": v})
	return nil
}

// Created writes {"data": v} with 201.
func Created(w http.ResponseWriter, v any) error {
	WriteJSON(w, http.StatusCreated, map[string]any{"data": v})
	return nil
}

// Accepted writes {"data": v} with 202 (async operation started).
func Accepted(w http.ResponseWriter, v any) error {
	WriteJSON(w, http.StatusAccepted, map[string]any{"data": v})
	return nil
}

// NoContent writes 204.
func NoContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ListMeta describes a paginated list.
type ListMeta struct {
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
	Total   int `json:"total"`
}

// List writes {"data": items, "meta": meta}.
func List(w http.ResponseWriter, items any, meta ListMeta) error {
	WriteJSON(w, http.StatusOK, map[string]any{"data": items, "meta": meta})
	return nil
}

// DecodeJSON strictly decodes a JSON body into dst.
func DecodeJSON(r *http.Request, dst any) error {
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		return NewError(http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json.")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, MaxJSONBody+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return BadRequest("Request body is required.")
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return NewError(http.StatusRequestEntityTooLarge, "payload_too_large", "Request body is too large.")
		}
		return BadRequest("Malformed JSON body: " + sanitizeJSONErr(err))
	}
	if dec.More() {
		return BadRequest("Request body must contain a single JSON object.")
	}
	return nil
}

func sanitizeJSONErr(err error) string {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	switch {
	case errors.As(err, &se):
		return "syntax error at offset " + strconv.FormatInt(se.Offset, 10)
	case errors.As(err, &te):
		return "invalid type for field " + strconv.Quote(te.Field)
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return strings.TrimPrefix(err.Error(), "json: ")
	}
	return "invalid JSON"
}

// Page holds parsed pagination, sorting and search parameters.
type Page struct {
	Page    int
	PerPage int
	Sort    string
	Desc    bool
	Search  string
}

// Offset returns the SQL offset.
func (p Page) Offset() int { return (p.Page - 1) * p.PerPage }

// Meta builds list metadata.
func (p Page) Meta(total int) ListMeta {
	return ListMeta{Page: p.Page, PerPage: p.PerPage, Total: total}
}

// ParsePage parses ?page, ?per_page, ?sort (prefix "-" for desc) and ?q.
// sort is validated against allowed; the default is used when absent.
func ParsePage(r *http.Request, allowed []string, def string) (Page, error) {
	q := r.URL.Query()
	p := Page{Page: 1, PerPage: 20}
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100000 {
			return p, Validation(map[string]string{"page": "must be a positive integer"})
		}
		p.Page = n
	}
	if v := q.Get("per_page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return p, Validation(map[string]string{"per_page": "must be between 1 and 100"})
		}
		p.PerPage = n
	}
	sort := q.Get("sort")
	if sort == "" {
		sort = def
	}
	if strings.HasPrefix(sort, "-") {
		p.Desc = true
		sort = sort[1:]
	}
	valid := false
	for _, a := range allowed {
		if a == sort {
			valid = true
			break
		}
	}
	if !valid {
		return p, Validation(map[string]string{"sort": "unsupported sort field"})
	}
	p.Sort = sort
	p.Search = strings.TrimSpace(q.Get("q"))
	if len(p.Search) > 100 {
		return p, Validation(map[string]string{"q": "search term is too long"})
	}
	return p, nil
}

// OrderBy returns a safe ORDER BY clause by mapping the validated sort key to
// a column expression.
func (p Page) OrderBy(columns map[string]string) string {
	col, ok := columns[p.Sort]
	if !ok {
		for _, c := range columns {
			col = c
			break
		}
	}
	dir := "ASC"
	if p.Desc {
		dir = "DESC"
	}
	return col + " " + dir
}
