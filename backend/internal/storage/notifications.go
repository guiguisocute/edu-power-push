package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/edu-power-push/edu-power-push/backend/internal/notification"
	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

/* 用户推送偏好与渠道。见 frontend/docs/USER-PREFERENCES.md §1–3。
   邮件与个人机器人共用规则 → 触发 → 投递链路。
   未实现渠道仅可存为 Coming Soon。API 禁止用户启用。 */

const (
	// 低额预警去抖缓冲。回升超过阈值加缓冲才解除 latched。
	LowBalanceAlertBufferYuan = 5
	// 邮件保留多收件人。必须阻断单账号批量群发放大。
	MaxMailRecipients = 5
	// 渠道测试发送最小间隔。
	ChannelTestInterval = 60 * time.Second
)

func channelTestInterval(channel string) time.Duration {
	// PushPlus 相同内容每小时限 3 条。固定测试文案至少间隔 20 分钟。
	// 全天点满最多 72 次。为 200 次/日额度留余量。
	if channel == "pushplus" {
		return 20 * time.Minute
	}
	return ChannelTestInterval
}

var (
	ErrChannelDisabled      = errors.New("channel is not configured or not enabled")
	ErrChannelUnsupported   = errors.New("channel delivery is not implemented")
	ErrChannelRateLimited   = errors.New("channel test was requested too recently")
	ErrInvalidChannel       = errors.New("unsupported channel")
	ErrInvalidChannelConfig = errors.New("invalid channel configuration")
	ErrInvalidNotifSettings = errors.New("invalid notification settings")
)

var (
	pushTimePattern      = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	pushPlusTokenPattern = regexp.MustCompile(`^[A-Fa-f0-9]{32}$`)
	telegramTokenPattern = regexp.MustCompile(`^[1-9][0-9]{5,15}:[A-Za-z0-9_-]{30,50}$`)
	telegramChatPattern  = regexp.MustCompile(`^-?[1-9][0-9]{4,19}$`)
	napCatTargetPattern  = regexp.MustCompile(`^(?:(?:group|user):)?[1-9][0-9]{4,19}$`)
	serverChanTurboKey   = regexp.MustCompile(`^SCT[A-Za-z0-9_-]{8,256}$`)
	serverChanThreeKey   = regexp.MustCompile(`^sctp[1-9][0-9]*t[A-Za-z0-9_-]{8,256}$`)
)

var knownChannels = map[string]bool{
	"mail": true, "sms": true, "dingtalk": true, "wecom": true,
	"wecom_webhook": true, "discord": true, "feishu": true, "lark": true,
	"pushplus": true, "mp": true, "qq": true, "napcat": true, "telegram": true,
	"whatsapp": true, "serverchan_turbo": true, "serverchan3": true,
	"webhook": true, "bark": true, "gotify": true,
}

var implementedChannels = map[string]bool{
	"mail": true, "dingtalk": true, "wecom": true, "feishu": true, "lark": true,
	"wecom_webhook": true, "discord": true, "pushplus": true, "mp": true, "qq": true, "napcat": true, "telegram": true,
	"whatsapp": true, "serverchan_turbo": true, "serverchan3": true,
	"webhook": true, "bark": true, "gotify": true,
}

// IsKnownChannel 供 API 层校验 path 参数。
func IsKnownChannel(channel string) bool { return knownChannels[channel] }

// IsImplementedChannel 为管理面板「可用」状态的服务端护栏。
func IsImplementedChannel(channel string) bool { return implementedChannels[channel] }

// 各渠道敏感字段。读回掩码。写时省略=保留，null=清空。
var channelSecretFields = map[string]map[string]bool{
	"mail":             {},
	"sms":              {},
	"dingtalk":         {"webhook": true, "secret": true},
	"wecom":            {"secret": true},
	"wecom_webhook":    {"webhook": true},
	"discord":          {"webhook": true},
	"feishu":           {"webhook": true, "secret": true},
	"lark":             {"webhook": true, "secret": true},
	"pushplus":         {"token": true},
	"mp":               {"secret": true},
	"qq":               {"secret": true},
	"napcat":           {"token": true},
	"telegram":         {"token": true},
	"whatsapp":         {"webhook": true},
	"serverchan_turbo": {"sendkey": true},
	"serverchan3":      {"sendkey": true},
	"webhook":          {"webhook": true, "token": true},
	"bark":             {"device_key": true},
	"gotify":           {"token": true},
}

// 各渠道允许的配置键。未知键返回 400。
var channelAllowedFields = map[string]map[string]bool{
	"mail":             {"to": true},
	"sms":              {"sms": true},
	"dingtalk":         {"webhook": true, "secret": true},
	"wecom":            {"botid": true, "secret": true, "chatid": true},
	"wecom_webhook":    {"webhook": true},
	"discord":          {"webhook": true},
	"feishu":           {"webhook": true, "secret": true},
	"lark":             {"webhook": true, "secret": true},
	"pushplus":         {"token": true},
	"mp":               {"appid": true, "secret": true, "tpl": true, "openid": true},
	"qq":               {"appid": true, "secret": true, "user_openid": true},
	"napcat":           {"base_url": true, "token": true, "target": true},
	"telegram":         {"token": true, "chat": true},
	"whatsapp":         {"webhook": true},
	"serverchan_turbo": {"sendkey": true},
	"serverchan3":      {"sendkey": true},
	"webhook":          {"webhook": true, "token": true},
	"bark":             {"base_url": true, "device_key": true},
	"gotify":           {"base_url": true, "token": true, "priority": true},
}

