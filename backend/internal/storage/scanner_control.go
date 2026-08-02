package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/edu-power-push/edu-power-push/backend/internal/secrets"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ScannerControlSettings 为三路上游采集器的运行时配置文档。
// 安全上限仍属部署配置。禁止持久化到此处。
type ScannerControlSettings struct {
	Shared       SharedUpstreamSettings `json:"shared"`
	Balance      BalanceScannerSettings `json:"balance"`
	Bills        BatchScannerSettings   `json:"bills"`
	DailyDetails DetailScannerSettings  `json:"daily_details"`
}

// SharedUpstreamSettings 为各采集器共用闸门。共同约束三路上游。
// 旧文档无该字段时沿用部署默认值。
type SharedUpstreamSettings struct {
	QPS         float64 `json:"qps"`
	Concurrency int     `json:"concurrency"`
}

type BatchScannerSettings struct {
	Cron          string  `json:"cron"`
	QPS           float64 `json:"qps"`
	Concurrency   int     `json:"concurrency"`
	RetryMax      int     `json:"retry_max"`
	MonthRetryMax int     `json:"month_retry_max"`
	Enabled       bool    `json:"enabled"`
}

type BalanceScannerSettings struct {
	Cron        string  `json:"cron"`
	BoundCron   string  `json:"bound_cron"`
	QPS         float64 `json:"qps"`
	Concurrency int     `json:"concurrency"`
	RetryMax    int     `json:"retry_max"`
	Enabled     bool    `json:"enabled"`
}

type DetailScannerSettings struct {
	BatchScannerSettings
	RetryCron     string `json:"retry_cron"`
	BootstrapFrom string `json:"bootstrap_from"`
	AutoBootstrap bool   `json:"auto_bootstrap"`
}

type ScannerControlRecord struct {
	Settings  ScannerControlSettings
	Source    string
	Version   int64
	UpdatedAt *string
	UpdatedBy string
}

// LoadScannerControlSettings 将已存文档叠在部署默认值上。
// 新二进制增加字段时保持对旧文档前向兼容。
func LoadScannerControlSettings(
	ctx context.Context,
	pool *pgxpool.Pool,
	box *secrets.Box,
	defaults ScannerControlSettings,
) (ScannerControlRecord, error) {
	out := ScannerControlRecord{Settings: defaults, Source: "env"}
	record, err := GetSetting(ctx, pool, box, SettingKeyScanner)
	if errors.Is(err, ErrSettingNotFound) {
		return out, nil
	}
	if err != nil {
		return ScannerControlRecord{}, err
	}

	settings, err := decodeScannerControlSettings(record.Value, defaults)
	if err != nil {
		return ScannerControlRecord{}, err
	}
	out.Settings = settings
	out.Source = "panel"
	out.Version = record.Version
	updatedAt := record.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
	out.UpdatedAt = &updatedAt
	out.UpdatedBy = record.UpdatedBy
	return out, nil
}

func decodeScannerControlSettings(raw json.RawMessage, defaults ScannerControlSettings) (ScannerControlSettings, error) {
	out := defaults
	// 三路面板前，文档即余额扫描器本身。
	// 保留该运维选择。账单与日明细取环境默认值。
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return ScannerControlSettings{}, err
	}
	if _, current := shape["balance"]; current {
		if err := json.Unmarshal(raw, &out); err != nil {
			return ScannerControlSettings{}, err
		}
	} else {
		legacy := out.Balance
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return ScannerControlSettings{}, err
		}
		out.Balance = legacy
	}
	return out, nil
}
