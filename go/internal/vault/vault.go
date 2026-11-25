// Package vault manages the keys for optional encryption at rest. It is the Go port of packages/server/src/vault.ts
// and reads and writes the same vault.json.
//
// A random 256-bit master key encrypts the data. It is stored wrapped (AES-256-GCM) under two independent
// key-encryption keys: one derived from the passphrase and one from a generated recovery key, both with scrypt.
// Changing the passphrase re-wraps the master key, so no data is rewritten. vault.json holds only salts, the KDF cost
// and the two wrapped keys; it contains nothing that identifies your content.
package vault

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"golang.org/x/crypto/scrypt"
	"golang.org/x/text/unicode/norm"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/domain"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
)

// File is the vault file name inside the data folder.
const File = "vault.json"

// MinPassphrase is the shortest passphrase accepted, in UTF-16 units after NFKC (as JavaScript counts).
const MinPassphrase = 10

// KDF is the scrypt cost.
type KDF struct {
	Algo string `json:"algo"`
	N    int    `json:"N"`
	R    int    `json:"r"`
	P    int    `json:"p"`
}

// DefaultKDF is the cost new vaults use.
var DefaultKDF = KDF{Algo: "scrypt", N: 1 << 15, R: 8, P: 1}

func (k KDF) valid() bool {
	return k.Algo == "scrypt" && k.N >= 1024 && k.N <= 1<<20 && k.R >= 1 && k.R <= 16 && k.P >= 1 && k.P <= 4
}

type wrapped struct {
	Salt string `json:"salt"`
	Wrap string `json:"wrap"`
}

type data struct {
	Version    int     `json:"version"`
	KDF        KDF     `json:"kdf"`
	Passphrase wrapped `json:"passphrase"`
	Recovery   wrapped `json:"recovery"`
	CreatedAt  string  `json:"createdAt"`
}

// Vault reads and writes <data>/vault.json.
type Vault struct {
	dir string
	kdf KDF
}

// New returns the vault of a data folder. A zero kdf means DefaultKDF.
func New(dataDir string, kdf KDF) *Vault {
	if kdf == (KDF{}) {
		kdf = DefaultKDF
	}
	return &Vault{dir: dataDir, kdf: kdf}
}

// Path is the vault file.
func (v *Vault) Path() string { return filepath.Join(v.dir, File) }

// Enabled reports whether the library is encrypted (vault.json exists).
func (v *Vault) Enabled() bool {
	_, err := os.Stat(v.Path())
	return err == nil
}

func badCredentials() error {
	return apperr.New(401, "BAD_CREDENTIALS", "Wrong passphrase or recovery key")
}

// derive runs scrypt over the NFKC form of secret, as the TypeScript version does.
func derive(secret string, salt []byte, k KDF) ([]byte, error) {
	if 128*k.N*k.R > 256*1024*1024 { // Node's maxmem
		return nil, errors.New("vault: scrypt parameters exceed the memory limit")
	}
	return scrypt.Key([]byte(norm.NFKC.String(secret)), salt, k.N, k.R, k.P, 32)
}

func (v *Vault) read() (data, error) {
	damaged := func(err error) error {
		return apperr.New(500, "VAULT_DAMAGED", "vault.json is unreadable: %s", err.Error())
	}
	raw, err := os.ReadFile(v.Path())
	if err != nil {
		return data{}, damaged(err)
	}
	var d data
	if err := json.Unmarshal(raw, &d); err != nil {
		return data{}, damaged(err)
	}
	if d.Version != 1 || !d.KDF.valid() || d.CreatedAt == "" {
		return data{}, damaged(errors.New("unexpected contents"))
	}
	return d, nil
}

func (v *Vault) write(d data) error { return jsonstore.AtomicWriteJSON(v.Path(), d) }

func wrap(key []byte, secret string, k KDF) (wrapped, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return wrapped{}, err
	}
	kek, err := derive(secret, salt, k)
	if err != nil {
		return wrapped{}, err
	}
	sealed, err := jsonstore.SealRaw(kek, key)
	if err != nil {
		return wrapped{}, err
	}
	return wrapped{Salt: base64.StdEncoding.EncodeToString(salt), Wrap: base64.StdEncoding.EncodeToString(sealed)}, nil
}

func unwrap(w wrapped, secret string, k KDF) ([]byte, error) {
	salt, err := base64.StdEncoding.DecodeString(w.Salt)
	if err != nil {
		return nil, apperr.New(500, "VAULT_DAMAGED", "vault.json is unreadable: bad salt")
	}
	blob, err := base64.StdEncoding.DecodeString(w.Wrap)
	if err != nil {
		return nil, apperr.New(500, "VAULT_DAMAGED", "vault.json is unreadable: bad wrapped key")
	}
	kek, err := derive(secret, salt, k)
	if err != nil {
		return nil, err
	}
	key, err := jsonstore.OpenRaw(kek, blob)
	if err != nil {
		if jsonstore.IsCode(err, jsonstore.CodeDecryptFailed) {
			return nil, badCredentials()
		}
		return nil, err
	}
	return key, nil
}

