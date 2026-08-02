package bdfairy

import (
	"net/url"
	"testing"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
)

// 嵌入清单必须非空且 id 唯一。
func TestEmbeddedSchoolsAreUsable(t *testing.T) {
	schools := (bdfairyProvider{}).Schools()
	if len(schools) == 0 {
		t.Fatal("schools.json produced an empty registry")
	}
	seen := map[string]bool{}
	for _, s := range schools {
		if s.ID == "" || s.Name == "" {
			t.Fatalf("school with an empty id or name: %+v", s)
		}
		if seen[s.ID] {
			t.Fatalf("duplicate school id %q", s.ID)
		}
		seen[s.ID] = true
	}
}

// Schools 返回副本，禁止改动共享切片。
func TestSchoolsReturnsACopy(t *testing.T) {
	first := (bdfairyProvider{}).Schools()
	if len(first) == 0 {
		t.Skip("no schools embedded")
	}
	first[0].Name = "被改过了"
	if (bdfairyProvider{}).Schools()[0].Name == "被改过了" {
		t.Fatal("Schools() handed out the shared slice")
	}
}

func TestOpenRequiresAnArea(t *testing.T) {
	if _, err := (bdfairyProvider{}).Open(provider.SchoolConfig{}); err == nil {
		t.Fatal("Open must reject an empty area id")
	}
	base, _ := url.Parse("http://example.test")
	if _, err := (bdfairyProvider{}).Open(provider.SchoolConfig{
		AreaID: "83", BaseURL: base, Timeout: time.Second,
	}); err != nil {
		t.Fatalf("Open with a valid area: %v", err)
	}
	// 无地址时回落默认入口。
	if _, err := (bdfairyProvider{}).Open(provider.SchoolConfig{AreaID: "83"}); err != nil {
		t.Fatalf("Open without an explicit base URL: %v", err)
	}
}

// init 必须登记，否则 PROVIDER=bdfairy 解析失败。
func TestProviderIsRegistered(t *testing.T) {
	impl, ok := provider.Get(Name)
	if !ok {
		t.Fatalf("provider %q is not registered", Name)
	}
	if impl.DefaultBaseURL() == "" {
		t.Fatal("DefaultBaseURL must not be empty for this provider")
	}
}
