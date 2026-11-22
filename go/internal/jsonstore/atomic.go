package jsonstore

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// AtomicWriteFile writes data to a temp file in the same directory, fsyncs it, then renames it over the target,
// so a reader (or a crash) never sees a half-written file.
func AtomicWriteFile(file string, data []byte) error {
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	tmp := filepath.Join(dir, "."+filepath.Base(file)+"."+hex.EncodeToString(rnd[:])+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp)
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// AtomicWriteJSON writes value as stable JSON (see StableStringify).
func AtomicWriteJSON(file string, value any) error {
	b, err := StableStringify(value)
	if err != nil {
		return err
	}
	return AtomicWriteFile(file, b)
}

// transient reports the errors Windows returns for a file that another process is still finishing.
func transient(err error) bool {
	if errors.Is(err, syscall.EBUSY) || errors.Is(err, os.ErrPermission) {
		return true
	}
	var en syscall.Errno
	if runtime.GOOS == "windows" && errors.As(err, &en) {
		return en == 5 || en == 32 || en == 33 // access denied, sharing violation, lock violation
	}
	return false
}

// ReadFileRetry reads a file that another process may be finishing a write to, retrying briefly when the
// system reports it busy. A watcher that gave up on the first failure would miss the edit for good.
func ReadFileRetry(file string) ([]byte, error) {
	const tries = 6
	for i := 1; ; i++ {
		b, err := os.ReadFile(file)
		if err == nil || i >= tries || !transient(err) {
			return b, err
		}
		time.Sleep(time.Duration(50*i) * time.Millisecond)
	}
}

// StableStringify renders v as pretty JSON that matches the TypeScript version byte for byte for the same data:
// object keys sorted, two-space indent, a trailing newline, and JavaScript's string escaping (it does not escape
// <, > or &, and does not escape U+2028/2029).
func StableStringify(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeReflect(&buf, reflect.ValueOf(v), 0); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// toGeneric converts any value to the generic form (maps, slices, strings, json.Number, bool, nil).
func toGeneric(v any) (any, error) {
	var raw []byte
	switch t := v.(type) {
	case json.RawMessage:
		raw = t
	case []byte:
		raw = t
	default:
		b, err := marshalNoEscape(v)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func writeValue(buf *bytes.Buffer, v any, depth int) {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case json.Number:
		buf.WriteString(jsNumber(t))
	case string:
		writeString(buf, t)
	case []any:
		if len(t) == 0 {
			buf.WriteString("[]")
			return
		}
		buf.WriteString("[\n")
		for i, e := range t {
			indent(buf, depth+1)
			writeValue(buf, e, depth+1)
			if i < len(t)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		indent(buf, depth)
		buf.WriteByte(']')
	case map[string]any:
		if len(t) == 0 {
			buf.WriteString("{}")
			return
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		buf.WriteString("{\n")
		for i, k := range keys {
			indent(buf, depth+1)
			writeString(buf, k)
			buf.WriteString(": ")
			writeValue(buf, t[k], depth+1)
			if i < len(keys)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		indent(buf, depth)
		buf.WriteByte('}')
	default:
		buf.WriteString("null")
	}
}

func indent(buf *bytes.Buffer, depth int) {
	for i := 0; i < depth; i++ {
		buf.WriteString("  ")
	}
}

// utf16Less orders strings the way JavaScript's `<` does (by UTF-16 code unit), so key order matches Node.
func utf16Less(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ra, sa := utf8.DecodeRuneInString(a[ai:])
		rb, sb := utf8.DecodeRuneInString(b[bi:])
		ai += sa
		bi += sb
		if ra == rb {
			continue
		}
		ua, ub := utf16Units(ra), utf16Units(rb)
		for k := 0; k < len(ua) && k < len(ub); k++ {
			if ua[k] != ub[k] {
				return ua[k] < ub[k]
			}
		}
		return len(ua) < len(ub)
	}
	return len(a)-ai < len(b)-bi
}

func utf16Units(r rune) []uint16 {
	if r >= 0x10000 {
		r -= 0x10000
		return []uint16{uint16(0xD800 + (r >> 10)), uint16(0xDC00 + (r & 0x3FF))}
	}
	return []uint16{uint16(r)}
}

const hexDigits = "0123456789abcdef"

func writeString(buf *bytes.Buffer, s string) {
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\b':
			buf.WriteString(`\b`)
		case '\f':
			buf.WriteString(`\f`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			if r < 0x20 {
				buf.WriteString(`\u00`)
				buf.WriteByte(hexDigits[r>>4])
				buf.WriteByte(hexDigits[r&0xF])
			} else if r == utf8.RuneError {
				buf.WriteString("�")
			} else {
				buf.WriteRune(r)
			}
		}
	}
	buf.WriteByte('"')
}

// jsNumber formats a number the way JSON.stringify(Number(x)) would.
func jsNumber(n json.Number) string {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		return s // an integer literal is printed as written (leading zeros cannot occur in valid JSON)
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	abs := f
	if abs < 0 {
		abs = -abs
	}
	if abs == 0 {
		return "0"
	}
	if abs >= 1e-6 && abs < 1e21 { // JavaScript prints plain decimals in this range
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	// JavaScript writes 1e+21 and 1.5e-7: it keeps the plus sign and never pads the exponent.
	mant, exp, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	sign := "+"
	if strings.HasPrefix(exp, "-") {
		sign, exp = "-", exp[1:]
	} else {
		exp = strings.TrimPrefix(exp, "+")
	}
	exp = strings.TrimLeft(exp, "0")
	if exp == "" {
		exp = "0"
	}
	return mant + "e" + sign + exp
}

var (
	jsonNumberType    = reflect.TypeOf(json.Number(""))
	marshalerType     = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*interface{ MarshalText() ([]byte, error) })(nil)).Elem()
)

// writeReflect writes v in canonical form straight from the Go value (maps, slices, strings, numbers, structs with
// json tags), which avoids encoding to JSON and decoding again. Anything unusual (custom marshalers, embedded structs,
// json.RawMessage) takes the slower round trip through encoding/json, so the output is always the same.
func writeReflect(buf *bytes.Buffer, v reflect.Value, depth int) error {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer) {
		if v.IsNil() {
			buf.WriteString("null")
			return nil
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		buf.WriteString("null")
		return nil
	}
	t := v.Type()
	if t == jsonNumberType {
		buf.WriteString(jsNumber(json.Number(v.String()))) // a number, not a string
		return nil
	}
	if t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType) ||
		t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType) {
		return writeViaJSON(buf, v, depth)
	}
	switch v.Kind() {
	case reflect.String:
		writeString(buf, v.String())
	case reflect.Bool:
		if v.Bool() {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		buf.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		buf.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return errors.New("jsonstore: cannot encode NaN or Inf")
		}
		buf.WriteString(jsNumber(json.Number(strconv.FormatFloat(f, 'g', -1, 64))))
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			buf.WriteString("null")
			return nil
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return writeViaJSON(buf, v, depth) // []byte is base64 in JSON
		}
		n := v.Len()
		if n == 0 {
			buf.WriteString("[]")
			return nil
		}
		buf.WriteString("[\n")
		for i := 0; i < n; i++ {
			indent(buf, depth+1)
			if err := writeReflect(buf, v.Index(i), depth+1); err != nil {
				return err
			}
			if i < n-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		indent(buf, depth)
		buf.WriteByte(']')
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return writeViaJSON(buf, v, depth)
		}
		if v.IsNil() {
			buf.WriteString("null")
			return nil
		}
		keys := v.MapKeys()
		if len(keys) == 0 {
			buf.WriteString("{}")
			return nil
		}
		names := make([]string, len(keys))
		for i, k := range keys {
			names[i] = k.String()
		}
		order := make([]int, len(keys))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return utf16Less(names[order[a]], names[order[b]]) })
		buf.WriteString("{\n")
		for n, i := range order {
			indent(buf, depth+1)
			writeString(buf, names[i])
			buf.WriteString(": ")
			if err := writeReflect(buf, v.MapIndex(keys[i]), depth+1); err != nil {
				return err
			}
			if n < len(order)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		indent(buf, depth)
		buf.WriteByte('}')
	case reflect.Struct:
		return writeStruct(buf, v, depth)
	default:
		return writeViaJSON(buf, v, depth)
	}
	return nil
}

