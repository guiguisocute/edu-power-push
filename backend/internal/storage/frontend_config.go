package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FrontendAuthFeatures struct {
	EmailLogin   bool `json:"email_login"`
	SMSLogin     bool `json:"sms_login"`
	EmailCode    bool `json:"email_code"`
	SMSCode      bool `json:"sms_code"`
	Registration bool `json:"registration"`
	/* 第三方登录面板开关。仅控制入口是否显示。
	   是否可用由 FrontendOAuth 回报凭证是否已配置。
	   两层都为真时按钮才显示。 */
	GoogleOAuth bool `json:"google_oauth"`
	GitHubOAuth bool `json:"github_oauth"`
}

/*
FrontendOAuth 为服务端第三方登录能力。只读。
	来自运行配置。面板禁止修改。
	无凭证时禁止打开入口。
*/
type FrontendOAuth struct {
	Google bool `json:"google"`
	GitHub bool `json:"github"`
}

/*
FrontendChartFeatures 控制依赖上游能力的图表入口。
	无可靠小时级抄表时 DayRange / HourlyUsage 默认 false。
*/
type FrontendChartFeatures struct {
	DayRange    bool `json:"day_range"`
	HourlyUsage bool `json:"hourly_usage"`
}

/*
FrontendChannelCategory 为推送渠道列表中的一个分组。
	分组仅用于展示。组内顺序由 ChannelOrder 决定。
	ID 为稳定键。Name / EN 为中英标题。Desc 为说明。
*/
type FrontendChannelCategory struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	EN       string   `json:"en"`
	Desc     string   `json:"desc,omitempty"`
	Channels []string `json:"channels"`
}

type FrontendFeatures struct {
	Auth              FrontendAuthFeatures `json:"auth"`
	Channels          map[string]bool      `json:"channels"`
	ChannelComingSoon map[string]bool      `json:"channel_coming_soon,omitempty"`
	ChannelOrder      []string             `json:"channel_order,omitempty"`
	/* 不加 omitempty。清空分类必须传到用户端。
	   缺字段会被深合并当成未配置并回落默认分类。 */
	ChannelCategories []FrontendChannelCategory `json:"channel_categories"`
	Charts            FrontendChartFeatures     `json:"charts"`
}

var defaultFrontendChannelOrder = []string{
	"mail", "dingtalk", "lark", "wecom", "wecom_webhook", "feishu", "discord", "webhook", "bark", "gotify", "whatsapp", "pushplus", "serverchan_turbo", "serverchan3", "mp", "qq", "napcat", "telegram", "sms",
}

/*
FrontendSemester 为一个学期的起止。
	学期视图完全由本配置决定。禁止写死在代码中。
	Key 为开学年月（YYYY-MM）。必须与 Start 同月。
*/
type FrontendSemester struct {
	Key   string `json:"key"`
	Start string `json:"start"`
	End   string `json:"end"`
}

type FrontendDisplay struct {
	ElectricityRate       *string `json:"electricity_rate,omitempty"`
	EmptyRoomThresholdKWH *string `json:"empty_room_threshold_kwh,omitempty"`
	RankingRefreshTime    string  `json:"ranking_refresh_time"`
	CampusName            string  `json:"campus_name"`
	AreaName              string  `json:"area_name"`
	/* BrandName 为产品对外名称。用于标题、侧栏与邮件页脚。
	   与 AreaName 分离。站点名不等于学校名。 */
	BrandName string `json:"brand_name"`
	/** 校历。空表示未配置。学期视图退回近 18 周滚动窗口。 */
	Semesters []FrontendSemester `json:"semesters,omitempty"`
}

// DefaultEmptyRoomThresholdKWH 为自然日官方用量（ydl）的空房判定默认值。
// 用字符串保存。避免 JSON 浮点往返产生精度噪声。
const DefaultEmptyRoomThresholdKWH = "0.3"
const DefaultRankingRefreshTime = "09:00"

// DefaultBrandName 为未配置站点名时的中性默认值。
const DefaultBrandName = "POWER·PUSH"

