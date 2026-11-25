package api

import (
	"crypto/subtle"
	"net/http"
	"os"
	"path/filepath"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
	"github.com/reeper316099/Cipherkeep/go/internal/library"
)

func (s *Server) status() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{"enabled": s.key != nil || s.lib == nil, "locked": s.lib == nil, "busy": s.busy}
}

// exclusive runs fn as the only encryption change in progress.
func (s *Server) exclusive(fn func() error) error {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return apperr.New(409, "BUSY", "Another encryption change is in progress")
	}
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()
	return fn()
}

func (s *Server) currentKey() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.key
}

func (s *Server) requireOpen() (*library.Library, error) {
	if lib := s.Library(); lib != nil {
		return lib, nil
	}
	return nil, apperr.New(423, "LOCKED", "Unlock your library to continue")
}

// verify checks the passphrase against the key in use, counting wrong guesses.
func (s *Server) verify(passphrase string) error {
	if err := s.limiter.Check(); err != nil {
		return err
	}
	k, err := s.vault.UnlockWithPassphrase(passphrase)
	if err == nil {
		if cur := s.currentKey(); cur == nil || subtle.ConstantTimeCompare(k, cur) != 1 {
			err = apperr.New(401, "BAD_CREDENTIALS", "Wrong passphrase")
		}
	}
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Code == "BAD_CREDENTIALS" {
			s.limiter.Fail()
		}
		return err
	}
	s.limiter.OK()
	return nil
}

func (s *Server) unlock(passphrase, recoveryKey, newPassphrase string) error {
	if !s.Locked() {
		return apperr.New(409, "NOT_LOCKED", "The library is already unlocked")
	}
	return s.exclusive(func() error {
		if err := s.limiter.Check(); err != nil {
			return err
		}
		var k []byte
		var err error
		if passphrase != "" {
			k, err = s.vault.UnlockWithPassphrase(passphrase)
		} else {
			k, err = s.vault.UnlockWithRecoveryKey(recoveryKey)
		}
		if err != nil {
			if e, ok := apperr.As(err); ok && e.Code == "BAD_CREDENTIALS" {
				s.limiter.Fail()
			}
			return err
		}
		s.limiter.OK()
		if recoveryKey != "" {
			if newPassphrase == "" {
				return apperr.New(400, "NEW_PASSPHRASE_REQUIRED", "Choose a new passphrase to finish recovery")
			}
			if err := s.vault.SetPassphrase(k, newPassphrase); err != nil {
				return err
			}
		}
		aes, err := jsonstore.NewAESGCMCodec(k)
		if err != nil {
			return err
		}
		if err := s.openLibrary(aes); err != nil {
			return err
		}
		s.mu.Lock()
		s.key = k
		s.mu.Unlock()
		return nil
	})
}

func (s *Server) lockNow() error {
	if s.currentKey() == nil {
		return apperr.New(409, "NOT_ENCRYPTED", "Encryption is not enabled")
	}
	return s.exclusive(func() error {
		locked := s.lockedHandler()
		s.mu.Lock()
		lib := s.lib
		s.lib, s.open, s.locked, s.key = nil, nil, locked, nil
		s.mu.Unlock()
		// Wait for the background clean-up of this library, then close it (this also ends open event streams).
		s.pending.Wait()
		if lib != nil {
			return lib.Close()
		}
		return nil
	})
}

func (s *Server) enable(passphrase string, deleteOldBackups bool) (string, error) {
	lib, err := s.requireOpen()
	if err != nil {
		return "", err
	}
	if s.currentKey() != nil {
		return "", apperr.New(409, "ALREADY_ENABLED", "Encryption is already enabled")
	}
	var recovery string
	err = s.exclusive(func() error {
		key, rk, err := s.vault.Create(passphrase)
		if err != nil {
			return err
		}
		aes, err := jsonstore.NewAESGCMCodec(key)
		if err != nil {
			return err
		}
		convert := func() error {
			if err := recodeLibrary(lib, jsonstore.PlainCodec{}, aes); err != nil {
				return err
			}
			if deleteOldBackups {
				list, err := lib.Backups.List()
				if err != nil {
					return err
				}
				for _, b := range list {
					if err := os.Remove(b.File); err != nil && !os.IsNotExist(err) {
						return err
					}
				}
				if err := os.RemoveAll(filepath.Join(s.dir, ".quarantine")); err != nil {
					return err
				}
			}
			_, err := lib.Backups.Create("encryption-enabled")
			return err
		}
		if err := convert(); err != nil {
			// Put everything back the way it was rather than leave a half-converted library.
			_ = recodeLibrary(lib, aes, jsonstore.PlainCodec{})
			_ = s.vault.Remove()
			return err
		}
		s.mu.Lock()
		s.key = key
		s.mu.Unlock()
		recovery = rk
		return nil
	})
	return recovery, err
}