type field struct {
	name  string
	index int
	omit  bool
}

var fieldCache sync.Map // reflect.Type -> []field (nil: use the slow path)

func structFields(t reflect.Type) []field {
	if c, ok := fieldCache.Load(t); ok {
		return c.([]field)
	}
	var out []field
	ok := true
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			ok = false // embedded structs flatten in encoding/json; leave those to the slow path
			break
		}
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		out = append(out, field{name: name, index: i, omit: strings.Contains(opts, "omitempty")})
		if strings.Contains(opts, "string") {
			ok = false
			break
		}
	}
	if !ok {
		out = nil
	}
	fieldCache.Store(t, out)
	return out
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}

func writeStruct(buf *bytes.Buffer, v reflect.Value, depth int) error {
	fields := structFields(v.Type())
	if fields == nil {
		return writeViaJSON(buf, v, depth)
	}
	type kv struct {
		name string
		val  reflect.Value
	}
	var kvs []kv
	for _, f := range fields {
		fv := v.Field(f.index)
		if f.omit && isEmptyValue(fv) {
			continue
		}
		kvs = append(kvs, kv{f.name, fv})
	}
	if len(kvs) == 0 {
		buf.WriteString("{}")
		return nil
	}
	sort.Slice(kvs, func(a, b int) bool { return utf16Less(kvs[a].name, kvs[b].name) })
	buf.WriteString("{\n")
	for i, e := range kvs {
		indent(buf, depth+1)
		writeString(buf, e.name)
		buf.WriteString(": ")
		if err := writeReflect(buf, e.val, depth+1); err != nil {
			return err
		}
		if i < len(kvs)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	indent(buf, depth)
	buf.WriteByte('}')
	return nil
}

// writeViaJSON is the slow path: encode with encoding/json, decode generically, and write that.
func writeViaJSON(buf *bytes.Buffer, v reflect.Value, depth int) error {
	generic, err := toGeneric(v.Interface())
	if err != nil {
		return err
	}
	writeValue(buf, generic, depth)
	return nil
}
