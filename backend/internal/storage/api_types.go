package storage

import (
	"encoding/json"
	"time"
)

type ScanRunView struct {
	ID          string      `json:"id"`
	ParentRunID *string     `json:"parent_run_id"`
	Trigger     string      `json:"trigger"`
	Status      string      `json:"status"`
	StartedAt   time.Time   `json:"started_at"`
	FinishedAt  *time.Time  `json:"finished_at"`
	HeartbeatAt *time.Time  `json:"heartbeat_at"`
	QPS         float64     `json:"qps"`
	Concurrency int         `json:"concurrency"`
	Limit       *int        `json:"limit"`
	BoundOnly   bool        `json:"bound_only"`
	Counters    RunCounters `json:"counters"`
}

type ScanResultView struct {
	RunID        string     `json:"run_id"`
	Meter        string     `json:"meter"`
	Building     string     `json:"building"`
	Floor        string     `json:"floor"`
	Room         string     `json:"room"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	DurationMS   int64      `json:"duration_ms"`
	QueriedAt    time.Time  `json:"queried_at"`
	ReadingID    *string    `json:"reading_id"`
	ReadingTime  *time.Time `json:"reading_time"`
	ErrorCode    *string    `json:"error_code"`
	ErrorMessage *string    `json:"error_message"`
}

type AnomalyView struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Severity       string          `json:"severity"`
	Meter          *string         `json:"meter"`
	ScanRunID      *string         `json:"scan_run_id"`
	DetectedAt     time.Time       `json:"detected_at"`
	Acknowledged   bool            `json:"acknowledged"`
	AcknowledgedAt *time.Time      `json:"acknowledged_at"`
	Note           *string         `json:"note"`
	Details        json.RawMessage `json:"details"`
}

type InventoryImportView struct {
	ID              string          `json:"id"`
	SourceName      string          `json:"source_name"`
	SourceHash      string          `json:"source_hash"`
	SourceUpdatedAt *time.Time      `json:"source_updated_at"`
	ImportedAt      time.Time       `json:"imported_at"`
	Status          string          `json:"status"`
	Counts          json.RawMessage `json:"counts"`
}

type InventoryTree struct {
	UpdatedAt      time.Time         `json:"updated_at"`
	InventoryTotal int               `json:"inventory_total"`
	ExcludedTotal  int               `json:"excluded_total"`
	Campuses       []InventoryCampus `json:"campuses"`
}

type InventoryCampus struct {
	Name      string              `json:"name"`
	Buildings []InventoryBuilding `json:"buildings"`
}

type InventoryBuilding struct {
	Name   string           `json:"name"`
	Floors []InventoryFloor `json:"floors"`
}

type InventoryFloor struct {
	Name  string          `json:"name"`
	Rooms []InventoryRoom `json:"rooms"`
}

type InventoryRoom struct {
	Name     string `json:"name"`
	Meter    string `json:"meter"`
	Excluded bool   `json:"excluded"`
}

// CampusScopes 为筛选下拉的校区/楼栋/楼层层级。
// 不含房间与运维信息。见 frontend/docs/CAMPUS-GAPS.md §1。
type CampusScopes struct {
	UpdatedAt time.Time     `json:"updated_at"`
	Campuses  []ScopeCampus `json:"campuses"`
}

type ScopeCampus struct {
	Name      string          `json:"name"`
	Buildings []ScopeBuilding `json:"buildings"`
}

type ScopeBuilding struct {
	Name      string   `json:"name"`
	Floors    []string `json:"floors"`
	RoomCount int      `json:"room_count"`
}

type Quality struct {
	Eligible      int     `json:"eligible"`
	Covered       int     `json:"covered"`
	CoverageRatio float64 `json:"coverage_ratio"`
	Stale         int     `json:"stale"`
	Anomalies     int     `json:"anomalies"`
}

type LatestReadingView struct {
	ReadingTime time.Time `json:"reading_time"`
	ObservedAt  time.Time `json:"observed_at"`
	PrepaidYuan string    `json:"prepaid_yuan"`
	SubsidyYuan *string   `json:"subsidy_yuan"`
	TotalYuan   string    `json:"total_yuan"`
	TotalKWH    string    `json:"total_kwh"`
	MeterStatus string    `json:"meter_status"`
	Freshness   string    `json:"freshness"`
}

type MeterOverviewView struct {
	Meter        string             `json:"meter"`
	Location     MeterLocation      `json:"location"`
	Latest       *LatestReadingView `json:"latest"`
	Recent7DKWH  *string            `json:"recent_7d_kwh"`
	Availability string             `json:"availability"`
	Quality      Quality            `json:"quality"`
}

type MeterLocation struct {
	Building string `json:"building"`
	Floor    string `json:"floor"`
	Room     string `json:"room"`
}

/*
CampusBreakdownView 为楼栋或楼层 × 时间的用电矩阵。

	行合计供构成图。行 × 桶供热力图。
	无楼栋时按楼栋分组。有楼栋时按楼层分组。
	库存仅含宿舍电表。禁止按建筑类别拆分。
*/
type CampusBreakdownView struct {
	GroupBy      string               `json:"group_by"`
	Granularity  string               `json:"granularity"`
	Buckets      []time.Time          `json:"buckets"`
	TotalKWH     string               `json:"total_kwh"`
	MaxCellKWH   string               `json:"max_cell_kwh"`
	Rows         []CampusBreakdownRow `json:"rows"`
	Availability string               `json:"availability"`
	Quality      Quality              `json:"quality"`
}

type CampusBreakdownRow struct {
	Key         string  `json:"key"`
	TotalKWH    string  `json:"total_kwh"`
	Share       float64 `json:"share"`
	Meters      int     `json:"meters"`
	MeterCounts []int   `json:"meter_counts"`
	// Values 与 Buckets 一一对齐。nil 表示无有效读数，不是 0。
	Values []*string `json:"values"`
}

type SeriesPoint struct {
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	Value       *string   `json:"value"`
	// Meters 为该桶逐日判定后的日均非空房数。
	Meters       int     `json:"meters"`
	Availability string  `json:"availability"`
	Quality      Quality `json:"quality"`
}

type TimeSeriesView struct {
	Metric      string `json:"metric"`
	Unit        string `json:"unit"`
	Granularity string `json:"granularity"`
	// ScopeMeters 为各桶日均非空房数的最大值。供客户端兜底除数。
	ScopeMeters  int           `json:"scope_meters"`
	Availability string        `json:"availability"`
	Quality      Quality       `json:"quality"`
	Points       []SeriesPoint `json:"points"`
}

type MonthlyBillView struct {
	Month    string `json:"month"`
	StartKWH string `json:"start_kwh"`
	EndKWH   string `json:"end_kwh"`
	UsageKWH string `json:"usage_kwh"`
	CostYuan string `json:"cost_yuan"`
}

type CampusSummaryView struct {
	Scope      map[string]string `json:"scope"`
	From       time.Time         `json:"from"`
	To         time.Time         `json:"to"`
	TotalKWH   *string           `json:"total_kwh"`
	PerRoomKWH *string           `json:"per_room_kwh"`
	// Meters 为 PerRoomKWH 的除数。取查询窗口日均非空房数。
	Meters       int     `json:"meters"`
	Availability string  `json:"availability"`
	Quality      Quality `json:"quality"`
}

/*
榜单行。脱敏必须在服务端完成。禁止仅依赖前端打码。

	Name 为名称列。Label 为位置列。
	Building / Floor / Room 仅对本人下发明文。
*/
type RankingEntryView struct {
	Rank        int      `json:"rank"`
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	IsSelf      bool     `json:"is_self"`
	Building    *string  `json:"building"`
	Floor       *string  `json:"floor"`
	Room        *string  `json:"room"`
	ValueKWH    string   `json:"value_kwh"`
	ChangeRatio *float64 `json:"change_ratio"`
}

/*
榜单身份偏好。无账号电表使用 defaultLeaderboardPreference()。

	默认参与榜单。完整楼栋与楼层。房间打码。
	禁止匿名可读明文房间号。
*/
type LeaderboardPreference struct {
	OptedIn      bool      `json:"opted_in"`
	ShowBuilding bool      `json:"show_building"`
	ShowFloor    bool      `json:"show_floor"`
	ShowRoom     bool      `json:"show_room"`
	ShowNickname bool      `json:"show_nickname"`
	MaskBuilding bool      `json:"mask_building"`
	MaskFloor    bool      `json:"mask_floor"`
	MaskRoom     bool      `json:"mask_room"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type RankingView struct {
	Period         string             `json:"period"`
	Mode           string             `json:"mode"`
	CurrentPeriod  RankingPeriodView  `json:"current_period"`
	PreviousPeriod RankingPeriodView  `json:"previous_period"`
	UpdatedAt      time.Time          `json:"updated_at"`
	NextUpdateAt   time.Time          `json:"next_update_at"`
	Availability   string             `json:"availability"`
	Quality        Quality            `json:"quality"`
	ExcludedCount  int                `json:"excluded_count"`
	Items          []RankingEntryView `json:"items"`
	// Self 仅在已登录且绑表时下发。名次全量计算，与 items 的 limit 无关。
	Self *RankingSelfView `json:"self"`
}

// RankingPeriodView 为本次榜单的自然日边界。两端都包含。
// 禁止前端按浏览器时钟猜测范围。
type RankingPeriodView struct {
	From string `json:"from"`
	To   string `json:"to"`
}

/*
RankingSelfView 为调用者本人的名次。

	名次不受 limit 限制。明文位置仅回给本人。
*/
type RankingSelfView struct {
	Rank        int      `json:"rank"`
	Total       int      `json:"total"`
	Percentile  float64  `json:"percentile"`
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Building    string   `json:"building"`
	Floor       string   `json:"floor"`
	Room        string   `json:"room"`
	ValueKWH    string   `json:"value_kwh"`
	ChangeRatio *float64 `json:"change_ratio"`
	InList      bool     `json:"in_list"`
	// Neighbors 为上下各一名，含自己。邻居仍按各自偏好脱敏。
	Neighbors []RankingEntryView `json:"neighbors"`
}
