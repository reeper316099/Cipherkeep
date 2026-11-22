package jsonstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// InstanceLock is held while one process uses a data directory.
type InstanceLock struct{ file string }

// Release removes the lock file.
func (l *InstanceLock) Release() error {
	if l == nil {
		return nil
	}
	err := os.Remove(l.file)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// AcquireInstanceLock creates <dir>/.lock holding the owner's pid. A lock whose owner is gone (or unreadable) is
// taken over; a live owner gives a LOCKED error.
func AcquireInstanceLock(dir string) (*InstanceLock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, ".lock")
	body, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "startedAt": time.Now().UTC().Format("2006-01-02T15:04:05.000Z")})
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, werr := f.Write(body)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(file)
				if werr != nil {
					return nil, werr
				}
				return nil, cerr
			}
			return &InstanceLock{file: file}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		pid := 0
		if raw, rerr := os.ReadFile(file); rerr == nil {
			var l struct {
				Pid int `json:"pid"`
			}
			if json.Unmarshal(raw, &l) == nil {
				pid = l.Pid
			}
		}
		if pid > 0 && pid != os.Getpid() && processAlive(pid) {
			return nil, newErr(CodeLocked, fmt.Sprintf("Data directory is in use by process %d", pid), file)
		}
		if pid == os.Getpid() {
			return nil, newErr(CodeLocked, fmt.Sprintf("Data directory is in use by process %d", pid), file)
		}
		_ = os.Remove(file)
	}
	return nil, newErr(CodeLocked, "Could not acquire lock", file)
}
