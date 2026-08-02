/*
Package school 绑定面板学校配置与运行中的查询器。
路径：system_settings → Binding → provider.Dynamic。
Watch 周期刷新，无需重启进程。
本轮已取到的查询器用到结束，换挡发生在两轮之间。
*/
package school

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/edu-power-push/edu-power-push/backend/internal/config"
	"github.com/edu-power-push/edu-power-push/backend/internal/provider"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/edu-power-push/edu-power-push/backend/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
Binding 描述采集目标。
Provider 空表示用唯一注册实现。
AreaID 空表示未选学校：服务可起，采集器跳过。
*/
type Binding struct {
	Provider string `json:"provider"`
	AreaID   string `json:"area_id"`
	AreaName string `json:"area_name"`
	BaseURL  string `json:"base_url"`
}

func (b Binding) Configured() bool { return strings.TrimSpace(b.AreaID) != "" }

// FromConfig 读取 .env 绑定，供面板未配置时兜底。
func FromConfig(cfg config.Config) Binding {
	b := Binding{
		Provider: strings.TrimSpace(cfg.Upstream.Provider),
		AreaID:   strings.TrimSpace(cfg.Upstream.AreaID),
		AreaName: strings.TrimSpace(cfg.Upstream.AreaName),
	}
	if cfg.Upstream.BaseURL != nil {
		b.BaseURL = cfg.Upstream.BaseURL.String()
	}
	return b
}

// Record 附带来源，供面板区分 panel 与 env。
type Record struct {
	Binding   Binding
	Source    string // "panel" | "env"
	Version   int64
	UpdatedAt *time.Time
	UpdatedBy string
}

// Load 优先用面板绑定，未存过则回落 .env。
func Load(ctx context.Context, pool *pgxpool.Pool, box *secrets.Box, fallback Binding) (Record, error) {
	out := Record{Binding: fallback, Source: "env"}
	rec, err := storage.GetSetting(ctx, pool, box, storage.SettingKeySchool)
	if errors.Is(err, storage.ErrSettingNotFound) {
		return out, nil
	}
	if err != nil {
		return Record{}, err
	}
	var saved Binding
	if err := json.Unmarshal(rec.Value, &saved); err != nil {
		return Record{}, fmt.Errorf("decode school setting: %w", err)
	}
	// 面板已存且 AreaID 空时禁止回落 .env，保留停采意图。
	out.Binding, out.Source, out.Version = saved, "panel", rec.Version
	updatedAt := rec.UpdatedAt
	out.UpdatedAt, out.UpdatedBy = &updatedAt, rec.UpdatedBy
	return out, nil
}

// Save 写入绑定。调用方校验 AreaID 属于 Provider 清单。
func Save(ctx context.Context, pool *pgxpool.Pool, box *secrets.Box, b Binding, updatedBy string) (Record, error) {
	b.Provider = strings.TrimSpace(b.Provider)
	b.AreaID = strings.TrimSpace(b.AreaID)
	b.AreaName = strings.TrimSpace(b.AreaName)
	b.BaseURL = strings.TrimSpace(b.BaseURL)
	rec, err := storage.SaveSetting(ctx, pool, box, storage.SettingKeySchool, b, nil, updatedBy)
	if err != nil {
		return Record{}, err
	}
	updatedAt := rec.UpdatedAt
	return Record{Binding: b, Source: "panel", Version: rec.Version, UpdatedAt: &updatedAt, UpdatedBy: rec.UpdatedBy}, nil
}

/*
Resolve 将绑定打开为查询器。
未选学校返回 (nil, nil)，采集器据此跳过本轮。
*/
func Resolve(b Binding, timeout time.Duration) (provider.Querier, provider.SchoolConfig, error) {
	if !b.Configured() {
		return nil, provider.SchoolConfig{}, nil
	}
	impl, err := provider.Resolve(b.Provider)
	if err != nil {
		return nil, provider.SchoolConfig{}, err
	}
	raw := strings.TrimSpace(b.BaseURL)
	if raw == "" {
		raw = impl.DefaultBaseURL()
	}
	if raw == "" {
		return nil, provider.SchoolConfig{}, fmt.Errorf("provider %q needs an explicit base URL", impl.Name())
	}
	base, err := url.Parse(raw)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, provider.SchoolConfig{}, fmt.Errorf("provider %q base URL is not absolute: %q", impl.Name(), raw)
	}
	/* 缺学校名时从实现清单补全，避免日志仅有 area id。 */
	name := b.AreaName
	if name == "" {
		for _, s := range impl.Schools() {
			if s.ID == b.AreaID {
				name = s.Name
				break
			}
		}
	}
	cfg := provider.SchoolConfig{AreaID: b.AreaID, AreaName: name, BaseURL: base, Timeout: timeout}
	querier, err := impl.Open(cfg)
	if err != nil {
		return nil, provider.SchoolConfig{}, err
	}
	return querier, cfg, nil
}

/*
Binder 让 provider.Dynamic 跟随库内设置。
Refresh 幂等：绑定未变不重建客户端。
*/
type Binder struct {
	pool     *pgxpool.Pool
	box      *secrets.Box
	fallback Binding
	timeout  time.Duration
	dynamic  *provider.Dynamic
	logger   *slog.Logger

	applied Binding
	loaded  bool
}

func NewBinder(
	pool *pgxpool.Pool, box *secrets.Box, fallback Binding,
	timeout time.Duration, dynamic *provider.Dynamic, logger *slog.Logger,
) *Binder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Binder{pool: pool, box: box, fallback: fallback, timeout: timeout, dynamic: dynamic, logger: logger}
}

// Refresh 在绑定变化时替换 Dynamic 查询器。
func (b *Binder) Refresh(ctx context.Context) error {
	record, err := Load(ctx, b.pool, b.box, b.fallback)
	if err != nil {
		return err
	}
	if b.loaded && record.Binding == b.applied {
		return nil
	}
	querier, cfg, err := Resolve(record.Binding, b.timeout)
	if err != nil {
		/* 配错时保留上一份可用绑定，正在采集的学校继续采。 */
		b.logger.Error("apply school binding", "error", err, "source", record.Source, "area_id", record.Binding.AreaID)
		return err
	}
	b.dynamic.Set(querier, cfg)
	b.applied, b.loaded = record.Binding, true
	if querier == nil {
		b.logger.Warn("no school configured; collectors will skip runs", "source", record.Source)
	} else {
		b.logger.Info("school binding applied",
			"source", record.Source, "provider", record.Binding.Provider,
			"area_id", cfg.AreaID, "area_name", cfg.AreaName)
	}
	return nil
}

/*
Watch 按 interval 刷新，直到 ctx 结束。
刷新失败只记日志，保留当前绑定。
*/
func (b *Binder) Watch(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := b.Refresh(ctx); err != nil && ctx.Err() == nil {
				b.logger.Warn("refresh school binding", "error", err)
			}
		}
	}
}
