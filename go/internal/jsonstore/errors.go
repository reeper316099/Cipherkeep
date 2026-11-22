// Package jsonstore is a small, standalone storage engine for JSON files with a versioned envelope, atomic
// writes, hand-edit reload, quarantine and restore, rolling zip backups and optional AES-256-GCM encryption.
// It is the Go port of packages/jsonstore and reads and writes the same files.
package jsonstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Error codes, identical to the TypeScript version so callers and tests can match on them.
const (
	CodePathTraversal   = "PATH_TRAVERSAL"
	CodeInvalidJSON     = "INVALID_JSON"
	CodeInvalidEnvelope = "INVALID_ENVELOPE"
	CodeVersionTooNew   = "VERSION_TOO_NEW"
	CodeMissingMigrate  = "MISSING_MIGRATION"
	CodeLocked          = "LOCKED"
	CodeEncrypted       = "ENCRYPTED"
	CodeDecryptFailed   = "DECRYPT_FAILED"
)

// Error is the error type returned by this package.
type Error struct {
	Code string
	Msg  string
	File string
	Line int // 1-based, 0 when unknown
}

func (e *Error) Error() string {
	if e.File != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Msg, e.File)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

// IsCode reports whether err is (or wraps) an *Error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

func newErr(code, msg, file string) *Error { return &Error{Code: code, Msg: msg, File: file} }

var lineRe = regexp.MustCompile(`line (\d+)`)

// errorLine is a best-effort 1-based line number for a JSON syntax error in text.
func errorLine(text []byte, err error) int {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		off := int(se.Offset)
		if off > len(text) {
			off = len(text)
		}
		return strings.Count(string(text[:off]), "\n") + 1
	}
	if m := lineRe.FindStringSubmatch(err.Error()); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}