// DefaultCampusName 为未配置校区名时的中性默认值。用于库存导入等落库处。
const DefaultCampusName = "主校区"

func normalizeFrontendSettings(settings *FrontendConfigSettings) {
	if strings.TrimSpace(settings.Display.BrandName) == "" {
		settings.Display.BrandName = DefaultBrandName
	}
	if settings.Display.EmptyRoomThresholdKWH == nil || strings.TrimSpace(*settings.Display.EmptyRoomThresholdKWH) == "" {
		value := DefaultEmptyRoomThresholdKWH
		settings.Display.EmptyRoomThresholdKWH = &value
	}
	if strings.TrimSpace(settings.Display.RankingRefreshTime) == "" {
		settings.Display.RankingRefreshTime = DefaultRankingRefreshTime
	}
	if settings.Features.Channels == nil {
		settings.Features.Channels = map[string]bool{}
	}
	if settings.Features.ChannelComingSoon == nil {
		settings.Features.ChannelComingSoon = map[string]bool{}
	}
	seen := make(map[string]bool, len(defaultFrontendChannelOrder))
	order := make([]string, 0, len(defaultFrontendChannelOrder))
	for _, channel := range settings.Features.ChannelOrder {
		if knownChannels[channel] && !seen[channel] {
			seen[channel] = true
			order = append(order, channel)
		}
	}
	for _, channel := range defaultFrontendChannelOrder {
		if !seen[channel] {
			order = append(order, channel)
		}
	}
	settings.Features.ChannelOrder = order
	normalizeFrontendChannelCategories(&settings.Features)
}

/*
分类为纯展示分组。坏数据就地清洗。禁止导致整页保存失败。
丢弃未知渠道、重复归属与空标题。
未入组渠道由用户端兜底。此处不补组。
*/
func normalizeFrontendChannelCategories(features *FrontendFeatures) {
	categories := make([]FrontendChannelCategory, 0, len(features.ChannelCategories))
	seenID := make(map[string]bool, len(features.ChannelCategories))
	claimed := make(map[string]bool, len(knownChannels))
	for _, category := range features.ChannelCategories {
		id := strings.TrimSpace(category.ID)
		name := strings.TrimSpace(category.Name)
		if id == "" || name == "" || seenID[id] {
			continue
		}
		seenID[id] = true
		channels := make([]string, 0, len(category.Channels))
		for _, channel := range category.Channels {
			if knownChannels[channel] && !claimed[channel] {
				claimed[channel] = true
				channels = append(channels, channel)
			}
		}
		categories = append(categories, FrontendChannelCategory{
			ID:       id,
			Name:     name,
			EN:       strings.TrimSpace(category.EN),
			Desc:     strings.TrimSpace(category.Desc),
			Channels: channels,
		})
	}
	features.ChannelCategories = categories
}

type FrontendConfigSettings struct {
	Features FrontendFeatures `json:"features"`
	Display  FrontendDisplay  `json:"display"`
}

type FrontendConfigView struct {
	Version  int64            `json:"version"`
	Features FrontendFeatures `json:"features"`
	Display  FrontendDisplay  `json:"display"`
	// OAuth 为服务端能力。不落本表。由接口层按运行配置填充。
	OAuth     FrontendOAuth `json:"oauth"`
	UpdatedAt time.Time     `json:"updated_at"`
}

func GetFrontendConfig(ctx context.Context, pool *pgxpool.Pool) (FrontendConfigView, error) {
	var result FrontendConfigView
	var raw []byte
	if err := pool.QueryRow(ctx, `
		SELECT version, config, updated_at FROM frontend_config WHERE singleton
	`).Scan(&result.Version, &raw, &result.UpdatedAt); err != nil {
		return result, err
	}
	var settings FrontendConfigSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return result, fmt.Errorf("decode frontend config: %w", err)
	}
	normalizeFrontendSettings(&settings)
	result.Features = settings.Features
	result.Display = settings.Display
	return result, nil
}

