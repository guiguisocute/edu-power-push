package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"
)

type SnapshotRecord struct {
	Building    string
	Floor       string
	Room        string
	MeterNo     string
	Status      string
	Attempts    int
	QueriedAt   time.Time
	ReadingTime *time.Time
	PrepaidYuan *string
	SubsidyYuan *string
	TotalYuan   *string
	TotalKWh    *string
	Freshness   string
	Error       string
	ReadingHash string
}

type SnapshotPlan struct {
	SourceName      string
	SourceHash      string
	SourceUpdatedAt time.Time
	Records         []SnapshotRecord
	Total           int
	Valid           int
	Stale           int
	Empty           int
	Error           int
	ParseError      int
	Errors          []string
}

type snapshotDocument struct {
	UpdatedAt string               `json:"updated_at"`
	Count     int                  `json:"count"`
	Records   []snapshotJSONRecord `json:"records"`
}

type snapshotJSONRecord struct {
	Building    string  `json:"building"`
	Floor       string  `json:"floor"`
	Room        string  `json:"room"`
	Meter       string  `json:"meter"`
	OK          bool    `json:"ok"`
	PrepaidYuan *string `json:"prepaid_yuan"`
	SubsidyYuan *string `json:"subsidy_yuan"`
	TotalYuan   *string `json:"total_yuan"`
	KWh         *string `json:"kwh"`
	ReadingTime *string `json:"reading_time"`
	QueriedAt   string  `json:"queried_at"`
	Status      string  `json:"status"`
	Error       string  `json:"error"`
}

func ValidateSnapshot(
	reader io.Reader,
	sourceName string,
	loc *time.Location,
	staleAfter time.Duration,
) (SnapshotPlan, error) {
	raw, err := readBounded(reader)
	if err != nil {
		return SnapshotPlan{}, err
	}
	var doc snapshotDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return SnapshotPlan{}, fmt.Errorf("decode snapshot JSON: %w", err)
	}
	if loc == nil {
		loc = time.UTC
	}
	updatedAt, err := parseSourceTime(doc.UpdatedAt, loc)
	if err != nil {
		return SnapshotPlan{}, fmt.Errorf("snapshot updated_at: %w", err)
	}
	sum := sha256.Sum256(raw)
	plan := SnapshotPlan{
		SourceName:      sourceName,
		SourceHash:      hex.EncodeToString(sum[:]),
		SourceUpdatedAt: updatedAt,
		Total:           len(doc.Records),
		Records:         make([]SnapshotRecord, 0, len(doc.Records)),
		Errors:          []string{},
	}
	if doc.Count != 0 && doc.Count != len(doc.Records) {
		plan.Errors = append(plan.Errors, fmt.Sprintf(
			"declared count %d does not match records length %d", doc.Count, len(doc.Records),
		))
	}

	seen := make(map[string]struct{}, len(doc.Records))
	for index, rawRecord := range doc.Records {
		record := SnapshotRecord{
			Building: strings.TrimSpace(rawRecord.Building),
			Floor:    strings.TrimSpace(rawRecord.Floor),
			Room:     strings.TrimSpace(rawRecord.Room),
			MeterNo:  strings.TrimSpace(rawRecord.Meter),
			Attempts: 1,
			Error:    strings.TrimSpace(rawRecord.Error),
		}
		if record.MeterNo == "" {
			record.Status = "parse_error"
			record.Error = "missing meter number"
			plan.ParseError++
			plan.Errors = append(plan.Errors, fmt.Sprintf("record %d has no meter", index))
			plan.Records = append(plan.Records, record)
			continue
		}
		if _, exists := seen[record.MeterNo]; exists {
			record.Status = "parse_error"
			record.Error = "duplicate meter in snapshot"
			plan.ParseError++
			plan.Errors = append(plan.Errors, fmt.Sprintf("record %d duplicates a meter", index))
			plan.Records = append(plan.Records, record)
			continue
		}
		seen[record.MeterNo] = struct{}{}

		record.QueriedAt, err = parseSourceTime(rawRecord.QueriedAt, loc)
		if err != nil {
			record.Status = "parse_error"
			record.Error = "invalid queried_at"
			plan.ParseError++
			plan.Records = append(plan.Records, record)
			continue
		}

		if !rawRecord.OK {
			if rawRecord.Status == "empty" {
				record.Status = "empty"
				plan.Empty++
			} else {
				record.Status = "error"
				plan.Error++
			}
			plan.Records = append(plan.Records, record)
			continue
		}

		record.PrepaidYuan = cleanDecimal(rawRecord.PrepaidYuan)
		record.SubsidyYuan = cleanDecimal(rawRecord.SubsidyYuan)
		record.TotalYuan = cleanDecimal(rawRecord.TotalYuan)
		record.TotalKWh = cleanDecimal(rawRecord.KWh)
		if record.TotalKWh == nil || (record.PrepaidYuan == nil && record.TotalYuan == nil) ||
			rawRecord.ReadingTime == nil || strings.TrimSpace(*rawRecord.ReadingTime) == "" {
			record.Status = "parse_error"
			record.Error = "valid row is missing a numeric balance, kWh, or reading_time"
			plan.ParseError++
			plan.Records = append(plan.Records, record)
			continue
		}
		readingTime, parseErr := parseSourceTime(strings.TrimSpace(*rawRecord.ReadingTime), loc)
		if parseErr != nil {
			record.Status = "parse_error"
			record.Error = "invalid reading_time"
			plan.ParseError++
			plan.Records = append(plan.Records, record)
			continue
		}
		record.ReadingTime = &readingTime
		record.Freshness = "fresh"
		record.Status = "valid"
		if staleAfter > 0 && updatedAt.Sub(readingTime) > staleAfter {
			record.Freshness = "stale"
			record.Status = "stale"
			plan.Stale++
		} else {
			plan.Valid++
		}
		record.ReadingHash = hashReading(record)
		plan.Records = append(plan.Records, record)
	}
	return plan, nil
}

func cleanDecimal(value *string) *string {
	if value == nil {
		return nil
	}
	cleaned := strings.TrimSpace(*value)
	if cleaned == "" || cleaned == "undefined" {
		return nil
	}
	number, ok := new(big.Rat).SetString(cleaned)
	if !ok {
		return nil
	}
	canonical := number.FloatString(4)
	return &canonical
}

func hashReading(record SnapshotRecord) string {
	parts := []string{
		deref(record.PrepaidYuan),
		deref(record.SubsidyYuan),
		deref(record.TotalYuan),
		deref(record.TotalKWh),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
