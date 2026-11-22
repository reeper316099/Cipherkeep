package jsonstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type item struct {
	ID string `json:"id"`
	N  int    `json:"n"`
}
type doc struct {
	Items []item `json:"items"`
}

func opts() Options[doc] {
	return Options[doc]{
		Version:  1,
		Defaults: func() doc { return doc{Items: []item{}} },
		Validate: func(d *doc) error {
			for _, it := range d.Items {
				if it.N < 0 {
					return errors.New("negative n")
				}
			}
			return nil
		},
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func onDisk(t *testing.T, path string) (version int, d doc) {
	t.Helper()
	var env struct {
		SchemaVersion int `json:"schemaVersion"`
		Data          doc `json:"data"`
	}
	if err := json.Unmarshal([]byte(read(t, path)), &env); err != nil {
		t.Fatal(err)
	}
	return env.SchemaVersion, env.Data
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
