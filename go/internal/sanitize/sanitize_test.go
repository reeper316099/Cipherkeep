package sanitize

import (
	"reflect"
	"strings"
	"testing"
)

func TestRedactsFlagsAddressesEmailsAndCredentials(t *testing.T) {
	r := Writeup("flag: picoCTF{s3cr3t_v4lue} at 192.168.1.20 and mail a.b@ex.org; token=abc123 Bearer abcdefgh12345678", nil)
	want := "flag: [FLAG REDACTED] at [IP] and mail [EMAIL]; token=[REDACTED] Bearer [REDACTED]"
	if r.Text != want || r.Redactions != 5 {
		t.Fatalf("got %q (%d)", r.Text, r.Redactions)
	}
}

func TestPrivateKeysAndSafeCustomPatterns(t *testing.T) {
	r := Writeup("-----BEGIN RSA PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----\nhost lab-7.internal", []string{`lab-\d+\.internal`, "(x+)+y", "["})
	if r.Text != "[PRIVATE KEY REDACTED]\nhost [REDACTED]" {
		t.Fatalf("got %q", r.Text)
	}
	if !reflect.DeepEqual(r.InvalidPatterns, []string{"(x+)+y", "["}) {
		t.Fatalf("invalid: %v", r.InvalidPatterns)
	}
	if IsSafePattern(strings.Repeat("a", 201)) || IsSafePattern("") {
		t.Fatal("long or empty patterns must be unsafe")
	}
}

func TestLeavesOrdinaryTextAndVersionNumbersAlone(t *testing.T) {
	in := "Used nmap 7.94 on port 443 (version 1.2.3)"
	if got := Writeup(in, nil); got.Text != in || got.Redactions != 0 {
		t.Fatalf("got %q (%d)", got.Text, got.Redactions)
	}
}

func TestCustomPatternsAreCaseInsensitiveAndLookaheadIsRejected(t *testing.T) {
	if got := Writeup("Host LAB-9.Internal", []string{`lab-\d+\.internal`}); got.Text != "Host [REDACTED]" {
		t.Fatalf("got %q", got.Text)
	}
	if got := Writeup("x", []string{`a(?=b)`}); len(got.InvalidPatterns) != 1 {
		t.Fatal("RE2 cannot do lookahead; the pattern should be reported, not applied")
	}
}