type NotificationSettings struct {
	LowBalanceAlert bool                   `json:"low_balance_alert"`
	ThresholdYuan   string                 `json:"threshold_yuan"`
	ScheduledDigest bool                   `json:"scheduled_digest"`
	Period          string                 `json:"period"`
	PushTime        string                 `json:"push_time"`
	Timezone        string                 `json:"timezone"`
	Templates       notification.Templates `json:"templates"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

type ChannelResult struct {
	Status    string    `json:"status"`
	At        time.Time `json:"at"`
	Message   *string   `json:"message"`
	LatencyMS *int64    `json:"latency_ms,omitempty"`
}

type NotificationChannel struct {
	Channel    string          `json:"channel"`
	Enabled    bool            `json:"enabled"`
	Config     map[string]any  `json:"config"`
	SecretSet  map[string]bool `json:"secret_set"`
	LastResult *ChannelResult  `json:"last_result"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

type ChannelList struct {
	Channels []NotificationChannel `json:"channels"`
}

type PushLog struct {
	ID      string    `json:"id"`
	SentAt  time.Time `json:"sent_at"`
	Channel string    `json:"channel"`
	Kind    string    `json:"kind"`
	Status  string    `json:"status"`
	Summary string    `json:"summary"`
	Error   *string   `json:"error"`
}

type PushLogPage struct {
	Items      []PushLog `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

func defaultNotificationSettings() NotificationSettings {
	return NotificationSettings{
		LowBalanceAlert: true,
		ThresholdYuan:   "10",
		ScheduledDigest: true,
		Period:          "daily",
		PushTime:        "08:00",
		Timezone:        "Asia/Shanghai",
		Templates:       notification.DefaultTemplates(),
	}
}

func GetNotificationSettings(ctx context.Context, pool *pgxpool.Pool, userID string) (NotificationSettings, error) {
	settings := defaultNotificationSettings()
	var rawTemplates []byte
	err := pool.QueryRow(ctx, `
		SELECT low_balance_alert,threshold_yuan::text,scheduled_digest,period,push_time,timezone,templates,updated_at
		FROM user_notification_settings WHERE user_id=$1::uuid
	`, userID).Scan(
		&settings.LowBalanceAlert, &settings.ThresholdYuan, &settings.ScheduledDigest,
		&settings.Period, &settings.PushTime, &settings.Timezone, &rawTemplates, &settings.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultNotificationSettings(), nil
	}
	if err != nil {
		return settings, fmt.Errorf("read notification settings: %w", err)
	}
	settings.Templates, err = decodeNotificationTemplates(rawTemplates)
	if err != nil {
		return settings, err
	}
	return settings, nil
}

func SaveNotificationSettings(ctx context.Context, pool *pgxpool.Pool, userID string, in NotificationSettings) (NotificationSettings, error) {
	in.Templates = notification.NormalizeTemplates(in.Templates)
	if err := validateNotificationSettings(in); err != nil {
		return NotificationSettings{}, err
	}
	if strings.TrimSpace(in.Timezone) == "" {
		in.Timezone = "Asia/Shanghai"
	}
	in.ThresholdYuan = strings.TrimSpace(in.ThresholdYuan)
	templatesPayload, err := json.Marshal(in.Templates)
	if err != nil {
		return NotificationSettings{}, fmt.Errorf("encode notification templates: %w", err)
	}
	var saved NotificationSettings
	var rawTemplates []byte
	err = pool.QueryRow(ctx, `
		INSERT INTO user_notification_settings (
			user_id,low_balance_alert,threshold_yuan,scheduled_digest,period,push_time,timezone,templates,updated_at
		) VALUES ($1::uuid,$2,$3::numeric,$4,$5,$6,$7,$8::jsonb,now())
		ON CONFLICT (user_id) DO UPDATE SET
			low_balance_alert=EXCLUDED.low_balance_alert,
			threshold_yuan=EXCLUDED.threshold_yuan,
			scheduled_digest=EXCLUDED.scheduled_digest,
			period=EXCLUDED.period,
			push_time=EXCLUDED.push_time,
			timezone=EXCLUDED.timezone,
			templates=EXCLUDED.templates,
			updated_at=now()
		RETURNING low_balance_alert,threshold_yuan::text,scheduled_digest,period,push_time,timezone,templates,updated_at
	`, userID, in.LowBalanceAlert, in.ThresholdYuan, in.ScheduledDigest, in.Period, in.PushTime, in.Timezone, templatesPayload,
	).Scan(
		&saved.LowBalanceAlert, &saved.ThresholdYuan, &saved.ScheduledDigest,
		&saved.Period, &saved.PushTime, &saved.Timezone, &rawTemplates, &saved.UpdatedAt,
	)
	if err != nil {
		return saved, fmt.Errorf("save notification settings: %w", err)
	}
	saved.Templates, err = decodeNotificationTemplates(rawTemplates)
	if err != nil {
		return saved, err
	}
	return saved, nil
}

func decodeNotificationTemplates(raw []byte) (notification.Templates, error) {
	templates := notification.Templates{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &templates); err != nil {
			return templates, fmt.Errorf("decode notification templates: %w", err)
		}
	}
	return notification.NormalizeTemplates(templates), nil
}

var thresholdYuanPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,2})?$`)

func validateNotificationSettings(in NotificationSettings) error {
	raw := strings.TrimSpace(in.ThresholdYuan)
	if !thresholdYuanPattern.MatchString(raw) {
		return fmt.Errorf("%w: threshold_yuan must be a positive decimal with at most 2 fractional digits", ErrInvalidNotifSettings)
	}
	th, err := strconv.ParseFloat(raw, 64)
	if err != nil || th < 1 || th > 50 {
		return fmt.Errorf("%w: threshold_yuan must be between 1 and 50", ErrInvalidNotifSettings)
	}
	switch in.Period {
	case "daily", "twice", "every3", "weekly":
	default:
		return fmt.Errorf("%w: period must be daily, twice, every3, or weekly", ErrInvalidNotifSettings)
	}
	if !pushTimePattern.MatchString(in.PushTime) {
		return fmt.Errorf("%w: push_time must be HH:MM", ErrInvalidNotifSettings)
	}
	if err := notification.ValidateTemplates(in.Templates); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidNotifSettings, err)
	}
	return nil
}

