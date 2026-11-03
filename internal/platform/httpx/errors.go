// Package httpx contains HTTP helpers shared by all API modules: a consistent
// JSON envelope, typed errors, pagination parsing and middleware.
package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is an error that maps to a safe client-facing HTTP response.
type Error struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error { return e.cause }

// Wrap attaches an internal cause (never shown to the client).
func (e *Error) Wrap(err error) *Error {
	c := *e
	c.cause = err
	return &c
}

// NewError builds an Error.
func NewError(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

func BadRequest(msg string) *Error { return NewError(http.StatusBadRequest, "bad_request", msg) }
func Unauthorized(msg string) *Error {
	if msg == "" {
		msg = "Authentication required."
	}
	return NewError(http.StatusUnauthorized, "unauthorized", msg)
}
func Forbidden(msg string) *Error {
	if msg == "" {
		msg = "You do not have permission to perform this action."
	}
	return NewError(http.StatusForbidden, "forbidden", msg)
}
func NotFound(what string) *Error {
	return NewError(http.StatusNotFound, "not_found", what+" not found.")
}
func Conflict(msg string) *Error { return NewError(http.StatusConflict, "conflict", msg) }
func Unavailable(msg string) *Error {
	return NewError(http.StatusServiceUnavailable, "unavailable", msg)
}
func TooManyRequests() *Error {
	return NewError(http.StatusTooManyRequests, "rate_limited", "Too many requests. Please try again later.")
}
func Internal(err error) *Error {
	return NewError(http.StatusInternalServerError, "internal_error", "An unexpected error occurred.").Wrap(err)
}

// Validation returns a 422 error with field-level details.
func Validation(details map[string]string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed",
		Message: "Some fields are invalid.", Details: details}
}

// AsError converts any error into an *Error, hiding internal details.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err)
}
