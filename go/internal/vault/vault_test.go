package vault

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/reeper316099/Cipherkeep/go/internal/apperr"
	"github.com/reeper316099/Cipherkeep/go/internal/jsonstore"
)

// Public test data, shared with scripts/gen-vault-fixtures.ts and packages/server/test/vault-compat.test.ts.
const (
	compatPassphrase = "compat passphrase, public test data"
	compatSecret     = "vault compat ok: ünïcode 😀"
	compatRoot       = "../../../fixtures/compat/vault"
)

var cheap = KDF{Algo: "scrypt", N: 1024, R: 8, P: 1}

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func TestCreateUnlockRewrapAndReplaceTheRecoveryKey(t *testing.T) {
	v := New(t.TempDir(), cheap)
	if v.Enabled() {
		t.Fatal("a new folder has no vault")
	}
	if _, _, err := v.Create("short"); code(err) != "WEAK_PASSPHRASE" {
		t.Fatalf("short passphrase: %v", err)
	}
	key, rk, err := v.Create("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^([0-9A-Z]{4}-){7}[0-9A-Z]{4}$`).MatchString(rk) || len(key) != 32 {
		t.Fatalf("recovery key %q, key %d bytes", rk, len(key))
	}
	if _, _, err := v.Create("correct horse battery"); code(err) != "ALREADY_ENABLED" {
		t.Fatalf("second create: %v", err)
	}
	k, err := v.UnlockWithPassphrase("correct horse battery")
	if err != nil || string(k) != string(key) {
		t.Fatalf("unlock: %v", err)
	}
	if _, err := v.UnlockWithPassphrase("not the passphrase"); code(err) != "BAD_CREDENTIALS" {
		t.Fatalf("wrong passphrase: %v", err)
	}
	// Recovery keys are forgiving about case, separators and look-alike letters.
	loose := strings.ReplaceAll(strings.ToLower(rk), "-", " ")
	if k, err := v.UnlockWithRecoveryKey(loose); err != nil || string(k) != string(key) {
		t.Fatalf("recovery: %v", err)
	}

	if err := v.SetPassphrase(key, "a brand new passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.UnlockWithPassphrase("correct horse battery"); code(err) != "BAD_CREDENTIALS" {
		t.Fatal("the old passphrase must stop working")
	}
	rk2, err := v.NewRecoveryKey(key)
	if err != nil || rk2 == rk {
		t.Fatalf("new recovery key: %v", err)
	}
	if _, err := v.UnlockWithRecoveryKey(rk); code(err) != "BAD_CREDENTIALS" {
		t.Fatal("the old recovery key must stop working")
	}
	if err := v.Remove(); err != nil || v.Enabled() {
		t.Fatalf("remove: %v", err)
	}
}

func TestDamagedVaultIsReportedNotTreatedAsAWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, File), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, cheap).UnlockWithPassphrase("whatever passphrase"); code(err) != "VAULT_DAMAGED" {
		t.Fatalf("got %v", err)
	}
}

func TestPassphraseLengthCountsLikeJavaScript(t *testing.T) {
	// Five emoji are ten UTF-16 units: long enough in JavaScript, so long enough here.
	if err := CheckPassphrase(strings.Repeat("😀", 5)); err != nil {
		t.Fatal(err)
	}
	if err := CheckPassphrase("123456789"); code(err) != "WEAK_PASSPHRASE" {
		t.Fatal("nine characters are too few")
	}
	if NormalizeRecoveryKey("abcd-oil0 i l") != "ABCD011011" {
		t.Fatal(NormalizeRecoveryKey("abcd-oil0 i l"))
	}
}

func TestLimiterGivesFiveFreeTriesThenDoublesUpTo30Seconds(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := &Limiter{now: func() time.Time { return now }}
	for i := 0; i < 5; i++ {
		l.Fail()
		if err := l.Check(); err != nil {
			t.Fatalf("try %d should be free: %v", i+1, err)
		}
	}
	waits := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for i, w := range waits {
		l.Fail()
		if err := l.Check(); code(err) != "TOO_MANY_ATTEMPTS" {
			t.Fatalf("failure %d: %v", i+6, err)
		}
		now = now.Add(w*time.Second - time.Millisecond)
		if l.Check() == nil {
			t.Fatalf("failure %d: wait should be %v", i+6, w*time.Second)
		}
		now = now.Add(time.Millisecond)
		if err := l.Check(); err != nil {
			t.Fatalf("failure %d: wait should end after %v", i+6, w*time.Second)
		}
	}
	l.OK()
	l.Fail()
	if l.Check() != nil {
		t.Fatal("a right guess resets the counter")
	}
}

// openSecret decrypts the fixture file with the master key.
func openSecret(t *testing.T, dir string, key []byte) string {
	t.Helper()
	c, err := jsonstore.NewAESGCMCodec(key)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := jsonstore.ReadDecoded(c, filepath.Join(dir, "secret.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return string(plain)
}

func TestGoUnlocksAVaultMadeByNode(t *testing.T) {
	dir := filepath.Join(compatRoot, "node")
	v := New(dir, KDF{})
	k1, err := v.UnlockWithPassphrase(compatPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	rk, err := os.ReadFile(filepath.Join(dir, "recovery.txt"))
	if err != nil {
		t.Fatal(err)
	}
	k2, err := v.UnlockWithRecoveryKey(strings.TrimSpace(string(rk)))
	if err != nil || string(k1) != string(k2) {
		t.Fatalf("recovery key gives another key: %v", err)
	}
	if got := openSecret(t, dir, k1); got != compatSecret {
		t.Fatalf("secret = %q", got)
	}
}

// TestGoWritesAVaultNodeCanUnlock checks the committed Go fixture (fixtures/compat/vault/go), which Node's
// vault-compat test reads. UPDATE_COMPAT=1 rewrites it.
func TestGoWritesAVaultNodeCanUnlock(t *testing.T) {
	dir := filepath.Join(compatRoot, "go")
	if os.Getenv("UPDATE_COMPAT") == "1" {
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		key, rk, err := New(dir, cheap).Create(compatPassphrase)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "recovery.txt"), []byte(rk+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		c, _ := jsonstore.NewAESGCMCodec(key)
		if err := jsonstore.WriteEncoded(c, filepath.Join(dir, "secret.bin"), []byte(compatSecret)); err != nil {
			t.Fatal(err)
		}
	}
	k, err := New(dir, KDF{}).UnlockWithPassphrase(compatPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	if got := openSecret(t, dir, k); got != compatSecret {
		t.Fatalf("secret = %q", got)
	}
}
