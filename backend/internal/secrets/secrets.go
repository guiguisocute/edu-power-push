/*
Package secrets 加密落库凭证。
密钥仅来自 SETTINGS_ENCRYPTION_KEY，禁止入库。
未配密钥时 Box 禁用：读明文可，写凭证拒绝。
*/
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	// ErrNoKey：未配 SETTINGS_ENCRYPTION_KEY，禁止写凭证。
	ErrNoKey = errors.New("secrets: SETTINGS_ENCRYPTION_KEY is not configured")
	// ErrWrongKey：密文由另一密钥封装。
	ErrWrongKey = errors.New("secrets: ciphertext was sealed with a different key")
)

// Box 为 AES-256-GCM 封装器。零值表示未配密钥。
type Box struct {
	aead  cipher.AEAD
	keyID string
}

/*
New 解析密钥：base64 或 hex，必须 32 字节。
空串返回禁用 Box，本地开发不必先生成密钥。
*/
func New(raw string) (*Box, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return &Box{}, nil
	}
	key, err := decodeKey(raw)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets: SETTINGS_ENCRYPTION_KEY must decode to 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}
	// 指纹识别密钥轮换，不泄漏密钥本身。
	sum := sha256.Sum256(key)
	return &Box{aead: aead, keyID: hex.EncodeToString(sum[:4])}, nil
}

func decodeKey(raw string) ([]byte, error) {
	for _, decode := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
		hex.DecodeString,
	} {
		if key, err := decode(raw); err == nil && len(key) == 32 {
			return key, nil
		}
	}
	return nil, errors.New("secrets: SETTINGS_ENCRYPTION_KEY must be 32 bytes encoded as base64 or hex")
}

func (b *Box) Enabled() bool { return b != nil && b.aead != nil }

// KeyID 随密文存储，用于识别密钥轮换。
func (b *Box) KeyID() string {
	if !b.Enabled() {
		return ""
	}
	return b.keyID
}

// Seal 封装凭证字段。空 map 返回 nil。
func (b *Box) Seal(fields map[string]string) ([]byte, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	if !b.Enabled() {
		return nil, ErrNoKey
	}
	plain, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("secrets: encode: %w", err)
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secrets: nonce: %w", err)
	}
	// nonce 前置，解封时按 NonceSize 切开。
	return b.aead.Seal(nonce, nonce, plain, nil), nil
}

// Open 解密封装。空密文返回空 map。
func (b *Box) Open(sealed []byte) (map[string]string, error) {
	if len(sealed) == 0 {
		return map[string]string{}, nil
	}
	if !b.Enabled() {
		return nil, ErrNoKey
	}
	if len(sealed) < b.aead.NonceSize() {
		return nil, ErrWrongKey
	}
	nonce, body := sealed[:b.aead.NonceSize()], sealed[b.aead.NonceSize():]
	plain, err := b.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, ErrWrongKey
	}
	fields := map[string]string{}
	if err := json.Unmarshal(plain, &fields); err != nil {
		return nil, fmt.Errorf("secrets: decode: %w", err)
	}
	return fields, nil
}

/*
Mask 遮盖凭证后下发面板。
保留前 4 与后 4 位；过短则全遮。
*/
func Mask(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	if len(runes) <= 8 {
		return "••••••••"
	}
	return string(runes[:4]) + "••••" + string(runes[len(runes)-4:])
}
