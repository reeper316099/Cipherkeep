// Package apperr is the error type that carries an HTTP status and a machine-readable code, like AppError in the
// TypeScript server.
package apperr

import (
	"errors"
	"fmt"
)

// Error is an error with an HTTP status and code.
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return e.Message }

// New builds an Error.
func New(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// Validation is a 400 VALIDATION error.
func Validation(format string, args ...any) *Error { return New(400, "VALIDATION", format, args...) }

// NotFound is a 404 NOT_FOUND error for what/id.
func NotFound(what, id string) *Error { return New(404, "NOT_FOUND", "%s not found: %s", what, id) }

// As reports whether err is an *Error.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