func ListNotificationChannels(ctx context.Context, pool *pgxpool.Pool, box *secrets.Box, userID string) (ChannelList, error) {
	rows, err := pool.Query(ctx, `
		SELECT channel,enabled,config,secret_config,secret_key_id,last_status,last_message,last_at,updated_at
		FROM user_notification_channels WHERE user_id=$1::uuid ORDER BY channel
	`, userID)
	if err != nil {
		return ChannelList{}, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()
	items := make([]NotificationChannel, 0)
	for rows.Next() {
		ch, _, err := scanNotificationChannel(rows, box)
		if err != nil {
			return ChannelList{}, err
		}
		items = append(items, ch)
	}
	return ChannelList{Channels: items}, rows.Err()
}

type channelScanner interface {
	Scan(dest ...any) error
}

// dbConn 支持连接池与事务两种读写。批量保存必须全成或全败。
type dbConn interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func scanNotificationChannel(row channelScanner, box *secrets.Box) (NotificationChannel, map[string]any, error) {
	var ch NotificationChannel
	var raw, sealed []byte
	var keyID *string
	var lastStatus, lastMessage *string
	var lastAt *time.Time
	if err := row.Scan(&ch.Channel, &ch.Enabled, &raw, &sealed, &keyID, &lastStatus, &lastMessage, &lastAt, &ch.UpdatedAt); err != nil {
		return ch, nil, fmt.Errorf("scan channel: %w", err)
	}
	stored, err := decodeChannelConfig(ch.Channel, raw, sealed, keyID, box)
	if err != nil {
		return ch, nil, err
	}
	ch.Config, ch.SecretSet = maskChannelConfig(ch.Channel, stored)
	if lastStatus != nil && lastAt != nil {
		ch.LastResult = &ChannelResult{Status: *lastStatus, At: *lastAt, Message: lastMessage}
	}
	return ch, stored, nil
}

// decodeChannelConfig 合并非敏感 JSON 与 AES-GCM 密文。
// 旧版曾把 token 放在 config。读取仍兼容。
// 下次保存由 splitChannelConfig 迁移到密文列。
func decodeChannelConfig(channel string, raw, sealed []byte, keyID *string, box *secrets.Box) (map[string]any, error) {
	stored := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &stored); err != nil {
			return nil, fmt.Errorf("decode channel config: %w", err)
		}
	}
	if len(sealed) == 0 {
		return stored, nil
	}
	if keyID != nil && *keyID != "" && (box == nil || box.KeyID() != *keyID) {
		return nil, secrets.ErrWrongKey
	}
	secretValues, err := box.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("decrypt channel credentials: %w", err)
	}
	for field, value := range secretValues {
		if channelSecretFields[channel][field] {
			stored[field] = value
		}
	}
	return stored, nil
}

func splitChannelConfig(channel string, merged map[string]any) (map[string]any, map[string]string, error) {
	publicConfig := make(map[string]any, len(merged))
	secretConfig := map[string]string{}
	secretFields := channelSecretFields[channel]
	for field, value := range merged {
		if !secretFields[field] {
			publicConfig[field] = value
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, nil, fmt.Errorf("%w: %s must be a string", ErrInvalidChannelConfig, field)
		}
		if text = strings.TrimSpace(text); text != "" {
			secretConfig[field] = text
		}
	}
	return publicConfig, secretConfig, nil
}

func nullableKeyID(box *secrets.Box, sealed []byte) any {
	if len(sealed) == 0 {
		return nil
	}
	return box.KeyID()
}

