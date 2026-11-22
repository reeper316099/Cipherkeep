package jsonstore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
)

// At-rest encoding for everything the store writes. The default codec is the identity; the AES-256-GCM codec
// encrypts each file on its own with a fresh random IV.
//
// File format: 8-byte magic "CKENC1B\0", 12-byte IV, 16-byte GCM tag, ciphertext.
// Line format (append-only logs): "CKENC1:" + base64(IV | tag | ciphertext).
// Both are identical to the TypeScript version, so the two apps open each other's libraries.
var bytesMagic = []byte("CKENC1B\x00")

const (
	// LinePrefix starts every encrypted log line.
	LinePrefix = "CKENC1:"
	ivLen      = 12
	tagLen     = 16
)

// Codec converts between plain content and what is stored on disk.
type Codec interface {
	Encrypting() bool
	// Encode turns plain bytes into the bytes to put on disk.
	Encode(plain []byte) ([]byte, error)
	// Decode turns bytes read from disk into plain bytes. Plain input passes through when a key is held.
	Decode(raw []byte) ([]byte, error)
	EncodeLine(text string) (string, error)
	DecodeLine(line string) (string, error)
}

// IsEncrypted reports whether raw starts with the encrypted-file magic.
func IsEncrypted(raw []byte) bool { return bytes.HasPrefix(raw, bytesMagic) }

// IsEncryptedLine reports whether line is an encrypted log line.
func IsEncryptedLine(line string) bool { return strings.HasPrefix(line, LinePrefix) }

func encErr(code, file string) *Error {
	if code == CodeEncrypted {
		return newErr(code, "Data is encrypted and the store is locked", file)
	}
	return newErr(code, "Could not decrypt data (wrong key or damaged file)", file)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("AES-256 key must be 32 bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// SealRaw is an AES-256-GCM seal laid out as IV | tag | ciphertext.
func SealRaw(key, plain []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	iv := make([]byte, ivLen)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	sealed := g.Seal(nil, iv, plain, nil) // ciphertext | tag
	ct, tag := sealed[:len(sealed)-tagLen], sealed[len(sealed)-tagLen:]
	out := make([]byte, 0, ivLen+tagLen+len(ct))
	out = append(out, iv...)
	out = append(out, tag...)
	out = append(out, ct...)
	return out, nil
}

// OpenRaw is the inverse of SealRaw. It returns a DECRYPT_FAILED error for a wrong key or any tampering.
func OpenRaw(key, blob []byte) ([]byte, error) {
	if len(blob) < ivLen+tagLen {
		return nil, encErr(CodeDecryptFailed, "")
	}
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	iv, tag, ct := blob[:ivLen], blob[ivLen:ivLen+tagLen], blob[ivLen+tagLen:]
	joined := make([]byte, 0, len(ct)+tagLen)
	joined = append(joined, ct...)
	joined = append(joined, tag...)
	plain, err := g.Open(nil, iv, joined, nil)
	if err != nil {
		return nil, encErr(CodeDecryptFailed, "")
	}
	return plain, nil
}

// PlainCodec stores everything as it is and refuses encrypted data it cannot read.
type PlainCodec struct{}

func (PlainCodec) Encrypting() bool                       { return false }
func (PlainCodec) Encode(plain []byte) ([]byte, error)    { return append([]byte(nil), plain...), nil }
func (PlainCodec) EncodeLine(text string) (string, error) { return text, nil }
func (PlainCodec) Decode(raw []byte) ([]byte, error) {
	if IsEncrypted(raw) {
		return nil, encErr(CodeEncrypted, "")
	}
	return append([]byte(nil), raw...), nil
}
func (PlainCodec) DecodeLine(line string) (string, error) {
	if IsEncryptedLine(line) {
		return "", encErr(CodeEncrypted, "")
	}
	return line, nil
}

// AESGCMCodec encrypts with a 32-byte key. Unencrypted input still decodes, so a half-migrated library stays
// readable.
type AESGCMCodec struct{ key []byte }

// NewAESGCMCodec returns a codec for a 32-byte key.
func NewAESGCMCodec(key []byte) (*AESGCMCodec, error) {
	if _, err := newGCM(key); err != nil {
		return nil, err
	}
	return &AESGCMCodec{key: append([]byte(nil), key...)}, nil
}

func (*AESGCMCodec) Encrypting() bool { return true }

func (c *AESGCMCodec) Encode(plain []byte) ([]byte, error) {
	sealed, err := SealRaw(c.key, plain)
	if err != nil {
		return nil, err
	}
	return append(append([]byte(nil), bytesMagic...), sealed...), nil
}

func (c *AESGCMCodec) Decode(raw []byte) ([]byte, error) {
	if !IsEncrypted(raw) {
		return append([]byte(nil), raw...), nil
	}
	return OpenRaw(c.key, raw[len(bytesMagic):])
}

func (c *AESGCMCodec) EncodeLine(text string) (string, error) {
	sealed, err := SealRaw(c.key, []byte(text))
	if err != nil {
		return "", err
	}
	return LinePrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (c *AESGCMCodec) DecodeLine(line string) (string, error) {
	if !IsEncryptedLine(line) {
		return line, nil
	}
	blob, err := base64.StdEncoding.DecodeString(line[len(LinePrefix):])
	if err != nil {
		return "", encErr(CodeDecryptFailed, "")
	}
	plain, err := OpenRaw(c.key, blob)
	return string(plain), err
}

// CodecHolder lets the codec change at runtime (encryption turned on or off) while everything keeps one reference.
type CodecHolder struct {
	mu    sync.RWMutex
	inner Codec
}

// NewCodecHolder starts with the given codec, or the plain one when nil.
func NewCodecHolder(c Codec) *CodecHolder {
	if c == nil {
		c = PlainCodec{}
	}
	return &CodecHolder{inner: c}
}

// Set swaps the codec.
func (h *CodecHolder) Set(c Codec) { h.mu.Lock(); h.inner = c; h.mu.Unlock() }

func (h *CodecHolder) get() Codec { h.mu.RLock(); defer h.mu.RUnlock(); return h.inner }

func (h *CodecHolder) Encrypting() bool                    { return h.get().Encrypting() }
func (h *CodecHolder) Encode(p []byte) ([]byte, error)     { return h.get().Encode(p) }
func (h *CodecHolder) Decode(r []byte) ([]byte, error)     { return h.get().Decode(r) }
func (h *CodecHolder) EncodeLine(t string) (string, error) { return h.get().EncodeLine(t) }
func (h *CodecHolder) DecodeLine(l string) (string, error) { return h.get().DecodeLine(l) }

// ReadDecoded reads a file and decodes it.
func ReadDecoded(c Codec, file string) ([]byte, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return c.Decode(raw)
}

// WriteEncoded encodes data and writes it atomically.
func WriteEncoded(c Codec, file string, data []byte) error {
	enc, err := c.Encode(data)
	if err != nil {
		return err
	}
	return AtomicWriteFile(file, enc)
}

// WriteJSONEncoded writes value as stable JSON through the codec.
func WriteJSONEncoded(c Codec, file string, value any) error {
	b, err := StableStringify(value)
	if err != nil {
		return err
	}
	return WriteEncoded(c, file, b)
}
