package secrets

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testKey(fill byte) string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = fill + byte(i)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := New(testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	if !box.Enabled() {
		t.Fatal("box should be enabled")
	}
	sealed, err := box.Seal(map[string]string{"api_key": "re_secret_value", "pass": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "re_secret_value") {
		t.Fatal("plaintext leaked into ciphertext")
	}
	got, err := box.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if got["api_key"] != "re_secret_value" || got["pass"] != "hunter2" {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func TestOpenWithWrongKey(t *testing.T) {
	a, _ := New(testKey(1))
	b, _ := New(testKey(90))
	sealed, err := a.Seal(map[string]string{"api_key": "x"})
	if err != nil {
		t.Fatal(err)
	}
	// 换密钥必须报 ErrWrongKey，禁止静默当未配置。
	if _, err := b.Open(sealed); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("expected ErrWrongKey, got %v", err)
	}
	if a.KeyID() == b.KeyID() {
		t.Fatal("different keys must have different fingerprints")
	}
}

func TestDisabledBox(t *testing.T) {
	box, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if box.Enabled() {
		t.Fatal("empty key must yield a disabled box")
	}
	// 无密钥时写凭证必须拒绝，禁止明文入库。
	if _, err := box.Seal(map[string]string{"api_key": "x"}); !errors.Is(err, ErrNoKey) {
		t.Fatalf("expected ErrNoKey, got %v", err)
	}
	// 无凭证字段时 Seal 不报错。
	if sealed, err := box.Seal(nil); err != nil || sealed != nil {
		t.Fatalf("empty seal: %v %v", sealed, err)
	}
	if got, err := box.Open(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty open: %v %v", got, err)
	}
}

func TestNewRejectsShortKey(t *testing.T) {
	if _, err := New(base64.StdEncoding.EncodeToString([]byte("too-short"))); err == nil {
		t.Fatal("expected rejection of a short key")
	}
}

func TestMask(t *testing.T) {
	if got := Mask("re_ExampleKeyValue"); got != "re_E••••alue" {
		t.Fatalf("got %q", got)
	}
	if got := Mask("short"); got != "••••••••" {
		t.Fatalf("short: %q", got)
	}
	if got := Mask(""); got != "" {
		t.Fatalf("empty: %q", got)
	}
}
