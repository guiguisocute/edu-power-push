package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

const (
	accessAudience  = "edu-power-api"
	refreshAudience = "edu-power-refresh"
	passwordMemory  = 64 * 1024
	passwordTime    = 3
	passwordThreads = 2
	passwordKeyLen  = 32
	passwordSaltLen = 16
)

var ErrInvalidToken = errors.New("invalid token")

type Claims struct {
	TokenUse string `json:"token_use"`
	FamilyID string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret     []byte
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time
}

func NewManager(cfg config.Auth) (*Manager, error) {
	if len(cfg.JWTSecret) < 32 {
		return nil, errors.New("JWT secret must contain at least 32 bytes")
	}
	if cfg.AccessTTL < time.Minute || cfg.RefreshTTL <= cfg.AccessTTL {
		return nil, errors.New("invalid access or refresh token lifetime")
	}
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, errors.New("JWT issuer is required")
	}
	return &Manager{
		secret:     []byte(cfg.JWTSecret),
		issuer:     cfg.Issuer,
		accessTTL:  cfg.AccessTTL,
		refreshTTL: cfg.RefreshTTL,
		now:        time.Now,
	}, nil
}

func (m *Manager) AccessTTL() time.Duration  { return m.accessTTL }
func (m *Manager) RefreshTTL() time.Duration { return m.refreshTTL }

func (m *Manager) SignAccess(userID, familyID string) (string, time.Time, error) {
	if familyID == "" {
		return "", time.Time{}, errors.New("access session family is required")
	}
	return m.sign(userID, "access", accessAudience, familyID, NewID(), m.accessTTL)
}

func (m *Manager) SignRefresh(userID, familyID, jti string) (string, time.Time, error) {
	if familyID == "" || jti == "" {
		return "", time.Time{}, errors.New("refresh family and token IDs are required")
	}
	return m.sign(userID, "refresh", refreshAudience, familyID, jti, m.refreshTTL)
}

func (m *Manager) sign(userID, tokenUse, audience, familyID, jti string, ttl time.Duration) (string, time.Time, error) {
	if userID == "" {
		return "", time.Time{}, errors.New("user ID is required")
	}
	now := m.now().UTC()
	expiresAt := now.Add(ttl)
	claims := Claims{
		TokenUse: tokenUse,
		FamilyID: familyID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        jti,
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	return token, expiresAt, err
}

func (m *Manager) ParseAccess(raw string) (*Claims, error) {
	return m.parse(raw, "access", accessAudience)
}

func (m *Manager) ParseRefresh(raw string) (*Claims, error) {
	return m.parse(raw, "refresh", refreshAudience)
}

func (m *Manager) parse(raw, tokenUse, audience string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		raw,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
				return nil, ErrInvalidToken
			}
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithNotBeforeRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil || !token.Valid || claims.TokenUse != tokenUse || claims.Subject == "" || claims.ID == "" {
		return nil, ErrInvalidToken
	}
	if claims.FamilyID == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func HashPassword(password string) (string, error) {
	if len(password) < 8 || len(password) > 128 {
		return "", errors.New("password must contain between 8 and 128 characters")
	}
	salt := make([]byte, passwordSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, passwordTime, passwordMemory, passwordThreads, passwordKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		passwordMemory,
		passwordTime,
		passwordThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	if memory < 7*1024 || memory > 256*1024 || iterations < 1 || iterations > 10 || threads < 1 || threads > 16 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func TokenHash(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

func UserAgentHash(value string) []byte {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func NewID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}
