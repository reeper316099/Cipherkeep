package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
)

// bodyLimit is the largest JSON body accepted (Fastify's bodyLimit in the TypeScript server).
const bodyLimit = 20 << 20

type ctxKey int

const reqIDKey ctxKey = 0

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(reqIDKey).(string)
	return id
}

// marshal encodes like JSON.stringify: no HTML escaping, no trailing newline.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := marshal(v)
	if err != nil {
		log.Printf("api: encode response: %T", err)
		status, b = 500, []byte(`{"error":{"code":"INTERNAL","message":"Internal error"}}`)
	}
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
	Details   any    `json:"details,omitempty"`
}

// writeError sends the one error shape every route uses: {error: {code, message, requestId, details}}.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, body := 500, errorBody{Code: "INTERNAL", Message: "Internal error"}
	var ve *domain.ValidationError
	if e, ok := apperr.As(err); ok {
		status, body = e.Status, errorBody{Code: e.Code, Message: e.Message, Details: e.Details}
	} else if errors.As(err, &ve) {
		status, body = 400, errorBody{Code: "VALIDATION", Message: "Invalid request", Details: ve.Issues}
	} else {
		// Never log content: only the type of the error and the route.
		log.Printf("api: unhandled error on %s %s: %T", r.Method, r.URL.Path, err)
	}
	body.RequestID = requestID(r)
	writeJSON(w, status, map[string]any{"error": body})
}

func invalid(path, format string, args ...any) error {
	var p []any
	if path != "" {
		p = []any{path}
	} else {
		p = []any{}
	}
	return &apperr.Error{Status: 400, Code: "VALIDATION", Message: "Invalid request",
		Details: []domain.Issue{{Code: "custom", Path: p, Message: fmt.Sprintf(format, args...)}}}
}

// readBody decodes a JSON body the way Fastify does: no body gives (nil, nil); a JSON content type with an empty or
// broken body is a 400; text/plain becomes a string; any other type is a 415.
func readBody(r *http.Request) (any, error) {
	ct := r.Header.Get("content-type")
	if ct == "" && (r.ContentLength == 0 || r.Body == nil || r.Body == http.NoBody) {
		return nil, nil
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, bodyLimit+1))
	if err != nil {
		return nil, apperr.New(400, "BAD_REQUEST", "Could not read the request body")
	}
	if len(raw) > bodyLimit {
		return nil, apperr.New(413, "FST_ERR_CTP_BODY_TOO_LARGE", "Request body is too large")
	}
	if ct == "" && len(raw) == 0 {
		return nil, nil
	}
	mt, _, _ := mime.ParseMediaType(ct)
	switch {
	case mt == "application/json" || strings.HasSuffix(mt, "+json"):
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil, apperr.New(400, "FST_ERR_CTP_EMPTY_JSON_BODY",
				"Body cannot be empty when content-type is set to 'application/json'")
		}
		var v any
		dec := json.NewDecoder(bytes.NewReader(raw))
		if err := dec.Decode(&v); err != nil || dec.More() {
			return nil, apperr.New(400, "FST_ERR_CTP_INVALID_JSON_BODY", "Body is not valid JSON")
		}
		return v, nil
	case mt == "text/plain":
		return string(raw), nil
	}
	return nil, apperr.New(415, "FST_ERR_CTP_INVALID_MEDIA_TYPE", "Unsupported Media Type: %s", mt)
}

// object returns the body as a JSON object, or a validation error.
func object(r *http.Request) (map[string]any, error) {
	b, err := readBody(r)
	if err != nil {
		return nil, err
	}
	m, ok := b.(map[string]any)
	if !ok {
		return nil, invalid("", "Expected object, received %s", jsType(b))
	}
	return m, nil
}

// optionalObject is object, but no body at all counts as {} (routes written `req.body ?? {}`).
func optionalObject(r *http.Request) (map[string]any, error) {
	b, err := readBody(r)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return map[string]any{}, nil
	}
	m, ok := b.(map[string]any)
	if !ok {
		return nil, invalid("", "Expected object, received %s", jsType(b))
	}
	return m, nil
}

func jsType(v any) string {
	switch v.(type) {
	case nil:
		return "undefined"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	}
	return "object"
}

// jsLen is a string's length as JavaScript counts it (UTF-16 units).
func jsLen(s string) int { return len(utf16.Encode([]rune(s))) }

// field readers: each returns a validation error naming the field when the type is wrong.

func optString(m map[string]any, k string) (string, bool, error) {
	v, ok := m[k]
	if !ok || v == nil {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, invalid(k, "Expected string, received %s", jsType(v))
	}
	return s, true, nil
}

func reqString(m map[string]any, k string, min, max int) (string, error) {
	s, ok, err := optString(m, k)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", invalid(k, "Required")
	}
	return s, checkLen(k, s, min, max)
}

func checkLen(k, s string, min, max int) error {
	n := jsLen(s)
	if n < min {
		return invalid(k, "String must contain at least %d character(s)", min)
	}
	if max > 0 && n > max {
		return invalid(k, "String must contain at most %d character(s)", max)
	}
	return nil
}

func optEnum(m map[string]any, k string, allowed ...string) (string, error) {
	s, ok, err := optString(m, k)
	if err != nil || !ok {
		return "", err
	}
	for _, a := range allowed {
		if s == a {
			return s, nil
		}
	}
	return "", invalid(k, "Invalid enum value. Expected %s, received '%s'", strings.Join(allowed, " | "), s)
}

// jsNumber parses a query value the way z.coerce.number does (Number(value)).
func jsNumber(s string) (float64, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, true
	}
	if strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0X") {
		n, err := strconv.ParseInt(t[2:], 16, 64)
		return float64(n), err == nil
	}
	switch t {
	case "Infinity", "+Infinity":
		return math.Inf(1), true
	case "-Infinity":
		return math.Inf(-1), true
	}
	if strings.Trim(t, "0123456789.eE+-") != "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(t, 64)
	return f, err == nil
}

// queryInt reads an integer query parameter with bounds and a default (z.coerce.number().int().min().max()).
func queryInt(r *http.Request, k string, min, max, def int, hasDef bool) (int, bool, error) {
	q := r.URL.Query()
	if !q.Has(k) {
		return def, hasDef, nil
	}
	f, ok := jsNumber(q.Get(k))
	if !ok {
		return 0, false, invalid(k, "Expected number, received nan")
	}
	if f != float64(int64(f)) {
		return 0, false, invalid(k, "Expected integer, received float")
	}
	if int(f) < min {
		return 0, false, invalid(k, "Number must be greater than or equal to %d", min)
	}
	if int(f) > max {
		return 0, false, invalid(k, "Number must be less than or equal to %d", max)
	}
	return int(f), true, nil
}

func queryEnum(r *http.Request, k string, allowed ...string) (string, error) {
	q := r.URL.Query()
	if !q.Has(k) {
		return "", nil
	}
	v := q.Get(k)
	for _, a := range allowed {
		if v == a {
			return v, nil
		}
	}
	return "", invalid(k, "Invalid enum value. Expected %s, received '%s'", strings.Join(allowed, " | "), v)
}

func queryBool(r *http.Request, k string) (bool, error) {
	v, err := queryEnum(r, k, "true", "false")
	return v == "true", err
}