// CheckPassphrase rejects passphrases shorter than MinPassphrase.
func CheckPassphrase(p string) error {
	if len(utf16.Encode([]rune(norm.NFKC.String(p)))) < MinPassphrase {
		return apperr.New(400, "WEAK_PASSPHRASE", "Use at least %d characters", MinPassphrase)
	}
	return nil
}

// Create makes a vault with a fresh master key and returns the key and the one-time recovery key.
func (v *Vault) Create(passphrase string) (key []byte, recoveryKey string, err error) {
	if err := CheckPassphrase(passphrase); err != nil {
		return nil, "", err
	}
	if v.Enabled() {
		return nil, "", apperr.New(409, "ALREADY_ENABLED", "Already enabled")
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", err
	}
	if recoveryKey, err = GenerateRecoveryKey(); err != nil {
		return nil, "", err
	}
	d := data{Version: 1, KDF: v.kdf, CreatedAt: domain.NowISO()}
	if d.Passphrase, err = wrap(key, passphrase, v.kdf); err != nil {
		return nil, "", err
	}
	if d.Recovery, err = wrap(key, NormalizeRecoveryKey(recoveryKey), v.kdf); err != nil {
		return nil, "", err
	}
	if err := v.write(d); err != nil {
		return nil, "", err
	}
	return key, recoveryKey, nil
}

// UnlockWithPassphrase returns the master key, or a 401 BAD_CREDENTIALS error.
func (v *Vault) UnlockWithPassphrase(p string) ([]byte, error) {
	d, err := v.read()
	if err != nil {
		return nil, err
	}
	return unwrap(d.Passphrase, p, d.KDF)
}

// UnlockWithRecoveryKey returns the master key, or a 401 BAD_CREDENTIALS error.
func (v *Vault) UnlockWithRecoveryKey(rk string) ([]byte, error) {
	d, err := v.read()
	if err != nil {
		return nil, err
	}
	return unwrap(d.Recovery, NormalizeRecoveryKey(rk), d.KDF)
}

// SetPassphrase re-wraps the master key under a new passphrase (the data is untouched).
func (v *Vault) SetPassphrase(key []byte, p string) error {
	if err := CheckPassphrase(p); err != nil {
		return err
	}
	d, err := v.read()
	if err != nil {
		return err
	}
	if d.Passphrase, err = wrap(key, p, d.KDF); err != nil {
		return err
	}
	return v.write(d)
}

// NewRecoveryKey replaces the recovery key; the old one stops working.
func (v *Vault) NewRecoveryKey(key []byte) (string, error) {
	d, err := v.read()
	if err != nil {
		return "", err
	}
	rk, err := GenerateRecoveryKey()
	if err != nil {
		return "", err
	}
	if d.Recovery, err = wrap(key, NormalizeRecoveryKey(rk), d.KDF); err != nil {
		return "", err
	}
	return rk, v.write(d)
}

// Remove deletes vault.json.
func (v *Vault) Remove() error {
	if err := os.Remove(v.Path()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Crockford base32: no I, L, O or U, so a hand-copied key is hard to misread.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// GenerateRecoveryKey returns 160 random bits as eight dash-separated groups of four characters.
func GenerateRecoveryKey() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var acc uint32
	bits := 0
	out := make([]byte, 0, 32)
	for _, x := range b {
		acc = acc<<8 | uint32(x)
		bits += 8
		for bits >= 5 {
			out = append(out, alphabet[(acc>>(bits-5))&31])
			bits -= 5
		}
	}
	groups := make([]string, 0, 8)
	for i := 0; i < len(out); i += 4 {
		groups = append(groups, string(out[i:i+4]))
	}
	return strings.Join(groups, "-"), nil
}

// NormalizeRecoveryKey tolerates case, spaces, dashes and the usual look-alike letters.
func NormalizeRecoveryKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		switch {
		case r == 'O':
			b.WriteByte('0')
		case r == 'I' || r == 'L':
			b.WriteByte('1')
		case r >= '0' && r <= '9' || r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Limiter slows down guessing: five free tries, then a wait of 1 s doubling up to 30 s.
type Limiter struct {
	mu       sync.Mutex
	failures int
	until    time.Time
	now      func() time.Time
}

func (l *Limiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// Check returns a 429 error while a wait is in force.
func (l *Limiter) Check() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if wait := l.until.Sub(l.clock()); wait > 0 {
		return apperr.New(429, "TOO_MANY_ATTEMPTS", "Try again in %d s", int(math.Ceil(wait.Seconds())))
	}
	return nil
}

// Fail records a wrong guess.
func (l *Limiter) Fail() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures++
	if l.failures > 5 {
		ms := math.Min(30_000, 1000*math.Pow(2, float64(l.failures-6)))
		l.until = l.clock().Add(time.Duration(ms) * time.Millisecond)
	}
}

// OK resets the counter after a right guess.
func (l *Limiter) OK() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures = 0
	l.until = time.Time{}
}

// String is for debugging only and never shows key material.
func (v *Vault) String() string { return fmt.Sprintf("vault(%s)", v.dir) }