func (s *Server) disable(passphrase string) error {
	lib, err := s.requireOpen()
	if err != nil {
		return err
	}
	key := s.currentKey()
	if key == nil {
		return apperr.New(409, "NOT_ENCRYPTED", "Encryption is not enabled")
	}
	if err := s.verify(passphrase); err != nil {
		return err
	}
	return s.exclusive(func() error {
		aes, err := jsonstore.NewAESGCMCodec(key)
		if err != nil {
			return err
		}
		if err := recodeLibrary(lib, aes, jsonstore.PlainCodec{}); err != nil {
			return err
		}
		if err := s.vault.Remove(); err != nil {
			return err
		}
		s.mu.Lock()
		s.key = nil
		s.mu.Unlock()
		return nil
	})
}

func (s *Server) changePassphrase(current, next string) error {
	if _, err := s.requireOpen(); err != nil {
		return err
	}
	key := s.currentKey()
	if key == nil {
		return apperr.New(409, "NOT_ENCRYPTED", "Encryption is not enabled")
	}
	if err := s.verify(current); err != nil {
		return err
	}
	return s.vault.SetPassphrase(key, next)
}

func (s *Server) newRecoveryKey(passphrase string) (string, error) {
	if _, err := s.requireOpen(); err != nil {
		return "", err
	}
	key := s.currentKey()
	if key == nil {
		return "", apperr.New(409, "NOT_ENCRYPTED", "Encryption is not enabled")
	}
	if err := s.verify(passphrase); err != nil {
		return "", err
	}
	return s.vault.NewRecoveryKey(key)
}

// pass reads a 1 to 1024 character secret.
func pass(m map[string]any, k string, required bool) (string, error) {
	v, ok, err := optString(m, k)
	if err != nil {
		return "", err
	}
	if !ok {
		if required {
			return "", invalid(k, "Required")
		}
		return "", nil
	}
	return v, checkLen(k, v, 1, 1024)
}

// vaultRoutes registers what makes sense in the current state (locked: status and unlock; open: the rest).
func (s *Server) vaultRoutes(rt *router, locked bool) {
	rt.handle("GET /api/v1/vault", func(w http.ResponseWriter, r *http.Request) error {
		writeJSON(w, 200, s.status())
		return nil
	})
	ok := func(w http.ResponseWriter) { writeJSON(w, 200, map[string]any{"ok": true}) }
	if locked {
		rt.handle("POST /api/v1/vault/unlock", func(w http.ResponseWriter, r *http.Request) error {
			b, err := object(r)
			if err != nil {
				return err
			}
			p, err := pass(b, "passphrase", false)
			if err != nil {
				return err
			}
			rk, err := pass(b, "recoveryKey", false)
			if err != nil {
				return err
			}
			np, err := pass(b, "newPassphrase", false)
			if err != nil {
				return err
			}
			if (p != "") == (rk != "") {
				return invalid("", "Give a passphrase or a recovery key")
			}
			if err := s.unlock(p, rk, np); err != nil {
				return err
			}
			ok(w)
			return nil
		})
		return
	}
	rt.handle("POST /api/v1/vault/enable", func(w http.ResponseWriter, r *http.Request) error {
		b, err := object(r)
		if err != nil {
			return err
		}
		p, err := pass(b, "passphrase", true)
		if err != nil {
			return err
		}
		del := true
		if v, has := b["deleteOldBackups"]; has && v != nil {
			bv, isBool := v.(bool)
			if !isBool {
				return invalid("deleteOldBackups", "Expected boolean, received %s", jsType(v))
			}
			del = bv
		}
		rk, err := s.enable(p, del)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"recoveryKey": rk})
		return nil
	})
	rt.handle("POST /api/v1/vault/disable", func(w http.ResponseWriter, r *http.Request) error {
		b, err := object(r)
		if err != nil {
			return err
		}
		p, err := pass(b, "passphrase", true)
		if err != nil {
			return err
		}
		if err := s.disable(p); err != nil {
			return err
		}
		ok(w)
		return nil
	})
	rt.handle("POST /api/v1/vault/change-passphrase", func(w http.ResponseWriter, r *http.Request) error {
		b, err := object(r)
		if err != nil {
			return err
		}
		cur, err := pass(b, "current", true)
		if err != nil {
			return err
		}
		next, err := pass(b, "next", true)
		if err != nil {
			return err
		}
		if err := s.changePassphrase(cur, next); err != nil {
			return err
		}
		ok(w)
		return nil
	})
	rt.handle("POST /api/v1/vault/recovery-key", func(w http.ResponseWriter, r *http.Request) error {
		b, err := object(r)
		if err != nil {
			return err
		}
		p, err := pass(b, "passphrase", true)
		if err != nil {
			return err
		}
		rk, err := s.newRecoveryKey(p)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"recoveryKey": rk})
		return nil
	})
	rt.handle("POST /api/v1/vault/lock", func(w http.ResponseWriter, r *http.Request) error {
		if err := s.lockNow(); err != nil {
			return err
		}
		ok(w)
		return nil
	})
}
