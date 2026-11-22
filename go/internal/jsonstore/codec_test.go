package jsonstore

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sentinel = "ZEBRA-SENTINEL-4471"

func key32(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestCodecRoundTripsBytesTextAndLinesWithAFreshIVEveryTime(t *testing.T) {
	c := must(NewAESGCMCodec(key32(t)))
	a, b := must(c.Encode([]byte(sentinel))), must(c.Encode([]byte(sentinel)))
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same text are identical")
	}
	if !IsEncrypted(a) || !bytes.HasPrefix(a, []byte("CKENC1B\x00")) || bytes.Contains(a, []byte(sentinel)) {
		t.Fatal("not a proper encrypted file")
	}
	if got := must(c.Decode(a)); string(got) != sentinel {
		t.Fatalf("decode = %q", got)
	}
	line := must(c.EncodeLine(sentinel))
	if strings.Contains(line, sentinel) || !IsEncryptedLine(line) {
		t.Fatal("bad encrypted line")
	}
	if got := must(c.DecodeLine(line)); got != sentinel {
		t.Fatalf("line = %q", got)
	}
	bin := make([]byte, 5000)
	_, _ = rand.Read(bin)
	if got := must(c.Decode(must(c.Encode(bin)))); !bytes.Equal(got, bin) {
		t.Fatal("binary round trip failed")
	}
}

func TestCodecFailsClosedOnWrongKeyTamperingAndTruncation(t *testing.T) {
	c := must(NewAESGCMCodec(key32(t)))
	enc := must(c.Encode([]byte("secret")))
	if _, err := must(NewAESGCMCodec(key32(t))).Decode(enc); !IsCode(err, CodeDecryptFailed) {
		t.Fatalf("wrong key: %v", err)
	}
	flipped := append([]byte(nil), enc...)
	flipped[len(flipped)-1] ^= 1
	if _, err := c.Decode(flipped); !IsCode(err, CodeDecryptFailed) {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := c.Decode(enc[:20]); !IsCode(err, CodeDecryptFailed) {
		t.Fatalf("truncated: %v", err)
	}
	if _, err := OpenRaw(key32(t), []byte{1, 2, 3}); !IsCode(err, CodeDecryptFailed) {
		t.Fatalf("short blob: %v", err)
	}
}

func TestPlaintextPassesThroughWhenKeyedButCiphertextIsRefusedWhenUnkeyed(t *testing.T) {
	c := must(NewAESGCMCodec(key32(t)))
	if got := must(c.Decode([]byte("plain"))); string(got) != "plain" {
		t.Fatal("plaintext did not pass through")
	}
	enc := must(c.Encode([]byte("x")))
	if _, err := (PlainCodec{}).Decode(enc); !IsCode(err, CodeEncrypted) {
		t.Fatalf("unkeyed decode: %v", err)
	}
	if _, err := (PlainCodec{}).DecodeLine(must(c.EncodeLine("x"))); !IsCode(err, CodeEncrypted) {
		t.Fatalf("unkeyed line: %v", err)
	}
	k := key32(t)
	if got := must(OpenRaw(k, must(SealRaw(k, []byte("k"))))); string(got) != "k" {
		t.Fatal("seal/open round trip failed")
	}
	if _, err := NewAESGCMCodec(make([]byte, 16)); err == nil {
		t.Fatal("a 16-byte key must be rejected")
	}
}

func TestEncryptedStoreWritesCiphertextAndRewritesPlainFilesOnKeyChange(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, nil, nil)
	f := must(Open(s, "data/a.json", opts()))
	must(f.Update(func(d *doc) error { d.Items = append(d.Items, item{ID: sentinel, N: 1}); return nil }))
	if !strings.Contains(read(t, filepath.Join(dir, "data/a.json")), sentinel) {
		t.Fatal("plain file should hold the sentinel")
	}
	s.Codec.Set(must(NewAESGCMCodec(key32(t))))
	if err := s.RewriteAll(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "data/a.json"))
	if !IsEncrypted(raw) || bytes.Contains(raw, []byte(sentinel)) {
		t.Fatal("file was not encrypted")
	}
	must(f.Update(func(d *doc) error { d.Items = append(d.Items, item{ID: "more", N: 2}); return nil }))
	if len(f.Data().Items) != 2 {
		t.Fatal("update after encryption failed")
	}
}

func TestEncryptedFilesNeverQuarantineOrGetOverwrittenWithoutTheKey(t *testing.T) {
	dir := t.TempDir()
	key := key32(t)
	s1 := NewStore(dir, nil, NewCodecHolder(must(NewAESGCMCodec(key))))
	f := must(Open(s1, "data/a.json", opts()))
	must(f.Update(func(d *doc) error { d.Items = append(d.Items, item{ID: sentinel, N: 1}); return nil }))
	before, _ := os.ReadFile(filepath.Join(dir, "data/a.json"))

	if _, err := Open(NewStore(dir, nil, nil), "data/a.json", opts()); !IsCode(err, CodeEncrypted) {
		t.Fatalf("locked store: %v", err)
	}
	wrong := NewStore(dir, nil, NewCodecHolder(must(NewAESGCMCodec(key32(t)))))
	if _, err := Open(wrong, "data/a.json", opts()); !IsCode(err, CodeDecryptFailed) {
		t.Fatalf("wrong key: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "data/a.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("encrypted file was modified")
	}
	if _, err := os.Stat(filepath.Join(dir, ".quarantine")); err == nil {
		t.Fatal("an undecryptable file was quarantined")
	}
	s2 := NewStore(dir, nil, NewCodecHolder(must(NewAESGCMCodec(key))))
	if got := must(Open(s2, "data/a.json", opts())).Data().Items[0].ID; got != sentinel {
		t.Fatalf("with the key: %q", got)
	}
}

func TestBackupsOfAnEncryptedStoreHoldCiphertextAndCanRestoreADamagedFile(t *testing.T) {
	dir := t.TempDir()
	key := key32(t)
	backups := NewBackupManager(BackupOptions{DataDir: dir, BackupDir: filepath.Join(dir, "backups")})
	s := NewStore(dir, backups, NewCodecHolder(must(NewAESGCMCodec(key))))
	f := must(Open(s, "data/a.json", opts()))
	must(f.Update(func(d *doc) error { d.Items = append(d.Items, item{ID: sentinel, N: 1}); return nil }))
	b := must(backups.Create("test"))
	zipBytes, _ := os.ReadFile(b.File)
	if bytes.Contains(zipBytes, []byte(sentinel)) {
		t.Fatal("sentinel visible in the backup zip")
	}
	write(t, filepath.Join(dir, "data/a.json"), "garbage{")
	s2 := NewStore(dir, backups, NewCodecHolder(must(NewAESGCMCodec(key))))
	if got := must(Open(s2, "data/a.json", opts())).Data().Items[0].ID; got != sentinel {
		t.Fatalf("restored %q", got)
	}
}
