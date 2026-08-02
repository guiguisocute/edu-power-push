/*
Package bdfairy 实现 internal/provider 契约。
协议为微信内嵌 H5：选校区→选表→读页面。
余额与月账单解析 HTML；日明细走 JSON。
schools.json 为实测学校清单。
*/
package bdfairy

import (
	_ "embed"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
)

// Name 为注册名，亦为 PROVIDER 取值。
const Name = "bdfairy"

// defaultBaseURL 为默认入口，可用 ELECTRICITY_BASE_URL 覆盖。
const defaultBaseURL = "http://bd.bdfairy.cn"

// schools.json 随二进制嵌入，随实现更换。
//
//go:embed schools.json
var schoolsJSON []byte

type registry struct {
	Note      string            `json:"note"`
	UpdatedAt string            `json:"updated_at"`
	Count     int               `json:"count"`
	Schools   []provider.School `json:"schools"`
}

var (
	schoolsOnce sync.Once
	schoolList  []provider.School
)

func loadSchools() []provider.School {
	schoolsOnce.Do(func() {
		var doc registry
		if err := json.Unmarshal(schoolsJSON, &doc); err != nil {
			// 清单损坏时退化为无清单，服务仍可启动。
			schoolList = []provider.School{}
			return
		}
		schoolList = doc.Schools
	})
	return schoolList
}

type bdfairyProvider struct{}

func (bdfairyProvider) Name() string        { return Name }
func (bdfairyProvider) DisplayName() string { return "电精灵 / 北电系 H5" }

func (bdfairyProvider) Schools() []provider.School {
	src := loadSchools()
	// 返回副本，避免调用方改动共享切片。
	out := make([]provider.School, len(src))
	copy(out, src)
	return out
}

func (bdfairyProvider) DefaultBaseURL() string { return defaultBaseURL }

func (bdfairyProvider) Open(cfg provider.SchoolConfig) (provider.Querier, error) {
	if strings.TrimSpace(cfg.AreaID) == "" {
		return nil, errors.New("bdfairy: area id is required")
	}
	base := cfg.BaseURL
	if base == nil {
		parsed, err := url.Parse(defaultBaseURL)
		if err != nil {
			return nil, err
		}
		base = parsed
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return NewClient(base, cfg.AreaID, cfg.AreaName, timeout)
}

func init() { provider.Register(bdfairyProvider{}) }