func UpdateFrontendConfig(ctx context.Context, pool *pgxpool.Pool, settings FrontendConfigSettings) (FrontendConfigView, error) {
	normalizeFrontendSettings(&settings)
	raw, err := json.Marshal(settings)
	if err != nil {
		return FrontendConfigView{}, err
	}
	var result FrontendConfigView
	var previousThreshold *string
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// 空房阈值变更后须重建 daily_campus_rollup。先记录旧值。
		if err := tx.QueryRow(ctx, `
			SELECT NULLIF(config #>> '{display,empty_room_threshold_kwh}', '')
			FROM frontend_config WHERE singleton FOR UPDATE
		`).Scan(&previousThreshold); err != nil {
			return err
		}
		var stored []byte
		if err := tx.QueryRow(ctx, `
			UPDATE frontend_config
			SET version=version+1, config=$1, updated_at=now(), updated_by='admin_token'
			WHERE singleton
			RETURNING version, config, updated_at
		`, raw).Scan(&result.Version, &stored, &result.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO frontend_config_audit (version, config, changed_by)
			VALUES ($1, $2, 'admin_token')
		`, result.Version, stored); err != nil {
			return err
		}
		// 隐藏、Coming Soon 或未实现渠道必须同时停止后台投递。
		// 禁止仅隐藏前端入口。旧 enabled=true 仍会在 worker 中发送。
		blocked := make([]string, 0)
		for channel := range knownChannels {
			visible := true
			if configured, ok := settings.Features.Channels[channel]; ok {
				visible = configured
			}
			if !visible || settings.Features.ChannelComingSoon[channel] || !IsImplementedChannel(channel) {
				blocked = append(blocked, channel)
			}
		}
		if len(blocked) > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE user_notification_channels SET enabled=false, updated_at=now()
				WHERE channel = ANY($1::text[]) AND enabled
			`, blocked); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return FrontendConfigView{}, err
	}
	/* 阈值决定哪一天算空房。物化视图存旧阈值结果。
	   不重建则户均除数停在旧口径。此处同步重建。 */
	if thresholdChanged(previousThreshold, settings.Display.EmptyRoomThresholdKWH) {
		if err := RefreshCampusRollup(ctx, pool); err != nil {
			return FrontendConfigView{}, fmt.Errorf("rebuild campus rollup after threshold change: %w", err)
		}
	}
	result.Features = settings.Features
	result.Display = settings.Display
	return result, nil
}

// thresholdChanged 比较数值而非字面量。"0.3" 与 "0.30" 为同一阈值。禁止触发重建。
func thresholdChanged(previous, next *string) bool {
	before := DefaultEmptyRoomThresholdKWH
	if previous != nil && strings.TrimSpace(*previous) != "" {
		before = strings.TrimSpace(*previous)
	}
	after := DefaultEmptyRoomThresholdKWH
	if next != nil && strings.TrimSpace(*next) != "" {
		after = strings.TrimSpace(*next)
	}
	if before == after {
		return false
	}
	a, errA := strconv.ParseFloat(before, 64)
	b, errB := strconv.ParseFloat(after, 64)
	if errA != nil || errB != nil {
		return true
	}
	return a != b
}

/*
OAuthProviderEnabled 读取面板入口开关。
	缺字段按开启处理。兼容未跑 000044 的旧库。
	是否真正可用仍取决于 client id / secret。
*/
func OAuthProviderEnabled(ctx context.Context, pool *pgxpool.Pool, provider string) (bool, error) {
	column := "google_oauth"
	if provider == "github" {
		column = "github_oauth"
	}
	var enabled bool
	err := pool.QueryRow(ctx, `
		SELECT COALESCE((config #> '{features,auth}' ->> $1::text)::boolean, true)
		FROM frontend_config WHERE singleton
	`, column).Scan(&enabled)
	return enabled, err
}

func RegistrationEnabled(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var enabled bool
	err := pool.QueryRow(ctx, `
		SELECT COALESCE((config #>> '{features,auth,registration}')::boolean, true)
		FROM frontend_config WHERE singleton
	`).Scan(&enabled)
	return enabled, err
}
