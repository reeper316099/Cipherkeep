package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/reeper316099/Cipherkeep/go/internal/desktop"
)

// fixtures/compat/node-app holds two libraries written by the Node app through its own API (plain, and encrypted with
// a public test passphrase) and what the Node API answered for them (scripts/gen-node-app-fixture.ts, run once before
// the Node server was retired). The Go app must open both and answer every read the same way.

const nodeAppRoot = "../../../fixtures/compat/node-app"

type nodeAppExpected struct {
	Passphrase string   `json:"passphrase"`
	Reads      []string `json:"reads"`
	Plain      nodeAppKind
	Encrypted  nodeAppKind
}

type nodeAppKind struct {
	Reads      map[string]json.RawMessage `json:"reads"`
	Attachment struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"attachment"`
	ActivityMonth string          `json:"activityMonth"`
	Activity      json.RawMessage `json:"activity"`
}

// sameJSON compares two decoded JSON values; numbers may differ by a rounding error (search scores).
func sameJSON(path string, a, b any) error {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: object vs %T", path, b)
		}
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			if err := sameJSON(path+"."+k, x[k], y[k]); err != nil {
				return err
			}
		}
		return nil
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return fmt.Errorf("%s: %d items vs %v", path, len(x), b)
		}
		for i := range x {
			if err := sameJSON(fmt.Sprintf("%s[%d]", path, i), x[i], y[i]); err != nil {
				return err
			}
		}
		return nil
	case float64:
		y, ok := b.(float64)
		if !ok || math.Abs(x-y) > 1e-9*math.Max(1, math.Abs(x)) {
			return fmt.Errorf("%s: %v vs %v", path, a, b)
		}
		return nil
	}
	if fmt.Sprint(a) != fmt.Sprint(b) {
		return fmt.Errorf("%s: %#v vs %#v", path, a, b)
	}
	return nil
}

func TestGoOpensLibrariesWrittenByTheNodeApp(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(nodeAppRoot, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var exp nodeAppExpected
	if err := json.Unmarshal(raw, &exp); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"plain", "encrypted"} {
		t.Run(kind, func(t *testing.T) {
			want := exp.Plain
			if kind == "encrypted" {
				want = exp.Encrypted
			}
			// Always work on a copy: the fixture itself must never change.
			dir := t.TempDir()
			if err := desktop.CopyData(filepath.Join(nodeAppRoot, kind), dir); err != nil {
				t.Fatal(err)
			}
			s := newServer(t, Options{DataDir: dir})
			defer s.Close()
			if kind == "encrypted" {
				if !s.Locked() {
					t.Fatal("an encrypted Node library must open locked")
				}
				if r := call(t, s, "POST", "/vault/unlock", map[string]any{"passphrase": "wrong passphrase"}); r.status != 401 {
					t.Fatalf("wrong passphrase: %d", r.status)
				}
				if r := call(t, s, "POST", "/vault/unlock", map[string]any{"passphrase": exp.Passphrase}); r.status != 200 {
					t.Fatalf("unlock: %d %v", r.status, r.body)
				}
			}
			for _, p := range exp.Reads {
				req := httptest.NewRequest("GET", "/api/v1"+p, nil)
				rec := httptest.NewRecorder()
				s.ServeHTTP(rec, req)
				var got any
				_ = json.Unmarshal(rec.Body.Bytes(), &got)
				if m, ok := got.(map[string]any); ok {
					if e, ok := m["error"].(map[string]any); ok {
						delete(e, "requestId")
					}
				}
				var w struct {
					Status int `json:"status"`
					Body   any `json:"body"`
				}
				if err := json.Unmarshal(want.Reads[p], &w); err != nil {
					t.Fatal(err)
				}
				if rec.Code != w.Status {
					t.Errorf("GET %s: status %d, Node answered %d", p, rec.Code, w.Status)
					continue
				}
				if err := sameJSON("GET "+p, w.Body, got); err != nil {
					t.Errorf("differs from Node: %v", err)
				}
			}
			// The attachment comes back byte for byte (decrypted in the encrypted library).
			req := httptest.NewRequest("GET", "/api/v1"+want.Attachment.Path, nil)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			sum := sha256.Sum256(rec.Body.Bytes())
			if rec.Code != 200 || hex.EncodeToString(sum[:]) != want.Attachment.SHA256 {
				t.Errorf("attachment %s: %d, sha256 %x", want.Attachment.Path, rec.Code, sum)
			}
			var wantAct, gotAct any
			_ = json.Unmarshal(want.Activity, &wantAct)
			_ = json.Unmarshal(call2(t, s, "/activity/"+want.ActivityMonth), &gotAct)
			if err := sameJSON("activity", wantAct, gotAct); err != nil {
				t.Errorf("activity log differs from Node: %v", err)
			}
			// And the Go app can keep working on it.
			if r := call(t, s, "POST", "/notes", map[string]any{"title": "Written by Go", "markdown": "hi"}); r.status != 201 {
				t.Fatalf("write after opening: %d", r.status)
			}
		})
	}
}

func call2(t *testing.T, s *Server, path string) []byte {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1"+path, nil))
	return rec.Body.Bytes()
}
