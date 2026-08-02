package auth

import (
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery staple", hash) {
		t.Fatal("correct password did not verify")
	}
	if VerifyPassword("wrong password", hash) {
		t.Fatal("wrong password verified")
	}
}

func TestAccessAndRefreshClaimsAreSeparated(t *testing.T) {
	manager, err := NewManager(config.Auth{
		JWTSecret:  "unit-test-only-jwt-signing-key-material",
		Issuer:     "test",
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	familyID := NewID()
	access, _, err := manager.SignAccess("user-id", familyID)
	if err != nil {
		t.Fatal(err)
	}
	refresh, _, err := manager.SignRefresh("user-id", familyID, NewID())
	if err != nil {
		t.Fatal(err)
	}
	accessClaims, err := manager.ParseAccess(access)
	if err != nil {
		t.Fatalf("parse access: %v", err)
	}
	if accessClaims.FamilyID != familyID {
		t.Fatalf("access family = %q, want %q", accessClaims.FamilyID, familyID)
	}
	if _, err := manager.ParseRefresh(refresh); err != nil {
		t.Fatalf("parse refresh: %v", err)
	}
	if _, err := manager.ParseAccess(refresh); err == nil {
		t.Fatal("refresh token accepted as access token")
	}
	if _, err := manager.ParseRefresh(access); err == nil {
		t.Fatal("access token accepted as refresh token")
	}
}

func TestNewIDLooksLikeUUID(t *testing.T) {
	id := NewID()
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("unexpected ID %q", id)
	}
}
