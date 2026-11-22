package jsonstore

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStableStringifyMatchesJavaScript(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"sorted keys, two-space indent, newline", map[string]any{"b": 1, "a": []any{}, "c": map[string]any{}}, "{\n  \"a\": [],\n  \"b\": 1,\n  \"c\": {}\n}\n"},
		{"html is not escaped", map[string]any{"x": "<a href=\"x\">&</a>"}, "{\n  \"x\": \"<a href=\\\"x\\\">&</a>\"\n}\n"},
		{"line separators are not escaped", map[string]any{"x": "a\u2028b"}, "{\n  \"x\": \"a\u2028b\"\n}\n"},
		{"control characters", map[string]any{"x": "\x01\n\t"}, "{\n  \"x\": \"\\u0001\\n\\t\"\n}\n"},
		{"floats and integers", []any{2.5, 0.1, 3.0, 1e-7, 1e21, 123456789012}, "[\n  2.5,\n  0.1,\n  3,\n  1e-7,\n  1e+21,\n  123456789012\n]\n"},
		{"nested arrays", map[string]any{"a": []any{map[string]any{"z": nil, "y": true}}}, "{\n  \"a\": [\n    {\n      \"y\": true,\n      \"z\": null\n    }\n  ]\n}\n"},
	}
	for _, c := range cases {
		got, err := StableStringify(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestKeyOrderFollowsUTF16LikeJavaScript(t *testing.T) {
	// "\uFF5E" (BMP, above the surrogates) sorts AFTER an astral character in JavaScript, before it in UTF-8 order.
	got, _ := StableStringify(map[string]any{"\U0001F600": 1, "\uFF5E": 2})
	if !strings.Contains(string(got), "\U0001F600\": 1,\n  \"\uFF5E") {
		t.Fatalf("order differs from JavaScript: %q", got)
	}
}

func TestAtomicWriteLeavesNoTempFilesAndReplacesTheTarget(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "sub", "x.json")
	for i := 0; i < 3; i++ {
		if err := AtomicWriteFile(f, []byte{byte('0' + i)}); err != nil {
			t.Fatal(err)
		}
	}
	if read(t, f) != "2" {
		t.Fatal("not replaced")
	}
	ents, _ := os.ReadDir(filepath.Dir(f))
	if len(ents) != 1 {
		t.Fatalf("leftovers: %v", ents)
	}
}

func TestInstanceLockExcludesASecondHolderAndTakesOverStaleLocks(t *testing.T) {
	dir := t.TempDir()
	l := must(AcquireInstanceLock(dir))
	if _, err := AcquireInstanceLock(dir); !IsCode(err, CodeLocked) {
		t.Fatalf("second holder: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	// A lock left by a process that no longer exists is taken over.
	write(t, filepath.Join(dir, ".lock"), `{"pid": 2147483646}`)
	l2, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
	_ = l2.Release()
	write(t, filepath.Join(dir, ".lock"), `not json`)
	if l3, err := AcquireInstanceLock(dir); err != nil {
		t.Fatalf("unreadable lock not taken over: %v", err)
	} else {
		_ = l3.Release()
	}
}

func TestBackupsKeepOnlyTheNewestNAndSkipFreshLaunchBackups(t *testing.T) {
	dir := t.TempDir()
	m := NewBackupManager(BackupOptions{DataDir: dir, BackupDir: filepath.Join(dir, "backups"), Keep: 3})
	write(t, filepath.Join(dir, "x.json"), "1")
	for i := 0; i < 5; i++ {
		must(m.Create("r"))
	}
	list := must(m.List())
	if len(list) != 3 {
		t.Fatalf("kept %d", len(list))
	}
	if b, err := m.CreateIfStale(6, "launch"); err != nil || b != nil {
		t.Fatalf("fresh backup was not skipped: %v %v", b, err)
	}
	if b, _ := m.CreateIfStale(0.0000001, "launch"); b == nil {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBackupFailsCleanlyWhenAFileVanishes(t *testing.T) {
	dir := t.TempDir()
	m := NewBackupManager(BackupOptions{DataDir: dir, BackupDir: filepath.Join(dir, "backups")})
	write(t, filepath.Join(dir, "a.json"), "{}")
	files := must(m.collect(dir, "", nil))
	files = append(files, entry{abs: filepath.Join(dir, "missing.json"), name: "missing.json"})
	if err := writeZip(filepath.Join(dir, "x.zip.tmp"), files); err == nil {
		t.Fatal("expected an error for the missing file")
	}
}

func TestBackupRestoreWritesFilesAndRefusesPathsOutsideTheDataDir(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	m := NewBackupManager(BackupOptions{DataDir: data, BackupDir: filepath.Join(dir, "backups")})
	write(t, filepath.Join(data, "a", "b.json"), "orig")
	b := must(m.Create("t"))
	write(t, filepath.Join(data, "a", "b.json"), "changed")
	if err := m.Restore(b.Name); err != nil || read(t, filepath.Join(data, "a", "b.json")) != "orig" {
		t.Fatalf("restore: %v", err)
	}
	// A hostile zip with a "../" entry must not write outside the data dir.
	evil := filepath.Join(dir, "backups", "20200101T000000-evil.zip")
	out, _ := os.Create(evil)
	zw := zip.NewWriter(out)
	w, _ := zw.Create("../escaped.txt")
	_, _ = w.Write([]byte("pwned"))
	_ = zw.Close()
	_ = out.Close()
	if err := m.Restore("20200101T000000-evil.zip"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Fatal("zip-slip: a file was written outside the data directory")
	}
}

func TestKeyedMutexSerialisesTheSameKeyOnly(t *testing.T) {
	var k KeyedMutex
	order := make(chan int, 3)
	unlock := k.Lock("a")
	go func() { defer k.Lock("a")(); order <- 2 }()
	time.Sleep(50 * time.Millisecond)
	k.Lock("b")() // a different key is never blocked
	order <- 1
	unlock()
	if first, second := <-order, <-order; first != 1 || second != 2 {
		t.Fatalf("order %d then %d", first, second)
	}
}

type tagged struct {
	Name  string             `json:"name"`
	Omit  string             `json:"omit,omitempty"`
	Skip  string             `json:"-"`
	Num   float64            `json:"num"`
	Items []int              `json:"items"`
	Nil   []int              `json:"nil"`
	Map   map[string]any     `json:"map"`
	Ptr   *string            `json:"ptr"`
	Nest  struct{ A, B int } `json:"nest"`
	hid   int
}

type embedded struct {
	tagged
	Extra int `json:"extra"`
}

// The direct writer must produce exactly what the encode-decode-write route produces for the same value.
func TestDirectWriterMatchesTheRoundTripRoute(t *testing.T) {
	s := "x"
	values := []any{
		tagged{Name: "<a&b>", Num: 3, Items: []int{1, 2}, Map: map[string]any{"z": 1.5, "a": []any{map[string]any{"k": nil}}}, Ptr: &s},
		tagged{Num: 1e-7, Items: []int{}},
		embedded{tagged: tagged{Name: "n"}, Extra: 5}, // embedded structs take the slow path
		map[string]any{"b": []string{"x"}, "a": map[string]int{"y": 2}, "c": int64(7), "d": uint8(3), "e": true},
		[]any{nil, "s", 2.0, json.Number("12")},
		struct{ Raw json.RawMessage }{Raw: json.RawMessage(`{"q": [1, 2]}`)},
	}
	for i, v := range values {
		direct, err := StableStringify(v)
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		generic, err := toGeneric(v)
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		var buf bytes.Buffer
		writeValue(&buf, generic, 0)
		buf.WriteByte('\n')
		if string(direct) != buf.String() {
			t.Errorf("%d differs:\n direct %q\n  trip  %q", i, direct, buf.String())
		}
	}
}
