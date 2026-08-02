package provider

import (
	"context"
	"time"
)

type Status string

const (
	StatusValid      Status = "valid"
	StatusEmpty      Status = "empty"
	StatusError      Status = "error"
	StatusParseError Status = "parse_error"
)

type QueryOptions struct {
	IncludeBill    bool
	Month          string
	MaxAttempts    int
	BeforeRequest  func(context.Context) error
	AcquireRequest func(context.Context) (func(), error)
}

type Result struct {
	QueriedAt time.Time `json:"queried_at"`
	Meter     string    `json:"meter"`
	AreaID    string    `json:"area_id"`
	AreaName  string    `json:"area_name"`
	OK        bool      `json:"ok"`
	Status    Status    `json:"status"`
	Balance   Balance   `json:"balance"`
	Bill      *Bill     `json:"bill,omitempty"`
	Errors    []string  `json:"errors"`
}

type Balance struct {
	PrepaidYuan string `json:"prepaid_yuan,omitempty"`
	SubsidyYuan string `json:"subsidy_yuan,omitempty"`
	TotalYuan   string `json:"total_yuan,omitempty"`
	TotalKWh    string `json:"total_kwh,omitempty"`
	MeterStatus string `json:"meter_status,omitempty"`
	ReadingTime string `json:"reading_time,omitempty"`
	Room        string `json:"room,omitempty"`
	ChargeType  string `json:"charge_type,omitempty"`
}

type Bill struct {
	Month string   `json:"month,omitempty"`
	Data  BillData `json:"data"`
}

type BillData struct {
	StartKWh string `json:"qcdl,omitempty"`
	EndKWh   string `json:"qmdl,omitempty"`
	UsageKWh string `json:"ydl,omitempty"`
	CostYuan string `json:"ydje,omitempty"`
	Message  string `json:"msg,omitempty"`
	Period   string `json:"sj,omitempty"`
}

type MonthlyBillStatus string

const (
	MonthlyStatusValid   MonthlyBillStatus = "valid"
	MonthlyStatusPartial MonthlyBillStatus = "partial"
	MonthlyStatusNoData  MonthlyBillStatus = "no_data"
	MonthlyStatusEmpty   MonthlyBillStatus = "empty"
	MonthlyStatusError   MonthlyBillStatus = "error"
)

type MonthlyQueryOptions struct {
	MaxAttempts      int
	MonthMaxAttempts int
	BeforeRequest    func(context.Context) error
	AcquireRequest   func(context.Context) (func(), error)
}

type MonthlyBillMonth struct {
	Month        string   `json:"month"`
	Status       string   `json:"status"`
	Data         BillData `json:"data"`
	ErrorMessage string   `json:"error_message,omitempty"`
}

type MonthlyBillsResult struct {
	QueriedAt       time.Time          `json:"queried_at"`
	Meter           string             `json:"meter"`
	OK              bool               `json:"ok"`
	Status          MonthlyBillStatus  `json:"status"`
	Attempts        int                `json:"attempts"`
	MonthsRequested int                `json:"months_requested"`
	Bills           []MonthlyBillMonth `json:"bills"`
	Errors          []string           `json:"errors"`
}

type DailyDetailStatus string

const (
	DailyDetailStatusValid   DailyDetailStatus = "valid"
	DailyDetailStatusNoData  DailyDetailStatus = "no_data"
	DailyDetailStatusPartial DailyDetailStatus = "partial"
	DailyDetailStatusEmpty   DailyDetailStatus = "empty"
	DailyDetailStatusError   DailyDetailStatus = "error"
)

type DailyDetailQueryOptions struct {
	MaxAttempts      int
	MonthMaxAttempts int
	Location         *time.Location
	BeforeRequest    func(context.Context) error
	AcquireRequest   func(context.Context) (func(), error)
}

type DailyUsageDay struct {
	Date     string `json:"sj"`
	UsageKWh string `json:"ydl"`
	CostYuan string `json:"ydje"`
}

type DailyDetailMonth struct {
	Month          string          `json:"month"`
	Status         string          `json:"status"`
	CoveredThrough string          `json:"covered_through,omitempty"`
	RawRowCount    int             `json:"raw_row_count"`
	Days           []DailyUsageDay `json:"days"`
	ErrorMessage   string          `json:"error_message,omitempty"`
}

type DailyDetailsResult struct {
	QueriedAt       time.Time          `json:"queried_at"`
	Meter           string             `json:"meter"`
	OK              bool               `json:"ok"`
	Status          DailyDetailStatus  `json:"status"`
	Attempts        int                `json:"attempts"`
	MonthsRequested int                `json:"months_requested"`
	Months          []DailyDetailMonth `json:"months"`
	Errors          []string           `json:"errors"`
}