// EncryptLegacyChannelCredentials 将 config JSON 中遗留敏感字段迁到密文列。
// 并发启动安全。UPDATE 仅处理 secret_config 仍为空的行。
func EncryptLegacyChannelCredentials(ctx context.Context, pool *pgxpool.Pool, box *secrets.Box) error {
	rows, err := pool.Query(ctx, `
		SELECT user_id::text,channel,config
		FROM user_notification_channels
		WHERE secret_config IS NULL
	`)
	if err != nil {
		return fmt.Errorf("list legacy channel credentials: %w", err)
	}
	type legacyRow struct {
		userID, channel string
		config          map[string]any
	}
	legacy := make([]legacyRow, 0)
	for rows.Next() {
		var userID, channel string
		var raw []byte
		if err := rows.Scan(&userID, &channel, &raw); err != nil {
			rows.Close()
			return err
		}
		config := map[string]any{}
		if err := json.Unmarshal(raw, &config); err != nil {
			rows.Close()
			return fmt.Errorf("decode legacy %s config: %w", channel, err)
		}
		_, secretConfig, err := splitChannelConfig(channel, config)
		if err != nil {
			rows.Close()
			return err
		}
		if len(secretConfig) > 0 {
			legacy = append(legacy, legacyRow{userID: userID, channel: channel, config: config})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(legacy) == 0 {
		return nil
	}
	if box == nil || !box.Enabled() {
		return fmt.Errorf("legacy channel credentials require encryption: %w", secrets.ErrNoKey)
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		for _, row := range legacy {
			publicConfig, secretConfig, err := splitChannelConfig(row.channel, row.config)
			if err != nil {
				return err
			}
			sealed, err := box.Seal(secretConfig)
			if err != nil {
				return err
			}
			payload, err := json.Marshal(publicConfig)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE user_notification_channels
				SET config=$3::jsonb,secret_config=$4,secret_key_id=$5,updated_at=now()
				WHERE user_id=$1::uuid AND channel=$2 AND secret_config IS NULL
			`, row.userID, row.channel, payload, sealed, box.KeyID()); err != nil {
				return fmt.Errorf("encrypt legacy %s credentials: %w", row.channel, err)
			}
		}
		return nil
	})
}

func GetNotificationChannel(ctx context.Context, db dbConn, box *secrets.Box, userID, channel string) (NotificationChannel, map[string]any, error) {
	if !knownChannels[channel] {
		return NotificationChannel{}, nil, ErrInvalidChannel
	}
	var ch NotificationChannel
	var raw, sealed []byte
	var keyID *string
	var lastStatus, lastMessage *string
	var lastAt *time.Time
	err := db.QueryRow(ctx, `
		SELECT channel,enabled,config,secret_config,secret_key_id,last_status,last_message,last_at,updated_at
		FROM user_notification_channels WHERE user_id=$1::uuid AND channel=$2
	`, userID, channel).Scan(&ch.Channel, &ch.Enabled, &raw, &sealed, &keyID, &lastStatus, &lastMessage, &lastAt, &ch.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationChannel{Channel: channel, Config: map[string]any{}, SecretSet: map[string]bool{}}, map[string]any{}, nil
	}
	if err != nil {
		return ch, nil, fmt.Errorf("read channel: %w", err)
	}
	stored, err := decodeChannelConfig(channel, raw, sealed, keyID, box)
	if err != nil {
		return ch, nil, err
	}
	ch.Config, ch.SecretSet = maskChannelConfig(channel, stored)
	if lastStatus != nil && lastAt != nil {
		ch.LastResult = &ChannelResult{Status: *lastStatus, At: *lastAt, Message: lastMessage}
	}
	return ch, stored, nil
}

/*
SaveNotificationChannel 写入单个渠道。
	config 中省略的敏感字段保留原值。显式 null 清空。
*/
func SaveNotificationChannel(
	ctx context.Context, db dbConn,
	box *secrets.Box, userID, channel string, enabled bool, patch map[string]any,
) (NotificationChannel, error) {
	if !knownChannels[channel] {
		return NotificationChannel{}, ErrInvalidChannel
	}
	_, stored, err := GetNotificationChannel(ctx, db, box, userID, channel)
	if err != nil {
		return NotificationChannel{}, err
	}
	merged, err := mergeChannelConfig(channel, stored, patch)
	if err != nil {
		return NotificationChannel{}, err
	}
	if err := validateChannelConfig(channel, merged, enabled); err != nil {
		return NotificationChannel{}, err
	}
	if channel == "mail" {
		recipients, _ := parseMailRecipients(merged["to"])
		if err := EnsureVerifiedMailRecipients(ctx, db, userID, recipients); err != nil {
			return NotificationChannel{}, err
		}
	}
	publicConfig, secretConfig, err := splitChannelConfig(channel, merged)
	if err != nil {
		return NotificationChannel{}, err
	}
	sealed, err := box.Seal(secretConfig)
	if err != nil {
		return NotificationChannel{}, fmt.Errorf("encrypt channel credentials: %w", err)
	}
	payload, err := json.Marshal(publicConfig)
	if err != nil {
		return NotificationChannel{}, fmt.Errorf("encode channel config: %w", err)
	}
	var raw, sealedOut []byte
	var keyID *string
	var lastStatus, lastMessage *string
	var lastAt *time.Time
	var ch NotificationChannel
	err = db.QueryRow(ctx, `
		INSERT INTO user_notification_channels (user_id,channel,enabled,config,secret_config,secret_key_id,updated_at)
		VALUES ($1::uuid,$2,$3,$4::jsonb,$5,$6,now())
		ON CONFLICT (user_id,channel) DO UPDATE SET
			enabled=EXCLUDED.enabled, config=EXCLUDED.config,
			secret_config=EXCLUDED.secret_config, secret_key_id=EXCLUDED.secret_key_id, updated_at=now()
		RETURNING channel,enabled,config,secret_config,secret_key_id,last_status,last_message,last_at,updated_at
	`, userID, channel, enabled, payload, sealed, nullableKeyID(box, sealed)).Scan(
		&ch.Channel, &ch.Enabled, &raw, &sealedOut, &keyID, &lastStatus, &lastMessage, &lastAt, &ch.UpdatedAt,
	)
	if err != nil {
		return ch, fmt.Errorf("save channel: %w", err)
	}
	storedOut, err := decodeChannelConfig(channel, raw, sealedOut, keyID, box)
	if err != nil {
		return ch, err
	}
	ch.Config, ch.SecretSet = maskChannelConfig(channel, storedOut)
	if lastStatus != nil && lastAt != nil {
		ch.LastResult = &ChannelResult{Status: *lastStatus, At: *lastAt, Message: lastMessage}
	}
	return ch, nil
}

// ChannelPatch 为批量保存中的单个渠道。
type ChannelPatch struct {
	Channel string
	Enabled bool
	Config  map[string]any
}

/*
ReplaceNotificationChannels 一次写入多个渠道。
	整批在同一事务。禁止逐条提交导致半成功。
*/
func ReplaceNotificationChannels(ctx context.Context, pool *pgxpool.Pool, box *secrets.Box, userID string, items []ChannelPatch) (ChannelList, error) {
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		for _, item := range items {
			if _, err := SaveNotificationChannel(ctx, tx, box, userID, item.Channel, item.Enabled, item.Config); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return ChannelList{}, err
	}
	return ListNotificationChannels(ctx, pool, box, userID)
}

func RecordChannelResult(ctx context.Context, pool *pgxpool.Pool, userID, channel, status string, message *string) error {
	_, err := pool.Exec(ctx, `
		UPDATE user_notification_channels
		SET last_status=$3, last_message=$4, last_at=now()
		WHERE user_id=$1::uuid AND channel=$2
	`, userID, channel, status, message)
	return err
}

// RecordChannelTestResult 同 RecordChannelResult，并推进测试限流时间戳。
func RecordChannelTestResult(ctx context.Context, pool *pgxpool.Pool, userID, channel, status string, message *string) error {
	_, err := pool.Exec(ctx, `
		UPDATE user_notification_channels
		SET last_status=$3, last_message=$4, last_at=now(), last_test_at=now()
		WHERE user_id=$1::uuid AND channel=$2
	`, userID, channel, status, message)
	return err
}

/*
TruncateText 按字符截断。禁止按字节截断。
	按字节切会切开中文 rune。产生非法 UTF-8。PostgreSQL 拒收日志。
*/
func TruncateText(value string, max int) string {
	if max <= 0 || utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:max])) + "…"
}

func InsertPushLog(ctx context.Context, pool *pgxpool.Pool, userID, channel, kind, status, summary string, logErr *string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO user_push_logs (user_id,channel,kind,status,summary,error)
		VALUES ($1::uuid,$2,$3,$4,$5,$6)
	`, userID, channel, kind, status, summary, logErr)
	if err != nil {
		return fmt.Errorf("insert push log: %w", err)
	}
	return nil
}

/*
ListPushLogs 按时间倒序取一页推送记录。
	调用方传 limit+1 判断是否有下一页。与 httpapi nextCursor 约定一致。
	排序带 id DESC 兜底。保证同秒多条记录分页稳定。
*/
func ListPushLogs(ctx context.Context, pool *pgxpool.Pool, userID string, limit, offset int) ([]PushLog, error) {
	if limit <= 0 {
		limit = 5
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := pool.Query(ctx, `
		SELECT id::text,sent_at,channel,kind,status,summary,error
		FROM user_push_logs WHERE user_id=$1::uuid
		ORDER BY sent_at DESC, id DESC LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list push logs: %w", err)
	}
	defer rows.Close()
	items := make([]PushLog, 0)
	for rows.Next() {
		var item PushLog
		if err := rows.Scan(&item.ID, &item.SentAt, &item.Channel, &item.Kind, &item.Status, &item.Summary, &item.Error); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ReserveChannelTest 以单条条件 UPDATE 抢占测试额度。
// 禁止两并发请求在 SELECT 后同时打上游。
// 抢占成功后，即使上游失败也计入冷却。防止重试风暴。
func ReserveChannelTest(ctx context.Context, pool *pgxpool.Pool, userID, channel string) (time.Duration, error) {
	var reservedAt time.Time
	err := pool.QueryRow(ctx, `
		UPDATE user_notification_channels
		SET last_test_at=now()
		WHERE user_id=$1::uuid AND channel=$2
		  AND (last_test_at IS NULL OR last_test_at <= now() - $3::interval)
		RETURNING last_test_at
	`, userID, channel, channelTestInterval(channel).String()).Scan(&reservedAt)
	if err == nil {
		return 0, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("reserve channel test: %w", err)
	}
	var lastAt *time.Time
	err = pool.QueryRow(ctx, `
		SELECT last_test_at FROM user_notification_channels
		WHERE user_id=$1::uuid AND channel=$2
	`, userID, channel).Scan(&lastAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrChannelDisabled
	}
	if err != nil {
		return 0, fmt.Errorf("read channel test reservation: %w", err)
	}
	if lastAt == nil {
		return 0, ErrChannelRateLimited
	}
	wait := channelTestInterval(channel) - time.Since(*lastAt)
	if wait < 0 {
		wait = 0
	}
	return wait, ErrChannelRateLimited
}

func mergeChannelConfig(channel string, stored, patch map[string]any) (map[string]any, error) {
	allowed := channelAllowedFields[channel]
	if allowed == nil {
		return nil, ErrInvalidChannel
	}
	out := map[string]any{}
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range patch {
		if !allowed[k] {
			return nil, fmt.Errorf("%w: unknown field %q for channel %s", ErrInvalidChannelConfig, k, channel)
		}
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out, nil
}

func validateChannelConfig(channel string, config map[string]any, enabled bool) error {
	if enabled && !IsImplementedChannel(channel) {
		return fmt.Errorf("%w: channel %s", ErrChannelUnsupported, channel)
	}
	switch channel {
	case "mail":
		recipients, err := parseMailRecipients(config["to"])
		if err != nil {
			return err
		}
		if enabled && len(recipients) == 0 {
			return fmt.Errorf("%w: mail requires at least one recipient when enabled", ErrInvalidChannelConfig)
		}
	case "sms":
		// 暂未开通投递。仅做基础类型校验。
		if config["sms"] != nil {
			if _, ok := asStringSlice(config["sms"]); !ok {
				return fmt.Errorf("%w: sms must be an array of phone numbers", ErrInvalidChannelConfig)
			}
		}
	case "pushplus":
		token, tokenOK := config["token"].(string)
		token = strings.TrimSpace(token)
		if enabled && token == "" {
			return fmt.Errorf("%w: token is required when enabled", ErrInvalidChannelConfig)
		}
		if token != "" && (!tokenOK || !pushPlusTokenPattern.MatchString(token)) {
			return fmt.Errorf("%w: PushPlus token must be 32 hexadecimal characters", ErrInvalidChannelConfig)
		}
	case "telegram":
		token, tokenOK := config["token"].(string)
		chat, chatOK := config["chat"].(string)
		token, chat = strings.TrimSpace(token), strings.TrimSpace(chat)
		if enabled && (token == "" || chat == "") {
			return fmt.Errorf("%w: token and chat ID are required when enabled", ErrInvalidChannelConfig)
		}
		if token != "" && (!tokenOK || !telegramTokenPattern.MatchString(token)) {
			return fmt.Errorf("%w: Telegram bot token format is invalid", ErrInvalidChannelConfig)
		}
		// 接受私聊与群聊数字 ID。拒绝用户名目标。
		// 负数 ID 发送前经 getChat 确认不是频道。
		if chat != "" && (!chatOK || !telegramChatPattern.MatchString(chat)) {
			return fmt.Errorf("%w: Telegram chat must be a numeric private or group chat ID", ErrInvalidChannelConfig)
		}
	case "wecom":
		if err := validateRequiredFields(channel, config, enabled, "botid", "secret", "chatid"); err != nil {
			return err
		}
	case "mp":
		if err := validateRequiredFields(channel, config, enabled, "appid", "secret", "tpl", "openid"); err != nil {
			return err
		}
	case "qq":
		if err := validateRequiredFields(channel, config, enabled, "appid", "secret", "user_openid"); err != nil {
			return err
		}
	case "napcat":
		if err := validateRequiredFields(channel, config, enabled, "base_url", "target"); err != nil {
			return err
		}
		if raw, ok := config["base_url"].(string); ok && strings.TrimSpace(raw) != "" {
			if err := validateNapCatBaseURL(raw); err != nil {
				return err
			}
		}
		if raw, ok := config["target"].(string); ok && strings.TrimSpace(raw) != "" {
			target := strings.TrimSpace(raw)
			emptyMode := target == "group:" || target == "user:"
			if (!emptyMode || enabled) && !napCatTargetPattern.MatchString(target) {
				return fmt.Errorf("%w: napcat.target must be a numeric ID prefixed with group: or user:", ErrInvalidChannelConfig)
			}
		}
		if raw, exists := config["token"]; exists {
			token, ok := raw.(string)
			if !ok || len(token) > 512 || strings.ContainsAny(token, "\r\n\x00") {
				return fmt.Errorf("%w: napcat.token is invalid", ErrInvalidChannelConfig)
			}
		}
	case "webhook":
		if err := validateRequiredFields(channel, config, enabled, "webhook"); err != nil {
			return err
		}
		if raw, ok := config["webhook"].(string); ok && strings.TrimSpace(raw) != "" {
			if err := validateWebhookURL(channel, raw); err != nil {
				return err
			}
		}
		if err := validateOptionalCredential(channel, config, "token"); err != nil {
			return err
		}
	case "bark":
		if err := validateRequiredFields(channel, config, enabled, "base_url", "device_key"); err != nil {
			return err
		}
		if raw, ok := config["base_url"].(string); ok && strings.TrimSpace(raw) != "" {
			if err := validateServiceBaseURL("Bark", raw); err != nil {
				return err
			}
		}
	case "gotify":
		if err := validateRequiredFields(channel, config, enabled, "base_url", "token"); err != nil {
			return err
		}
		if raw, ok := config["base_url"].(string); ok && strings.TrimSpace(raw) != "" {
			if err := validateServiceBaseURL("Gotify", raw); err != nil {
				return err
			}
		}
		if raw, exists := config["priority"]; exists && strings.TrimSpace(fmt.Sprint(raw)) != "" {
			if _, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(raw)), 10, 32); err != nil {
				return fmt.Errorf("%w: gotify.priority must be an integer", ErrInvalidChannelConfig)
			}
		}
	case "serverchan_turbo", "serverchan3":
		if err := validateRequiredFields(channel, config, enabled, "sendkey"); err != nil {
			return err
		}
		if raw, ok := config["sendkey"].(string); ok && strings.TrimSpace(raw) != "" {
			key := strings.TrimSpace(raw)
			valid := serverChanTurboKey.MatchString(key)
			if channel == "serverchan3" {
				valid = serverChanThreeKey.MatchString(key)
			}
			if !valid {
				return fmt.Errorf("%w: %s SendKey format is invalid", ErrInvalidChannelConfig, channel)
			}
		}
	default:
		// webhook 类：启用时要求 webhook / token 非空。域名白名单防 SSRF。
		secretFields := channelSecretFields[channel]
		for field := range secretFields {
			if field == "webhook" {
				if raw, ok := config["webhook"].(string); ok && raw != "" {
					if err := validateWebhookURL(channel, raw); err != nil {
						return err
					}
				} else if enabled && (channel == "dingtalk" || channel == "feishu" || channel == "lark" || channel == "wecom_webhook" || channel == "discord" || channel == "whatsapp") {
					return fmt.Errorf("%w: webhook is required when enabled", ErrInvalidChannelConfig)
				}
			}
		}
		if enabled && (channel == "dingtalk" || channel == "feishu" || channel == "lark") {
			secret, ok := config["secret"].(string)
			if !ok || strings.TrimSpace(secret) == "" {
				return fmt.Errorf("%w: signing secret is required when %s is enabled", ErrInvalidChannelConfig, channel)
			}
		}
	}
	return nil
}

func validateRequiredFields(channel string, config map[string]any, enabled bool, fields ...string) error {
	for _, field := range fields {
		raw, exists := config[field]
		if !exists {
			if enabled {
				return fmt.Errorf("%w: %s.%s is required when enabled", ErrInvalidChannelConfig, channel, field)
			}
			continue
		}
		value, ok := raw.(string)
		value = strings.TrimSpace(value)
		if !ok || len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%w: %s.%s is invalid", ErrInvalidChannelConfig, channel, field)
		}
		if enabled && value == "" {
			return fmt.Errorf("%w: %s.%s is required when enabled", ErrInvalidChannelConfig, channel, field)
		}
	}
	return nil
}

func validateNapCatBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 || strings.ContainsAny(raw, "\r\n\x00") {
		return fmt.Errorf("%w: NapCat service URL is invalid", ErrInvalidChannelConfig)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%w: NapCat service URL must use http or https", ErrInvalidChannelConfig)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: NapCat service URL must not include credentials, query, or fragment", ErrInvalidChannelConfig)
	}
	return nil
}

func validateServiceBaseURL(provider, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 || strings.ContainsAny(raw, "\r\n\x00") {
		return fmt.Errorf("%w: %s service URL is invalid", ErrInvalidChannelConfig, provider)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%w: %s service URL must use http or https", ErrInvalidChannelConfig, provider)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%w: %s service URL must not include credentials, query, or fragment", ErrInvalidChannelConfig, provider)
	}
	return nil
}

func validateOptionalCredential(channel string, config map[string]any, field string) error {
	raw, exists := config[field]
	if !exists {
		return nil
	}
	value, ok := raw.(string)
	if !ok || len(value) > 512 || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%w: %s.%s is invalid", ErrInvalidChannelConfig, channel, field)
	}
	return nil
}

func parseMailRecipients(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := asStringSlice(raw)
	if !ok {
		return nil, fmt.Errorf("%w: mail.to must be an array of email addresses", ErrInvalidChannelConfig)
	}
	out := make([]string, 0, min(len(list), MaxMailRecipients))
	seen := map[string]bool{}
	for _, item := range list {
		addr := strings.ToLower(strings.TrimSpace(item))
		if addr == "" {
			continue
		}
		parsed, err := mail.ParseAddress(addr)
		if err != nil || strings.ToLower(parsed.Address) != addr {
			return nil, fmt.Errorf("%w: invalid recipient %q", ErrInvalidChannelConfig, item)
		}
		if seen[addr] {
			continue
		}
		if len(out) >= MaxMailRecipients {
			return nil, fmt.Errorf("%w: mail.to must not contain more than %d recipients", ErrInvalidChannelConfig, MaxMailRecipients)
		}
		seen[addr] = true
		out = append(out, addr)
	}
	return out, nil
}

func asStringSlice(raw any) ([]string, bool) {
	switch v := raw.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

var webhookHosts = map[string][]string{
	"dingtalk":      {"oapi.dingtalk.com"},
	"feishu":        {"open.feishu.cn"},
	"lark":          {"open.larksuite.com"},
	"wecom_webhook": {"qyapi.weixin.qq.com"},
	"discord":       {"discord.com"},
	"whatsapp":      {"api.callmebot.com"},
	"webhook":       {},
}

/*
validateWebhookURL 防 SSRF。仅允许各厂商已知域名。
	必须解析 Host 再比对。禁止子串匹配。
*/
func validateWebhookURL(channel, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\x00") {
		return fmt.Errorf("%w: webhook is invalid", ErrInvalidChannelConfig)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: webhook is not a valid URL", ErrInvalidChannelConfig)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("%w: webhook must use https", ErrInvalidChannelConfig)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: webhook must not carry credentials", ErrInvalidChannelConfig)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("%w: webhook host is missing", ErrInvalidChannelConfig)
	}
	hosts := webhookHosts[channel]
	if len(hosts) == 0 {
		// 无固定域名时仅挡内网目标。例如自建 telegram 代理。
		if isPrivateHost(host) {
			return fmt.Errorf("%w: webhook host is not allowed", ErrInvalidChannelConfig)
		}
		return nil
	}
	for _, allowed := range hosts {
		if host == allowed {
			switch channel {
			case "dingtalk":
				if parsed.Path != "/robot/send" || strings.TrimSpace(parsed.Query().Get("access_token")) == "" {
					return fmt.Errorf("%w: DingTalk webhook must include /robot/send and access_token", ErrInvalidChannelConfig)
				}
			case "feishu", "lark":
				const prefix = "/open-apis/bot/v2/hook/"
				hook := strings.TrimPrefix(parsed.Path, prefix)
				if !strings.HasPrefix(parsed.Path, prefix) || hook == "" || strings.Contains(hook, "/") {
					return fmt.Errorf("%w: %s webhook path is invalid", ErrInvalidChannelConfig, channel)
				}
			case "wecom_webhook":
				if parsed.Path != "/cgi-bin/webhook/send" || strings.TrimSpace(parsed.Query().Get("key")) == "" {
					return fmt.Errorf("%w: WeCom webhook must include /cgi-bin/webhook/send and key", ErrInvalidChannelConfig)
				}
			case "discord":
				parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
				validPrefix := len(parts) == 4 && parts[0] == "api" && parts[1] == "webhooks"
				validVersionedPrefix := len(parts) == 5 && parts[0] == "api" && strings.HasPrefix(parts[1], "v") && parts[2] == "webhooks"
				if (!validPrefix && !validVersionedPrefix) || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
					return fmt.Errorf("%w: Discord webhook path is invalid", ErrInvalidChannelConfig)
				}
			case "whatsapp":
				query := parsed.Query()
				if parsed.Path != "/whatsapp.php" || strings.TrimSpace(query.Get("phone")) == "" || strings.TrimSpace(query.Get("apikey")) == "" {
					return fmt.Errorf("%w: WhatsApp CallMeBot URL must include /whatsapp.php, phone, and apikey", ErrInvalidChannelConfig)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("%w: webhook host is not on the allowlist for %s", ErrInvalidChannelConfig, channel)
}

// isPrivateHost 挡掉本机与内网字面量地址。解析后地址在投递时再查一次。
func isPrivateHost(host string) bool {
	switch host {
	case "localhost", "0.0.0.0", "::", "metadata.google.internal":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsUnspecified() || ip.IsLinkLocalMulticast()
	}
	return strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal")
}

func maskChannelConfig(channel string, stored map[string]any) (map[string]any, map[string]bool) {
	secrets := channelSecretFields[channel]
	if secrets == nil {
		secrets = map[string]bool{}
	}
	out := map[string]any{}
	secretSet := map[string]bool{}
	for k, v := range stored {
		if secrets[k] {
			if s, ok := v.(string); ok && s != "" {
				secretSet[k] = true
				out[k] = maskSecret(s)
			} else {
				secretSet[k] = false
				out[k] = nil
			}
			continue
		}
		out[k] = v
		secretSet[k] = false
	}
	// 确保 secret_set 覆盖该渠道全部敏感字段。供前端渲染占位。
	for k := range secrets {
		if _, ok := secretSet[k]; !ok {
			secretSet[k] = false
		}
	}
	return out, secretSet
}

func maskSecret(value string) string {
	runes := []rune(value)
	if len(runes) <= 8 {
		return "••••••••"
	}
	return string(runes[:4]) + "••••" + string(runes[len(runes)-4:])
}

/* ---- 推送引擎查询 ---- */

// PushCandidate 为引擎一次评估所需的用户侧数据。
type PushCandidate struct {
	UserID          string
	Email           string
	EmailVerified   bool
	MeterNo         string
	Building        string
	Floor           string
	Room            string
	BalanceYuan     string
	Recent7DKWH     *string
	LowBalanceAlert bool
	ThresholdYuan   string
	ScheduledDigest bool
	Period          string
	PushTime        string
	Timezone        string
	AlertLatched    bool
	LastDigestAt    *time.Time
	Templates       notification.Templates
	// MailRecipients：显式配置的 mail 渠道。
	// nil 表示未配置行。引擎可回退账号邮箱。
	// 空切片表示已配置但关闭或无收件人。
	MailEnabled    bool
	MailConfigured bool
	MailRecipients []string
	// PersonalChannels 仅含已启用的用户自有凭证渠道。
	// 配置已在服务端解密。仅传固定供应商。禁止写日志或返回前端。
	PersonalChannels []PushDeliveryChannel
}

type PushDeliveryChannel struct {
	Channel string
	Config  map[string]any
}

// ListPushCandidates 返回有活跃绑表的用户及其偏好与余额。供引擎评估。
func ListPushCandidates(ctx context.Context, pool *pgxpool.Pool, box *secrets.Box) ([]PushCandidate, error) {
	rows, err := pool.Query(ctx, `
		SELECT u.id::text, u.email, u.email_verified_at IS NOT NULL, m.meter_no,
			m.building, m.floor, m.room,
			COALESCE(r.total_yuan, 0)::text,
			(
				SELECT sum(d.usage_kwh)::text
				FROM effective_daily_consumption d
				WHERE d.meter_id=m.id AND d.usage_date >= (current_date - 6)
			),
			COALESCE(s.low_balance_alert, true),
			COALESCE(s.threshold_yuan, 10)::text,
			COALESCE(s.scheduled_digest, true),
			COALESCE(s.period, 'daily'),
			COALESCE(s.push_time, '08:00'),
			COALESCE(s.timezone, 'Asia/Shanghai'),
			COALESCE(s.templates, '{}'::jsonb),
			COALESCE(s.alert_latched, false),
			s.last_digest_at,
			c.enabled,
			c.config
		FROM user_accounts u
		JOIN user_meter_bindings b ON b.user_id=u.id AND b.unbound_at IS NULL
		JOIN meters m ON m.id=b.meter_id AND m.active AND NOT m.excluded
		LEFT JOIN LATERAL (
			SELECT total_yuan FROM meter_readings
			WHERE meter_id=m.id ORDER BY reading_time DESC LIMIT 1
		) r ON true
		LEFT JOIN user_notification_settings s ON s.user_id=u.id
		LEFT JOIN user_notification_channels c ON c.user_id=u.id AND c.channel='mail'
		WHERE u.status='active'
	`)
	if err != nil {
		return nil, fmt.Errorf("list push candidates: %w", err)
	}
	out := make([]PushCandidate, 0)
	for rows.Next() {
		var c PushCandidate
		var mailEnabled *bool
		var rawConfig, rawTemplates []byte
		if err := rows.Scan(
			&c.UserID, &c.Email, &c.EmailVerified, &c.MeterNo, &c.Building, &c.Floor, &c.Room, &c.BalanceYuan, &c.Recent7DKWH,
			&c.LowBalanceAlert, &c.ThresholdYuan, &c.ScheduledDigest,
			&c.Period, &c.PushTime, &c.Timezone, &rawTemplates, &c.AlertLatched, &c.LastDigestAt,
			&mailEnabled, &rawConfig,
		); err != nil {
			return nil, err
		}
		c.Templates, err = decodeNotificationTemplates(rawTemplates)
		if err != nil {
			return nil, fmt.Errorf("load notification templates for user %s: %w", c.UserID, err)
		}
		if mailEnabled != nil {
			c.MailConfigured = true
			c.MailEnabled = *mailEnabled
			if len(rawConfig) > 0 {
				var cfg map[string]any
				if err := json.Unmarshal(rawConfig, &cfg); err == nil {
					recipients, _ := parseMailRecipients(cfg["to"])
					c.MailRecipients = recipients
				}
			}
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	verifiedByUser := make(map[string]map[string]bool)
	verifiedRows, err := pool.Query(ctx, `
		SELECT user_id::text,email FROM user_verified_notification_emails
	`)
	if err != nil {
		return nil, fmt.Errorf("list verified notification emails: %w", err)
	}
	for verifiedRows.Next() {
		var userID, email string
		if err := verifiedRows.Scan(&userID, &email); err != nil {
			verifiedRows.Close()
			return nil, err
		}
		if verifiedByUser[userID] == nil {
			verifiedByUser[userID] = make(map[string]bool)
		}
		verifiedByUser[userID][email] = true
	}
	if err := verifiedRows.Err(); err != nil {
		verifiedRows.Close()
		return nil, err
	}
	verifiedRows.Close()
	for i := range out {
		accountEmail := ""
		if out[i].EmailVerified {
			accountEmail = out[i].Email
		}
		out[i].MailRecipients = filterVerifiedMailRecipients(out[i].MailRecipients, accountEmail, verifiedByUser[out[i].UserID])
	}

	byUser := make(map[string]*PushCandidate, len(out))
	for i := range out {
		byUser[out[i].UserID] = &out[i]
	}
	channelRows, err := pool.Query(ctx, `
		SELECT user_id::text,channel,config,secret_config,secret_key_id
		FROM user_notification_channels
		WHERE enabled AND channel IN ('dingtalk','wecom','wecom_webhook','discord','feishu','lark','pushplus','mp','qq','napcat','telegram','whatsapp','serverchan_turbo','serverchan3','webhook','bark','gotify')
		ORDER BY user_id,channel
	`)
	if err != nil {
		return nil, fmt.Errorf("list personal push channels: %w", err)
	}
	defer channelRows.Close()
	for channelRows.Next() {
		var userID, channel string
		var raw, sealed []byte
		var keyID *string
		if err := channelRows.Scan(&userID, &channel, &raw, &sealed, &keyID); err != nil {
			return nil, err
		}
		candidate := byUser[userID]
		if candidate == nil {
			continue
		}
		config, err := decodeChannelConfig(channel, raw, sealed, keyID, box)
		if err != nil {
			return nil, fmt.Errorf("load %s credentials for user %s: %w", channel, userID, err)
		}
		candidate.PersonalChannels = append(candidate.PersonalChannels, PushDeliveryChannel{
			Channel: channel,
			Config:  config,
		})
	}
	return out, channelRows.Err()
}

func MarkAlertLatched(ctx context.Context, pool *pgxpool.Pool, userID string, latched bool) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO user_notification_settings (user_id, alert_latched, last_alert_at, updated_at)
		VALUES ($1::uuid, $2, CASE WHEN $2 THEN now() ELSE NULL END, now())
		ON CONFLICT (user_id) DO UPDATE SET
			alert_latched=EXCLUDED.alert_latched,
			last_alert_at=CASE WHEN EXCLUDED.alert_latched THEN now() ELSE user_notification_settings.last_alert_at END,
			updated_at=now()
	`, userID, latched)
	return err
}

func MarkDigestSent(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO user_notification_settings (user_id, last_digest_at, updated_at)
		VALUES ($1::uuid, now(), now())
		ON CONFLICT (user_id) DO UPDATE SET last_digest_at=now(), updated_at=now()
	`, userID)
	return err
}

// ResolveMailRecipients 决定实际发信地址。
// 已配置且启用 mail：使用 to[]。
// 从未配置 mail：回退账号邮箱。
// 配置过但关闭：不发。
func ResolveMailRecipients(c PushCandidate) []string {
	if c.MailConfigured {
		if !c.MailEnabled || len(c.MailRecipients) == 0 {
			return nil
		}
		return c.MailRecipients
	}
	if c.Email != "" {
		return []string{c.Email}
	}
	return nil
}
